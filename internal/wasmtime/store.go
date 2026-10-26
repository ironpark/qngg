package wasmtime

import (
	"reflect"
	"runtime"
	"sync"
)

// Store is a general group of wasm instances, and many objects
// must all be created with and reference the same `Store`
type Store struct {
	_ptr *wasmtime_store_t

	// The store's context pointer. Wasmtime keeps it stable for the lifetime
	// of the store, so it is fetched once instead of on every call.
	_ctx *wasmtime_context_t

	// The store's entry in gStoreMap, cached to skip the lookup on every call.
	_data *storeData

	// The `Engine` that this store uses for compilation and environment
	// settings.
	Engine *Engine
}

// Storelike represents types that can be used to contextually reference a
// `Store`.
//
// This interface is implemented by `*Store` and `*Caller` and is pervasively
// used throughout this library. You'll want to pass one of those two objects
// into functions that take a `Storelike`.
type Storelike interface {
	// Returns the wasmtime context pointer this store is attached to.
	Context() *wasmtime_context_t

	// Arbitrary data attached to the store. Embedders should cast this into an appropriate domain
	// specific type.
	// Note that this will return nil for a [Store] created with [NewStore]. Use [NewStoreWithData]
	// to use this method.
	Data() interface{}
}

var (
	gStoreLock sync.Mutex
	gStoreMap  = make(map[int]*storeData)
	gStoreSlab slab
)

// State associated with a `Store`, currently used to propagate panic
// information through invocations as well as store Go closures that have been
// added to the store.
type storeData struct {
	engine    *Engine
	funcNew   []funcNewEntry
	funcWrap  []funcWrapEntry
	lastPanic interface{}

	// See Store.SetEpochDeadlineCallback.
	epochDeadline func() (uint64, error)

	// Reusable argument frames for Func.CallUnchecked, one per level of
	// nested calls.
	callFrames []*callFrame
	callDepth  int

	// arbitrary data supplied by the embedder
	data interface{}
}

type funcNewEntry struct {
	callback func(*Caller, []Val) ([]Val, *Trap)
	results  []*ValType
}

type funcWrapEntry struct {
	callback reflect.Value
}

// NewStore creates a new `Store` from the configuration provided in `engine`
func NewStore(engine *Engine) *Store {
	return NewStoreWithData(engine, nil)
}

// NewStoreWithData creates a new [Store] from engine and associates data with it.
// Embedders can access data via [Storelike.Data]
func NewStoreWithData(engine *Engine, data interface{}) *Store {
	// Allocate an index for this store and allocate some internal data to go with
	// the store.
	gStoreLock.Lock()
	idx := gStoreSlab.allocate()
	sd := &storeData{engine: engine, data: data}
	gStoreMap[idx] = sd
	gStoreLock.Unlock()

	ptr := go_store_new(engine.ptr(), uintptr(idx))
	store := &Store{
		_ptr:   ptr,
		_ctx:   wasmtime_store_context(ptr),
		_data:  sd,
		Engine: engine,
	}
	runtime.SetFinalizer(store, func(store *Store) {
		store.Close()
	})
	return store
}

func goFinalizeStore(env uintptr) {
	// When a store is finalized this is used as the finalization callback for the
	// custom data within the store, and our finalization here will delete the
	// store's data from the global map and deallocate its index to get reused by
	// a future store.
	idx := int(uintptr(env))
	gStoreLock.Lock()
	defer gStoreLock.Unlock()
	delete(gStoreMap, idx)
	gStoreSlab.deallocate(idx)
}

func (store *Store) ptr() *wasmtime_store_t {
	ret := store._ptr
	if ret == nil {
		panic("object has been closed already")
	}
	maybeGC()
	return ret
}

// Close will deallocate this store's state explicitly.
//
// For more information see the documentation for engine.Close()
func (store *Store) Close() {
	if store._ptr == nil {
		return
	}
	runtime.SetFinalizer(store, nil)
	wasmtime_store_delete(store._ptr)
	store._ptr = nil
	store._ctx = nil
	store._data = nil
}

// Implementation of the `Storelike` interface
func (store *Store) Context() *wasmtime_context_t {
	store.ptr()
	return store._ctx
}

// Data returns arbitrary data attached to [Store] via [NewStoreWithData].
// If [Store] was created with [NewStore], this function returns nil.
func (store *Store) Data() interface{} {
	return getDataInStore(store).data
}

// SetEpochDeadline will configure the relative deadline, from the current
// engine's epoch number, after which wasm code will be interrupted.
func (store *Store) SetEpochDeadline(deadline uint64) {
	wasmtime_context_set_epoch_deadline(store.Context(), uint64(deadline))
	runtime.KeepAlive(store)
}

// Returns the underlying `*storeData` that this store references in Go, used
// for inserting functions or storing panic data.
func getDataInStore(store Storelike) *storeData {
	if s, ok := store.(*Store); ok && s._data != nil {
		s.ptr()
		return s._data
	}
	return storeDataFromContext(store.Context())
}

// storeDataFromContext looks up the `*storeData` of a raw store context.
func storeDataFromContext(ctx *wasmtime_context_t) *storeData {
	data := uintptr(wasmtime_context_get_data(ctx))
	gStoreLock.Lock()
	defer gStoreLock.Unlock()
	return gStoreMap[int(data)]
}

var (
	gEngineFuncLock     sync.Mutex
	gEngineFuncNew      = make(map[int]*funcNewEntry)
	gEngineFuncNewSlab  slab
	gEngineFuncWrap     = make(map[int]*funcWrapEntry)
	gEngineFuncWrapSlab slab
)

func insertFuncNew(data *storeData, ty *FuncType, callback func(*Caller, []Val) ([]Val, *Trap)) int {
	var idx int
	entry := funcNewEntry{
		callback: callback,
		results:  ty.Results(),
	}
	if data == nil {
		gEngineFuncLock.Lock()
		defer gEngineFuncLock.Unlock()
		idx = gEngineFuncNewSlab.allocate()
		gEngineFuncNew[idx] = &entry
		idx = (idx << 1)
	} else {
		idx = len(data.funcNew)
		data.funcNew = append(data.funcNew, entry)
		idx = (idx << 1) | 1
	}
	return idx
}

func (data *storeData) getFuncNew(idx int) *funcNewEntry {
	if idx&1 == 0 {
		gEngineFuncLock.Lock()
		defer gEngineFuncLock.Unlock()
		return gEngineFuncNew[idx>>1]
	} else {
		return &data.funcNew[idx>>1]
	}
}

func insertFuncWrap(data *storeData, callback reflect.Value) int {
	var idx int
	entry := funcWrapEntry{callback}
	if data == nil {
		gEngineFuncLock.Lock()
		defer gEngineFuncLock.Unlock()
		idx = gEngineFuncWrapSlab.allocate()
		gEngineFuncWrap[idx] = &entry
		idx = (idx << 1)
	} else {
		idx = len(data.funcWrap)
		data.funcWrap = append(data.funcWrap, entry)
		idx = (idx << 1) | 1
	}
	return idx
}

func (data *storeData) getFuncWrap(idx int) *funcWrapEntry {
	if idx&1 == 0 {
		gEngineFuncLock.Lock()
		defer gEngineFuncLock.Unlock()
		return gEngineFuncWrap[idx>>1]
	} else {
		return &data.funcWrap[idx>>1]
	}
}

func goFinalizeFuncNew(env uintptr) {
	idx := int(uintptr(env))
	if idx&1 != 0 {
		panic("shouldn't finalize a store-local index")
	}
	idx = idx >> 1
	gEngineFuncLock.Lock()
	defer gEngineFuncLock.Unlock()
	delete(gEngineFuncNew, idx)
	gEngineFuncNewSlab.deallocate(idx)
}

func goFinalizeFuncWrap(env uintptr) {
	idx := int(uintptr(env))
	if idx&1 != 0 {
		panic("shouldn't finalize a store-local index")
	}
	idx = idx >> 1
	gEngineFuncLock.Lock()
	defer gEngineFuncLock.Unlock()
	delete(gEngineFuncWrap, idx)
	gEngineFuncWrapSlab.deallocate(idx)
}

// GetFuel returns the amount of fuel remaining in this store.
//
// If fuel consumption is not enabled via `Config.SetConsumeFuel` then
// this function will return an error. Otherwise this will retrieve the fuel
// remaining and return it.
//
// Also note that fuel, if enabled, must be originally configured via
// `Store.SetFuel`.
func (store *Store) GetFuel() (uint64, error) {
	var remaining uint64
	c_remaining := uint64(remaining)
	err := wasmtime_context_get_fuel(store.Context(), &c_remaining)
	runtime.KeepAlive(store)
	if err != nil {
		return 0, mkError(err)
	}

	return uint64(c_remaining), nil
}

// SetFuel sets this store's fuel to the specified value.
//
// For this method to work fuel consumption must be enabled via
// `Config.SetConsumeFuel`. By default a store starts with 0 fuel
// for wasm to execute with (meaning it will immediately trap).
// This function must be called for the store to have
// some fuel to allow WebAssembly to execute.
//
// Note that at this time when fuel is entirely consumed it will cause
// wasm to trap. More usages of fuel are planned for the future.
//
// If fuel is not enabled within this store then an error is returned.
func (store *Store) SetFuel(fuel uint64) error {
	err := wasmtime_context_set_fuel(store.Context(), uint64(fuel))
	runtime.KeepAlive(store)
	if err != nil {
		return mkError(err)
	}

	return nil
}

// Limiter provides limits for a store. Used by hosts to limit resource
// consumption of instances. Use negative value to keep the default value
// for the limit.
func (store *Store) Limiter(
	memorySize int64,
	tableElements int64,
	instances int64,
	tables int64,
	memories int64,
) {
	wasmtime_store_limiter(
		store.ptr(),
		int64(memorySize),
		int64(tableElements),
		int64(instances),
		int64(tables),
		int64(memories),
	)
	runtime.KeepAlive(store)
}

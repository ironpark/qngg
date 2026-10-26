package qngg

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/ironpark/qngg/internal/wasmtime"
)

//go:embed qjs.wasm
var wasmBytes []byte

// wasmFunc is a cached export of the QuickJS module.
type wasmFunc struct {
	fn *wasmtime.Func
	// size of the args-and-results buffer: max(params, results)
	slots     int
	hasResult bool
	// whether the single result is a 32-bit value that must be zero-extended
	result32 bool
}

// Runtime wraps a QuickJS WebAssembly runtime with memory management.
type Runtime struct {
	store     *wasmtime.Store
	linker    *wasmtime.Linker
	instance  *wasmtime.Instance
	funcs     map[string]*wasmFunc
	mallocFn  *wasmFunc
	freeFn    *wasmFunc
	mem       *wasmMemory
	stopWatch func() bool
	limit     *execLimit
	// argument buffers for invoke, one per level of nested wasm calls
	callBufs  [][]wasmtime.ValRaw
	callDepth int
	option    Option
	handle    *rawHandle
	// goCtx is Option.Context, handed to Go functions called from JS.
	goCtx     context.Context
	ctxHandle *rawHandle // the JSContext
	global    *Value
	registry  *proxyRegistry
	closed    bool
}

// New creates a QuickJS runtime with optional configuration.
func New(options ...Option) (runtime *Runtime, err error) {
	defer func() {
		rerr := anyToError(recover())
		if rerr != nil {
			runtime.release()
			runtime = nil
			err = fmt.Errorf("failed to create QJS runtime: %w", rerr)
		}
	}()

	if err := wasmtime.LoadError(); err != nil {
		return nil, fmt.Errorf("failed to load wasmtime: %w", err)
	}

	proxyRegistry := newProxyRegistry()

	option := getRuntimeOption(options...)
	proxy := createFuncProxyWithRegistry(proxyRegistry)

	qjsBytes, hash := wasmBytes, embeddedHash()
	if len(option.quickJSWasm) > 0 {
		qjsBytes, hash = option.quickJSWasm, hashBytes(option.quickJSWasm)
	}

	engine, module, err := getCompiledModule(
		option.CloseOnContextDone || option.MaxExecutionTime > 0,
		option.DisableBuildCache,
		option.CacheDir,
		qjsBytes,
		hash,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create global compiled module: %w", err)
	}

	runtime = &Runtime{
		option:   option,
		goCtx:    option.Context,
		registry: proxyRegistry,
		funcs:    map[string]*wasmFunc{},
	}

	if err := runtime.instantiate(engine, module, proxy); err != nil {
		runtime.release()

		return nil, err
	}

	runtime.initializeRuntime()

	return runtime, nil
}

// String returns a string representation of the runtime.
func (r *Runtime) String() string {
	return fmt.Sprintf("QJSRuntime: %p", r)
}

// Close cleanly shuts down the runtime and frees all associated resources.
// Calling Close more than once is a no-op; using the runtime or its values
// afterwards fails with ErrRuntimeClosed.
func (r *Runtime) Close() {
	if r == nil || r.closed {
		return
	}

	// Free QJS runtime handle. An interrupted runtime cannot run wasm anymore
	// and its QuickJS state may be inconsistent; releasing the store below
	// frees its memory as a whole.
	if r.handle != nil && r.interruptErr() == nil {
		r.freeQJSRuntime()
	}

	r.closed = true
	r.handle = nil
	r.release()

	if r.registry != nil {
		r.registry.Clear()
		r.registry = nil
	}

	r.mallocFn = nil
	r.freeFn = nil
	r.mem = nil
}

// callHandle invokes a WebAssembly function by name and wraps its result.
func (r *Runtime) callHandle(name string, args ...uint64) *rawHandle {
	return newRawHandle(r, r.call(name, args...))
}

// callUnpack calls a WebAssembly function and unpacks the returned pointer.
func (r *Runtime) callUnpack(name string, args ...uint64) (uint32, uint32) {
	return r.mem.UnpackPtr(r.call(name, args...))
}

// malloc allocates memory in the WebAssembly linear memory and return a pointer to it.
func (r *Runtime) malloc(size uint64) uint64 {
	ptr, err := r.invoke(r.mallocFn, size)
	if err != nil {
		panic(fmt.Errorf("failed to allocate memory: %w", err))
	}

	return ptr
}

// freeHandle releases memory allocated in WebAssembly linear memory. It is a
// no-op once the runtime is closed or interrupted: the memory goes with it.
func (r *Runtime) freeHandle(ptr uint64) {
	if r.interruptErr() != nil {
		return
	}

	if _, err := r.invoke(r.freeFn, ptr); err != nil {
		panic(fmt.Errorf("failed to free memory: %w", err))
	}
}

// freeJsValue frees a JavaScript value in the QuickJS runtime. Like
// FreeHandle, it is a no-op once the runtime is closed or interrupted.
func (r *Runtime) freeJsValue(val uint64) {
	if r.interruptErr() != nil {
		return
	}

	r.call("QJS_FreeValue", r.ctxPtr(), val)
}

// newBytesHandle creates a handle for byte data in WebAssembly memory.
func (r *Runtime) newBytesHandle(b []byte) *rawHandle {
	if len(b) == 0 {
		return nil
	}

	ptr := r.malloc(uint64(len(b)))
	r.mem.MustWrite(uint32(ptr), b)

	return newRawHandle(r, ptr)
}

// newCString copies v into wasm memory as a NUL-terminated string.
func (r *Runtime) newCString(v string) *rawHandle {
	// Allocate len+1 for null terminator
	ptr := r.malloc(uint64(len(v) + 1))

	// Write string data
	r.mem.MustWrite(uint32(ptr), []byte(v))
	// Add null terminator
	r.mem.MustWrite(uint32(ptr)+uint32(len(v)), []byte{0})

	return newRawHandle(r, ptr)
}

// initializeRuntime sets up the runtime components after module instantiation.
func (r *Runtime) initializeRuntime() {
	r.mallocFn = r.export("malloc")
	r.freeFn = r.export("free")
	r.mem = &wasmMemory{store: r.store, mem: r.instance.GetExport(r.store, "memory").Memory()}
	r.handle = r.callHandle(
		"New_QJS",
		uint64(r.option.MemoryLimit),
		uint64(r.option.MaxStackSize),
		uint64(r.option.GCThreshold),
	)

	r.ctxHandle = r.callHandle("QJS_GetContext", r.handle.raw)
}

// export returns the cached export `name`, looking it up on first use.
func (r *Runtime) export(name string) *wasmFunc {
	if f, ok := r.funcs[name]; ok {
		return f
	}

	fn := r.instance.GetFunc(r.store, name)
	if fn == nil {
		panic(fmt.Errorf("WASM function %s not found", name))
	}

	ty := fn.Type(r.store)
	params, results := ty.Params(), ty.Results()
	f := &wasmFunc{
		fn:        fn,
		slots:     max(len(params), len(results)),
		hasResult: len(results) > 0,
	}

	if len(results) == 1 {
		kind := results[0].Kind()
		f.result32 = kind == wasmtime.KindI32 || kind == wasmtime.KindF32
	}

	r.funcs[name] = f

	return f
}

// invoke calls `f` with raw wasm arguments: every value is passed as a
// uint64 holding the little-endian bits of the wasm value. It returns the
// first result encoded the same way.
func (r *Runtime) invoke(f *wasmFunc, args ...uint64) (uint64, error) {
	if err := r.interruptErr(); err != nil {
		return 0, err
	}

	if r.limit != nil && r.callDepth == 0 {
		r.limit.start()
		defer r.limit.stop()
	}

	// Calls nest when JavaScript calls back into Go, so each nesting level
	// gets its own reusable buffer.
	if r.callDepth == len(r.callBufs) {
		r.callBufs = append(r.callBufs, nil)
	}

	n := max(f.slots, len(args))
	if len(r.callBufs[r.callDepth]) < n {
		r.callBufs[r.callDepth] = make([]wasmtime.ValRaw, max(n, 8))
	}

	vals := r.callBufs[r.callDepth][:n]
	clear(vals)

	for i, arg := range args {
		vals[i] = wasmtime.ValRawI64(int64(arg))
	}

	r.callDepth++
	defer func() { r.callDepth-- }()

	if err := f.fn.CallUnchecked(r.store, vals); err != nil {
		if r.limit != nil && r.limit.timedOut {
			return 0, ErrExecutionTimeout
		}

		return 0, fmt.Errorf("wasm call failed: %w", err)
	}

	if !f.hasResult {
		return 0, nil
	}

	result := uint64(vals[0].I64())
	if f.result32 {
		result = uint64(uint32(result))
	}

	return result, nil
}

// interruptErr reports why the runtime can no longer run wasm: it is closed,
// its context is done or a call exceeded MaxExecutionTime. It returns nil
// otherwise.
func (r *Runtime) interruptErr() error {
	if r.closed {
		return ErrRuntimeClosed
	}

	if r.limit != nil && r.limit.timedOut {
		return ErrExecutionTimeout
	}

	if err := r.goCtx.Err(); err != nil {
		return fmt.Errorf("context done: %w", err)
	}

	return nil
}

// recoverInterrupt turns the panic of a wasm call that failed because the
// runtime was interrupted into an error. Other panics propagate. It must be
// deferred directly.
func recoverInterrupt(r *Runtime, err *error) {
	rec := recover()
	if rec == nil {
		return
	}

	if ierr := r.interruptErr(); ierr != nil {
		*err = ierr

		return
	}

	panic(rec)
}

func (r *Runtime) call(name string, args ...uint64) uint64 {
	result, err := r.invoke(r.export(name), args...)
	if err != nil {
		stack := debug.Stack()
		panic(fmt.Errorf("failed to call %s: %w\nstack: %s", name, err, stack))
	}

	return result
}

// Pool manages a collection of reusable QuickJS runtimes.
type Pool struct {
	pools      chan *Runtime
	size       int
	option     Option
	setupFuncs []func(*Runtime) error
	mu         sync.Mutex
}

// NewPool creates a new runtime pool with the specified size and configuration.
func NewPool(size int, option Option, setupFuncs ...func(*Runtime) error) *Pool {
	if size <= 0 {
		panic("pool size must be greater than 0")
	}

	p := &Pool{
		pools:      make(chan *Runtime, size),
		size:       size,
		option:     option,
		setupFuncs: setupFuncs,
	}

	return p
}

// Get returns a runtime from the pool or creates a new one if the pool is empty.
// The caller must call Put() to return the runtime when finished.
func (p *Pool) Get() (*Runtime, error) {
	if p == nil {
		return nil, errors.New("pool is nil")
	}

	// Try to get from pool first
	select {
	case rt := <-p.pools:
		return p.prepareRuntimeForUse(rt), nil
	// Pool is empty, need to create new runtime
	default:
	}

	// Double-check with lock to avoid race conditions
	p.mu.Lock()
	defer p.mu.Unlock()

	select {
	case rt := <-p.pools:
		return p.prepareRuntimeForUse(rt), nil
	default:
		return p.createNewRuntime()
	}
}

// Put returns the runtime back to the pool for reuse.
// If the pool is full, or the runtime was closed or interrupted and cannot be
// reused, the runtime is closed instead.
func (p *Pool) Put(rt *Runtime) {
	if rt == nil {
		return
	}

	if rt.interruptErr() != nil {
		rt.Close()

		return
	}

	// Update stack top before returning to pool
	if rt.handle != nil {
		rt.callHandle("QJS_UpdateStackTop", rt.handle.raw)
	}

	select {
	case p.pools <- rt:
		// Successfully returned to pool
	default:
		// Pool is full, close the runtime
		rt.Close()
	}
}

// prepareRuntimeForUse prepares a pooled runtime for use.
func (p *Pool) prepareRuntimeForUse(rt *Runtime) *Runtime {
	if rt != nil && rt.handle != nil {
		rt.callHandle("QJS_UpdateStackTop", rt.handle.raw)
	}

	return rt
}

// createNewRuntime creates a new runtime with setup functions applied.
func (p *Pool) createNewRuntime() (*Runtime, error) {
	rt, err := New(p.option)
	if err != nil {
		return nil, fmt.Errorf("failed to create new runtime: %w", err)
	}

	// Apply setup functions
	for i, setupFunc := range p.setupFuncs {
		err := setupFunc(rt)
		if err != nil {
			rt.Close()

			return nil, fmt.Errorf("setup function %d failed: %w", i, err)
		}
	}

	return rt, nil
}

// freeQJSRuntime frees the QJS runtime.
func (r *Runtime) freeQJSRuntime() {
	defer func() {
		err := anyToError(recover())
		if err != nil {
			panic(fmt.Errorf("failed to free QJS runtime: %w", err))
		}
	}()

	// Freeing twice is undefined behavior inside QuickJS, so refuse it here.
	if !atomic.CompareAndSwapInt32(&r.handle.freed, 0, 1) {
		panic(errors.New("already freed"))
	}

	r.callHandle("QJS_Free", r.handle.raw)
}

// memory returns the WebAssembly memory interface for this runtime.
func (r *Runtime) memory() *wasmMemory {
	return r.mem
}

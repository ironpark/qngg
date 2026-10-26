package qjs

import (
	_ "embed"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/fastschema/qjs/internal/wasmtime"
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
	malloc    *wasmFunc
	free      *wasmFunc
	mem       *Mem
	stopWatch func() bool
	// argument buffers for invoke, one per level of nested wasm calls
	callBufs  [][]wasmtime.ValRaw
	callDepth int
	option    Option
	handle    *Handle
	context   *Context
	registry  *ProxyRegistry
}

// New creates a QuickJS runtime with optional configuration.
func New(options ...Option) (runtime *Runtime, err error) {
	defer func() {
		rerr := AnyToError(recover())
		if rerr != nil {
			runtime.release()
			runtime = nil
			err = fmt.Errorf("failed to create QJS runtime: %w", rerr)
		}
	}()

	if err := wasmtime.LoadError(); err != nil {
		return nil, fmt.Errorf("failed to load wasmtime: %w", err)
	}

	proxyRegistry := NewProxyRegistry()

	option, err := getRuntimeOption(proxyRegistry, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime options: %w", err)
	}

	proxy, ok := option.ProxyFunction.(JsFunctionProxy)
	if !ok {
		return nil, fmt.Errorf("invalid proxy function type %T, expected qjs.JsFunctionProxy", option.ProxyFunction)
	}

	qjsBytes, hash := wasmBytes, embeddedHash()
	if len(option.QuickJSWasmBytes) > 0 {
		qjsBytes, hash = option.QuickJSWasmBytes, hashBytes(option.QuickJSWasmBytes)
	}

	engine, module, err := getCompiledModule(
		option.CloseOnContextDone,
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
		context:  &Context{Context: option.Context},
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

func (r *Runtime) Raw() uint64 {
	return r.handle.raw
}

// FreeQJSRuntime frees the QJS runtime.
func (r *Runtime) FreeQJSRuntime() {
	defer func() {
		err := AnyToError(recover())
		if err != nil {
			panic(fmt.Errorf("failed to free QJS runtime: %w", err))
		}
	}()

	r.Call("QJS_Free", r.handle.raw)
}

// Mem returns the WebAssembly memory interface for this runtime.
func (r *Runtime) Mem() *Mem {
	return r.mem
}

// String returns a string representation of the runtime.
func (r *Runtime) String() string {
	return fmt.Sprintf("QJSRuntime: %p", r)
}

// Close cleanly shuts down the runtime and frees all associated resources.
func (r *Runtime) Close() {
	if r == nil {
		return
	}

	// Free QJS runtime handle
	if r.handle != nil {
		r.FreeQJSRuntime()
		r.handle = nil
	}

	r.release()

	// Clear references
	if r.context != nil {
		r.context = nil
	}

	if r.registry != nil {
		r.registry.Clear()
		r.registry = nil
	}

	// Clear function references
	r.malloc = nil
	r.free = nil
	r.mem = nil
}

// Load executes a JavaScript file in the runtime's context.
func (r *Runtime) Load(file string, flags ...EvalOptionFunc) (*Value, error) {
	return r.context.Load(file, flags...)
}

// Eval executes JavaScript code in the runtime's context.
func (r *Runtime) Eval(file string, flags ...EvalOptionFunc) (*Value, error) {
	return r.context.Eval(file, flags...)
}

// Compile compiles JavaScript code to bytecode without executing it.
func (r *Runtime) Compile(file string, flags ...EvalOptionFunc) ([]byte, error) {
	return r.context.Compile(file, flags...)
}

// Context returns the JavaScript execution context for this runtime.
func (r *Runtime) Context() *Context {
	return r.context
}

// Call invokes a WebAssembly function by name with the given arguments.
func (r *Runtime) Call(name string, args ...uint64) *Handle {
	return NewHandle(r, r.call(name, args...))
}

// CallUnPack calls a WebAssembly function and unpacks the returned pointer.
func (r *Runtime) CallUnPack(name string, args ...uint64) (uint32, uint32) {
	return r.mem.UnpackPtr(r.Call(name, args...).raw)
}

// Malloc allocates memory in the WebAssembly linear memory and return a pointer to it.
func (r *Runtime) Malloc(size uint64) uint64 {
	ptr, err := r.invoke(r.malloc, size)
	if err != nil {
		panic(fmt.Errorf("failed to allocate memory: %w", err))
	}

	return ptr
}

// FreeHandle releases memory allocated in WebAssembly linear memory.
func (r *Runtime) FreeHandle(ptr uint64) {
	if _, err := r.invoke(r.free, ptr); err != nil {
		panic(fmt.Errorf("failed to free memory: %w", err))
	}
}

// FreeJsValue frees a JavaScript value in the QuickJS runtime.
func (r *Runtime) FreeJsValue(val uint64) {
	r.Call("QJS_FreeValue", r.context.Raw(), val)
}

// NewBytesHandle creates a handle for byte data in WebAssembly memory.
func (r *Runtime) NewBytesHandle(b []byte) *Handle {
	if len(b) == 0 {
		return nil
	}

	ptr := r.Malloc(uint64(len(b)))
	r.mem.MustWrite(uint32(ptr), b)

	return NewHandle(r, ptr)
}

// NewStringHandle creates a handle for string data with null termination.
func (r *Runtime) NewStringHandle(v string) *Handle {
	// Allocate len+1 for null terminator
	ptr := r.Malloc(uint64(len(v) + 1))

	// Write string data
	r.mem.MustWrite(uint32(ptr), []byte(v))
	// Add null terminator
	r.mem.MustWrite(uint32(ptr)+uint32(len(v)), []byte{0})

	return NewHandle(r, ptr)
}

// initializeRuntime sets up the runtime components after module instantiation.
func (r *Runtime) initializeRuntime() {
	r.malloc = r.export("malloc")
	r.free = r.export("free")
	r.mem = &Mem{store: r.store, mem: r.instance.GetExport(r.store, "memory").Memory()}
	r.handle = r.Call(
		"New_QJS",
		uint64(r.option.MemoryLimit),
		uint64(r.option.MaxStackSize),
		uint64(r.option.MaxExecutionTime),
		uint64(r.option.GCThreshold),
	)

	r.context.handle = r.Call("QJS_GetContext", r.handle.raw)
	r.context.runtime = r
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
	if err := r.context.Err(); err != nil {
		return 0, fmt.Errorf("context done: %w", err)
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
// If the pool is full, the runtime is closed to prevent resource leaks.
func (p *Pool) Put(rt *Runtime) {
	if rt == nil {
		return
	}

	// Update stack top before returning to pool
	if rt.handle != nil {
		rt.Call("QJS_UpdateStackTop", rt.handle.raw)
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
		rt.Call("QJS_UpdateStackTop", rt.handle.raw)
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

package qngg

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/ironpark/qngg/internal/wasmtime"
)

//go:embed qjs.wasm
var wasmBytes []byte

// Config configures a Runtime. The zero value is a sandboxed runtime: no
// filesystem, no output, no resource limits beyond those of WebAssembly, and
// interruptible calls.
type Config struct {
	// CWD is a host directory exposed to JavaScript as "/", used for module
	// loading, EvalFile and the qjs:std/qjs:os file APIs. Empty means
	// JavaScript has no filesystem access at all. The directory is read-only
	// unless WritableCWD is set.
	CWD string
	// WritableCWD lets JavaScript create, modify and delete files under CWD.
	WritableCWD bool

	// Stdout and Stderr receive console.log/console.error and std.out/std.err
	// output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer

	// MemoryLimit, MaxStackSize and GCThreshold configure the QuickJS heap
	// limit, stack limit and GC trigger threshold, in bytes. Zero keeps the
	// QuickJS default.
	MemoryLimit  int
	MaxStackSize int
	GCThreshold  int

	// DisableInterruption compiles QuickJS without interruption checks,
	// which makes compute-heavy JavaScript about 30% faster. Contexts passed
	// to calls are then only checked before the call starts, so a runaway
	// script can no longer be stopped.
	DisableInterruption bool

	// CacheDir is a directory where the compiled QuickJS module is cached
	// across processes. Empty disables the on-disk cache.
	CacheDir string
	// DisableBuildCache recompiles the QuickJS module instead of reusing the
	// in-memory compilation of earlier runtimes.
	DisableBuildCache bool

	// quickJSWasm replaces the embedded QuickJS build, for tests.
	quickJSWasm []byte
}

// wasmFunc is a cached export of the QuickJS module.
type wasmFunc struct {
	fn *wasmtime.Func
	// size of the args-and-results buffer: max(params, results)
	slots     int
	hasResult bool
	// whether the single result is a 32-bit value that must be zero-extended
	result32 bool
}

// Runtime is a QuickJS runtime with its JavaScript context, running in its
// own WebAssembly instance. A Runtime is not safe for concurrent use; use a
// Pool to run JavaScript from several goroutines.
type Runtime struct {
	config    Config
	store     *wasmtime.Store
	linker    *wasmtime.Linker
	instance  *wasmtime.Instance
	engine    *wasmtime.Engine
	funcs     map[string]*wasmFunc
	mallocFn  *wasmFunc
	freeFn    *wasmFunc
	mem       *wasmMemory
	interrupt *interrupter
	// argument buffers for invoke, one per level of nested wasm calls
	callBufs  [][]wasmtime.ValRaw
	callDepth int
	qjs       uint64 // QJSRuntime*
	jsCtx     uint64 // JSContext*
	global    *Value
	registry  *proxyRegistry
	closed    bool
}

// New creates a runtime.
func New(config Config) (runtime *Runtime, err error) {
	defer func() {
		if rerr := anyToError(recover()); rerr != nil {
			runtime.release()
			runtime = nil
			err = fmt.Errorf("failed to create QJS runtime: %w", rerr)
		}
	}()

	if err := wasmtime.LoadError(); err != nil {
		return nil, fmt.Errorf("failed to load wasmtime: %w", err)
	}

	qjsBytes, hash := wasmBytes, embeddedHash()
	if len(config.quickJSWasm) > 0 {
		qjsBytes, hash = config.quickJSWasm, hashBytes(config.quickJSWasm)
	}

	engine, module, err := getCompiledModule(
		!config.DisableInterruption,
		config.DisableBuildCache,
		config.CacheDir,
		qjsBytes,
		hash,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create global compiled module: %w", err)
	}

	runtime = &Runtime{
		config:    config,
		engine:    engine,
		interrupt: &interrupter{},
		registry:  newProxyRegistry(),
		funcs:     map[string]*wasmFunc{},
	}

	if err := runtime.instantiate(module); err != nil {
		runtime.release()

		return nil, err
	}

	runtime.initializeRuntime()

	return runtime, nil
}

// String returns a string representation of the runtime.
func (r *Runtime) String() string {
	return fmt.Sprintf("qngg.Runtime(%p)", r)
}

// Close frees the runtime and everything it owns, including values that were
// not freed. Calling Close more than once is a no-op; using the runtime or
// its values afterwards fails with ErrRuntimeClosed.
func (r *Runtime) Close() (err error) {
	if r == nil || r.closed {
		return nil
	}

	// An interrupted runtime cannot run wasm anymore and its QuickJS state
	// may be inconsistent; releasing the store frees its memory as a whole.
	if r.qjs != 0 && r.interruptErr() == nil {
		err = r.freeQJS()
	}

	r.closed = true
	r.interrupt.unwatch()
	r.release()
	r.registry.Clear()
	r.mallocFn, r.freeFn, r.mem = nil, nil, nil

	return err
}

// freeQJS frees the QuickJS runtime.
func (r *Runtime) freeQJS() (err error) {
	defer func() {
		if rerr := anyToError(recover()); rerr != nil {
			err = fmt.Errorf("failed to free QJS runtime: %w", rerr)
		}
	}()

	qjs := r.qjs
	r.qjs = 0
	r.call("QJS_Free", qjs)

	return nil
}

// initializeRuntime sets up the runtime components after module instantiation.
func (r *Runtime) initializeRuntime() {
	r.mallocFn = r.export("malloc")
	r.freeFn = r.export("free")
	r.mem = &wasmMemory{store: r.store, mem: r.instance.GetExport(r.store, "memory").Memory()}
	r.qjs = r.call(
		"New_QJS",
		uint64(r.config.MemoryLimit),
		uint64(r.config.MaxStackSize),
		uint64(r.config.GCThreshold),
	)
	r.jsCtx = r.call("QJS_GetContext", r.qjs)
	r.global = r.borrowValue(r.call("JS_GetGlobalObject", r.jsCtx))
}

// interrupter tracks the context of the current call and stops running
// wasm when it is done. It is kept apart from Runtime so the store's epoch
// callback does not keep the runtime alive.
type interrupter struct {
	ctx context.Context // of the outermost call into wasm, nil outside calls
	err error           // why wasm was interrupted; sticky

	// watched is the context whose cancellation bumps the engine epoch.
	// It stays registered after the call, since callers tend to reuse a
	// context for several calls; a stale bump only costs a check.
	watched     context.Context
	stopWatched func() bool
}

// check runs whenever the engine epoch passes the store's deadline: on any
// watched context being done, in any runtime of the engine.
func (in *interrupter) check() (uint64, error) {
	if in.ctx != nil && in.ctx.Err() != nil {
		in.err = fmt.Errorf("%w: %w", ErrInterrupted, context.Cause(in.ctx))

		return 0, in.err
	}

	return 1, nil
}

// watch makes the cancellation of ctx interrupt wasm.
func (in *interrupter) watch(ctx context.Context, engine *wasmtime.Engine) {
	if ctx == in.watched || ctx.Done() == nil {
		return
	}

	in.unwatch()
	in.watched, in.stopWatched = ctx, context.AfterFunc(ctx, engine.IncrementEpoch)
}

func (in *interrupter) unwatch() {
	if in.stopWatched != nil {
		in.stopWatched()
	}

	in.watched, in.stopWatched = nil, nil
}

// enter starts a call governed by ctx; the caller must defer exit. Calls
// made while wasm is already running, from Go functions called by
// JavaScript, stay governed by the outer call's context.
func (r *Runtime) enter(ctx context.Context) error {
	if err := r.interruptErr(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context done before the call: %w", context.Cause(ctx))
	}

	if r.callDepth == 0 {
		r.interrupt.ctx = ctx
		if !r.config.DisableInterruption {
			r.interrupt.watch(ctx, r.engine)
		}
	}

	return nil
}

// exit ends a call started by enter.
func (r *Runtime) exit() {
	if r.callDepth == 0 {
		r.interrupt.ctx = nil
	}
}

// interruptErr reports why the runtime can no longer run wasm: it is closed
// or a call was interrupted. It returns nil otherwise.
func (r *Runtime) interruptErr() error {
	if r.closed {
		return ErrRuntimeClosed
	}

	return r.interrupt.err
}

// recoverInterrupt turns the panic of a wasm call that failed because the
// runtime is closed or was interrupted into an error. Other panics
// propagate. It must be deferred directly.
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
		if ierr := r.interruptErr(); ierr != nil {
			return 0, ierr
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

// call invokes the wasm export `name`. It panics if the call fails, which
// only happens when the runtime is closed or interrupted, or on a bug.
func (r *Runtime) call(name string, args ...uint64) uint64 {
	result, err := r.invoke(r.export(name), args...)
	if err != nil {
		if errors.Is(err, ErrRuntimeClosed) || errors.Is(err, ErrInterrupted) {
			panic(err)
		}

		panic(fmt.Errorf("failed to call %s: %w\nstack: %s", name, err, debug.Stack()))
	}

	return result
}

// malloc allocates size bytes of wasm memory.
func (r *Runtime) malloc(size uint64) uint64 {
	ptr, err := r.invoke(r.mallocFn, size)
	if err != nil {
		panic(fmt.Errorf("failed to allocate memory: %w", err))
	}

	return ptr
}

// free releases wasm memory from malloc or a C helper. It is a no-op once
// the runtime is closed or interrupted: the memory goes with it.
func (r *Runtime) free(ptr uint64) {
	if ptr == 0 || r.interruptErr() != nil {
		return
	}

	if _, err := r.invoke(r.freeFn, ptr); err != nil {
		panic(fmt.Errorf("failed to free memory: %w", err))
	}
}

// freeValue releases a reference to a JS value. Like free, it is a no-op
// once the runtime is closed or interrupted.
func (r *Runtime) freeValue(raw uint64) {
	if !hasRefCount(raw) || r.interruptErr() != nil {
		return
	}

	r.call("QJS_FreeValue", r.jsCtx, raw)
}

// dupValue returns a new reference to a JS value.
func (r *Runtime) dupValue(raw uint64) uint64 {
	if !hasRefCount(raw) {
		return raw
	}

	return r.call("QJS_CloneValue", r.jsCtx, raw)
}

// newCString copies s into wasm memory as a NUL-terminated string. The
// caller frees it.
func (r *Runtime) newCString(s string) uint64 {
	ptr := r.malloc(uint64(len(s) + 1))
	r.mem.MustWrite(uint32(ptr), append([]byte(s), 0))

	return ptr
}

// newBuffer copies b into wasm memory. The caller frees it.
func (r *Runtime) newBuffer(b []byte) uint64 {
	if len(b) == 0 {
		return 0
	}

	ptr := r.malloc(uint64(len(b)))
	r.mem.MustWrite(uint32(ptr), b)

	return ptr
}

// takeString reads and frees a string handed over by a C helper as a
// malloc'd box holding (addr<<32 | len) of a malloc'd buffer.
func (r *Runtime) takeString(box uint64) string {
	return string(r.takeBytes(box))
}

// takeBytes reads and frees binary data handed over like takeString. The
// result is a copy.
func (r *Runtime) takeBytes(box uint64) []byte {
	if box == 0 {
		return nil
	}

	defer r.free(box)

	addr, size := r.mem.UnpackPtr(box)
	if addr == 0 {
		return nil
	}

	defer r.free(uint64(addr))

	if size == 0 {
		return nil
	}

	return bytes.Clone(r.mem.MustRead(addr, uint64(size)))
}

// newValue wraps a JS value the caller owns.
func (r *Runtime) newValue(raw uint64) *Value {
	return &Value{rt: r, ref: raw}
}

// borrowValue wraps a JS value owned by someone else, such as the arguments
// of a Go function called from JavaScript.
func (r *Runtime) borrowValue(raw uint64) *Value {
	return &Value{rt: r, ref: raw, borrowed: true}
}

// result turns the result of a C call returning a JS value into a Value, or
// into the pending exception if the call threw.
func (r *Runtime) result(raw uint64) (*Value, error) {
	if tagOf(raw) == tagException {
		return nil, r.takeException()
	}

	return r.newValue(raw), nil
}

// status turns the int result of a C call that returns -1 on exception into
// a bool, or into the pending exception.
func (r *Runtime) status(res uint64) (bool, error) {
	if int32(res) < 0 {
		return false, r.takeException()
	}

	return int32(res) != 0, nil
}

// takeException clears the pending exception and returns it as an error.
func (r *Runtime) takeException() error {
	exc := r.newValue(r.call("JS_GetException", r.jsCtx))
	defer exc.Free()

	return exc.toError()
}

// clearException discards a pending exception, if any.
func (r *Runtime) clearException() {
	if uint32(r.call("JS_HasException", r.jsCtx)) != 0 {
		r.freeValue(r.call("JS_GetException", r.jsCtx))
	}
}

package wasmtime

import (
	"errors"
	"io"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Extensions over upstream wasmtime-go. Everything in this file is specific to
// this fork and exposes parts of the Wasmtime C API that upstream does not.

// ValRaw is an untyped WebAssembly value, mirroring the 16-byte
// `wasmtime_val_raw_t` union.
//
// Values are stored little-endian in the low bytes: an `i32` occupies the low
// 4 bytes and an `i64` the low 8 bytes. Use the accessors to convert.
type ValRaw struct {
	lo uint64
	hi uint64
}

// ValRawI32 creates a ValRaw holding an `i32`.
func ValRawI32(v int32) ValRaw { return ValRaw{lo: uint64(uint32(v))} }

// ValRawI64 creates a ValRaw holding an `i64`.
func ValRawI64(v int64) ValRaw { return ValRaw{lo: uint64(v)} }

// I32 returns the value as an `i32`.
func (v ValRaw) I32() int32 { return int32(uint32(v.lo)) }

// I64 returns the value as an `i64`.
func (v ValRaw) I64() int64 { return int64(v.lo) }

// handleTable maps the small integers handed to C as callback data to Go
// values. Entries never change after insertion, so lookups only take a read
// lock.
type handleTable[T any] struct {
	mu   sync.RWMutex
	m    map[int]T
	slab slab
}

func (t *handleTable[T]) add(v T) uintptr {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[int]T)
	}
	idx := t.slab.allocate()
	t.m[idx] = v
	return uintptr(idx)
}

func (t *handleTable[T]) get(idx uintptr) T {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.m[int(idx)]
}

func (t *handleTable[T]) remove(idx uintptr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, int(idx))
	t.slab.deallocate(int(idx))
}

// callFrame holds the SyscallN arguments of one CallUnchecked invocation.
// Keeping them in reused heap memory avoids allocating on every call; the
// `keep*` fields make sure everything the raw arguments point at escapes to
// the heap, where it cannot move while wasm calls back into Go.
type callFrame struct {
	args      [5]uintptr
	trap      *wasm_trap_t
	keepFunc  *Func
	keepSlice []ValRaw
}

// Raw address of `wasmtime_func_call_unchecked`, called through
// purego.SyscallN to avoid reflection on this hot path.
var (
	wasmtime_func_call_unchecked_addr uintptr
	_                                 = bind(&wasmtime_func_call_unchecked_addr, "wasmtime_func_call_unchecked")
)

// CallUnchecked invokes this function without any type checking, using
// `argsAndResults` both to pass the parameters and to receive the results.
//
// The slice must be at least as long as the larger of the function's
// parameter and result counts, and each parameter must have the type the
// function expects. This avoids the per-call type queries and allocations of
// Call, which makes it suited to hot paths that know the signature upfront.
func (f *Func) CallUnchecked(store Storelike, argsAndResults []ValRaw) error {
	data := getDataInStore(store)
	if data.callDepth == len(data.callFrames) {
		data.callFrames = append(data.callFrames, new(callFrame))
	}
	frame := data.callFrames[data.callDepth]
	data.callDepth++

	var ptr *ValRaw
	if len(argsAndResults) > 0 {
		ptr = &argsAndResults[0]
	}
	frame.keepFunc, frame.keepSlice = f, argsAndResults
	frame.args = [5]uintptr{
		uintptr(unsafe.Pointer(store.Context())),
		uintptr(unsafe.Pointer(&f.val)),
		uintptr(unsafe.Pointer(ptr)),
		uintptr(len(argsAndResults)),
		uintptr(unsafe.Pointer(&frame.trap)),
	}
	ret, _, _ := purego.SyscallN(wasmtime_func_call_unchecked_addr, frame.args[:]...)

	trap := frame.trap
	*frame = callFrame{}
	data.callDepth--
	runtime.KeepAlive(store)

	return takeWasmResult(data, trap, *(**wasmtime_error_t)(unsafe.Pointer(&ret)))
}

type funcUncheckedEntry struct {
	callback func(*Caller, []ValRaw) *Trap
}

var (
	gFuncUnchecked handleTable[*funcUncheckedEntry]

	trampolineUncheckedCallback = purego.NewCallback(func(
		env uintptr,
		caller *wasmtime_caller_t,
		argsAndResults uintptr,
		num uintptr,
	) *wasm_trap_t {
		return goTrampolineUnchecked(caller, env, argsAndResults, num)
	})

	finalizeFuncUncheckedCallback = voidCallback(gFuncUnchecked.remove)
)

// FuncNewUnchecked defines a function in this linker like FuncNew, except
// that `f` receives the raw parameter values and writes its results back into
// the same slice, avoiding the conversions FuncNew performs on every call.
//
// The slice holds max(params, results) values. Results must match the result
// types of `ty`; nothing is checked at runtime.
func (l *Linker) FuncNewUnchecked(module, name string, ty *FuncType, f func(*Caller, []ValRaw) *Trap) error {
	idx := gFuncUnchecked.add(&funcUncheckedEntry{callback: f})
	err := wasmtime_linker_define_func_unchecked(
		l.ptr(),
		goStrPtr(module),
		uintptr(len(module)),
		goStrPtr(name),
		uintptr(len(name)),
		ty.ptr(),
		trampolineUncheckedCallback,
		idx,
		finalizeFuncUncheckedCallback,
	)
	runtime.KeepAlive(l)
	runtime.KeepAlive(module)
	runtime.KeepAlive(name)
	runtime.KeepAlive(ty)
	if err == nil {
		return nil
	}

	return mkError(err)
}

func goTrampolineUnchecked(callerPtr *wasmtime_caller_t, env, argsAndResults, num uintptr) *wasm_trap_t {
	caller := &Caller{ptr: callerPtr}
	defer func() { caller.ptr = nil }()
	entry := gFuncUnchecked.get(env)

	var trap *Trap
	var lastPanic interface{}
	func() {
		defer func() { lastPanic = recover() }()
		trap = entry.callback(caller, cSlice[ValRaw](argsAndResults, num))
	}()
	if lastPanic != nil {
		return panicTrap(getDataInStore(caller), lastPanic)
	}
	if trap != nil {
		return trap.intoC()
	}
	return nil
}

var epochDeadlineCallback = purego.NewCallback(func(
	ctx *wasmtime_context_t,
	data uintptr,
	delta *uint64,
	kind *uint8,
) *wasmtime_error_t {
	next, err := storeDataFromContext(ctx).epochDeadline()
	if err != nil {
		return wasmtime_error_new(err.Error())
	}
	*delta = next
	*kind = 0 // WASMTIME_UPDATE_DEADLINE_CONTINUE
	return nil
})

// SetEpochDeadlineCallback configures `f` to run whenever wasm executing in
// this store reaches its epoch deadline (see Config.SetEpochInterruption and
// Store.SetEpochDeadline).
//
// Returning an error interrupts the running wasm and the error is reported
// to the caller of the wasm function. Otherwise execution resumes and the
// deadline is moved `delta` ticks past the current epoch.
func (store *Store) SetEpochDeadlineCallback(f func() (delta uint64, err error)) {
	getDataInStore(store).epochDeadline = f
	wasmtime_store_epoch_deadline_callback(store.ptr(), epochDeadlineCallback, 0, 0)
	runtime.KeepAlive(store)
}

var (
	gWasiWriters handleTable[io.Writer]

	wasiWriteCallback = purego.NewCallback(func(data uintptr, buf *byte, n uintptr) int {
		written, err := gWasiWriters.get(data).Write(cSlice[byte](uintptr(unsafe.Pointer(buf)), n))
		if err != nil && written == 0 {
			return -5 // EIO
		}
		return written
	})

	finalizeWasiWriterCallback = voidCallback(gWasiWriters.remove)
)

// SetStdoutWriter directs the WASI program's stdout to `w`.
func (c *WasiConfig) SetStdoutWriter(w io.Writer) error {
	return c.setCustomWriter(wasi_config_set_stdout_custom, w)
}

// SetStderrWriter directs the WASI program's stderr to `w`.
func (c *WasiConfig) SetStderrWriter(w io.Writer) error {
	return c.setCustomWriter(wasi_config_set_stderr_custom, w)
}

func (c *WasiConfig) setCustomWriter(set func(*wasi_config_t, uintptr, uintptr, uintptr), w io.Writer) error {
	if w == nil {
		return errors.New("nil writer")
	}
	set(c.ptr(), wasiWriteCallback, gWasiWriters.add(w), finalizeWasiWriterCallback)
	runtime.KeepAlive(c)
	return nil
}

// SetMemoryMayMove configures whether linear memories may move in the host
// address space when they grow. When disabled, memories only grow within
// their initial reservation (see the Wasmtime `memory_reservation` option),
// so views obtained from Memory.UnsafeData stay valid across growth; only
// their length becomes outdated.
func (cfg *Config) SetMemoryMayMove(enabled bool) {
	wasmtime_config_memory_may_move_set(cfg.ptr(), enabled)
	runtime.KeepAlive(cfg)
}

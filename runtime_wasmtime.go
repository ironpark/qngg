package qngg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/ironpark/qngg/internal/atomicfile"
	"github.com/ironpark/qngg/internal/wasmtime"
)

// Wasmtime integration: engines, compiled module caching and instantiation.

// moduleKey identifies a compiled QuickJS module: the wasm bytes it was built
// from and whether it was compiled for an engine with epoch interruption.
type moduleKey struct {
	hash          uint64
	interruptible bool
}

var (
	compilationMutex sync.Mutex
	compiledModules  = map[moduleKey]*wasmtime.Module{}
	engines          [2]*wasmtime.Engine
	embeddedHash     = sync.OnceValue(func() uint64 { return hashBytes(wasmBytes) })
)

// getEngine returns the shared engine, creating it on first use. Engines that
// support interrupting wasm when a context is canceled are kept separate,
// since epoch interruption adds checks to the generated code.
func getEngine(interruptible bool) *wasmtime.Engine {
	idx := 0
	if interruptible {
		idx = 1
	}

	if engines[idx] == nil {
		config := wasmtime.NewConfig()
		config.SetEpochInterruption(interruptible)
		// Keep linear memory at a fixed address so Mem can cache its view.
		config.SetMemoryMayMove(false)
		engines[idx] = wasmtime.NewEngineWithConfig(config)
	}

	return engines[idx]
}

// getCompiledModule returns the compiled QuickJS module for the given wasm
// bytes, whose hash is `hash`, reusing an in-memory or on-disk compilation
// when allowed.
func getCompiledModule(
	interruptible bool,
	disableBuildCache bool,
	cacheDir string,
	qjsBytes []byte,
	hash uint64,
) (*wasmtime.Engine, *wasmtime.Module, error) {
	compilationMutex.Lock()
	defer compilationMutex.Unlock()

	engine := getEngine(interruptible)

	key := moduleKey{hash: hash, interruptible: interruptible}
	if module, ok := compiledModules[key]; ok && !disableBuildCache {
		return engine, module, nil
	}

	var cacheFile string

	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("failed to create compilation cache with dir %s: %w", cacheDir, err)
		}

		cacheFile = filepath.Join(cacheDir, fmt.Sprintf(
			"qngg-wasmtime-%s-%016x-%t.cwasm", wasmtime.Version, hash, interruptible,
		))
	}

	module, err := loadOrCompileModule(engine, qjsBytes, cacheFile)
	if err != nil {
		return nil, nil, err
	}

	compiledModules[key] = module

	return engine, module, nil
}

// loadOrCompileModule deserializes the module from cacheFile when possible,
// otherwise compiles it and, if cacheFile is set, stores the compilation.
func loadOrCompileModule(engine *wasmtime.Engine, qjsBytes []byte, cacheFile string) (*wasmtime.Module, error) {
	if cacheFile != "" {
		// A missing, stale or incompatible file is simply recompiled below.
		if module, err := wasmtime.NewModuleDeserializeFile(engine, cacheFile); err == nil {
			return module, nil
		}
	}

	module, err := wasmtime.NewModule(engine, qjsBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile qjs module: %w", err)
	}

	if cacheFile != "" {
		if err := writeModuleCache(module, cacheFile); err != nil {
			return nil, fmt.Errorf("failed to write compilation cache %s: %w", cacheFile, err)
		}
	}

	return module, nil
}

func writeModuleCache(module *wasmtime.Module, cacheFile string) error {
	serialized, err := module.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize module: %w", err)
	}

	err = atomicfile.Write(cacheFile, 0o644, func(w io.Writer) error {
		_, err := bytes.NewReader(serialized).WriteTo(w)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// instantiate creates the store, links WASI and the JS function proxy, and
// instantiates the QuickJS module.
func (r *Runtime) instantiate(engine *wasmtime.Engine, module *wasmtime.Module, proxy jsFunctionProxy) error {
	r.store = wasmtime.NewStore(engine)

	if r.option.CloseOnContextDone || r.option.MaxExecutionTime > 0 {
		// Wasm is interrupted at its next epoch check after the deadline. Any
		// context cancellation or expired time limit bumps the shared engine
		// epoch; stores with nothing to stop push their deadline forward and
		// keep running.
		limit := &execLimit{maxTime: r.option.MaxExecutionTime}
		if r.option.CloseOnContextDone {
			limit.ctx = r.option.Context
			r.stopWatch = context.AfterFunc(limit.ctx, engine.IncrementEpoch)
		}

		if limit.maxTime > 0 {
			limit.timer = time.AfterFunc(limit.maxTime, func() {
				limit.armed.Store(false)
				engine.IncrementEpoch()
			})
			limit.timer.Stop()
			r.limit = limit
		}

		r.store.SetEpochDeadline(1)
		r.store.SetEpochDeadlineCallback(limit.check)
	}

	wasi, err := r.newWasiConfig()
	if err != nil {
		return err
	}

	r.store.SetWasi(wasi)

	r.linker = wasmtime.NewLinker(engine)
	if err := r.linker.DefineWasi(); err != nil {
		return fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	if err := r.defineProxy(proxy); err != nil {
		return fmt.Errorf("failed to setup host module: %w", err)
	}

	if r.instance, err = r.linker.Instantiate(r.store, module); err != nil {
		return fmt.Errorf("failed to instantiate module: %w", err)
	}

	return nil
}

// execLimit decides, at each epoch deadline, whether running wasm must stop.
// It is kept apart from Runtime so the store's callback does not keep the
// runtime alive.
//
// Arming a timer on every call would dominate short calls, so the timer is
// left running across calls: it may fire before the current deadline, in
// which case check re-arms it for the remaining time.
type execLimit struct {
	ctx      context.Context // checked for cancellation when set
	maxTime  time.Duration
	timer    *time.Timer // bumps the engine epoch when it fires
	armed    atomic.Bool // timer is pending
	deadline time.Time   // end of the current call, zero outside calls
	timedOut bool
}

// start sets the deadline of an outermost call into wasm.
func (l *execLimit) start() {
	l.deadline = time.Now().Add(l.maxTime)
	l.arm(l.maxTime)
}

// stop clears the deadline once the outermost call returns.
func (l *execLimit) stop() {
	l.deadline = time.Time{}
}

// arm makes sure the timer fires within d.
func (l *execLimit) arm(d time.Duration) {
	if l.armed.CompareAndSwap(false, true) {
		l.timer.Reset(d)
	}
}

func (l *execLimit) check() (uint64, error) {
	if l.ctx != nil {
		if err := l.ctx.Err(); err != nil {
			return 0, fmt.Errorf("interrupted: %w", err)
		}
	}

	if !l.deadline.IsZero() {
		remaining := time.Until(l.deadline)
		if remaining <= 0 {
			l.timedOut = true

			return 0, ErrExecutionTimeout
		}

		l.arm(remaining)
	}

	return 1, nil
}

// newWasiConfig grants the capabilities configured in the options: the CWD
// mount and stdout/stderr. Anything not granted here is unavailable to wasm.
func (r *Runtime) newWasiConfig() (wasi *wasmtime.WasiConfig, err error) {
	wasi = wasmtime.NewWasiConfig()

	defer func() {
		if err != nil {
			wasi.Close()
		}
	}()

	if cwd := r.option.CWD; cwd != "" {
		if err := wasi.PreopenDir(cwd, "/", r.option.WritableCWD); err != nil {
			return nil, fmt.Errorf("failed to mount %s: %w", cwd, err)
		}
	}

	switch out := r.option.Stdout; out {
	case nil:
	case os.Stdout:
		wasi.InheritStdout()
	default:
		if err := wasi.SetStdoutWriter(out); err != nil {
			return nil, fmt.Errorf("failed to set stdout: %w", err)
		}
	}

	switch out := r.option.Stderr; out {
	case nil:
	case os.Stderr:
		wasi.InheritStderr()
	default:
		if err := wasi.SetStderrWriter(out); err != nil {
			return nil, fmt.Errorf("failed to set stderr: %w", err)
		}
	}

	return wasi, nil
}

// defineProxy links `env.jsFunctionProxy`, through which QuickJS calls Go
// functions:
//
//	extern JSValue jsFunctionProxy(JSContext *ctx, JSValueConst this, int argc, JSValueConst *argv);
func (r *Runtime) defineProxy(proxy jsFunctionProxy) error {
	i32 := wasmtime.NewValType(wasmtime.KindI32)
	i64 := wasmtime.NewValType(wasmtime.KindI64)
	ty := wasmtime.NewFuncType([]*wasmtime.ValType{i32, i64, i32, i32}, []*wasmtime.ValType{i64})

	// The handler lives in a process-wide registry until the linker is freed,
	// so it must not keep the runtime alive: a runtime that is dropped without
	// Close is then still collected, and its finalizers free the linker.
	weakRuntime := weak.Make(r)
	handler := func(_ *wasmtime.Caller, vals []wasmtime.ValRaw) *wasmtime.Trap {
		rt := weakRuntime.Value()
		if rt == nil {
			return wasmtime.NewTrap("qjs runtime has been released")
		}

		rs := proxy(
			rt.goCtx,
			rt.mem,
			uint32(vals[0].I32()),
			uint64(vals[1].I64()),
			uint32(vals[2].I32()),
			uint32(vals[3].I32()),
		)
		vals[0] = wasmtime.ValRawI64(int64(rs))

		return nil
	}

	if err := r.linker.FuncNewUnchecked("env", "jsFunctionProxy", ty, handler); err != nil {
		return fmt.Errorf("failed to define jsFunctionProxy: %w", err)
	}

	return nil
}

// release frees the wasm store and everything it owns without calling into
// wasm, so it is safe on a partially constructed runtime.
func (r *Runtime) release() {
	if r == nil {
		return
	}

	if r.stopWatch != nil {
		r.stopWatch()
		r.stopWatch = nil
	}

	if r.limit != nil {
		r.limit.timer.Stop()
	}

	if r.store != nil {
		r.store.Close()
		r.store = nil
	}

	if r.linker != nil {
		r.linker.Close()
		r.linker = nil
	}

	r.instance = nil
	r.funcs = nil
}

package qjs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"weak"

	"github.com/fastschema/qjs/internal/atomicfile"
	"github.com/fastschema/qjs/internal/wasmtime"
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
			"qjs-wasmtime-%s-%016x-%t.cwasm", wasmtime.Version, hash, interruptible,
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
func (r *Runtime) instantiate(engine *wasmtime.Engine, module *wasmtime.Module, proxy JsFunctionProxy) error {
	r.store = wasmtime.NewStore(engine)

	if r.option.CloseOnContextDone {
		ctx := r.option.Context
		// Wasm is interrupted at its next epoch check after the deadline. Any
		// cancellation bumps the shared engine epoch; stores whose context is
		// still alive just push their deadline forward and keep running.
		r.store.SetEpochDeadline(1)
		r.store.SetEpochDeadlineCallback(func() (uint64, error) {
			if err := ctx.Err(); err != nil {
				return 0, fmt.Errorf("interrupted: %w", err)
			}

			return 1, nil
		})
		r.stopWatch = context.AfterFunc(ctx, engine.IncrementEpoch)
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

	// A missing start function is not an error, matching wazero's behavior.
	if name := r.option.StartFunctionName; name != "" && r.instance.GetFunc(r.store, name) != nil {
		if _, err := r.invoke(r.export(name)); err != nil {
			return fmt.Errorf("failed to instantiate module: start function %s: %w", name, err)
		}
	}

	return nil
}

// newWasiConfig exposes the working directory as "/" and wires stdout/stderr.
func (r *Runtime) newWasiConfig() (wasi *wasmtime.WasiConfig, err error) {
	wasi = wasmtime.NewWasiConfig()

	defer func() {
		if err != nil {
			wasi.Close()
		}
	}()

	if err := wasi.PreopenDir(r.option.CWD, "/", true); err != nil {
		return nil, fmt.Errorf("failed to mount %s: %w", r.option.CWD, err)
	}

	if r.option.Stdout == os.Stdout {
		wasi.InheritStdout()
	} else if err := wasi.SetStdoutWriter(r.option.Stdout); err != nil {
		return nil, fmt.Errorf("failed to set stdout: %w", err)
	}

	if r.option.Stderr == os.Stderr {
		wasi.InheritStderr()
	} else if err := wasi.SetStderrWriter(r.option.Stderr); err != nil {
		return nil, fmt.Errorf("failed to set stderr: %w", err)
	}

	return wasi, nil
}

// defineProxy links `env.jsFunctionProxy`, through which QuickJS calls Go
// functions:
//
//	extern JSValue jsFunctionProxy(JSContext *ctx, JSValueConst this, int argc, JSValueConst *argv);
func (r *Runtime) defineProxy(proxy JsFunctionProxy) error {
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
			rt.context,
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

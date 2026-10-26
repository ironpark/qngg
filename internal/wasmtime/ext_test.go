package wasmtime

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFuncCallUnchecked(t *testing.T) {
	store := NewStore(NewEngine())
	wasm, err := Wat2Wasm(`(module
	  (func (export "add") (param i32 i64) (result i64)
	    (i64.add (i64.extend_i32_s (local.get 0)) (local.get 1)))
	  (func (export "neg") (param i32) (result i32)
	    (i32.sub (i32.const 0) (local.get 0))))`)
	require.NoError(t, err)
	module, err := NewModule(store.Engine, wasm)
	require.NoError(t, err)
	instance, err := NewInstance(store, module, nil)
	require.NoError(t, err)

	vals := []ValRaw{ValRawI32(-2), ValRawI64(10)}
	require.NoError(t, instance.GetFunc(store, "add").CallUnchecked(store, vals))
	require.Equal(t, int64(8), vals[0].I64())

	vals = []ValRaw{ValRawI32(5)}
	require.NoError(t, instance.GetFunc(store, "neg").CallUnchecked(store, vals))
	require.Equal(t, int32(-5), vals[0].I32())
}

func TestLinkerFuncNewUnchecked(t *testing.T) {
	engine := NewEngine()
	store := NewStore(engine)
	linker := NewLinker(engine)
	i32 := NewValType(KindI32)
	i64 := NewValType(KindI64)

	err := linker.FuncNewUnchecked("host", "mul", NewFuncType([]*ValType{i32, i64}, []*ValType{i64}),
		func(_ *Caller, vals []ValRaw) *Trap {
			vals[0] = ValRawI64(int64(vals[0].I32()) * vals[1].I64())
			return nil
		})
	require.NoError(t, err)
	err = linker.FuncNewUnchecked("host", "fail", NewFuncType(nil, nil),
		func(_ *Caller, _ []ValRaw) *Trap { return NewTrap("host failure") })
	require.NoError(t, err)
	err = linker.FuncNewUnchecked("host", "panic", NewFuncType(nil, nil),
		func(_ *Caller, _ []ValRaw) *Trap { panic("host panic") })
	require.NoError(t, err)

	wasm, err := Wat2Wasm(`(module
	  (import "host" "mul" (func $mul (param i32 i64) (result i64)))
	  (import "host" "fail" (func $fail))
	  (import "host" "panic" (func $panic))
	  (func (export "run") (result i64) (call $mul (i32.const 6) (i64.const 7)))
	  (func (export "fail") (call $fail))
	  (func (export "panic") (call $panic)))`)
	require.NoError(t, err)
	module, err := NewModule(engine, wasm)
	require.NoError(t, err)
	instance, err := linker.Instantiate(store, module)
	require.NoError(t, err)

	vals := make([]ValRaw, 1)
	require.NoError(t, instance.GetFunc(store, "run").CallUnchecked(store, vals))
	require.Equal(t, int64(42), vals[0].I64())

	err = instance.GetFunc(store, "fail").CallUnchecked(store, nil)
	require.ErrorContains(t, err, "host failure")

	require.PanicsWithValue(t, "host panic", func() {
		_ = instance.GetFunc(store, "panic").CallUnchecked(store, nil)
	})
}

func TestEpochDeadlineCallback(t *testing.T) {
	config := NewConfig()
	config.SetEpochInterruption(true)
	engine := NewEngineWithConfig(config)
	store := NewStore(engine)
	store.SetEpochDeadline(1)

	ticks := 0
	store.SetEpochDeadlineCallback(func() (uint64, error) {
		ticks++
		if ticks < 3 {
			// A zero delta puts the deadline at the current epoch, so the
			// callback fires again at the next check.
			return 0, nil
		}
		return 0, errors.New("stopped by callback")
	})

	wasm, err := Wat2Wasm(`(module (func (export "spin") (loop br 0)))`)
	require.NoError(t, err)
	module, err := NewModule(engine, wasm)
	require.NoError(t, err)
	instance, err := NewInstance(store, module, nil)
	require.NoError(t, err)

	engine.IncrementEpoch()
	err = instance.GetFunc(store, "spin").CallUnchecked(store, nil)
	require.ErrorContains(t, err, "stopped by callback")
	require.Equal(t, 3, ticks)
}

func TestWasiWriters(t *testing.T) {
	engine := NewEngine()
	store := NewStore(engine)
	linker := NewLinker(engine)
	require.NoError(t, linker.DefineWasi())

	var stdout, stderr bytes.Buffer
	wasi := NewWasiConfig()
	require.NoError(t, wasi.SetStdoutWriter(&stdout))
	require.NoError(t, wasi.SetStderrWriter(&stderr))
	store.SetWasi(wasi)

	wasm, err := Wat2Wasm(`(module
	  (import "wasi_snapshot_preview1" "fd_write" (func $fd_write (param i32 i32 i32 i32) (result i32)))
	  (memory (export "memory") 1)
	  (data (i32.const 16) "out")
	  (data (i32.const 32) "err!")
	  (func (export "run")
	    (i32.store (i32.const 0) (i32.const 16))
	    (i32.store (i32.const 4) (i32.const 3))
	    (drop (call $fd_write (i32.const 1) (i32.const 0) (i32.const 1) (i32.const 8)))
	    (i32.store (i32.const 0) (i32.const 32))
	    (i32.store (i32.const 4) (i32.const 4))
	    (drop (call $fd_write (i32.const 2) (i32.const 0) (i32.const 1) (i32.const 8)))))`)
	require.NoError(t, err)
	module, err := NewModule(engine, wasm)
	require.NoError(t, err)
	instance, err := linker.Instantiate(store, module)
	require.NoError(t, err)

	_, err = instance.GetFunc(store, "run").Call(store)
	require.NoError(t, err)
	require.Equal(t, "out", stdout.String())
	require.Equal(t, "err!", stderr.String())
}

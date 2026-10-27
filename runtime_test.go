package qngg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evalInt(t *testing.T, rt *Runtime, code string) int32 {
	t.Helper()
	val, err := rt.Eval(context.Background(), "test.js", code)
	require.NoError(t, err)
	defer val.Free()
	return val.Int32()
}

func TestRuntime(t *testing.T) {
	t.Run("Creation", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		assert.Contains(t, rt.String(), "qngg.Runtime")
		assert.Equal(t, int32(42), evalInt(t, rt, "40 + 2"))
	})

	t.Run("CloseIsIdempotentAndNilSafe", func(t *testing.T) {
		rt := must(New(Config{}))
		assert.NoError(t, rt.Close())
		assert.NoError(t, rt.Close())

		var nilRuntime *Runtime
		assert.NoError(t, nilRuntime.Close())
	})

	t.Run("UseAfterClose", func(t *testing.T) {
		rt := must(New(Config{}))
		val := must(rt.Eval(context.Background(), "test.js", "({a: 1})"))
		require.NoError(t, rt.Close())

		_, err := rt.Eval(context.Background(), "test.js", "1")
		assert.ErrorIs(t, err, ErrRuntimeClosed)
		_, err = val.Get("a")
		assert.ErrorIs(t, err, ErrRuntimeClosed)
		assert.NotPanics(t, val.Free, "values freed after Close go with the runtime")
	})

	t.Run("TruncatedWasmBytes", func(t *testing.T) {
		_, err := New(Config{quickJSWasm: []byte{0x00, 0x61, 0x73}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to compile qjs module")
	})

	t.Run("InvalidWasmDoesNotPoisonCache", func(t *testing.T) {
		_, err := New(Config{quickJSWasm: []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}})
		assert.Error(t, err)

		rt, _ := setupRuntime(t)
		assert.Equal(t, int32(42), evalInt(t, rt, "42"))
	})

	t.Run("LookupNonExistentExport", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		assert.Panics(t, func() { rt.lookup("NonExistentFunction") })
	})

	t.Run("MallocAndFree", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		ptr := rt.malloc(1024)
		assert.NotZero(t, ptr)
		rt.free(ptr)
	})

	t.Run("CacheDir", func(t *testing.T) {
		cacheDir := t.TempDir()
		rt := must(New(Config{CacheDir: cacheDir, DisableBuildCache: true}))
		assert.Equal(t, int32(42), evalInt(t, rt, "21 + 21"))
		require.NoError(t, rt.Close())

		entries, err := os.ReadDir(cacheDir)
		require.NoError(t, err)
		assert.NotEmpty(t, entries)
	})

	t.Run("InvalidCacheDir", func(t *testing.T) {
		// A directory can't be created under a regular file on any platform.
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o644))
		_, err := New(Config{CacheDir: filepath.Join(file, "cache"), DisableBuildCache: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create compilation cache")
	})

	t.Run("IndependentRuntimesInParallel", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := range 10 {
			wg.Go(func() {
				rt := must(New(Config{}))
				defer rt.Close()

				type data struct {
					ThreadID int
					Name     string
				}
				noErr(rt.Set("data", data{ThreadID: i, Name: fmt.Sprintf("thread%d", i)}))
				assert.Equal(t, int32(i), evalInt(t, rt, "data.ThreadID"))
			})
		}
		wg.Wait()
	})
}

func TestPool(t *testing.T) {
	bg := context.Background()

	t.Run("InvalidSize", func(t *testing.T) {
		_, err := NewPool(0, Config{}, nil)
		assert.Error(t, err)
	})

	t.Run("SetupAndReuse", func(t *testing.T) {
		setups := 0
		pool := must(NewPool(1, Config{}, func(rt *Runtime) error {
			setups++
			return rt.Set("initialized", true)
		}))
		defer pool.Close()

		for range 3 {
			require.NoError(t, pool.Do(bg, func(rt *Runtime) error {
				assert.Equal(t, int32(1), evalInt(t, rt, "initialized ? 1 : 0"))
				return nil
			}))
		}
		assert.Equal(t, 1, setups, "the idle runtime is reused")
	})

	t.Run("StatePersistsAcrossReuse", func(t *testing.T) {
		pool := must(NewPool(1, Config{}, nil))
		defer pool.Close()

		noErr(pool.Do(bg, func(rt *Runtime) error { return rt.Set("testValue", 42) }))
		noErr(pool.Do(bg, func(rt *Runtime) error {
			assert.Equal(t, int32(42), evalInt(t, rt, "testValue"))
			return nil
		}))
	})

	t.Run("ReturnsFnError", func(t *testing.T) {
		pool := must(NewPool(1, Config{}, nil))
		defer pool.Close()

		want := errors.New("boom")
		assert.ErrorIs(t, pool.Do(bg, func(*Runtime) error { return want }), want)
	})

	t.Run("SetupError", func(t *testing.T) {
		want := errors.New("setup failed")
		pool := must(NewPool(1, Config{}, func(*Runtime) error { return want }))
		defer pool.Close()

		err := pool.Do(bg, func(*Runtime) error {
			t.Fatal("fn must not run")
			return nil
		})
		assert.ErrorIs(t, err, want)
	})

	t.Run("InvalidWasm", func(t *testing.T) {
		pool := must(NewPool(1, Config{quickJSWasm: []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}}, nil))
		assert.Error(t, pool.Do(bg, func(*Runtime) error { return nil }))
	})

	t.Run("DiscardsInterruptedRuntimes", func(t *testing.T) {
		pool := must(NewPool(1, Config{}, nil))
		defer pool.Close()

		var first *Runtime
		err := pool.Do(bg, func(rt *Runtime) error {
			first = rt
			ctx, cancel := context.WithTimeout(bg, timeoutForTest)
			defer cancel()
			_, err := rt.Eval(ctx, "loop.js", "for (;;) {}")
			return err
		})
		require.ErrorIs(t, err, ErrInterrupted)

		noErr(pool.Do(bg, func(rt *Runtime) error {
			assert.NotSame(t, first, rt)
			assert.Equal(t, int32(2), evalInt(t, rt, "1 + 1"))
			return nil
		}))
	})

	t.Run("DiscardsRuntimeOnPanic", func(t *testing.T) {
		pool := must(NewPool(1, Config{}, nil))
		defer pool.Close()

		var first *Runtime
		assert.Panics(t, func() {
			_ = pool.Do(bg, func(rt *Runtime) error {
				first = rt
				panic("boom")
			})
		})
		noErr(pool.Do(bg, func(rt *Runtime) error {
			assert.NotSame(t, first, rt)
			return nil
		}))
	})

	t.Run("Closed", func(t *testing.T) {
		pool := must(NewPool(1, Config{}, nil))
		require.NoError(t, pool.Close())
		assert.Error(t, pool.Do(bg, func(*Runtime) error { return nil }))
	})

	t.Run("Concurrent", func(t *testing.T) {
		pool := must(NewPool(3, Config{}, func(rt *Runtime) error {
			result, err := rt.Eval(bg, "<main>", `export default { hello: (i, getEntity) => getEntity().id }`, AsModule())
			if err != nil {
				return err
			}
			defer result.Free()
			return rt.Global().Set("exports", result)
		}))
		defer pool.Close()

		var wg sync.WaitGroup
		for i := range 20 {
			wg.Go(func() {
				err := pool.Do(bg, func(rt *Runtime) error {
					exports := must(rt.Get("exports"))
					defer exports.Free()

					getEntity := func() map[string]any { return map[string]any{"id": i} }
					result, err := exports.Invoke(bg, "hello", i, getEntity)
					if err != nil {
						return err
					}
					defer result.Free()
					assert.Equal(t, int32(i), result.Int32())
					return nil
				})
				assert.NoError(t, err)
			})
		}
		wg.Wait()
	})
}

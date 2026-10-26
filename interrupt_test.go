package qngg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const timeoutForTest = 100 * time.Millisecond

func TestInterrupt(t *testing.T) {
	bg := context.Background()

	t.Run("DeadlineStopsInfiniteLoop", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		ctx, cancel := context.WithTimeout(bg, timeoutForTest)
		defer cancel()

		start := time.Now()
		_, err := rt.Eval(ctx, "loop.js", `for (;;) {}`)
		require.ErrorIs(t, err, ErrInterrupted)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Less(t, time.Since(start), 5*time.Second)

		// The interrupted runtime refuses further work, but can be closed.
		_, err = rt.Eval(bg, "next.js", `1 + 1`)
		require.ErrorIs(t, err, ErrInterrupted)
		assert.NoError(t, rt.Close())
	})

	t.Run("CancelStopsInfiniteLoop", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		ctx, cancel := context.WithCancelCause(bg)
		cause := errors.New("stop")
		time.AfterFunc(timeoutForTest, func() { cancel(cause) })

		_, err := rt.Eval(ctx, "loop.js", `for (;;) {}`)
		require.ErrorIs(t, err, ErrInterrupted)
		assert.ErrorIs(t, err, cause)
	})

	t.Run("DoneContextIsCheckedFirst", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		ctx, cancel := context.WithCancel(bg)
		cancel()

		_, err := rt.Eval(ctx, "test.js", `1`)
		require.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, ErrInterrupted, "nothing ran, so the runtime is still usable")
		assert.Equal(t, int32(1), evalInt(t, rt, "1"))
	})

	t.Run("EachCallHasItsOwnContext", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		for range 3 {
			ctx, cancel := context.WithTimeout(bg, 200*time.Millisecond)
			v, err := rt.Eval(ctx, "busy.js", `{ const end = Date.now() + 60; while (Date.now() < end) {} } 42`)
			cancel()
			require.NoError(t, err)
			assert.Equal(t, int32(42), v.Int32())
			v.Free()
		}
	})

	t.Run("TimeInGoFunctionsCounts", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		noErr(rt.SetFunc("slow", func(*FunctionCall) (*Value, error) {
			time.Sleep(3 * timeoutForTest)
			return nil, nil
		}))

		ctx, cancel := context.WithTimeout(bg, timeoutForTest)
		defer cancel()
		_, err := rt.Eval(ctx, "slow.js", `slow(); for (let i = 0; i < 1e6; i++) {}`)
		require.ErrorIs(t, err, ErrInterrupted)
	})

	t.Run("GoFunctionsSeeTheCallContext", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		type key struct{}
		noErr(rt.Set("fromCtx", func(ctx context.Context) string { return ctx.Value(key{}).(string) }))

		ctx := context.WithValue(bg, key{}, "hello")
		v, err := rt.Eval(ctx, "test.js", `fromCtx()`)
		require.NoError(t, err)
		defer v.Free()
		assert.Equal(t, "hello", v.String())
	})

	t.Run("OtherRuntimesKeepRunning", func(t *testing.T) {
		victim, _ := setupRuntime(t)
		other, _ := setupRuntime(t)

		ctx, cancel := context.WithTimeout(bg, timeoutForTest)
		defer cancel()
		_, err := victim.Eval(ctx, "loop.js", `for (;;) {}`)
		require.ErrorIs(t, err, ErrInterrupted)
		assert.Equal(t, int32(3), evalInt(t, other, "1 + 2"))
	})

	t.Run("CallAndAwaitAreInterruptible", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		fn := must(rt.Eval(bg, "fn.js", `(() => { for (;;) {} })`))
		ctx, cancel := context.WithTimeout(bg, timeoutForTest)
		defer cancel()
		_, err := fn.Call(ctx, nil)
		require.ErrorIs(t, err, ErrInterrupted)
	})

	t.Run("DisableInterruption", func(t *testing.T) {
		rt := must(New(Config{DisableInterruption: true}))
		defer rt.Close()

		ctx, cancel := context.WithTimeout(bg, time.Hour)
		defer cancel()
		v, err := rt.Eval(ctx, "test.js", `6 * 7`)
		require.NoError(t, err)
		defer v.Free()
		assert.Equal(t, int32(42), v.Int32())
	})
}

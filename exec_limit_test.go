package qngg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxExecutionTime(t *testing.T) {
	t.Run("interrupts a long running script", func(t *testing.T) {
		rt := must(New(Option{MaxExecutionTime: 100 * time.Millisecond}))
		defer rt.Close()

		start := time.Now()
		_, err := rt.Eval("loop.js", Code(`for (;;) {}`))
		require.ErrorIs(t, err, ErrExecutionTimeout)
		assert.Less(t, time.Since(start), 5*time.Second)

		// The interrupted runtime refuses further work.
		_, err = rt.Eval("next.js", Code(`1 + 1`))
		require.ErrorIs(t, err, ErrExecutionTimeout)
	})

	t.Run("limit applies to each call", func(t *testing.T) {
		rt := must(New(Option{MaxExecutionTime: 200 * time.Millisecond}))
		defer rt.Close()

		ctx := rt
		for range 5 {
			v, err := ctx.Eval("busy.js", Code(`
				{
					const end = Date.now() + 60;
					while (Date.now() < end) {}
				}
				42
			`))
			require.NoError(t, err)
			assert.Equal(t, int32(42), v.Int32())
			v.Free()
		}
	})

	t.Run("time spent in Go functions counts", func(t *testing.T) {
		rt := must(New(Option{MaxExecutionTime: 100 * time.Millisecond}))
		defer rt.Close()

		ctx := rt
		ctx.SetFunc("slow", func(this *This) (*Value, error) {
			time.Sleep(300 * time.Millisecond)

			return this.Runtime().NewUndefined(), nil
		})

		_, err := ctx.Eval("slow.js", Code(`slow(); for (let i = 0; i < 1e6; i++) {}`))
		require.ErrorIs(t, err, ErrExecutionTimeout)
	})
}

func TestCloseAfterContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := must(New(Option{Context: ctx, CloseOnContextDone: true}))

	cancel()

	assert.NotPanics(t, rt.Close)
}

func TestUseAfterClose(t *testing.T) {
	rt := must(New())
	val := must(rt.Eval("test.js", Code("({a: 1})")))
	rt.Close()
	rt.Close() // idempotent

	_, err := rt.Eval("test.js", Code("1"))
	assert.ErrorIs(t, err, ErrRuntimeClosed)
	assert.NotPanics(t, val.Free, "values freed after Close go with the runtime")
}

func TestPoolDiscardsInterruptedRuntimes(t *testing.T) {
	pool := NewPool(1, Option{MaxExecutionTime: 50 * time.Millisecond})

	rt := must(pool.Get())
	_, err := rt.Eval("loop.js", Code("for (;;) {}"))
	require.ErrorIs(t, err, ErrExecutionTimeout)
	pool.Put(rt) // must not panic, and must not be handed out again

	next := must(pool.Get())
	defer pool.Put(next)
	assert.NotSame(t, rt, next)
	val, err := next.Eval("test.js", Code("1 + 1"))
	require.NoError(t, err)
	defer val.Free()
	assert.Equal(t, int32(2), val.Int32())
}

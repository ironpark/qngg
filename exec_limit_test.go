package qngg_test

import (
	"context"
	"testing"
	"time"

	"github.com/ironpark/qngg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxExecutionTime(t *testing.T) {
	t.Run("interrupts a long running script", func(t *testing.T) {
		rt := must(qngg.New(qngg.Option{MaxExecutionTime: 100 * time.Millisecond}))
		defer rt.Close()

		start := time.Now()
		_, err := rt.Context().Eval("loop.js", qngg.Code(`for (;;) {}`))
		require.ErrorIs(t, err, qngg.ErrExecutionTimeout)
		assert.Less(t, time.Since(start), 5*time.Second)

		// The interrupted runtime refuses further work.
		_, err = rt.Context().Eval("next.js", qngg.Code(`1 + 1`))
		require.ErrorIs(t, err, qngg.ErrExecutionTimeout)
	})

	t.Run("limit applies to each call", func(t *testing.T) {
		rt := must(qngg.New(qngg.Option{MaxExecutionTime: 200 * time.Millisecond}))
		defer rt.Close()

		ctx := rt.Context()
		for range 5 {
			v, err := ctx.Eval("busy.js", qngg.Code(`
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
		rt := must(qngg.New(qngg.Option{MaxExecutionTime: 100 * time.Millisecond}))
		defer rt.Close()

		ctx := rt.Context()
		ctx.SetFunc("slow", func(this *qngg.This) (*qngg.Value, error) {
			time.Sleep(300 * time.Millisecond)

			return this.Context().NewUndefined(), nil
		})

		_, err := ctx.Eval("slow.js", qngg.Code(`slow(); for (let i = 0; i < 1e6; i++) {}`))
		require.ErrorIs(t, err, qngg.ErrExecutionTimeout)
	})
}

func TestCloseAfterContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := must(qngg.New(qngg.Option{Context: ctx, CloseOnContextDone: true}))

	cancel()

	assert.NotPanics(t, rt.Close)
}

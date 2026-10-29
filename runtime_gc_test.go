package qngg

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeCollectedWithoutClose(t *testing.T) {
	collected := make(chan struct{})
	func() {
		rt, err := New(Config{})
		if err != nil {
			t.Fatal(err)
		}
		runtime.AddCleanup(rt, func(ch chan struct{}) { close(ch) }, collected)
	}()

	for range 50 {
		runtime.GC()
		select {
		case <-collected:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("runtime dropped without Close was never collected")
}

func TestConvertedJSFunctionReleasedWhenCollected(t *testing.T) {
	rt := must(New(Config{}))
	defer rt.Close()

	calls := 0
	must(0, rt.Set("take", func(cb func() int) { calls += cb() }))

	val, err := rt.Eval(context.Background(), "setup.js", `
		globalThis.refs = [];
		for (let i = 0; i < 10; i++) {
			const cb = () => 1;
			refs.push(new WeakRef(cb));
			take(cb);
		}
	`)
	require.NoError(t, err)
	val.Free()
	require.Equal(t, 10, calls)

	for range 50 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)

		val, err := rt.Eval(context.Background(), "check.js", `refs.filter(r => r.deref() !== undefined).length`)
		require.NoError(t, err)
		alive := val.Int32()
		val.Free()

		if alive == 0 {
			return
		}
	}
	t.Fatal("JavaScript functions converted to Go functions were never released")
}

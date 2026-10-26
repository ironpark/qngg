package qjs_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/fastschema/qjs"
)

func TestRuntimeCollectedWithoutClose(t *testing.T) {
	collected := make(chan struct{})
	func() {
		rt, err := qjs.New()
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

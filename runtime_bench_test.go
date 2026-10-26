package qngg

import (
	"testing"
	"time"
)

func BenchmarkRuntimeNew(b *testing.B) {
	for b.Loop() {
		rt, err := New()
		if err != nil {
			b.Fatal(err)
		}
		rt.Close()
	}
}

// timeLimit enables MaxExecutionTime, which arms a timer on every call into
// wasm and turns on epoch interruption in the generated code.
var timeLimit = Option{MaxExecutionTime: time.Minute}

func BenchmarkEvalFactorial(b *testing.B) { benchEvalFactorial(b) }

func BenchmarkEvalFactorialWithTimeLimit(b *testing.B) { benchEvalFactorial(b, timeLimit) }

func benchEvalFactorial(b *testing.B, options ...Option) {
	rt, _ := New(options...)
	defer rt.Close()
	for b.Loop() {
		v, err := rt.Eval("f.js", Code(`(() => {
			function f(n) { return n <= 1 ? 1 : n * f(n - 1) }
			let s = 0; for (let i = 0; i < 100000; i++) s += f(10); return s })()`))
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkGoFunctionCall(b *testing.B) {
	rt, _ := New()
	defer rt.Close()
	ctx := rt
	ctx.SetFunc("add", func(this *This) (*Value, error) {
		a := this.Args()
		return ctx.NewInt32(a[0].Int32() + a[1].Int32()), nil
	})
	for b.Loop() {
		v, err := rt.Eval("g.js", Code(`(() => { let s = 0; for (let i = 0; i < 10000; i++) s = add(s, 1); return s })()`))
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkValueProperties(b *testing.B) { benchValueProperties(b) }

func BenchmarkValuePropertiesWithTimeLimit(b *testing.B) { benchValueProperties(b, timeLimit) }

func benchValueProperties(b *testing.B, options ...Option) {
	rt, _ := New(options...)
	defer rt.Close()
	ctx := rt
	for b.Loop() {
		o := ctx.NewObject()
		for i := 0; i < 100; i++ {
			o.SetPropertyStr("k", ctx.NewString("hello world"))
			_ = o.GetPropertyStr("k").String()
		}
		o.Free()
	}
}

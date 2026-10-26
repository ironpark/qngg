package qngg

import (
	"context"
	"testing"
)

func BenchmarkRuntimeNew(b *testing.B) {
	for b.Loop() {
		rt, err := New(Config{})
		if err != nil {
			b.Fatal(err)
		}
		rt.Close()
	}
}

// noInterrupt compiles QuickJS without the epoch checks that make calls
// interruptible.
var noInterrupt = Config{DisableInterruption: true}

func BenchmarkEvalFactorial(b *testing.B) { benchEvalFactorial(b, Config{}) }

func BenchmarkEvalFactorialNoInterrupt(b *testing.B) { benchEvalFactorial(b, noInterrupt) }

func benchEvalFactorial(b *testing.B, config Config) {
	rt, _ := New(config)
	defer rt.Close()
	for b.Loop() {
		v, err := rt.Eval(context.Background(), "f.js", `(() => {
			function f(n) { return n <= 1 ? 1 : n * f(n - 1) }
			let s = 0; for (let i = 0; i < 100000; i++) s += f(10); return s })()`)
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkGoFunctionCall(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	noErr(rt.SetFunc("add", func(call *FunctionCall) (*Value, error) {
		return rt.NewInt32(call.Arg(0).Int32() + call.Arg(1).Int32()), nil
	}))
	for b.Loop() {
		v, err := rt.Eval(context.Background(), "g.js", `(() => { let s = 0; for (let i = 0; i < 10000; i++) s = add(s, 1); return s })()`)
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkValueProperties(b *testing.B) { benchValueProperties(b, Config{}) }

func BenchmarkValuePropertiesNoInterrupt(b *testing.B) { benchValueProperties(b, noInterrupt) }

func benchValueProperties(b *testing.B, config Config) {
	rt, _ := New(config)
	defer rt.Close()
	for b.Loop() {
		o := rt.NewObject()
		for range 100 {
			s := rt.NewString("hello world")
			noErr(o.Set("k", s))
			s.Free()

			v := must(o.Get("k"))
			_ = v.String()
			v.Free()
		}
		o.Free()
	}
}

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

type benchRecord struct {
	ID    int      `json:"id"`
	Name  string   `json:"name"`
	Score float64  `json:"score"`
	Tags  []string `json:"tags"`
	Child *benchRecord
}

var benchRecordValue = benchRecord{
	ID: 1, Name: "alice", Score: 9.5, Tags: []string{"a", "b", "c"},
	Child: &benchRecord{ID: 2, Name: "bob", Tags: []string{"d"}},
}

func BenchmarkToValueStruct(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	for b.Loop() {
		v, err := rt.ToValue(benchRecordValue)
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkAsStruct(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	obj := must(rt.Eval(context.Background(), "o.js",
		`({id: 1, name: "alice", score: 9.5, tags: ["a", "b", "c"], Child: {id: 2, name: "bob", tags: ["d"]}})`))
	defer obj.Free()
	for b.Loop() {
		if _, err := obj.As[benchRecord](); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAsAny(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	obj := must(rt.Eval(context.Background(), "o.js",
		`({n: 1, s: "x", d: new Date(0), m: new Map([["k", 1]]), a: [1, 2, 3], u: new Uint8Array(4), o: {x: null}})`))
	defer obj.Free()
	for b.Loop() {
		if _, err := obj.As[map[string]any](); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCallFromGo(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	fn := must(rt.Eval(context.Background(), "f.js", `(a, b) => a + b`))
	defer fn.Free()
	ctx := context.Background()
	for b.Loop() {
		for range 100 {
			v, err := fn.Call(ctx, nil, 1, 2)
			if err != nil {
				b.Fatal(err)
			}
			v.Free()
		}
	}
}

func BenchmarkNewFunction(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	fn := func(*FunctionCall) (*Value, error) { return nil, nil }
	for b.Loop() {
		for range 100 {
			rt.NewFunction(fn).Free()
		}
	}
}

func BenchmarkReflectFuncCall(b *testing.B) {
	rt, _ := New(Config{})
	defer rt.Close()
	noErr(rt.Set("add", func(a, b int) int { return a + b }))
	for b.Loop() {
		v, err := rt.Eval(context.Background(), "g.js", `(() => { let s = 0; for (let i = 0; i < 1000; i++) s = add(s, 1); return s })()`)
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

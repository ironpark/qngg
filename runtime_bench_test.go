package qjs_test

import (
	"testing"

	"github.com/fastschema/qjs"
)

func BenchmarkRuntimeNew(b *testing.B) {
	for b.Loop() {
		rt, err := qjs.New()
		if err != nil {
			b.Fatal(err)
		}
		rt.Close()
	}
}

func BenchmarkEvalFactorial(b *testing.B) {
	rt, _ := qjs.New()
	defer rt.Close()
	for b.Loop() {
		v, err := rt.Eval("f.js", qjs.Code(`(() => {
			function f(n) { return n <= 1 ? 1 : n * f(n - 1) }
			let s = 0; for (let i = 0; i < 100000; i++) s += f(10); return s })()`))
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkGoFunctionCall(b *testing.B) {
	rt, _ := qjs.New()
	defer rt.Close()
	ctx := rt.Context()
	ctx.SetFunc("add", func(this *qjs.This) (*qjs.Value, error) {
		a := this.Args()
		return ctx.NewInt32(a[0].Int32() + a[1].Int32()), nil
	})
	for b.Loop() {
		v, err := rt.Eval("g.js", qjs.Code(`(() => { let s = 0; for (let i = 0; i < 10000; i++) s = add(s, 1); return s })()`))
		if err != nil {
			b.Fatal(err)
		}
		v.Free()
	}
}

func BenchmarkValueProperties(b *testing.B) {
	rt, _ := qjs.New()
	defer rt.Close()
	ctx := rt.Context()
	for b.Loop() {
		o := ctx.NewObject()
		for i := 0; i < 100; i++ {
			o.SetPropertyStr("k", ctx.NewString("hello world"))
			_ = o.GetPropertyStr("k").String()
		}
		o.Free()
	}
}

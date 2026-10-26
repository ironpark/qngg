package qngg

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of the bridge between Go and the C helpers: scratch buffers,
// results larger than the output buffers, and Go values referenced from
// JavaScript.

func TestRepeatedNestedConversion(t *testing.T) {
	// Converting objects used to release the property list of
	// JS_GetOwnPropertyNames with free instead of js_free, corrupting the
	// QuickJS heap after a few dozen conversions.
	type child struct {
		ID int `json:"id"`
	}

	type parent struct {
		Tags  []string `json:"tags"`
		Child *child
	}

	rt, _ := setupRuntime(t)
	obj := must(rt.Eval(context.Background(), "o.js", `({tags: ["a", "b"], Child: {id: 2}})`))
	defer obj.Free()

	for range 500 {
		got, err := obj.As[parent]()
		require.NoError(t, err)
		require.Equal(t, parent{Tags: []string{"a", "b"}, Child: &child{ID: 2}}, got)
	}
}

func TestLargeResults(t *testing.T) {
	rt, _ := setupRuntime(t)
	bg := context.Background()

	t.Run("String", func(t *testing.T) {
		big := strings.Repeat("é", 3*outBufSize)
		s := rt.NewString(big)
		defer s.Free()
		assert.Equal(t, big, s.String())

		json, err := s.MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, `"`+big+`"`, string(json))
	})

	t.Run("ArrayBuffer", func(t *testing.T) {
		data := make([]byte, 3*outBufSize)
		for i := range data {
			data[i] = byte(i)
		}

		buf := rt.NewArrayBuffer(data)
		defer buf.Free()
		assert.Equal(t, data, must(buf.Bytes()))
	})

	t.Run("ManyKeys", func(t *testing.T) {
		obj := must(rt.Eval(bg, "keys.js", `const o = {}; for (let i = 0; i < 3000; i++) o["k" + i] = i; o`))
		defer obj.Free()

		keys := must(obj.Keys())
		require.Len(t, keys, 3000)
		assert.Equal(t, "k2999", keys[2999])

		m := must(obj.As[map[string]int]())
		assert.Len(t, m, 3000)
		assert.Equal(t, 1234, m["k1234"])
	})

	t.Run("NameLargerThanScratch", func(t *testing.T) {
		name := strings.Repeat("n", 2*scratchSize)
		obj := rt.NewObject()
		defer obj.Free()

		require.NoError(t, obj.Set(name, rt.NewInt32(7)))
		assert.Equal(t, int32(7), must(obj.Get(name)).Int32())
		assert.True(t, must(obj.Has(name)))
		assert.Equal(t, uint32(0), rt.scratch.top, "the scratch stack is released")
	})

	t.Run("EmbeddedNUL", func(t *testing.T) {
		s := rt.NewString("a\x00b")
		defer s.Free()
		assert.Equal(t, int64(3), must(s.Get("length")).Int64())
		assert.Equal(t, "a\x00b", s.String())
	})
}

func TestMemoryView(t *testing.T) {
	rt, _ := setupRuntime(t)
	size := rt.mem.Size()

	_, ok := rt.mem.view(size-8, 8)
	assert.True(t, ok)
	_, ok = rt.mem.view(size-8, 9)
	assert.False(t, ok)
	assert.Panics(t, func() { rt.view(size, 1) })
}

func TestScratchNesting(t *testing.T) {
	rt, _ := setupRuntime(t)

	// The getter runs while the property name is still in the scratch
	// stack, and calls back into Go, which allocates above it.
	noErr(rt.SetFunc("inner", func(call *FunctionCall) (*Value, error) {
		obj := call.Arg(0)
		v, err := obj.Get("plain-property-with-a-long-name")
		if err != nil {
			return nil, err
		}
		defer v.Free()

		return call.Runtime().NewString(v.String() + "!"), nil
	}))

	obj := must(rt.Eval(context.Background(), "o.js", `({
		"plain-property-with-a-long-name": "value",
		get "getter-property-with-a-long-name"() { return inner(this) },
	})`))
	defer obj.Free()

	for range 3 {
		v := must(obj.Get("getter-property-with-a-long-name"))
		assert.Equal(t, "value!", v.String())
		v.Free()
	}

	assert.Equal(t, uint32(0), rt.scratch.top)
}

func TestGoReferences(t *testing.T) {
	bg := context.Background()

	t.Run("ReleasedWhenCollected", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		base := rt.registry.Len()

		for range 100 {
			rt.NewFunction(func(*FunctionCall) (*Value, error) { return nil, nil }).Free()
			rt.NewProxyValue(struct{}{}).Free()
		}

		assert.Equal(t, base, rt.registry.Len(), "unreferenced Go values are released")

		noErr(rt.SetFunc("keep", func(*FunctionCall) (*Value, error) { return nil, nil }))
		must(rt.Eval(bg, "gc.js", "gc()")).Free()
		assert.Equal(t, base+1, rt.registry.Len(), "referenced Go values are kept")
	})

	t.Run("ProxyValuesCannotBeForged", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		secret := &struct{ Token string }{"secret"}
		pv := rt.NewProxyValue(secret)
		defer pv.Free()

		forged := must(rt.Eval(bg, "forge.js", `typeof QJS_PROXY_VALUE`))
		defer forged.Free()
		assert.Equal(t, "undefined", forged.String())

		fake := must(rt.Eval(bg, "fake.js", `({proxyId: 1})`))
		defer fake.Free()
		got, err := fake.As[any]()
		require.NoError(t, err)
		assert.IsType(t, map[string]any{}, got, "a look-alike object is a plain object")

		assert.Same(t, secret, must(pv.As[any]()))
	})

	t.Run("FunctionName", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		noErr(rt.SetFunc("hello", func(*FunctionCall) (*Value, error) { return nil, nil }))
		name := must(rt.Eval(bg, "name.js", `hello.name`))
		defer name.Free()
		assert.Equal(t, "hello", name.String())
	})

	t.Run("AsyncRejection", func(t *testing.T) {
		rt, _ := setupRuntime(t)
		noErr(rt.SetAsyncFunc("fail", func(*FunctionCall) (*Value, error) {
			return nil, &Error{Name: "RangeError", Message: "too far"}
		}))
		v := must(rt.Eval(bg, "async.js", `fail().catch(e => e instanceof RangeError && e.message)`))
		defer v.Free()
		assert.Equal(t, "too far", v.String())
	})
}

func TestNumbersBoxedInGo(t *testing.T) {
	rt, _ := setupRuntime(t)
	must(rt.Eval(context.Background(), "is.js", `globalThis.same = (a, b) => Object.is(a, b)`)).Free()

	same := must(rt.Get("same"))
	defer same.Free()

	cases := map[string]*Value{
		"0":                      rt.NewInt64(0),
		"-0":                     rt.NewFloat64(math.Copysign(0, -1)),
		"NaN":                    rt.NewFloat64(math.NaN()),
		"Infinity":               rt.NewFloat64(math.Inf(1)),
		"-Infinity":              rt.NewFloat64(math.Inf(-1)),
		"2147483647":             rt.NewInt64(math.MaxInt32),
		"2147483648":             rt.NewInt64(math.MaxInt32 + 1),
		"-2147483649":            rt.NewInt64(math.MinInt32 - 1),
		"4294967295":             rt.NewUint32(math.MaxUint32),
		"9007199254740993":       rt.NewInt64(1<<53 + 1),
		"1.5":                    rt.NewFloat64(1.5),
		"5e-324":                 rt.NewFloat64(math.SmallestNonzeroFloat64),
		"1.7976931348623157e308": rt.NewFloat64(math.MaxFloat64),
	}
	for code, v := range cases {
		want := must(rt.Eval(context.Background(), "n.js", code))
		got := must(same.Call(context.Background(), nil, v, want))
		assert.True(t, got.Bool(), code)
		v.Free()
		want.Free()
		got.Free()
	}
}

package qngg

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eval evaluates code and frees the result when the test ends.
func eval(t *testing.T, rt *Runtime, code string) *Value {
	t.Helper()
	v, err := rt.Eval(context.Background(), "test.js", code)
	require.NoError(t, err)
	t.Cleanup(v.Free)
	return v
}

func TestValueOwnership(t *testing.T) {
	rt, _ := setupRuntime(t)

	t.Run("FreeIsIdempotent", func(t *testing.T) {
		v := rt.NewObject()
		v.Free()
		assert.NotPanics(t, v.Free)

		var nilValue *Value
		assert.NotPanics(t, nilValue.Free)
	})

	t.Run("UseAfterFree", func(t *testing.T) {
		v := rt.NewObject()
		v.Free()

		_, err := v.Get("a")
		assert.ErrorIs(t, err, ErrFreed)
		assert.ErrorIs(t, v.Set("a", rt.Global()), ErrFreed)
		assert.PanicsWithError(t, ErrFreed.Error(), func() { v.IsObject() })
		assert.Equal(t, "<invalid qngg.Value>", v.String())
	})

	t.Run("SetDoesNotTakeOwnership", func(t *testing.T) {
		obj := rt.NewObject()
		defer obj.Free()
		val := rt.NewString("kept")
		defer val.Free()

		require.NoError(t, obj.Set("a", val))
		require.NoError(t, obj.SetIndex(0, val))
		assert.Equal(t, "kept", val.String(), "val stays usable after Set")
	})

	t.Run("GlobalIsBorrowed", func(t *testing.T) {
		rt.Global().Free()
		assert.True(t, rt.Global().IsObject(), "freeing the global object is a no-op")
	})

	t.Run("Clone", func(t *testing.T) {
		v := rt.NewString("hello")
		c := v.Clone()
		v.Free()
		defer c.Free()
		assert.Equal(t, "hello", c.String())
	})

	t.Run("FunctionArgumentsAreBorrowed", func(t *testing.T) {
		var kept *Value
		noErr(rt.SetFunc("keep", func(call *FunctionCall) (*Value, error) {
			call.Arg(0).Free() // no-op
			kept = call.Arg(0).Clone()
			return call.Arg(0), nil // returning a borrowed value is fine
		}))

		result := eval(t, rt, `keep({ x: 1 })`)
		defer kept.Free()
		assert.Equal(t, int32(1), must(result.Get("x")).Int32())
		assert.Equal(t, int32(1), must(kept.Get("x")).Int32())
	})

	t.Run("StringConversionsDoNotLeak", func(t *testing.T) {
		str := eval(t, rt, `"x".repeat(4096)`)
		obj := eval(t, rt, `({ a: "x".repeat(4096) })`)
		buf := eval(t, rt, `new ArrayBuffer(4096)`)

		before := rt.mem.Size()
		for range 2000 {
			_ = str.String()
			_, _ = obj.MarshalJSON()
			_ = must(buf.Bytes())
			v := must(obj.Get("a"))
			v.Free()
		}

		// Each iteration copies ~12KiB out of wasm; leaking it would grow
		// memory by about 24MiB.
		assert.Less(t, rt.mem.Size()-before, uint32(1<<20))
	})
}

func TestValueTypeChecks(t *testing.T) {
	rt, _ := setupRuntime(t)

	cases := []struct {
		code  string
		check func(*Value) bool
		want  bool
	}{
		{"42", (*Value).IsNumber, true},
		{"4.5", (*Value).IsNumber, true},
		{"'s'", (*Value).IsNumber, false},
		{"'s'", (*Value).IsString, true},
		{"'a'.repeat(100) + 'b'", (*Value).IsString, true},
		{"true", (*Value).IsBool, true},
		{"null", (*Value).IsNull, true},
		{"undefined", (*Value).IsUndefined, true},
		{"({})", (*Value).IsObject, true},
		{"null", (*Value).IsObject, false},
		{"[1]", (*Value).IsArray, true},
		{"({})", (*Value).IsArray, false},
		{"(() => 1)", (*Value).IsFunction, true},
		{"(class A {})", (*Value).IsConstructor, true},
		{"(() => 1)", (*Value).IsConstructor, false},
		{"Promise.resolve(1).then", (*Value).IsFunction, true},
		{"new Error('x')", (*Value).IsError, true},
		{"10n", (*Value).IsBigInt, true},
		{"2n ** 100n", (*Value).IsBigInt, true},
		{"Symbol('s')", (*Value).IsSymbol, true},
		{"new Map()", (*Value).IsMap, true},
		{"new Set()", (*Value).IsSet, true},
		{"new Map()", (*Value).IsSet, false},
		{"new ArrayBuffer(8)", (*Value).IsArrayBuffer, true},
		{"new Uint8Array(8)", (*Value).IsArrayBuffer, false},
		{"new Date()", (*Value).IsDate, true},
		{"NaN", (*Value).IsNaN, true},
		{"1", (*Value).IsNaN, false},
		{"Infinity", (*Value).IsInfinity, true},
		{"-Infinity", (*Value).IsInfinity, true},
		{"1e308", (*Value).IsInfinity, false},
	}

	for _, c := range cases {
		assert.Equal(t, c.want, c.check(eval(t, rt, c.code)), c.code)
	}

	t.Run("Promise", func(t *testing.T) {
		// Eval awaits promises, so get one through a function.
		fn := eval(t, rt, `() => new Promise(() => {})`)
		p := must(fn.Call(context.Background(), nil))
		defer p.Free()
		assert.True(t, p.IsPromise())
		assert.Equal(t, "Promise", p.Type())
	})

	t.Run("Type", func(t *testing.T) {
		types := map[string]string{
			"42": "Number", "'s'": "String", "true": "Boolean", "null": "Null",
			"undefined": "Undefined", "NaN": "NaN", "Infinity": "Infinity", "10n": "BigInt",
			"Symbol()": "Symbol", "[1]": "Array", "new Map()": "Map", "new Set()": "Set",
			"new Date()": "Date", "new Error('x')": "Error", "(() => 1)": "Function",
			"(class Foo {})": "Constructor Foo", "new ArrayBuffer(1)": "ArrayBuffer", "({})": "unknown",
		}
		for code, want := range types {
			assert.Equal(t, want, eval(t, rt, code).Type(), code)
		}
	})
}

func TestValueConversions(t *testing.T) {
	rt, _ := setupRuntime(t)

	t.Run("Numbers", func(t *testing.T) {
		assert.Equal(t, int32(42), eval(t, rt, "42").Int32())
		assert.Equal(t, int32(-3), eval(t, rt, "-3.7").Int32())
		assert.Equal(t, int64(1)<<40, eval(t, rt, "2 ** 40").Int64())
		assert.Equal(t, uint32(math.MaxUint32), eval(t, rt, "-1").Uint32())
		assert.InDelta(t, 3.14, eval(t, rt, "3.14").Float64(), 1e-9)
		assert.InDelta(t, 12.5, eval(t, rt, "'12.5'").Float64(), 1e-9, "strings are coerced")
		assert.True(t, math.IsNaN(eval(t, rt, "'x'").Float64()))
	})

	t.Run("ThrowingCoercionReturnsZero", func(t *testing.T) {
		v := eval(t, rt, "({ valueOf() { throw new Error('no') } })")
		assert.Equal(t, int32(0), v.Int32())
		assert.Equal(t, int32(1), eval(t, rt, "1").Int32(), "the exception does not linger")
	})

	t.Run("Bool", func(t *testing.T) {
		assert.True(t, eval(t, rt, "true").Bool())
		assert.True(t, eval(t, rt, "'x'").Bool())
		assert.False(t, eval(t, rt, "0").Bool())
		assert.False(t, eval(t, rt, "''").Bool())
	})

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, "hello", eval(t, rt, "'hello'").String())
		assert.Equal(t, "1,2", eval(t, rt, "[1, 2]").String())
		assert.Equal(t, "[object Object]", eval(t, rt, "({})").String())
		assert.Equal(t, "", eval(t, rt, "({ toString() { throw 1 } })").String())
	})

	t.Run("BigInt", func(t *testing.T) {
		want, _ := new(big.Int).SetString("1267650600228229401496703205376", 10)
		assert.Equal(t, want, eval(t, rt, "2n ** 100n").BigInt())
		assert.Nil(t, eval(t, rt, "1").BigInt())
	})

	t.Run("Time", func(t *testing.T) {
		got, err := eval(t, rt, "new Date(Date.UTC(2024, 0, 2, 3, 4, 5, 678))").Time()
		require.NoError(t, err)
		assert.True(t, got.Equal(time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC)))

		_, err = eval(t, rt, "new Date(NaN)").Time()
		assert.Error(t, err)
		_, err = eval(t, rt, "1").Time()
		assert.Error(t, err)
	})

	t.Run("Bytes", func(t *testing.T) {
		assert.Equal(t, []byte{1, 2, 3}, must(eval(t, rt, "new Uint8Array([1, 2, 3]).buffer").Bytes()))
		assert.Equal(t, []byte{2, 3}, must(eval(t, rt, "new Uint8Array([1, 2, 3, 4]).subarray(1, 3)").Bytes()))
		_, err := eval(t, rt, "1").Bytes()
		assert.Error(t, err)
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		b, err := json.Marshal(map[string]any{"v": eval(t, rt, "({ a: [1, 'x'] })")})
		require.NoError(t, err)
		assert.JSONEq(t, `{"v":{"a":[1,"x"]}}`, string(b))

		b, err = eval(t, rt, "undefined").MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, "null", string(b))

		_, err = eval(t, rt, "(() => { const o = {}; o.o = o; return o })()").MarshalJSON()
		assert.Error(t, err)
	})

	t.Run("As", func(t *testing.T) {
		type user struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		u, err := eval(t, rt, "({ name: 'Ann', age: 30 })").As[user]()
		require.NoError(t, err)
		assert.Equal(t, user{"Ann", 30}, u)

		nums, err := eval(t, rt, "[1, 2, 3]").As[[]int]()
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, nums)

		add, err := eval(t, rt, "(a, b) => a + b").As[func(int, int) (int, error)]()
		require.NoError(t, err)
		assert.Equal(t, 5, must(add(2, 3)))

		_, err = eval(t, rt, "'x'").As[int]()
		assert.Error(t, err)
	})
}

func TestValueProperties(t *testing.T) {
	rt, _ := setupRuntime(t)
	bg := context.Background()

	t.Run("GetSet", func(t *testing.T) {
		obj := rt.NewObject()
		defer obj.Free()

		v := rt.NewString("John")
		defer v.Free()
		require.NoError(t, obj.Set("name", v))

		got := must(obj.Get("name"))
		defer got.Free()
		assert.Equal(t, "John", got.String())

		missing := must(obj.Get("missing"))
		defer missing.Free()
		assert.True(t, missing.IsUndefined())
	})

	t.Run("GetterAndSetterExceptions", func(t *testing.T) {
		obj := eval(t, rt, `({ get bad() { throw new TypeError('getter') }, set bad(v) { throw new RangeError('setter') } })`)

		_, err := obj.Get("bad")
		var jsErr *Error
		require.ErrorAs(t, err, &jsErr)
		assert.Equal(t, "TypeError", jsErr.Name)
		assert.Equal(t, "getter", jsErr.Message)

		err = obj.Set("bad", rt.Global())
		require.ErrorAs(t, err, &jsErr)
		assert.Equal(t, "RangeError", jsErr.Name)
	})

	t.Run("GetOnNullThrows", func(t *testing.T) {
		_, err := eval(t, rt, "null").Get("x")
		assert.Error(t, err)
	})

	t.Run("Keys", func(t *testing.T) {
		obj := eval(t, rt, `(() => { const o = { a: 1, b: 2 }; Object.defineProperty(o, 'hidden', { value: 3 }); return o })()`)
		assert.Equal(t, []string{"a", "b"}, must(obj.Keys()))
	})

	t.Run("KeyedAndIndexed", func(t *testing.T) {
		arr := rt.NewArray()
		defer arr.Free()

		for i, s := range []string{"a", "b"} {
			v := rt.NewString(s)
			require.NoError(t, arr.SetIndex(uint32(i), v))
			v.Free()
		}
		assert.Equal(t, int64(2), arr.Len())
		assert.Equal(t, "b", must(arr.Index(1)).String())

		sym := eval(t, rt, "Symbol('k')")
		obj := rt.NewObject()
		defer obj.Free()
		require.NoError(t, obj.SetKey(sym, rt.Global()))
		got := must(obj.GetKey(sym))
		defer got.Free()
		assert.True(t, got.IsObject())
	})

	t.Run("HasAndDelete", func(t *testing.T) {
		obj := eval(t, rt, `(() => { const o = { a: 1 }; Object.defineProperty(o, 'fixed', { value: 2 }); return o })()`)

		assert.True(t, must(obj.Has("a")))
		assert.True(t, must(obj.Has("toString")), "inherited properties count")
		assert.False(t, must(obj.Has("b")))

		assert.True(t, must(obj.Delete("a")))
		assert.False(t, must(obj.Has("a")))
		assert.False(t, must(obj.Delete("fixed")), "non-configurable")
	})

	t.Run("Invoke", func(t *testing.T) {
		obj := eval(t, rt, `({ add(a, b) { return a + b }, fail() { throw new Error('nope') } })`)

		sum := must(obj.Invoke(bg, "add", 2, 3))
		defer sum.Free()
		assert.Equal(t, int32(5), sum.Int32())

		three := rt.NewInt32(3)
		defer three.Free()
		sum2 := must(obj.Invoke(bg, "add", three, 4))
		defer sum2.Free()
		assert.Equal(t, int32(7), sum2.Int32(), "*Value and Go arguments mix")

		_, err := obj.Invoke(bg, "fail")
		assert.ErrorContains(t, err, "nope")
		_, err = obj.Invoke(bg, "missing")
		assert.ErrorContains(t, err, "is not a function")
		_, err = eval(t, rt, "1").Invoke(bg, "toString")
		assert.Error(t, err, "Invoke needs an object")
	})

	t.Run("CallWithThis", func(t *testing.T) {
		fn := eval(t, rt, `(function (x) { return this.base + x })`)
		this := eval(t, rt, `({ base: 10 })`)

		v := must(fn.Call(bg, this, 5))
		defer v.Free()
		assert.Equal(t, int32(15), v.Int32())
	})

	t.Run("New", func(t *testing.T) {
		date := must(must(rt.Get("Date")).New(bg, 0))
		defer date.Free()
		assert.True(t, date.IsDate())

		_, err := eval(t, rt, "(() => 1)").New(bg)
		assert.Error(t, err)
	})
}

func TestValueAwait(t *testing.T) {
	rt, _ := setupRuntime(t)
	bg := context.Background()

	t.Run("Resolved", func(t *testing.T) {
		fn := eval(t, rt, `async (x) => { await null; return x * 2 }`)
		p := must(fn.Call(bg, nil, 21))
		defer p.Free()

		v := must(p.Await(bg))
		defer v.Free()
		assert.Equal(t, int32(42), v.Int32())
		assert.True(t, p.IsPromise(), "the promise stays usable after Await")
	})

	t.Run("Rejected", func(t *testing.T) {
		fn := eval(t, rt, `async () => { throw new Error('rejected') }`)
		p := must(fn.Call(bg, nil))
		defer p.Free()

		_, err := p.Await(bg)
		assert.ErrorContains(t, err, "rejected")
	})

	t.Run("Timers", func(t *testing.T) {
		fn := eval(t, rt, `() => new Promise(resolve => setTimeout(() => resolve('done'), 10))`)
		p := must(fn.Call(bg, nil))
		defer p.Free()

		v := must(p.Await(bg))
		defer v.Free()
		assert.Equal(t, "done", v.String())
	})

	t.Run("NotAPromise", func(t *testing.T) {
		_, err := eval(t, rt, "1").Await(bg)
		assert.Error(t, err)
	})

	t.Run("AsyncGoFunction", func(t *testing.T) {
		noErr(rt.SetAsyncFunc("double", func(call *FunctionCall) (*Value, error) {
			if call.Arg(0).IsUndefined() {
				return nil, &Error{Name: "TypeError", Message: "missing argument"}
			}
			return call.Runtime().NewInt32(call.Arg(0).Int32() * 2), nil
		}))

		assert.Equal(t, int32(42), eval(t, rt, `double(21)`).Int32())
		assert.Equal(t, "TypeError: missing argument",
			eval(t, rt, `double().catch(e => e.toString())`).String())
	})
}

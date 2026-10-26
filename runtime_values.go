package qngg

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"
)

// Global returns the global object. The runtime owns it: Free is a no-op.
func (r *Runtime) Global() *Value {
	return r.global
}

// Get returns the global variable `name`.
func (r *Runtime) Get(name string) (*Value, error) {
	return r.global.Get(name)
}

// Set converts v with ToValue and assigns it to the global variable `name`.
func (r *Runtime) Set(name string, v any) error {
	jsVal, err := r.ToValue(v)
	if err != nil {
		return err
	}

	return setTake(r.global, name, jsVal)
}

// SetFunc assigns a Go function to the global variable `name`.
func (r *Runtime) SetFunc(name string, fn Function) error {
	return setTake(r.global, name, r.newFunction(name, fn, false))
}

// SetAsyncFunc assigns a Go function that JavaScript sees as an async
// function to the global variable `name`.
func (r *Runtime) SetAsyncFunc(name string, fn Function) error {
	return setTake(r.global, name, r.newFunction(name, fn, true))
}

// ToValue converts a Go value to a JavaScript value: numbers, strings,
// booleans, nil, time.Time, []byte (as an ArrayBuffer), *big.Int (as a
// BigInt), errors, slices, arrays, maps, structs (exported fields, using
// json tags, and methods), functions and channels. A *Value is cloned.
func (r *Runtime) ToValue(v any) (*Value, error) {
	jsVal, err := newConvTracker[uintptr]().toJsValue(r, v)
	if err != nil {
		return nil, fmt.Errorf("[ToValue] %w", err)
	}

	return jsVal, nil
}

// ParseJSON parses a JSON document.
func (r *Runtime) ParseJSON(s string) (_ *Value, err error) {
	defer recoverInterrupt(r, &err)

	cstr := r.pushCString(s)
	defer r.drop(cstr)

	return r.result(r.call(xParseJSON, r.jsCtx, uint64(cstr.ptr)))
}

// NewProxyValue wraps a Go value so it can be passed through JavaScript
// unchanged: converting the result back with Value.As returns v itself.
func (r *Runtime) NewProxyValue(v any) *Value {
	return r.newValue(r.call(xNewProxyValue, r.jsCtx, uint64(r.registry.Register(v))))
}

// NewNull returns null.
func (r *Runtime) NewNull() *Value {
	return r.newValue(uint64(tagNull) << 32)
}

// NewUndefined returns undefined.
func (r *Runtime) NewUndefined() *Value {
	return r.newValue(jsUndefined)
}

// NewBool returns a boolean.
func (r *Runtime) NewBool(b bool) *Value {
	raw := uint64(tagBool) << 32
	if b {
		raw |= 1
	}

	return r.newValue(raw)
}

// NewInt32 returns a number.
func (r *Runtime) NewInt32(v int32) *Value {
	return r.newValue(int64Raw(int64(v)))
}

// NewUint32 returns a number.
func (r *Runtime) NewUint32(v uint32) *Value {
	return r.newValue(int64Raw(int64(v)))
}

// NewInt64 returns a number. Integers beyond ±2^53 lose precision.
func (r *Runtime) NewInt64(v int64) *Value {
	return r.newValue(int64Raw(v))
}

// NewFloat64 returns a number.
func (r *Runtime) NewFloat64(v float64) *Value {
	return r.newValue(float64Raw(v))
}

// NewBigInt64 returns a BigInt.
func (r *Runtime) NewBigInt64(v int64) *Value {
	return r.newValue(r.call(xNewBigInt64, r.jsCtx, uint64(v)))
}

// NewBigUint64 returns a BigInt.
func (r *Runtime) NewBigUint64(v uint64) *Value {
	return r.newValue(r.call(xNewBigUint64, r.jsCtx, v))
}

// newBigInt returns the BigInt n.
func (r *Runtime) newBigInt(n *big.Int) (_ *Value, err error) {
	switch {
	case n.IsInt64():
		return r.NewBigInt64(n.Int64()), nil
	case n.IsUint64():
		return r.NewBigUint64(n.Uint64()), nil
	}

	defer recoverInterrupt(r, &err)

	s := n.String()

	digits := r.push(s)
	defer r.drop(digits)

	return r.result(r.call(xNewBigIntDecimal, r.jsCtx, uint64(digits.ptr), uint64(len(s))))
}

// NewString returns a string. Invalid UTF-8 is replaced.
func (r *Runtime) NewString(s string) *Value {
	str := r.push(s)
	defer r.drop(str)

	return r.newValue(r.call(xNewStringLen, r.jsCtx, uint64(str.ptr), uint64(len(s))))
}

// NewObject returns an empty object.
func (r *Runtime) NewObject() *Value {
	return r.newValue(r.call(xNewObject, r.jsCtx))
}

// NewArray returns an empty array.
func (r *Runtime) NewArray() *Value {
	return r.newValue(r.call(xNewArray, r.jsCtx))
}

// NewMap returns an empty Map.
func (r *Runtime) NewMap() *Value {
	return r.mustNewGlobal("Map")
}

// NewSet returns an empty Set.
func (r *Runtime) NewSet() *Value {
	return r.mustNewGlobal("Set")
}

// mustNewGlobal is newGlobal for builtin constructors that cannot fail.
func (r *Runtime) mustNewGlobal(name string) *Value {
	v, err := r.newGlobal(name)
	if err != nil {
		panic(err)
	}

	return v
}

// NewDate returns a Date.
func (r *Runtime) NewDate(t time.Time) *Value {
	epochMs := float64(t.UnixNano()) / nanosToMillis

	return r.newValue(r.call(xNewDate, r.jsCtx, math.Float64bits(epochMs)))
}

// NewArrayBuffer returns an ArrayBuffer holding a copy of data.
func (r *Runtime) NewArrayBuffer(data []byte) *Value {
	buf := r.push(data)
	defer r.drop(buf)

	return r.newValue(r.call(xNewArrayBufferCopy, r.jsCtx, uint64(buf.ptr), uint64(len(data))))
}

// errorName is the name of the base Error constructor.
const errorName = "Error"

// NewError returns an Error whose message is err's message. An *Error
// keeps its name, so it becomes a TypeError, RangeError and so on.
func (r *Runtime) NewError(err error) *Value {
	name, message := errorName, err.Error()

	var jsErr *Error
	if errors.As(err, &jsErr) && jsErr.Name != "" {
		name, message = jsErr.Name, jsErr.Message
	}

	if name != errorName {
		if errVal := r.newBuiltinError(name, message); errVal != nil {
			return errVal
		}
	}

	errVal := r.newValue(r.call(xNewError, r.jsCtx))
	_ = setTake(errVal, "message", r.NewString(message))

	if name != errorName {
		_ = setTake(errVal, "name", r.NewString(name))
	}

	return errVal
}

// newBuiltinError returns `new globalThis[name](message)`, or nil if there
// is no such error constructor.
func (r *Runtime) newBuiltinError(name, message string) *Value {
	msg := r.NewString(message)
	defer msg.Free()

	errVal, err := r.newGlobal(name, msg.ref)
	if err != nil || !errVal.IsError() {
		errVal.Free()

		return nil
	}

	return errVal
}

// NewFunction returns a JavaScript function that calls fn.
func (r *Runtime) NewFunction(fn Function) *Value {
	return r.newFunction("", fn, false)
}

// NewAsyncFunction returns a JavaScript async function that calls fn: it
// returns a promise that resolves to fn's result, or rejects with its error.
func (r *Runtime) NewAsyncFunction(fn Function) *Value {
	return r.newFunction("", fn, true)
}

// newFunction returns a JavaScript function named `name` that calls fn. Its
// registry entry is released when the function is garbage collected.
func (r *Runtime) newFunction(name string, fn Function, async bool) *Value {
	cname := r.pushCString(name)
	defer r.drop(cname)

	id := r.registry.Register(fn)

	raw := r.call(xNewGoFunction, r.jsCtx, uint64(id), uint64(cname.ptr), boolArg(async))
	if tagOf(raw) == tagException {
		r.registry.Unregister(id)
		panic(r.takeException())
	}

	return r.newValue(raw)
}

// newGlobal returns `new globalThis[name](...args)`, or undefined if there
// is no such constructor.
func (r *Runtime) newGlobal(name string, args ...uint64) (_ *Value, err error) {
	defer recoverInterrupt(r, &err)

	cname := r.pushCString(name)
	defer r.drop(cname)

	argv := r.pushValues(args...)
	defer r.drop(argv)

	return r.result(r.call(xNewGlobal, r.jsCtx, uint64(cname.ptr), uint64(len(args)), uint64(argv.ptr)))
}

// setTake sets obj[name] to val, handing val over: it is freed even if
// setting fails. It saves the reference copy of Set for values built here.
func setTake(obj *Value, name string, val *Value) error {
	if err := checkAll(obj, val); err != nil {
		val.Free()

		return err
	}

	return obj.setProp(name, val.take(), false)
}

// setIndexTake is setTake for array elements.
func setIndexTake(obj *Value, i uint32, val *Value) error {
	if err := checkAll(obj, val); err != nil {
		val.Free()

		return err
	}

	return obj.setIndex(i, val.take(), false)
}

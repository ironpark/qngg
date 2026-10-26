package qngg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
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
	return setTake(r.global, name, r.NewFunction(fn))
}

// SetAsyncFunc assigns a Go function that JavaScript sees as an async
// function to the global variable `name`.
func (r *Runtime) SetAsyncFunc(name string, fn Function) error {
	return setTake(r.global, name, r.NewAsyncFunction(fn))
}

// ToValue converts a Go value to a JavaScript value: numbers, strings,
// booleans, nil, time.Time, []byte (as an ArrayBuffer), errors, slices,
// arrays, maps, structs (exported fields, using json tags, and methods),
// functions and channels. A *Value is cloned.
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

	cstr := r.newCString(s)
	defer r.free(cstr)

	return r.result(r.call("QJS_ParseJSON", r.jsCtx, cstr))
}

// NewProxyValue wraps a Go value so it can be passed through JavaScript
// unchanged: converting the result back with Value.As returns v itself.
func (r *Runtime) NewProxyValue(v any) *Value {
	return r.newValue(r.call("QJS_NewProxyValue", r.jsCtx, r.registry.Register(v)))
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
	return r.newValue(uint64(tagInt)<<32 | uint64(uint32(v)))
}

// NewUint32 returns a number.
func (r *Runtime) NewUint32(v uint32) *Value {
	return r.newValue(r.call("QJS_NewUint32", r.jsCtx, uint64(v)))
}

// NewInt64 returns a number. Integers beyond ±2^53 lose precision.
func (r *Runtime) NewInt64(v int64) *Value {
	return r.newValue(r.call("QJS_NewInt64", r.jsCtx, uint64(v)))
}

// NewFloat64 returns a number.
func (r *Runtime) NewFloat64(v float64) *Value {
	return r.newValue(r.call("QJS_NewFloat64", r.jsCtx, math.Float64bits(v)))
}

// NewBigInt64 returns a BigInt.
func (r *Runtime) NewBigInt64(v int64) *Value {
	return r.newValue(r.call("QJS_NewBigInt64", r.jsCtx, uint64(v)))
}

// NewBigUint64 returns a BigInt.
func (r *Runtime) NewBigUint64(v uint64) *Value {
	return r.newValue(r.call("QJS_NewBigUint64", r.jsCtx, v))
}

// NewString returns a string. Invalid UTF-8 is replaced.
func (r *Runtime) NewString(s string) *Value {
	cstr := r.newCString(s)
	defer r.free(cstr)

	return r.newValue(r.call("QJS_NewString", r.jsCtx, cstr))
}

// NewObject returns an empty object.
func (r *Runtime) NewObject() *Value {
	return r.newValue(r.call("JS_NewObject", r.jsCtx))
}

// NewArray returns an empty array.
func (r *Runtime) NewArray() *Value {
	return r.newValue(r.call("JS_NewArray", r.jsCtx))
}

// NewMap returns an empty Map.
func (r *Runtime) NewMap() *Value {
	return r.newGlobalInstance("Map")
}

// NewSet returns an empty Set.
func (r *Runtime) NewSet() *Value {
	return r.newGlobalInstance("Set")
}

// NewDate returns a Date.
func (r *Runtime) NewDate(t time.Time) *Value {
	epochMs := float64(t.UnixNano()) / nanosToMillis

	return r.newValue(r.call("JS_NewDate", r.jsCtx, math.Float64bits(epochMs)))
}

// NewArrayBuffer returns an ArrayBuffer holding a copy of data.
func (r *Runtime) NewArrayBuffer(data []byte) *Value {
	ptr := r.newBuffer(data)
	defer r.free(ptr)

	return r.newValue(r.call("QJS_NewArrayBufferCopy", r.jsCtx, ptr, uint64(len(data))))
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

	errVal := r.newValue(r.call("JS_NewError", r.jsCtx))
	_ = setTake(errVal, "message", r.NewString(message))

	if name != errorName {
		_ = setTake(errVal, "name", r.NewString(name))
	}

	return errVal
}

// newBuiltinError returns `new globalThis[name](message)`, or nil if there
// is no such error constructor.
func (r *Runtime) newBuiltinError(name, message string) *Value {
	ctor, err := r.global.Get(name)
	if err != nil {
		return nil
	}
	defer ctor.Free()

	if !ctor.IsConstructor() {
		return nil
	}

	msg := r.NewString(message)
	defer msg.Free()

	argv := r.newBuffer(binary.LittleEndian.AppendUint64(nil, msg.ref))
	defer r.free(argv)

	errVal, err := r.result(r.call("JS_CallConstructor", r.jsCtx, ctor.ref, 1, argv))
	if err != nil || !errVal.IsError() {
		errVal.Free()

		return nil
	}

	return errVal
}

// NewFunction returns a JavaScript function that calls fn.
func (r *Runtime) NewFunction(fn Function) *Value {
	return r.newFunction(fn, false)
}

// NewAsyncFunction returns a JavaScript async function that calls fn: it
// returns a promise that resolves to fn's result, or rejects with its error.
func (r *Runtime) NewAsyncFunction(fn Function) *Value {
	return r.newFunction(fn, true)
}

func (r *Runtime) newFunction(fn Function, async bool) *Value {
	isAsync := uint64(0)
	if async {
		isAsync = 1
	}

	fnID := r.registry.Register(fn)

	return r.newValue(r.call("QJS_CreateFunctionProxy", r.jsCtx, fnID, 0, isAsync))
}

// newGlobalInstance returns `new globalThis[name]()`.
func (r *Runtime) newGlobalInstance(name string) *Value {
	ctor, err := r.global.Get(name)
	if err != nil {
		panic(err)
	}
	defer ctor.Free()

	return r.newValue(r.call("JS_CallConstructor", r.jsCtx, ctor.ref, 0, 0))
}

// newAtom returns the atom for name. The caller frees it with JS_FreeAtom.
func (r *Runtime) newAtom(name string) uint64 {
	cstr := r.newCString(name)
	defer r.free(cstr)

	return r.call("JS_NewAtom", r.jsCtx, cstr)
}

// setTake sets obj[name] to val, handing val over: it is freed even if
// setting fails. It saves the reference copy of Set for values built here.
func setTake(obj *Value, name string, val *Value) (err error) {
	if err := checkAll(obj, val); err != nil {
		val.Free()

		return err
	}

	defer recoverInterrupt(obj.rt, &err)

	cname := obj.rt.newCString(name)
	defer obj.rt.free(cname)

	_, err = obj.rt.status(obj.rt.call("JS_SetPropertyStr", obj.rt.jsCtx, obj.ref, cname, val.take()))

	return err
}

// setIndexTake is setTake for array elements.
func setIndexTake(obj *Value, i uint32, val *Value) (err error) {
	if err := checkAll(obj, val); err != nil {
		val.Free()

		return err
	}

	defer recoverInterrupt(obj.rt, &err)

	_, err = obj.rt.status(obj.rt.call("QJS_SetPropertyUint32", obj.rt.jsCtx, obj.ref, uint64(i), val.take()))

	return err
}

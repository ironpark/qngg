package qngg

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// NewProxyValue creates a new Value that represents a proxy to a Go value.
func (r *Runtime) NewProxyValue(v any) *Value {
	proxyID := r.registry.Register(v)

	return r.jsCall("QJS_NewProxyValue", r.ctxPtr(), proxyID)
}

// Global returns the global object, caching it for subsequent calls.
func (r *Runtime) Global() *Value {
	if r.global == nil {
		r.global = r.jsCall("JS_GetGlobalObject", r.ctxPtr())
	}

	return r.global
}

// SetFunc sets a function with given name in the global object.
func (r *Runtime) SetFunc(name string, fn Function) {
	jsFn := r.Function(fn)
	global := r.Global()
	global.SetPropertyStr(name, jsFn)
}

// SetAsyncFunc sets an async function with given name in the global object.
func (r *Runtime) SetAsyncFunc(name string, fn AsyncFunction) {
	jsFn := r.Function(func(this *This) (*Value, error) {
		fn(this)

		return this.runtime.NewUndefined(), nil
	}, true)
	global := r.Global()
	global.SetPropertyStr(name, jsFn)
}

// ParseJSON parses given JSON string and returns an object value.
func (r *Runtime) ParseJSON(v string) *Value {
	cStr := r.newStringHandle(v)
	defer cStr.Free()

	return r.jsCall("QJS_ParseJSON", r.ctxPtr(), cStr.raw())
}

// NewNull creates a new null JavaScript value.
func (r *Runtime) NewNull() *Value {
	return r.jsCall("JS_NewNull")
}

// NewUndefined creates a new undefined JavaScript value.
func (r *Runtime) NewUndefined() *Value {
	return r.jsCall("JS_NewUndefined")
}

// NewDate creates a new JavaScript Date object from the given time.
func (r *Runtime) NewDate(t *time.Time) *Value {
	epochMs := float64(t.UnixNano()) / nanosToMillis // Nanoseconds to milliseconds

	return r.jsCall("JS_NewDate", r.ctxPtr(), math.Float64bits(epochMs))
}

// NewBool creates a new JavaScript boolean value.
func (r *Runtime) NewBool(b bool) *Value {
	boolVal := uint64(0)
	if b {
		boolVal = 1
	}

	return r.jsCall("QJS_NewBool", r.ctxPtr(), boolVal)
}

// NewUint32 creates a new JavaScript number from uint32.
func (r *Runtime) NewUint32(v uint32) *Value {
	return r.jsCall("QJS_NewUint32", r.ctxPtr(), uint64(v))
}

// NewInt32 creates a new JavaScript number from int32.
func (r *Runtime) NewInt32(v int32) *Value {
	return r.jsCall("QJS_NewInt32", r.ctxPtr(), uint64(v))
}

// NewInt64 creates a new JavaScript number from int64.
func (r *Runtime) NewInt64(v int64) *Value {
	return r.jsCall("QJS_NewInt64", r.ctxPtr(), uint64(v))
}

// NewBigInt64 creates a new JavaScript BigInt from int64.
func (r *Runtime) NewBigInt64(v int64) *Value {
	return r.jsCall("QJS_NewBigInt64", r.ctxPtr(), uint64(v))
}

// NewBigUint64 creates a new JavaScript BigInt from uint64.
func (r *Runtime) NewBigUint64(v uint64) *Value {
	return r.jsCall("QJS_NewBigUint64", r.ctxPtr(), v)
}

// NewFloat64 creates a new JavaScript number from float64.
func (r *Runtime) NewFloat64(v float64) *Value {
	return r.jsCall("QJS_NewFloat64", r.ctxPtr(), math.Float64bits(v))
}

// NewString creates a new JavaScript string value.
func (r *Runtime) NewString(v string) *Value {
	str := r.newStringHandle(v)

	return r.jsCall("QJS_NewString", r.ctxPtr(), str.raw())
}

// NewObject creates a new empty JavaScript object.
func (r *Runtime) NewObject() *Value {
	return r.jsCall("JS_NewObject", r.ctxPtr())
}

// NewArray creates a new empty JavaScript array.
func (r *Runtime) NewArray() *Array {
	val := r.jsCall("JS_NewArray", r.ctxPtr())

	return wrapArray(val)
}

// NewMap creates a new JavaScript Map object.
func (r *Runtime) NewMap() *Map {
	m := r.Global().GetPropertyStr("Map")
	defer m.Free()

	val := r.jsCall("JS_CallConstructor", r.ctxPtr(), m.raw(), 0, 0)

	return wrapMap(val)
}

// NewSet creates a new JavaScript Set object.
func (r *Runtime) NewSet() *Set {
	s := r.Global().GetPropertyStr("Set")
	defer s.Free()

	val := r.jsCall("JS_CallConstructor", r.ctxPtr(), s.raw(), 0, 0)

	return wrapSet(val)
}

// NewArrayBuffer creates a new JavaScript ArrayBuffer with the given binary data.
func (r *Runtime) NewArrayBuffer(binaryData []byte) *Value {
	ptr := r.malloc(uint64(len(binaryData)))
	defer r.freeHandle(ptr)

	r.memWrite(uint32(ptr), binaryData)

	return r.jsCall("QJS_NewArrayBufferCopy", r.ctxPtr(), ptr, uint64(len(binaryData)))
}

// NewError creates a new JavaScript Error object from Go error.
func (r *Runtime) NewError(e error) *Value {
	errString := r.NewString(e.Error())
	errVal := r.jsCall("JS_NewError", r.ctxPtr())
	errVal.SetPropertyStr("message", errString)

	return errVal
}

// Throw throws a value as an exception.
func (r *Runtime) Throw(v *Value) *Value {
	return r.jsCall("JS_Throw", r.ctxPtr(), v.raw())
}

// ThrowError throws an exception with the given error.
func (r *Runtime) ThrowError(err error) *Value {
	return r.Throw(r.NewError(err))
}

// ThrowSyntaxError throws syntax error with given cause.
func (r *Runtime) ThrowSyntaxError(format string, args ...any) *Value {
	cause := fmt.Sprintf(format, args...)

	causePtr := r.newStringHandle(cause)
	defer causePtr.Free()

	return r.jsCall("QJS_ThrowSyntaxError", r.ctxPtr(), causePtr.raw())
}

// ThrowTypeError throws type error with given cause.
func (r *Runtime) ThrowTypeError(format string, args ...any) *Value {
	cause := fmt.Sprintf(format, args...)

	causePtr := r.newStringHandle(cause)
	defer causePtr.Free()

	return r.jsCall("QJS_ThrowTypeError", r.ctxPtr(), causePtr.raw())
}

// ThrowReferenceError throws reference error with given cause.
func (r *Runtime) ThrowReferenceError(format string, args ...any) *Value {
	cause := fmt.Sprintf(format, args...)

	causePtr := r.newStringHandle(cause)
	defer causePtr.Free()

	return r.jsCall("QJS_ThrowReferenceError", r.ctxPtr(), causePtr.raw())
}

// ThrowRangeError throws range error with given cause.
func (r *Runtime) ThrowRangeError(format string, args ...any) *Value {
	cause := fmt.Sprintf(format, args...)

	causePtr := r.newStringHandle(cause)
	defer causePtr.Free()

	return r.jsCall("QJS_ThrowRangeError", r.ctxPtr(), causePtr.raw())
}

// ThrowInternalError throws internal error with given cause.
func (r *Runtime) ThrowInternalError(format string, args ...any) *Value {
	cause := fmt.Sprintf(format, args...)

	causePtr := r.newStringHandle(cause)
	defer causePtr.Free()

	return r.jsCall("QJS_ThrowInternalError", r.ctxPtr(), causePtr.raw())
}

// Function creates a JavaScript function that wraps the given Go function.
func (r *Runtime) Function(fn Function, isAsyncs ...bool) *Value {
	isAsync := uint64(0)
	if len(isAsyncs) > 0 && isAsyncs[0] {
		isAsync = 1
	}

	fnID := r.registry.Register(fn)
	ctxID := r.registry.Register(r)
	proxyFuncVal := r.jsCall("QJS_CreateFunctionProxy", r.ctxPtr(), fnID, ctxID, isAsync)

	// Registry: Store IDs for cleanup and callback identification
	proxyFuncVal.SetPropertyStr("__fnID", r.NewInt64(int64(fnID)))
	proxyFuncVal.SetPropertyStr("__ctxID", r.NewInt64(int64(ctxID)))

	return proxyFuncVal
}

// Invoke invokes a function with given this value and arguments.
func (r *Runtime) Invoke(fn *Value, this *Value, args ...*Value) (*Value, error) {
	argc, argvPtr := createJsCallArgs(r, args...)
	defer r.freeHandle(argvPtr)

	jsCallArgs := []uint64{
		r.ctxPtr(),
		fn.raw(),
		this.raw(),
		argc,
		argvPtr,
	}
	result := r.jsCall("QJS_Call", jsCallArgs...)

	return normalizeJsValue(r, result)
}

// createJsCallArgs marshals Go Value arguments to WASM memory for JavaScript calls.
func createJsCallArgs(r *Runtime, args ...*Value) (uint64, uint64) {
	var argvPtr uint64

	argc := uint64(len(args))
	if argc > 0 {
		argvBytes := make([]byte, uint64ByteSize*argc)
		for i, v := range args {
			// WASM: Pack 64-bit JSValue handles in little-endian format
			binary.LittleEndian.PutUint64(argvBytes[i*uint64ByteSize:], v.raw())
		}

		// WASM: Allocate and write argument array to memory
		argvPtr = r.malloc(uint64(len(argvBytes)))
		r.memWrite(uint32(argvPtr), argvBytes)
	}

	return argc, argvPtr
}

func (r *Runtime) jsCall(name string, args ...uint64) *Value {
	return r.newValue(r.callHandle(name, args...))
}

// memRead reads bytes from WASM memory at the given address.
func (r *Runtime) memRead(addr uint32, size uint64) []byte {
	return r.mem.MustRead(addr, size)
}

// memWrite writes bytes to WASM memory at the given address.
func (r *Runtime) memWrite(addr uint32, b []byte) {
	r.mem.MustWrite(addr, b)
}

// newStringHandle creates a Value from a string using runtime handle.
func (r *Runtime) newStringHandle(v string) *Value {
	handle := r.newCString(v)

	return r.newValue(handle)
}

// newBytes creates a Value from a byte slice.
func (r *Runtime) newBytes(v []byte) *Value {
	return r.newValue(r.newBytesHandle(v))
}

// ctxPtr returns the raw handle value for low-level operations.
func (r *Runtime) ctxPtr() uint64 {
	return r.ctxHandle.raw
}

// newValue creates a new Value wrapper around the given handle.
func (r *Runtime) newValue(handle *rawHandle) *Value {
	return &Value{runtime: r, handle: handle}
}

// newUninitialized creates a new uninitialized JavaScript value.
func (r *Runtime) newUninitialized() *Value {
	return r.jsCall("JS_NewUninitialized")
}

// newAtom creates a new Atom from the given string.
func (r *Runtime) newAtom(v string) jsAtom {
	cstr := r.newStringHandle(v)

	atomValue := r.jsCall("JS_NewAtom", r.ctxPtr(), cstr.raw())

	defer cstr.Free()

	return jsAtom{runtime: r, Value: atomValue}
}

// newAtomIndex creates a new Atom with the given index.
func (r *Runtime) newAtomIndex(index int64) jsAtom {
	return jsAtom{
		runtime: r,
		Value:   r.jsCall("QJS_NewAtomUInt32", r.ctxPtr(), uint64(index)),
	}
}

// hasException returns true if there is a pending exception.
func (r *Runtime) hasException() bool {
	return r.jsCall("JS_HasException", r.ctxPtr()).Bool()
}

// exception returns and clears the current pending exception.
func (r *Runtime) exception() error {
	val := r.jsCall("JS_GetException", r.ctxPtr())
	defer val.Free()

	return val.exception()
}

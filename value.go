package qngg

import (
	"errors"
	"fmt"
	"math/big"
	"time"
	"unsafe"
)

type (
	Function      func(ctx *This) (*Value, error)
	AsyncFunction func(ctx *This)
	atomID        uint32
)

type Value struct {
	handle  *rawHandle
	runtime *Runtime
}

type This struct {
	*Value

	runtime *Runtime
	args    []*Value
	promise *Value
	isAsync bool
}

type jsPropertyEnum struct {
	isEnumerable bool
	atom         atomID
}

const jsPropertyEnumSize = uint32(unsafe.Sizeof(jsPropertyEnum{}))

// jsAtom represents a JavaScript atom:
// Object property names and some strings are stored as Atoms (unique strings) to save memory and allow fast comparison.
type jsAtom struct {
	*Value

	runtime *Runtime
}

type ownProperty struct {
	isEnumerable bool
	atom         jsAtom
}

func (p ownProperty) String() string {
	return p.atom.String()
}

func (t *This) Runtime() *Runtime {
	return t.runtime
}

func (t *This) Args() []*Value {
	return t.args
}

func (t *This) Promise() *Value {
	return t.promise
}

func (t *This) IsAsync() bool {
	return t.isAsync
}

func (a jsAtom) Free() {
	a.runtime.jsCall("JS_FreeAtom", a.runtime.ctxPtr(), a.raw())
}

func (a jsAtom) String() string {
	result := a.runtime.jsCall("QJS_AtomToCString", a.runtime.ctxPtr(), a.raw())
	defer result.handle.Free()

	return result.handle.String()
}

func (a jsAtom) ToValue() *Value {
	return a.runtime.jsCall("JS_AtomToValue", a.runtime.ctxPtr(), a.raw())
}

func (v *Value) Free() {
	if v != nil && v.raw() != 0 {
		v.runtime.freeJsValue(v.handle.raw)
		v.handle.raw = 0
	}
}

func (v *Value) Runtime() *Runtime {
	return v.runtime
}

func (v *Value) Clone() *Value {
	return v.jsCall("QJS_CloneValue", v.ctxPtr(), v.raw())
}

func (v *Value) Type() string {
	// Check Symbol first, as it is a special case
	if v.IsSymbol() {
		return "Symbol"
	}

	if v.isProxyValue() {
		return "QJSProxyValue"
	}

	if v.IsNaN() {
		return "NaN"
	}

	if v.IsInfinity() {
		return "Infinity"
	}

	if v.IsBigInt() {
		return "BigInt"
	}

	if v.IsNumber() {
		return "Number"
	}

	if v.IsDate() {
		return "Date"
	}

	if v.IsBool() {
		return "Boolean"
	}

	if v.IsNull() {
		return "Null"
	}

	if v.IsUndefined() {
		return "Undefined"
	}

	if v.IsUninitialized() {
		return "Uninitialized"
	}

	if v.IsString() {
		return "String"
	}

	if v.IsError() {
		return "Error"
	}

	if v.IsArray() {
		return "Array"
	}

	if v.IsMap() {
		return "Map"
	}

	if v.IsSet() {
		return "Set"
	}

	// Check Promise before Function and Constructor.
	if v.IsPromise() {
		return "Promise"
	}

	// Check Constructor before Function.
	if v.IsConstructor() {
		name := v.GetPropertyStr("name")
		defer name.Free()

		constructorName := ""
		if name.IsString() && name.String() != "" {
			constructorName = " " + name.String()
		}

		return "Constructor" + constructorName
	}

	if v.IsFunction() {
		return "Function"
	}

	if v.IsByteArray() {
		return "ArrayBuffer"
	}

	return "unknown"
}

// GetOwnPropertyNames returns the names of the properties of the value.
func (v *Value) GetOwnPropertyNames() (_ []string, err error) {
	pList := v.getOwnProperties()

	names := make([]string, len(pList))

	for i := range names {
		names[i] = pList[i].String()
	}

	return names, nil
}

func (v *Value) GetProperty(name *Value) *Value {
	atom := v.jsCall("JS_ValueToAtom", v.ctxPtr(), name.raw())

	return v.jsCall("JS_GetProperty", v.ctxPtr(), v.raw(), atom.raw())
}

// SetProperty sets the value of the property with the given name.
// The property takes ownership of val; do not free val afterwards.
func (v *Value) SetProperty(name, val *Value) {
	atom := v.jsCall("JS_ValueToAtom", v.ctxPtr(), name.raw())
	v.jsCall("JS_SetProperty", v.ctxPtr(), v.raw(), atom.raw(), val.raw())
}

// GetPropertyStr returns the value of the property with the given name.
func (v *Value) GetPropertyStr(name string) *Value {
	nameVal := v.runtime.newStringHandle(name)
	defer v.runtime.freeHandle(nameVal.raw())

	return v.jsCall("JS_GetPropertyStr", v.ctxPtr(), v.raw(), nameVal.raw())
}

// SetPropertyStr sets the value of the property with the given name.
// The property takes ownership of val; do not free val afterwards.
func (v *Value) SetPropertyStr(name string, val *Value) {
	if val != nil {
		nameVal := v.runtime.newStringHandle(name)
		v.jsCall("JS_SetPropertyStr", v.ctxPtr(), v.raw(), nameVal.raw(), val.raw())
	}
}

// HasPropertyIndex returns true if the value has the property with the given index.
func (v *Value) HasPropertyIndex(index int64) bool {
	prop := v.runtime.newAtomIndex(index)
	defer prop.Free()

	return v.jsCall("JS_HasProperty", v.ctxPtr(), v.raw(), prop.raw()).Bool()
}

// HasProperty returns true if the value has the property with the given name.
func (v *Value) HasProperty(name string) bool {
	prop := v.runtime.newAtom(name)
	defer prop.Free()

	return v.jsCall("JS_HasProperty", v.ctxPtr(), v.raw(), prop.raw()).Bool()
}

// DeleteProperty deletes the property with the given name.
func (v *Value) DeleteProperty(name string) bool {
	prop := v.runtime.newAtom(name)
	defer prop.Free()

	return v.jsCall("JS_DeleteProperty", v.ctxPtr(), v.raw(), prop.raw(), uint64(1)).Bool()
}

// Invoke call the object's method with the given name and arguments.
func (v *Value) Invoke(fname string, args ...any) (_ *Value, err error) {
	jsArgs := make([]*Value, len(args))
	for i, arg := range args {
		if jsArgs[i], err = ToJsValue(v.runtime, arg); err != nil {
			return nil, err
		}
		defer jsArgs[i].Free()
	}

	return v.InvokeJS(fname, jsArgs...)
}

// InvokeJS call the object's method with the given name and JS arguments.
func (v *Value) InvokeJS(fname string, args ...*Value) (*Value, error) {
	if !v.IsObject() {
		return v.newUndefined(), ErrCallFuncOnNonObject
	}

	fn := v.GetPropertyStr(fname)
	defer fn.Free()

	if !fn.IsFunction() {
		return v.newUndefined(), fmt.Errorf("JS property '%s' is not a function", fname)
	}

	argc, argvPtr := createJsCallArgs(v.runtime, args...)
	defer v.runtime.freeHandle(argvPtr)

	jsCallArgs := []uint64{v.ctxPtr(), fn.raw(), v.raw(), argc, argvPtr}
	result := v.jsCall("QJS_Call", jsCallArgs...)

	return normalizeJsValue(v.runtime, result)
}

// SetPropertyIndex sets the value of the property with the given index.
// The property takes ownership of val; do not free val afterwards.
func (v *Value) SetPropertyIndex(index int64, val *Value) {
	v.jsCall("JS_SetPropertyUint32", v.ctxPtr(), v.raw(), uint64(index), val.raw())
}

// GetPropertyIndex returns the value of the property with the given index.
func (v *Value) GetPropertyIndex(index int64) *Value {
	return v.jsCall("QJS_GetPropertyUint32", v.ctxPtr(), v.raw(), uint64(index))
}

// Len returns the length of the array.
func (v *Value) Len() int64 {
	l := v.GetPropertyStr("length")
	defer l.Free()

	return l.Int64()
}

// ByteLen returns the length of the ArrayBuffer.
func (v *Value) ByteLen() int64 {
	return v.GetPropertyStr("byteLength").Int64()
}

// ToByteArray returns the byte array of the ArrayBuffer.
func (v *Value) ToByteArray() []byte {
	result := v.jsCall("QJS_GetArrayBuffer", v.ctxPtr(), v.raw())
	defer result.handle.Free()

	return result.handle.Bytes()
}

func (v *Value) IsNumber() bool {
	return v.jsCall("QJS_IsNumber", v.raw()).handle.Bool()
}

func (v *Value) IsNaN() bool {
	return v.String() == "NaN"
}

func (v *Value) IsInfinity() bool {
	return v.String() == "Infinity"
}

func (v *Value) IsBigInt() bool {
	return v.jsCall("QJS_IsBigInt", v.raw()).handle.Bool()
}

func (v *Value) IsDate() bool {
	return v.jsCall("JS_IsDate", v.raw()).handle.Bool()
}

func (v *Value) IsBool() bool {
	return v.jsCall("QJS_IsBool", v.raw()).handle.Bool()
}

func (v *Value) IsNull() bool {
	return v.jsCall("QJS_IsNull", v.raw()).handle.Bool()
}

func (v *Value) IsUndefined() bool {
	return v.jsCall("QJS_IsUndefined", v.raw()).handle.Bool()
}

// func (v *Value) IsException() bool {
// 	return v.Call("QJS_IsException", v.Raw()).handle.Bool()
// }

func (v *Value) IsUninitialized() bool {
	return v.jsCall("QJS_IsUninitialized", v.raw()).handle.Bool()
}

func (v *Value) IsString() bool {
	return v.jsCall("QJS_IsString", v.raw()).handle.Bool()
}

func (v *Value) IsSymbol() bool {
	return v.jsCall("QJS_IsSymbol", v.raw()).handle.Bool()
}

func (v *Value) IsObject() bool {
	return v.jsCall("QJS_IsObject", v.raw()).handle.Bool()
}

func (v *Value) IsArray() bool {
	return v.jsCall("QJS_IsArray", v.raw()).handle.Bool()
}

func (v *Value) IsError() bool {
	return v.jsCall("QJS_IsError", v.ctxPtr(), v.raw()).handle.Bool()
}

func (v *Value) IsFunction() bool {
	return v.jsCall("QJS_IsFunction", v.ctxPtr(), v.raw()).handle.Bool()
}

func (v *Value) IsConstructor() bool {
	return v.jsCall("QJS_IsConstructor", v.ctxPtr(), v.raw()).handle.Bool()
}

func (v *Value) IsPromise() bool {
	return v.jsCall("QJS_IsPromise", v.ctxPtr(), v.raw()).handle.Bool()
}

// Resolve resolves a promise with the given arguments.
// This method is intended for use with Go function bindings (this.Promise() in async Go functions).
// It will NOT work with native JavaScript promises created via "new Promise()".
// For native JS promises, use direct function calls or Promise.withResolvers instead.
func (v *Value) Resolve(args ...*Value) error {
	if v.IsPromise() {
		result, err := v.InvokeJS("resolve", args...)
		if err != nil {
			return err
		}

		result.Free()
	}

	return nil
}

// Reject rejects a promise with the given arguments.
// This method is intended for use with Go function bindings (this.Promise() in async Go functions).
// It will NOT work with native JavaScript promises created via "new Promise()".
// For native JS promises, use direct function calls or Promise.withResolvers instead.
func (v *Value) Reject(args ...*Value) error {
	if v.IsPromise() {
		result, err := v.InvokeJS("reject", args...)
		if err != nil {
			return err
		}

		result.Free()
	}

	return nil
}

func (v *Value) Await() (*Value, error) {
	if !v.IsPromise() {
		return nil, newInvalidJsInputErr("Promise", v)
	}

	result := v.jsCall("js_std_await", v.ctxPtr(), v.raw())

	return normalizeJsValue(v.runtime, result)
}

func (v *Value) IsMap() bool {
	return v.IsObject() && v.isGlobalInstanceOf("Map") ||
		v.String() == "[object Map]"
}

func (v *Value) IsSet() bool {
	return v.IsObject() && v.isGlobalInstanceOf("Set") ||
		v.String() == "[object Set]"
}

// IsByteArray return true if the value is array buffer.
func (v *Value) IsByteArray() bool {
	return v.IsObject() && v.isGlobalInstanceOf("ArrayBuffer") ||
		v.String() == "[object ArrayBuffer]"
}

// ForEach iterates over the properties of the object and calls the given function for each property.
func (v *Value) ForEach(fn func(key *Value, value *Value)) {
	if !v.IsObject() {
		return
	}

	props := v.getOwnProperties()
	for _, prop := range props {
		key := prop.atom

		keyValue := key.ToValue()
		if keyValue.String() == "length" {
			keyValue.Free()

			continue // Skip the length property
		}

		value := v.GetProperty(keyValue)
		fn(keyValue, value)
		key.Free()

		if !value.IsFunction() {
			value.Free()
		}
	}
}

func (v *Value) String() string {
	result := v.jsCall("QJS_ToCString", v.ctxPtr(), v.raw())
	defer result.handle.Free()

	return result.handle.String()
}

// JSONStringify returns the JSON string representation of the value.
func (v *Value) JSONStringify() (_ string, err error) {
	defer func() {
		r := anyToError(recover())
		if r != nil {
			err = fmt.Errorf("failed to stringify JS value: %w", r)
		}
	}()

	result := v.jsCall("QJS_JSONStringify", v.ctxPtr(), v.raw())
	defer result.handle.Free()

	return result.handle.String(), nil
}

// DateTime returns the date value of the value.
func (v *Value) DateTime(tzs ...string) *time.Time {
	var loc *time.Location
	if len(tzs) > 0 && tzs[0] != "" {
		loc = parseTimezone(tzs[0])
	} else {
		loc = time.Local
	}

	if !v.IsDate() {
		return nil
	}

	epochValue := v.jsCall("QJS_ToEpochTime", v.ctxPtr(), v.raw())
	defer epochValue.Free()

	epoch := epochValue.handle.Float64()

	// Check for NaN which indicates an invalid Date
	// Note: NaN != NaN is true, so this checks for NaN
	if epoch != epoch {
		return nil
	}

	epochNs := int64(epoch * 1e6)
	t := time.Unix(0, epochNs).In(loc)

	return &t
}

// Bool returns the boolean value of the value.
func (v *Value) Bool() bool {
	return v.jsCall("JS_ToBool", v.ctxPtr(), v.raw()).handle.Bool()
}

// Int32 returns the int32 value of the value.
// in c int is 32 bit, but in go it is depends on the architecture.
func (v *Value) Int32() int32 {
	return v.jsCall("QJS_ToInt32", v.ctxPtr(), v.raw()).handle.Int32()
}

func (v *Value) Int64() int64 {
	return v.jsCall("QJS_ToInt64", v.ctxPtr(), v.raw()).handle.Int64()
}

// Uint32 returns the uint32 value of the value.
func (v *Value) Uint32() uint32 {
	return v.jsCall("QJS_ToUint32", v.ctxPtr(), v.raw()).handle.Uint32()
}

// Float64 returns the float64 value of the value.
func (v *Value) Float64() float64 {
	return v.jsCall("QJS_ToFloat64", v.ctxPtr(), v.raw()).handle.Float64()
}

// BigInt returns the big.Int value of the value.
func (v *Value) BigInt() *big.Int {
	if !v.IsBigInt() {
		return nil
	}

	val, _ := new(big.Int).SetString(v.String(), 10)

	return val
}

// ToArray returns the array value of the value. DO NOT FREE.
func (v *Value) ToArray() (*Array, error) {
	if !v.IsArray() {
		return nil, fmt.Errorf("expected JS array, got %s=%s", v.Type(), v.String())
	}

	return wrapArray(v), nil
}

// ToMap returns the map value of the value. DO NOT FREE.
func (v *Value) ToMap() *Map {
	if !v.IsMap() {
		return nil
	}

	return wrapMap(v)
}

// ToSet returns the set value of the value. DO NOT FREE.
func (v *Value) ToSet() *Set {
	if !v.IsSet() {
		return nil
	}

	return wrapSet(v)
}

// New creates a new instance of the value as a constructor with the given arguments.
func (v *Value) New(args ...*Value) *Value {
	return v.CallConstructor(args...)
}

// CallConstructor calls the constructor with the given arguments.
func (v *Value) CallConstructor(args ...*Value) *Value {
	if !v.IsConstructor() {
		return v.runtime.NewError(ErrObjectNotAConstructor)
	}

	argc, argvPtr := createJsCallArgs(v.runtime, args...)
	defer v.runtime.freeHandle(argvPtr)

	jsCallArgs := []uint64{v.ctxPtr(), v.raw(), argc, argvPtr}

	return v.jsCall("JS_CallConstructor", jsCallArgs...)
}

func (v *Value) rawHandle() *rawHandle {
	return v.handle
}

func (v *Value) raw() uint64 {
	if v == nil || v.handle == nil {
		return 0
	}

	return v.handle.raw
}

func (v *Value) ctxPtr() uint64 {
	return v.runtime.ctxPtr()
}

func (v *Value) jsCall(name string, args ...uint64) *Value {
	return v.runtime.jsCall(name, args...)
}

func (v *Value) newUndefined() *Value {
	return v.runtime.NewUndefined()
}

func (v *Value) getOwnProperties() []ownProperty {
	ptr, entriesCount := v.runtime.callUnpack(
		"QJS_GetOwnPropertyNames",
		v.ctxPtr(),
		v.raw(),
	)
	if entriesCount == 0 {
		return []ownProperty{}
	}

	// Block size: number of entries * sizeof(JSPropertyEnum)
	blockSize := entriesCount * jsPropertyEnumSize
	bytes := v.runtime.memRead(ptr, uint64(blockSize))

	// SAFETY: This converts C memory layout to Go structs.
	// The memory comes from QJS C code and matches JSPropertyEnum layout.
	// This is safe because:
	// 1. Memory size is validated (size * jsPropertyEnumSize)
	// 2. JSPropertyEnum layout matches C struct layout
	// 3. Memory lifetime is managed by context.FreeHandle()
	entries := unsafe.Slice((*jsPropertyEnum)(unsafe.Pointer(&bytes[0])), entriesCount)

	property := make([]ownProperty, len(entries))

	for i, entry := range entries {
		property[i].isEnumerable = entry.isEnumerable
		property[i].atom = jsAtom{
			runtime: v.runtime,
			Value: v.runtime.newValue(newRawHandle(
				v.runtime,
				uint64(entry.atom),
			)),
		}
	}

	v.runtime.freeHandle(uint64(ptr))

	return property
}

func (v *Value) exception() error {
	cause := v.String()

	stack := v.GetPropertyStr("stack")
	defer stack.Free()

	if stack.IsUndefined() {
		return errors.New(cause)
	}

	stackStr := stack.String()

	return errors.New(cause + "\n" + stackStr)
}

func (v *Value) isProxyValue() bool {
	return v.IsObject() && v.isGlobalInstanceOf("QJS_PROXY_VALUE")
}

// isGlobalInstanceOf checks if the value is an instance of the given global constructor.
func (v *Value) isGlobalInstanceOf(name string) bool {
	ctor := v.runtime.Global().GetPropertyStr(name)
	defer ctor.Free()

	if ctor.IsUndefined() {
		return false
	}

	instanceOf := v.jsCall("QJS_IsInstanceOf", v.ctxPtr(), v.raw(), ctor.raw())

	return instanceOf.handle.Bool()
}

// toObject returns the object value of the value.
func (v *Value) toObject() *Value {
	return v.jsCall("JS_ToObject", v.ctxPtr(), v.raw())
}

func (v *Value) takeBytes() []byte {
	return v.handle.Bytes()
}

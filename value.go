package qngg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
)

// Value is a JavaScript value.
//
// Ownership follows one rule: every Value returned to you is yours and must
// be released with Free, and passing a Value to any method never takes it
// over. The exceptions are values you do not own, whose Free is a no-op:
// Runtime.Global, and the arguments and this value of a Function, which are
// only valid until it returns (Clone them to keep them). Values returned
// from a Function are handed to JavaScript.
//
// Using a Value after Free, or after its runtime is closed, fails with
// ErrFreed or ErrRuntimeClosed, or panics with it in methods without an
// error result.
type Value struct {
	rt       *Runtime
	ref      uint64 // JSValue
	borrowed bool
	freed    bool
}

// JSValue tags from quickjs.h. On wasm32, QuickJS uses NaN boxing: the tag
// is the upper 32 bits of the value.
const (
	tagFirst         = -9
	tagBigInt        = -9
	tagSymbol        = -8
	tagString        = -7
	tagStringRope    = -6
	tagObject        = -1
	tagInt           = 0
	tagBool          = 1
	tagNull          = 2
	tagUndefined     = 3
	tagUninitialized = 4
	tagException     = 6
	tagShortBigInt   = 7
	tagFloat64       = 8
	// float64TagAddend is JS_FLOAT64_TAG_ADDEND, the offset NaN boxing
	// subtracts from the upper bits of a float64.
	float64TagAddend = 0x7ff80000 - tagFirst + 1
	// tagNaN is the tag of the canonical NaN.
	tagNaN = int32(0x7ff80000 - float64TagAddend)

	jsUndefined = uint64(tagUndefined) << 32
)

func tagOf(raw uint64) int32 { return int32(raw >> 32) }

func isFloat64Tag(tag int32) bool { return uint32(tag-tagFirst) >= tagFloat64-tagFirst }

// hasRefCount reports whether a value is reference counted, and must be
// duplicated and freed.
func hasRefCount(raw uint64) bool {
	tag := tagOf(raw)

	return tag >= tagFirst && tag < 0
}

func float64Of(raw uint64) float64 {
	return math.Float64frombits(raw + uint64(float64TagAddend)<<32)
}

// Runtime returns the runtime the value belongs to.
func (v *Value) Runtime() *Runtime {
	return v.rt
}

// Free releases the value. It is a no-op for values you do not own, for
// values already freed, and once the runtime is closed.
func (v *Value) Free() {
	if v == nil || v.borrowed || v.freed {
		return
	}

	v.freed = true
	v.rt.freeValue(v.ref)
}

// Clone returns a new, owned reference to the same JavaScript value.
func (v *Value) Clone() *Value {
	return v.rt.newValue(v.rt.dupValue(v.raw()))
}

// Type predicates.

// IsUndefined reports whether the value is undefined.
func (v *Value) IsUndefined() bool { return tagOf(v.raw()) == tagUndefined }

// IsNull reports whether the value is null.
func (v *Value) IsNull() bool { return tagOf(v.raw()) == tagNull }

// IsBool reports whether the value is a boolean.
func (v *Value) IsBool() bool { return tagOf(v.raw()) == tagBool }

// IsNumber reports whether the value is a number, including NaN and
// Infinity.
func (v *Value) IsNumber() bool {
	tag := tagOf(v.raw())

	return tag == tagInt || isFloat64Tag(tag)
}

// IsNaN reports whether the value is the number NaN.
func (v *Value) IsNaN() bool { return tagOf(v.raw()) == tagNaN }

// IsInfinity reports whether the value is the number Infinity or -Infinity.
func (v *Value) IsInfinity() bool {
	raw := v.raw()

	return isFloat64Tag(tagOf(raw)) && math.IsInf(float64Of(raw), 0)
}

// IsBigInt reports whether the value is a BigInt.
func (v *Value) IsBigInt() bool {
	tag := tagOf(v.raw())

	return tag == tagBigInt || tag == tagShortBigInt
}

// IsString reports whether the value is a string.
func (v *Value) IsString() bool {
	tag := tagOf(v.raw())

	return tag == tagString || tag == tagStringRope
}

// IsSymbol reports whether the value is a symbol.
func (v *Value) IsSymbol() bool { return tagOf(v.raw()) == tagSymbol }

// IsObject reports whether the value is an object, including functions and
// arrays.
func (v *Value) IsObject() bool { return tagOf(v.raw()) == tagObject }

// IsArray reports whether the value is an array.
func (v *Value) IsArray() bool {
	return v.IsObject() && v.rt.call("QJS_IsArray", v.ref) != 0
}

// IsFunction reports whether the value is a function.
func (v *Value) IsFunction() bool {
	return v.IsObject() && v.rt.call("QJS_IsFunction", v.rt.jsCtx, v.ref) != 0
}

// IsConstructor reports whether the value is a constructor.
func (v *Value) IsConstructor() bool {
	return v.IsObject() && v.rt.call("QJS_IsConstructor", v.rt.jsCtx, v.ref) != 0
}

// IsPromise reports whether the value is a promise.
func (v *Value) IsPromise() bool {
	return v.IsObject() && v.rt.call("QJS_IsPromise", v.rt.jsCtx, v.ref) != 0
}

// IsError reports whether the value is an Error object.
func (v *Value) IsError() bool {
	return v.IsObject() && v.rt.call("QJS_IsError", v.rt.jsCtx, v.ref) != 0
}

// IsDate reports whether the value is a Date object.
func (v *Value) IsDate() bool {
	return v.IsObject() && v.rt.call("JS_IsDate", v.ref) != 0
}

// IsMap reports whether the value is a Map.
func (v *Value) IsMap() bool { return v.IsObject() && v.isGlobalInstanceOf("Map") }

// IsSet reports whether the value is a Set.
func (v *Value) IsSet() bool { return v.IsObject() && v.isGlobalInstanceOf("Set") }

// IsArrayBuffer reports whether the value is an ArrayBuffer.
func (v *Value) IsArrayBuffer() bool { return v.IsObject() && v.isGlobalInstanceOf("ArrayBuffer") }

// Type returns the name of the value's type, such as "String", "Array" or
// "Constructor Foo".
func (v *Value) Type() string {
	switch {
	case v.IsSymbol():
		return "Symbol"
	case v.isProxyValue():
		return "QJSProxyValue"
	case v.IsNaN():
		return "NaN"
	case v.IsInfinity():
		return "Infinity"
	case v.IsBigInt():
		return "BigInt"
	case v.IsNumber():
		return "Number"
	case v.IsDate():
		return "Date"
	case v.IsBool():
		return "Boolean"
	case v.IsNull():
		return "Null"
	case v.IsUndefined():
		return "Undefined"
	case tagOf(v.ref) == tagUninitialized:
		return "Uninitialized"
	case v.IsString():
		return "String"
	case v.IsError():
		return errorName
	case v.IsArray():
		return "Array"
	case v.IsMap():
		return "Map"
	case v.IsSet():
		return "Set"
	case v.IsPromise():
		return "Promise"
	case v.IsConstructor():
		if name, err := v.Get("name"); err == nil {
			defer name.Free()

			if s := name.String(); name.IsString() && s != "" {
				return "Constructor " + s
			}
		}

		return "Constructor"
	case v.IsFunction():
		return "Function"
	case v.IsArrayBuffer():
		return "ArrayBuffer"
	}

	return "unknown"
}

// Conversions.

// String converts the value to a string like JavaScript's String(). It
// returns "" if the conversion throws.
func (v *Value) String() string {
	if v.check() != nil {
		return "<invalid qngg.Value>"
	}

	box := v.rt.call("QJS_ToCString", v.rt.jsCtx, v.ref)
	if box == 0 {
		v.rt.clearException()

		return ""
	}

	return v.rt.takeString(box)
}

// Bool converts the value to a boolean using JavaScript truthiness.
func (v *Value) Bool() bool {
	raw := v.raw()
	if tagOf(raw) == tagBool {
		return int32(raw) != 0
	}

	return v.rt.call("JS_ToBool", v.rt.jsCtx, raw) == 1
}

// Float64 converts the value to a number like JavaScript's Number(). It
// returns 0 if the conversion throws.
func (v *Value) Float64() float64 {
	raw := v.raw()

	switch tag := tagOf(raw); {
	case tag == tagInt:
		return float64(int32(raw))
	case isFloat64Tag(tag):
		return float64Of(raw)
	}

	return math.Float64frombits(v.convert("QJS_ToFloat64"))
}

// Int32 converts the value to a 32-bit integer like JavaScript's `x | 0`.
// It returns 0 if the conversion throws.
func (v *Value) Int32() int32 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return int32(raw)
	}

	return int32(v.convert("QJS_ToInt32"))
}

// Uint32 converts the value to an unsigned 32-bit integer like
// JavaScript's `x >>> 0`. It returns 0 if the conversion throws.
func (v *Value) Uint32() uint32 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return uint32(int32(raw))
	}

	return uint32(v.convert("QJS_ToUint32"))
}

// Int64 converts the value to a 64-bit integer, truncating fractions and
// clamping out-of-range numbers. It returns 0 if the conversion throws.
func (v *Value) Int64() int64 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return int64(int32(raw))
	}

	return int64(v.convert("QJS_ToInt64"))
}

// BigInt returns the value of a BigInt, or nil if the value is not one.
func (v *Value) BigInt() *big.Int {
	if !v.IsBigInt() {
		return nil
	}

	n, _ := new(big.Int).SetString(v.String(), 10)

	return n
}

// Time returns the time of a Date object.
func (v *Value) Time() (time.Time, error) {
	if err := v.check(); err != nil {
		return time.Time{}, err
	}

	if !v.IsDate() {
		return time.Time{}, newInvalidJsInputErr("Date", v)
	}

	ms := math.Float64frombits(v.rt.call("QJS_ToEpochTime", v.rt.jsCtx, v.ref))
	if math.IsNaN(ms) {
		return time.Time{}, errors.New("invalid Date")
	}

	return time.UnixMilli(int64(ms)), nil
}

// Bytes returns a copy of the contents of an ArrayBuffer, typed array or
// DataView.
func (v *Value) Bytes() ([]byte, error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	if v.IsArrayBuffer() {
		return v.rt.takeBytes(v.rt.call("QJS_GetArrayBuffer", v.rt.jsCtx, v.ref)), nil
	}

	return jsTypedArrayToGo(v)
}

// MarshalJSON returns the JSON.stringify representation of the value.
// Values that JSON cannot represent, such as undefined and functions, are
// encoded as null.
func (v *Value) MarshalJSON() ([]byte, error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	s, err := v.jsonStringify()
	if err != nil {
		return nil, err
	}

	if s == "undefined" {
		s = "null"
	}

	return []byte(s), nil
}

// Properties.

// Get returns the property `name`. Getters may run, and their exceptions
// are returned.
func (v *Value) Get(name string) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	cname := v.rt.newCString(name)
	defer v.rt.free(cname)

	return v.rt.result(v.rt.call("JS_GetPropertyStr", v.rt.jsCtx, v.ref, cname))
}

// GetKey returns the property whose key is the given value, such as a
// symbol.
func (v *Value) GetKey(key *Value) (_ *Value, err error) {
	if err := checkAll(v, key); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	atom := v.rt.call("JS_ValueToAtom", v.rt.jsCtx, key.ref)
	defer v.rt.call("JS_FreeAtom", v.rt.jsCtx, atom)

	return v.rt.result(v.rt.call("JS_GetProperty", v.rt.jsCtx, v.ref, atom))
}

// Index returns the element at index i.
func (v *Value) Index(i uint32) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	return v.rt.result(v.rt.call("QJS_GetPropertyUint32", v.rt.jsCtx, v.ref, uint64(i)))
}

// Set sets the property `name` to val. Setters may run, and their
// exceptions are returned.
func (v *Value) Set(name string, val *Value) (err error) {
	if err := checkAll(v, val); err != nil {
		return err
	}

	defer recoverInterrupt(v.rt, &err)

	cname := v.rt.newCString(name)
	defer v.rt.free(cname)

	_, err = v.rt.status(v.rt.call("JS_SetPropertyStr", v.rt.jsCtx, v.ref, cname, v.rt.dupValue(val.ref)))

	return err
}

// SetKey sets the property whose key is the given value to val.
func (v *Value) SetKey(key, val *Value) (err error) {
	if err := checkAll(v, key, val); err != nil {
		return err
	}

	defer recoverInterrupt(v.rt, &err)

	atom := v.rt.call("JS_ValueToAtom", v.rt.jsCtx, key.ref)
	defer v.rt.call("JS_FreeAtom", v.rt.jsCtx, atom)

	_, err = v.rt.status(v.rt.call("JS_SetProperty", v.rt.jsCtx, v.ref, atom, v.rt.dupValue(val.ref)))

	return err
}

// SetIndex sets the element at index i to val.
func (v *Value) SetIndex(i uint32, val *Value) (err error) {
	if err := checkAll(v, val); err != nil {
		return err
	}

	defer recoverInterrupt(v.rt, &err)

	_, err = v.rt.status(v.rt.call("QJS_SetPropertyUint32", v.rt.jsCtx, v.ref, uint64(i), v.rt.dupValue(val.ref)))

	return err
}

// Has reports whether the value has the property `name`, own or inherited.
func (v *Value) Has(name string) (_ bool, err error) {
	if err := v.check(); err != nil {
		return false, err
	}

	defer recoverInterrupt(v.rt, &err)

	atom := v.rt.newAtom(name)
	defer v.rt.call("JS_FreeAtom", v.rt.jsCtx, atom)

	return v.rt.status(v.rt.call("JS_HasProperty", v.rt.jsCtx, v.ref, atom))
}

// Delete deletes the own property `name`. It reports false if the property
// exists but cannot be deleted.
func (v *Value) Delete(name string) (_ bool, err error) {
	if err := v.check(); err != nil {
		return false, err
	}

	defer recoverInterrupt(v.rt, &err)

	atom := v.rt.newAtom(name)
	defer v.rt.call("JS_FreeAtom", v.rt.jsCtx, atom)

	return v.rt.status(v.rt.call("JS_DeleteProperty", v.rt.jsCtx, v.ref, atom, 0))
}

// Keys returns the value's own enumerable string keys, like Object.keys.
func (v *Value) Keys() (_ []string, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	object, err := v.rt.global.Get("Object")
	if err != nil {
		return nil, err
	}
	defer object.Free()

	keys, err := object.Invoke(context.Background(), "keys", v)
	if err != nil {
		return nil, err
	}
	defer keys.Free()

	return keys.As[[]string]()
}

// Len returns the value of the `length` property as an integer, or 0 if
// it has none.
func (v *Value) Len() int64 {
	return propInt(v, "length")
}

// propInt returns the integer value of a property, or 0.
func propInt(v *Value, name string) int64 {
	prop, err := v.Get(name)
	if err != nil {
		return 0
	}
	defer prop.Free()

	return prop.Int64()
}

// Calls.

// Call calls the function value with the given this value, which may be
// nil for undefined, and arguments. Arguments that are not *Value are
// converted with Runtime.ToValue.
func (v *Value) Call(ctx context.Context, this *Value, args ...any) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	thisRaw := jsUndefined
	if this != nil {
		if thisRaw, err = this.ref, this.check(); err != nil {
			return nil, err
		}
	}

	if err := v.rt.enter(ctx); err != nil {
		return nil, err
	}
	defer v.rt.exit()
	defer recoverInterrupt(v.rt, &err)

	argc, argv, release, err := v.rt.callArgs(args)
	if err != nil {
		return nil, err
	}
	defer release()

	return v.rt.result(v.rt.call("QJS_Call", v.rt.jsCtx, v.ref, thisRaw, argc, argv))
}

// Invoke calls the method `name` of the value, like value[name](...args).
func (v *Value) Invoke(ctx context.Context, name string, args ...any) (*Value, error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	if !v.IsObject() {
		return nil, errCallFuncOnNonObject
	}

	fn, err := v.Get(name)
	if err != nil {
		return nil, err
	}
	defer fn.Free()

	if !fn.IsFunction() {
		return nil, fmt.Errorf("JS property '%s' is not a function", name)
	}

	return fn.Call(ctx, v, args...)
}

// New calls the value as a constructor, like `new value(...args)`.
func (v *Value) New(ctx context.Context, args ...any) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	if !v.IsConstructor() {
		return nil, errObjectNotAConstructor
	}

	if err := v.rt.enter(ctx); err != nil {
		return nil, err
	}
	defer v.rt.exit()
	defer recoverInterrupt(v.rt, &err)

	argc, argv, release, err := v.rt.callArgs(args)
	if err != nil {
		return nil, err
	}
	defer release()

	return v.rt.result(v.rt.call("JS_CallConstructor", v.rt.jsCtx, v.ref, argc, argv))
}

// Await waits for a promise to settle, running pending jobs and timers, and
// returns its result. A rejection is returned as an error.
func (v *Value) Await(ctx context.Context) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	if !v.IsPromise() {
		return nil, newInvalidJsInputErr("Promise", v)
	}

	if err := v.rt.enter(ctx); err != nil {
		return nil, err
	}
	defer v.rt.exit()
	defer recoverInterrupt(v.rt, &err)

	// js_std_await takes ownership of the promise.
	return v.rt.result(v.rt.call("js_std_await", v.rt.jsCtx, v.rt.dupValue(v.ref)))
}

// As converts the value to a Go value of type T. Objects convert to maps,
// structs (matching fields by their json tag or name) and slices; functions
// convert to Go functions that call them; ProxyValues convert back to the Go
// value they wrap.
func (v *Value) As[T any]() (T, error) {
	if err := v.check(); err != nil {
		var zero T

		return zero, err
	}

	return toGoValue[T](newConvTracker[uint64](), v)
}

// callArgs converts and marshals call arguments into wasm memory. release
// frees the array and the values converted here.
func (r *Runtime) callArgs(args []any) (argc, argv uint64, release func(), err error) {
	if len(args) == 0 {
		return 0, 0, func() {}, nil
	}

	var converted []*Value

	release = func() {
		for _, v := range converted {
			v.Free()
		}

		r.free(argv)
	}

	buf := make([]byte, 8*len(args))

	for i, arg := range args {
		jsArg, ok := arg.(*Value)
		if !ok {
			if jsArg, err = r.ToValue(arg); err != nil {
				release()

				return 0, 0, nil, fmt.Errorf("argument %d: %w", i, err)
			}

			converted = append(converted, jsArg)
		} else if err := jsArg.check(); err != nil {
			release()

			return 0, 0, nil, fmt.Errorf("argument %d: %w", i, err)
		}

		binary.LittleEndian.PutUint64(buf[8*i:], jsArg.ref)
	}

	argv = r.newBuffer(buf)

	return uint64(len(args)), argv, release, nil
}

// toError converts a thrown value into an *Error.
func (v *Value) toError() error {
	e := &Error{}

	if !v.IsError() {
		e.Message = v.String()

		return e
	}

	for _, field := range [...]struct {
		name string
		dst  *string
	}{{"name", &e.Name}, {"message", &e.Message}, {"stack", &e.Stack}} {
		if prop, err := v.Get(field.name); err == nil {
			if !prop.IsUndefined() {
				*field.dst = prop.String()
			}

			prop.Free()
		}
	}

	e.Stack = strings.TrimRight(e.Stack, "\n")

	return e
}

// jsonStringify returns JSON.stringify(v), or "undefined" for values JSON
// cannot represent.
func (v *Value) jsonStringify() (_ string, err error) {
	defer recoverInterrupt(v.rt, &err)

	box := v.rt.call("QJS_JSONStringify", v.rt.jsCtx, v.raw())
	if box == 0 {
		return "", fmt.Errorf("failed to stringify JS value: %w", v.rt.takeException())
	}

	return v.rt.takeString(box), nil
}

// isProxyValue reports whether the value wraps a Go value.
func (v *Value) isProxyValue() bool {
	return v.IsObject() && v.isGlobalInstanceOf("QJS_PROXY_VALUE")
}

// isGlobalInstanceOf reports whether the value is an instance of the global
// constructor `name`.
func (v *Value) isGlobalInstanceOf(name string) bool {
	ctor, err := v.rt.global.Get(name)
	if err != nil {
		return false
	}
	defer ctor.Free()

	if ctor.IsUndefined() {
		return false
	}

	return v.rt.call("QJS_IsInstanceOf", v.rt.jsCtx, v.raw(), ctor.ref) != 0
}

// jsPropertyEnumSize is the size of the C JSPropertyEnum, a bool followed
// by the uint32 atom at offset 4.
const jsPropertyEnumSize = 8

// forEach calls fn with every own property key and value, including
// non-enumerable and symbol keys, except `length`, until fn returns an
// error. Properties whose getter throws are skipped. Both values are only
// valid during the call.
func (v *Value) forEach(fn func(key, value *Value) error) error {
	if !v.IsObject() {
		return nil
	}

	box := v.rt.call("QJS_GetOwnPropertyNames", v.rt.jsCtx, v.ref)
	if box == 0 {
		v.rt.clearException()

		return nil
	}

	ptr, count := v.rt.mem.UnpackPtr(box)
	v.rt.free(box)

	defer v.rt.free(uint64(ptr))

	entries := bytes.Clone(v.rt.mem.MustRead(ptr, uint64(count*jsPropertyEnumSize)))
	atoms := make([]uint64, count)

	for i := range atoms {
		atoms[i] = uint64(binary.LittleEndian.Uint32(entries[uint32(i)*jsPropertyEnumSize+4:]))
	}

	defer func() {
		for _, atom := range atoms {
			v.rt.call("JS_FreeAtom", v.rt.jsCtx, atom)
		}
	}()

	for _, atom := range atoms {
		if err := v.forEachProperty(atom, fn); err != nil {
			return err
		}
	}

	return nil
}

// forEachProperty calls fn with the property `atom` of v, unless it is
// `length` or its getter throws.
func (v *Value) forEachProperty(atom uint64, fn func(key, value *Value) error) error {
	key := v.rt.newValue(v.rt.call("JS_AtomToValue", v.rt.jsCtx, atom))
	defer key.Free()

	if key.IsString() && key.String() == "length" {
		return nil
	}

	value, err := v.rt.result(v.rt.call("JS_GetProperty", v.rt.jsCtx, v.ref, atom))
	if err != nil {
		// Throwing getters are skipped.
		return nil
	}
	defer value.Free()

	return fn(key, value)
}

// ref returns the JSValue, panicking if it cannot be used.
func (v *Value) raw() uint64 {
	if err := v.check(); err != nil {
		panic(err)
	}

	return v.ref
}

// checkAll reports the first value of vs that cannot be used.
func checkAll(vs ...*Value) error {
	for _, v := range vs {
		if err := v.check(); err != nil {
			return err
		}
	}

	return nil
}

// check reports whether v can be used.
func (v *Value) check() error {
	switch {
	case v == nil:
		return errors.New("nil *qngg.Value")
	case v.freed:
		return ErrFreed
	case v.rt.closed:
		return ErrRuntimeClosed
	}

	return nil
}

// take hands the value's reference over to the caller, who passes it to a
// QuickJS function that takes ownership. Values the caller does not own are
// duplicated instead.
func (v *Value) take() uint64 {
	raw := v.raw()
	if v.borrowed {
		return v.rt.dupValue(raw)
	}

	v.freed = true

	return raw
}

// convert runs a QJS_ToX conversion helper. The helpers return 0 when the
// conversion throws, leaving the exception pending, so it is cleared then.
func (v *Value) convert(helper string) uint64 {
	result := v.rt.call(helper, v.rt.jsCtx, v.ref)
	if result == 0 {
		v.rt.clearException()
	}

	return result
}

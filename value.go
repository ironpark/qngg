package qngg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"runtime"
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
	// kind+1 once known: the class of an object never changes
	kindPlus1 objectKind
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
	jsNaN       = uint64(1<<32+int64(tagNaN)) << 32
)

// objectKind classifies objects by their class, as QJS_GetKind does.
type objectKind int32

const (
	kindNone objectKind = iota // not an object
	kindObject
	kindArray
	kindFunction
	kindConstructor
	kindError
	kindDate
	kindRegExp
	kindMap
	kindSet
	kindPromise
	kindArrayBuffer
	kindTypedArray
	kindDataView
	kindProxyValue
)

func (k objectKind) isFunction() bool { return k == kindFunction || k == kindConstructor }

func (k objectKind) isTypedArray() bool { return k == kindTypedArray || k == kindDataView }

// JS_GPN_* flags of JS_GetOwnPropertyNames, and QJS_GPN_SKIP_LENGTH.
const (
	gpnString   = 1 << 0
	gpnSymbol   = 1 << 1
	gpnPrivate  = 1 << 2
	gpnEnumOnly = 1 << 4
	gpnNoLength = 1 << 8
)

func tagOf(raw uint64) int32 { return int32(raw >> 32) }

func isFloat64Tag(tag int32) bool { return uint32(tag-tagFirst) >= tagFloat64-tagFirst }

func isNumberTag(tag int32) bool { return tag == tagInt || isFloat64Tag(tag) }

func isBigIntTag(tag int32) bool { return tag == tagBigInt || tag == tagShortBigInt }

func isStringTag(tag int32) bool { return tag == tagString || tag == tagStringRope }

// hasRefCount reports whether a value is reference counted, and must be
// duplicated and freed.
func hasRefCount(raw uint64) bool {
	tag := tagOf(raw)

	return tag >= tagFirst && tag < 0
}

func float64Of(raw uint64) float64 {
	return math.Float64frombits(raw + uint64(float64TagAddend)<<32)
}

// float64Raw boxes a float64 like JS_NewFloat64, normalizing NaNs.
func float64Raw(f float64) uint64 {
	bits := math.Float64bits(f)
	if bits&^(1<<63) > 0x7ff0000000000000 {
		return jsNaN
	}

	return bits - uint64(float64TagAddend)<<32
}

// int64Raw boxes an integer like JS_NewInt64: as an int if it fits in 32
// bits, and otherwise as a float64.
func int64Raw(i int64) uint64 {
	if i >= math.MinInt32 && i <= math.MaxInt32 {
		return uint64(tagInt)<<32 | uint64(uint32(i))
	}

	return float64Raw(float64(i))
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
	clone := v.rt.newValue(v.rt.dupValue(v.raw()))
	clone.kindPlus1 = v.kindPlus1

	return clone
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
func (v *Value) IsNumber() bool { return isNumberTag(tagOf(v.raw())) }

// IsNaN reports whether the value is the number NaN.
func (v *Value) IsNaN() bool { return tagOf(v.raw()) == tagNaN }

// IsInfinity reports whether the value is the number Infinity or -Infinity.
func (v *Value) IsInfinity() bool {
	raw := v.raw()

	return isFloat64Tag(tagOf(raw)) && math.IsInf(float64Of(raw), 0)
}

// IsBigInt reports whether the value is a BigInt.
func (v *Value) IsBigInt() bool { return isBigIntTag(tagOf(v.raw())) }

// IsString reports whether the value is a string.
func (v *Value) IsString() bool { return isStringTag(tagOf(v.raw())) }

// IsSymbol reports whether the value is a symbol.
func (v *Value) IsSymbol() bool { return tagOf(v.raw()) == tagSymbol }

// IsObject reports whether the value is an object, including functions and
// arrays.
func (v *Value) IsObject() bool { return tagOf(v.raw()) == tagObject }

// IsArray reports whether the value is an array.
func (v *Value) IsArray() bool { return v.kind() == kindArray }

// IsFunction reports whether the value is a function.
func (v *Value) IsFunction() bool { return v.kind().isFunction() }

// IsConstructor reports whether the value is a constructor.
func (v *Value) IsConstructor() bool { return v.kind() == kindConstructor }

// IsPromise reports whether the value is a promise.
func (v *Value) IsPromise() bool { return v.kind() == kindPromise }

// IsError reports whether the value is an Error object.
func (v *Value) IsError() bool { return v.kind() == kindError }

// IsDate reports whether the value is a Date object.
func (v *Value) IsDate() bool { return v.kind() == kindDate }

// IsMap reports whether the value is a Map.
func (v *Value) IsMap() bool { return v.kind() == kindMap }

// IsSet reports whether the value is a Set.
func (v *Value) IsSet() bool { return v.kind() == kindSet }

// IsArrayBuffer reports whether the value is an ArrayBuffer.
func (v *Value) IsArrayBuffer() bool { return v.kind() == kindArrayBuffer }

// Type returns the name of the value's type, such as "String", "Array" or
// "Constructor Foo".
func (v *Value) Type() string {
	raw := v.raw()

	switch tag := tagOf(raw); {
	case tag == tagSymbol:
		return "Symbol"
	case tag == tagNaN:
		return "NaN"
	case isFloat64Tag(tag) && math.IsInf(float64Of(raw), 0):
		return "Infinity"
	case isBigIntTag(tag):
		return "BigInt"
	case isNumberTag(tag):
		return "Number"
	case tag == tagBool:
		return "Boolean"
	case tag == tagNull:
		return "Null"
	case tag == tagUndefined:
		return "Undefined"
	case tag == tagUninitialized:
		return "Uninitialized"
	case isStringTag(tag):
		return "String"
	case tag == tagObject:
		return v.objectType()
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

	s, ok := v.rt.callString(xToStringBuf, v.ref)
	if !ok {
		v.rt.clearException()
	}

	return s
}

// Bool converts the value to a boolean using JavaScript truthiness.
func (v *Value) Bool() bool {
	raw := v.raw()
	if tagOf(raw) == tagBool {
		return int32(raw) != 0
	}

	return v.rt.call(xToBool, v.rt.jsCtx, raw) == 1
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

	return math.Float64frombits(v.convert(xToFloat64))
}

// Int32 converts the value to a 32-bit integer like JavaScript's `x | 0`.
// It returns 0 if the conversion throws.
func (v *Value) Int32() int32 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return int32(raw)
	}

	return int32(v.convert(xToInt32))
}

// Uint32 converts the value to an unsigned 32-bit integer like
// JavaScript's `x >>> 0`. It returns 0 if the conversion throws.
func (v *Value) Uint32() uint32 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return uint32(int32(raw))
	}

	return uint32(v.convert(xToUint32))
}

// Int64 converts the value to a 64-bit integer, truncating fractions and
// clamping out-of-range numbers. It returns 0 if the conversion throws.
func (v *Value) Int64() int64 {
	if raw := v.raw(); tagOf(raw) == tagInt {
		return int64(int32(raw))
	}

	return int64(v.convert(xToInt64))
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

	ms := math.Float64frombits(v.rt.call(xToEpochTime, v.rt.jsCtx, v.ref))
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

	if !v.IsArrayBuffer() {
		return jsTypedArrayToGo(v)
	}

	res := v.rt.call(xGetArrayBuffer, v.rt.jsCtx, v.ref)
	if res == outFailed {
		return nil, v.rt.takeException()
	}

	var data []byte
	if size := uint32(res); size > 0 {
		data = bytes.Clone(v.rt.view(uint32(res>>32), size))
	}

	return data, nil
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

	cname := v.rt.push(name)
	defer v.rt.drop(cname)

	return v.rt.result(v.rt.call(xGetPropertyLen, v.rt.jsCtx, v.ref, uint64(cname.ptr), uint64(len(name))))
}

// GetKey returns the property whose key is the given value, such as a
// symbol.
func (v *Value) GetKey(key *Value) (_ *Value, err error) {
	if err := checkAll(v, key); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	return v.rt.result(v.rt.call(xGetPropertyValue, v.rt.jsCtx, v.ref, key.ref))
}

// Index returns the element at index i.
func (v *Value) Index(i uint32) (_ *Value, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	return v.rt.result(v.rt.call(xGetPropertyUint32, v.rt.jsCtx, v.ref, uint64(i)))
}

// Set sets the property `name` to val, converted with ToValue unless it is
// a *Value. Setters may run, and their exceptions are returned.
func (v *Value) Set(name string, val any) error {
	if err := v.check(); err != nil {
		return err
	}

	jsVal, owned, err := v.rt.argValue(val)
	if err != nil {
		return err
	}

	if owned {
		return v.setProp(name, jsVal.take(), false)
	}

	return v.setProp(name, jsVal.ref, true)
}

// SetKey sets the property whose key is the given value to val, converted
// like in Set.
func (v *Value) SetKey(key *Value, val any) (err error) {
	if err := checkAll(v, key); err != nil {
		return err
	}

	jsVal, owned, err := v.rt.argValue(val)
	if err != nil {
		return err
	}

	if owned {
		defer jsVal.Free()
	}

	defer recoverInterrupt(v.rt, &err)

	_, err = v.rt.status(v.rt.call(xSetPropertyValue, v.rt.jsCtx, v.ref, key.ref, jsVal.ref))

	return err
}

// SetIndex sets the element at index i to val, converted like in Set.
func (v *Value) SetIndex(i uint32, val any) error {
	if err := v.check(); err != nil {
		return err
	}

	jsVal, owned, err := v.rt.argValue(val)
	if err != nil {
		return err
	}

	if owned {
		return v.setIndex(i, jsVal.take(), false)
	}

	return v.setIndex(i, jsVal.ref, true)
}

// Has reports whether the value has the property `name`, own or inherited.
func (v *Value) Has(name string) (_ bool, err error) {
	if err := v.check(); err != nil {
		return false, err
	}

	defer recoverInterrupt(v.rt, &err)

	cname := v.rt.push(name)
	defer v.rt.drop(cname)

	return v.rt.status(v.rt.call(xHasPropertyLen, v.rt.jsCtx, v.ref, uint64(cname.ptr), uint64(len(name))))
}

// Delete deletes the own property `name`. It reports false if the property
// exists but cannot be deleted.
func (v *Value) Delete(name string) (_ bool, err error) {
	if err := v.check(); err != nil {
		return false, err
	}

	defer recoverInterrupt(v.rt, &err)

	cname := v.rt.push(name)
	defer v.rt.drop(cname)

	return v.rt.status(v.rt.call(xDeletePropertyLen, v.rt.jsCtx, v.ref, uint64(cname.ptr), uint64(len(name))))
}

// Keys returns the value's own enumerable string keys, like Object.keys.
func (v *Value) Keys() (_ []string, err error) {
	if err := v.check(); err != nil {
		return nil, err
	}

	defer recoverInterrupt(v.rt, &err)

	atoms, err := v.ownAtoms(gpnString | gpnEnumOnly)
	if err != nil {
		return nil, err
	}
	defer v.rt.freeAtoms(atoms)

	keys := make([]string, len(atoms))
	for i, atom := range atoms {
		if keys[i], err = v.rt.atomString(atom); err != nil {
			return nil, err
		}
	}

	return keys, nil
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

	return v.rt.callWith(args, func(argc, argv uint64) uint64 {
		return v.rt.call(xCall, v.rt.jsCtx, v.ref, thisRaw, argc, argv)
	})
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

	return v.rt.callWith(args, func(argc, argv uint64) uint64 {
		return v.rt.call(xCallConstructor, v.rt.jsCtx, v.ref, argc, argv)
	})
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
	return v.rt.result(v.rt.call(xStdAwait, v.rt.jsCtx, v.rt.dupValue(v.ref)))
}

// As converts the value to a Go value of type T. Objects convert to maps,
// structs (matching fields by their json tag or name) and slices; functions
// convert to Go functions that call them; BigInts convert to *big.Int or to
// integer types they fit in; ProxyValues convert back to the Go value they
// wrap.
func (v *Value) As[T any]() (T, error) {
	if err := v.check(); err != nil {
		var zero T

		return zero, err
	}

	return toGoValue[T](newConvTracker(), v)
}

// objectType is Type for objects.
func (v *Value) objectType() string {
	switch v.kind() {
	case kindProxyValue:
		return "QJSProxyValue"
	case kindDate:
		return "Date"
	case kindError:
		return errorName
	case kindArray:
		return "Array"
	case kindMap:
		return "Map"
	case kindSet:
		return "Set"
	case kindPromise:
		return "Promise"
	case kindConstructor:
		if name, err := v.Get("name"); err == nil {
			defer name.Free()

			if s := name.String(); name.IsString() && s != "" {
				return "Constructor " + s
			}
		}

		return "Constructor"
	case kindFunction:
		return "Function"
	case kindArrayBuffer:
		return "ArrayBuffer"
	case kindRegExp:
		return "RegExp"
	case kindTypedArray:
		return "TypedArray"
	case kindDataView:
		return "DataView"
	}

	return "Object"
}

// argValue returns arg as a JS value: arg itself if it is a *Value, or else
// arg converted with ToValue, which the caller owns and must free or take.
func (r *Runtime) argValue(arg any) (_ *Value, owned bool, err error) {
	if v, ok := arg.(*Value); ok {
		return v, false, v.check()
	}

	v, err := r.ToValue(arg)

	return v, err == nil, err
}

// callWith converts args, copies them into a temporary array and returns
// the result of the C call made by fn with it.
func (r *Runtime) callWith(args []any, fn func(argc, argv uint64) uint64) (*Value, error) {
	var rawBuf [8]uint64

	var ownedBuf [8]*Value

	raws, owned := rawBuf[:0], ownedBuf[:0]

	defer func() {
		for _, v := range owned {
			v.Free()
		}
	}()

	for i, arg := range args {
		jsArg, isOwned, err := r.argValue(arg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}

		if isOwned {
			owned = append(owned, jsArg)
		}

		raws = append(raws, jsArg.ref)
	}

	argv := r.pushValues(raws...)
	defer r.drop(argv)

	return r.result(fn(uint64(len(raws)), uint64(argv.ptr)))
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

	s, ok := v.rt.callString(xJSONStringifyBuf, v.raw())
	if !ok {
		return "", fmt.Errorf("failed to stringify JS value: %w", v.rt.takeException())
	}

	return s, nil
}

// kind classifies the value.
func (v *Value) kind() objectKind {
	raw := v.raw()
	if tagOf(raw) != tagObject {
		return kindNone
	}

	if v.kindPlus1 == 0 {
		v.kindPlus1 = objectKind(v.rt.call(xGetKind, v.rt.jsCtx, raw)) + 1
	}

	return v.kindPlus1 - 1
}

// isProxyValue reports whether the value wraps a Go value.
func (v *Value) isProxyValue() bool { return v.kind() == kindProxyValue }

// proxyID returns the registry ID of the Go value wrapped by a proxy value,
// or 0 if the value is not one.
func (v *Value) proxyID() uint32 {
	return uint32(v.rt.call(xGetProxyID, v.raw()))
}

// ownAtoms returns the atoms of the value's own properties selected by the
// gpn* flags. The caller frees them with freeAtoms.
func (v *Value) ownAtoms(flags uint64) (atoms []uint32, err error) {
	ok := v.rt.callOut(xGetOwnPropertyAtoms, v.rt.freePtr, func(data []byte) {
		atoms = make([]uint32, len(data)/4)
		for i := range atoms {
			atoms[i] = binary.LittleEndian.Uint32(data[4*i:])
		}
	}, v.ref, flags)
	if !ok {
		return nil, v.rt.takeException()
	}

	return atoms, nil
}

// freeAtoms frees atoms. Like free, it is a no-op once the runtime is
// closed or interrupted.
func (r *Runtime) freeAtoms(atoms []uint32) {
	if len(atoms) == 0 || r.interruptErr() != nil {
		return
	}

	buf := r.alloc(uint32(4 * len(atoms)))
	defer r.drop(buf)

	view := r.view(buf.ptr, uint32(4*len(atoms)))
	for i, atom := range atoms {
		binary.LittleEndian.PutUint32(view[4*i:], atom)
	}

	r.call(xFreeAtoms, r.jsCtx, uint64(buf.ptr), uint64(len(atoms)))
}

// atomString returns the name of an atom.
func (r *Runtime) atomString(atom uint32) (string, error) {
	s, ok := r.callString(xAtomToStringBuf, uint64(atom))
	if !ok {
		return "", r.takeException()
	}

	return s, nil
}

// forEach calls fn with every own property key and value, including
// non-enumerable and symbol keys, except `length`, until fn returns an
// error. Properties whose getter throws are skipped. Both values are only
// valid during the call.
func (v *Value) forEach(fn func(key, value *Value) error) error {
	if !v.IsObject() {
		return nil
	}

	atoms, err := v.ownAtoms(gpnString | gpnSymbol | gpnPrivate | gpnNoLength)
	if err != nil {
		// Objects whose keys cannot be listed, such as revoked proxies,
		// have no entries.
		return nil
	}
	defer v.rt.freeAtoms(atoms)

	for _, atom := range atoms {
		if err := v.forEachProperty(atom, fn); err != nil {
			return err
		}
	}

	return nil
}

// forEachProperty calls fn with the property `atom` of v, unless its
// getter throws.
func (v *Value) forEachProperty(atom uint32, fn func(key, value *Value) error) error {
	key := v.rt.newValue(v.rt.call(xAtomToValue, v.rt.jsCtx, uint64(atom)))
	defer key.Free()

	value, err := v.rt.result(v.rt.call(xGetProperty, v.rt.jsCtx, v.ref, uint64(atom)))
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

// freeWhenCollected frees v, which must be owned and must not be freed
// otherwise, once v is garbage collected. It is for values the library
// keeps internally, with no owner to call Free.
func (v *Value) freeWhenCollected() {
	runtime.AddCleanup(v, v.rt.released.add, v.raw())
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
func (v *Value) convert(helper export) uint64 {
	result := v.rt.call(helper, v.rt.jsCtx, v.ref)
	if result == 0 {
		v.rt.clearException()
	}

	return result
}

// setProp sets the property `name` of v to raw, taking raw over unless dup
// is set.
func (v *Value) setProp(name string, raw uint64, dup bool) (err error) {
	defer recoverInterrupt(v.rt, &err)

	cname := v.rt.push(name)
	defer v.rt.drop(cname)

	_, err = v.rt.status(v.rt.call(xSetPropertyLen,
		v.rt.jsCtx, v.ref, uint64(cname.ptr), uint64(len(name)), raw, boolArg(dup)))

	return err
}

// setIndex is setProp for array elements.
func (v *Value) setIndex(i uint32, raw uint64, dup bool) (err error) {
	defer recoverInterrupt(v.rt, &err)

	_, err = v.rt.status(v.rt.call(xSetPropertyUint32, v.rt.jsCtx, v.ref, uint64(i), raw, boolArg(dup)))

	return err
}

func boolArg(b bool) uint64 {
	if b {
		return 1
	}

	return 0
}

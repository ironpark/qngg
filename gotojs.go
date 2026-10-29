package qngg

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"time"
)

// goStructToJs converts a Go struct to a JavaScript object.
// Includes both fields and methods as object properties.
func (tracker *convTracker) goStructToJs(
	rt *Runtime,
	rtype reflect.Type,
	rval reflect.Value,
) (*Value, error) {
	return withJSObject(rt, func(obj *Value) error {
		// Determine the struct type and value for field processing:
		// - pointer to struct: dereference for field processing.
		// - direct struct: use as-is.
		var (
			structType reflect.Type
			structVal  reflect.Value
		)

		if rtype.Kind() == reflect.Pointer {
			structType = rtype.Elem()
			structVal = rval.Elem()
		} else {
			structType = rtype
			structVal = rval
		}

		err := tracker.addStructFieldsToObject(rt, obj, structType, structVal)
		if err != nil {
			return err
		}

		// For methods, use the original type and value to preserve pointer receiver methods
		return tracker.addStructMethodsToObject(rt, obj, rtype, rval)
	})
}

// goSliceToJs converts a Go slice to a JavaScript array.
func (tracker *convTracker) goSliceToJs(rt *Runtime, rval reflect.Value) (*Value, error) {
	return tracker.arrayLikeToJS(rt, rval, "slice")
}

// goArrayToJs converts a Go array to a JavaScript array without unnecessary copying.
func (tracker *convTracker) goArrayToJs(rt *Runtime, rval reflect.Value) (*Value, error) {
	return tracker.arrayLikeToJS(rt, rval, "array")
}

// goMapToJs converts a Go map to a JavaScript object.
// Non-string keys are converted to string representation.
func (tracker *convTracker) goMapToJs(
	rt *Runtime,
	rval reflect.Value,
) (*Value, error) {
	return withJSObject(rt, func(obj *Value) error {
		for _, key := range rval.MapKeys() {
			value := rval.MapIndex(key)

			var keyStr string
			if key.Kind() == reflect.String {
				keyStr = key.String()
			} else {
				keyStr = fmt.Sprintf("%v", key.Interface())
			}

			jsValue, err := tracker.toJsValue(rt, value.Interface())
			if err != nil {
				return newGoToJsErr("map key: "+keyStr, err)
			}

			if err := setTake(obj, keyStr, jsValue); err != nil {
				return err
			}
		}

		return nil
	})
}

// goNumberToJs converts Go numeric types to appropriate JS number types.
func goNumberToJs[T numberType](rt *Runtime, i T) *Value {
	switch v := any(i).(type) {
	case int32:
		return rt.NewInt32(v)
	case uint32:
		return rt.NewUint32(v)
	case int64:
		return rt.NewInt64(v)
	case float64:
		return rt.NewFloat64(v)
	case float32:
		return rt.NewFloat64(float64(v))
	default:
		// Convert other integer types to int64, floats to float64
		switch any(i).(type) {
		case int, int8, int16:
			return rt.NewInt64(int64(i))
		case uint, uint8, uint16, uint64:
			if uint64(i) > math.MaxInt64 {
				return rt.NewBigUint64(uint64(i))
			}

			return rt.NewInt64(int64(i))
		default:
			return rt.NewFloat64(float64(i))
		}
	}
}

// goComplexToJs converts Go complex numbers to JS objects with real/imag properties.
func goComplexToJs[T complex64 | complex128](rt *Runtime, z T) *Value {
	obj := rt.NewObject()

	var realPart, imagPart float64

	switch v := any(z).(type) {
	case complex64:
		realPart = float64(real(v))
		imagPart = float64(imag(v))
	case complex128:
		realPart = real(v)
		imagPart = imag(v)
	}

	_ = setTake(obj, "real", rt.NewFloat64(realPart))
	_ = setTake(obj, "imag", rt.NewFloat64(imagPart))

	return obj
}

// tryConvertBuiltinTypes handles built-in Go types that don't require reflection.
func tryConvertBuiltinTypes(rt *Runtime, v any) *Value {
	switch val := v.(type) {
	case bool:
		return rt.NewBool(val)
	case string:
		return rt.NewString(val)
	case []byte:
		if val == nil {
			return rt.NewNull()
		}

		return rt.NewArrayBuffer(val)
	case time.Time:
		return rt.NewDate(val)
	case *time.Time:
		if val == nil {
			return rt.NewNull()
		}

		return rt.NewDate(*val)
	case uintptr:
		return rt.NewInt64(int64(val))
	}

	return nil
}

// tryConvertNumeric handles all numeric types in a consolidated way.
func tryConvertNumeric(rt *Runtime, v any) *Value {
	switch val := v.(type) {
	case int8:
		return goNumberToJs(rt, val)
	case int16:
		return goNumberToJs(rt, val)
	case int32:
		return goNumberToJs(rt, val)
	case int:
		return goNumberToJs(rt, val)
	case int64:
		return goNumberToJs(rt, val)
	case uint8:
		return goNumberToJs(rt, val)
	case uint16:
		return goNumberToJs(rt, val)
	case uint32:
		return goNumberToJs(rt, val)
	case uint:
		return goNumberToJs(rt, val)
	case uint64:
		// Large uint64 values use float64 to avoid overflow
		if val&(uint64(1)<<uint64SignBitPosition) == 0 {
			return goNumberToJs(rt, int64(val))
		}

		return rt.NewFloat64(float64(val))
	case float32:
		return goNumberToJs(rt, val)
	case float64:
		return goNumberToJs(rt, val)
	case complex64:
		return goComplexToJs(rt, val)
	case complex128:
		return goComplexToJs(rt, val)
	}

	return nil
}

// convertReflectValue handles types that require reflection.
func (tracker *convTracker) convertReflectValue(rt *Runtime, v any) (*Value, error) {
	rtype := reflect.TypeOf(v)
	rval := reflect.ValueOf(v)

	var ct circularTracker
	defer ct.cleanup()

	if rtype.Kind() == reflect.Pointer {
		if rval.IsNil() {
			return rt.NewNull(), nil
		}

		// Track pointer address for circular reference detection.
		if err := ct.trackPtr(tracker, rval.Pointer()); err != nil {
			return nil, newGoToJsErr(goTypeName(reflect.TypeOf(v)), err, "recursive pointer")
		}

		// If pointer points to a struct,
		// preserve the pointer type for method resolution.
		elemType := rtype.Elem()
		if elemType.Kind() == reflect.Struct {
			return tracker.goStructToJs(rt, rtype, rval)
		}

		return tracker.toJsValue(rt, rval.Elem().Interface())
	}

	switch rtype.Kind() {
	case reflect.Func:
		return funcToJS(rt, v)
	case reflect.Struct:
		return tracker.goStructToJs(rt, rtype, rval)
	case reflect.Slice:
		if rval.IsNil() {
			return rt.NewNull(), nil
		}

		return tracker.goSliceToJs(rt, rval)
	case reflect.Map:
		if rval.IsNil() {
			return rt.NewNull(), nil
		}

		return tracker.goMapToJs(rt, rval)
	case reflect.Array:
		return tracker.goArrayToJs(rt, rval)
	case reflect.Chan:
		return channelToJSObjectValue(rt, rtype, rval)
	case reflect.UnsafePointer:
		return nil, newGoToJsErr(goTypeName(rtype), nil)

	// Handle custom types based on their underlying type.
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return goNumberToJs(rt, rval.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintVal := rval.Uint()
		if uintVal > math.MaxInt64 {
			return rt.NewFloat64(float64(uintVal)), nil
		}

		return goNumberToJs(rt, int64(uintVal)), nil
	case reflect.Float32, reflect.Float64:
		return goNumberToJs(rt, rval.Float()), nil
	case reflect.String:
		return rt.NewString(rval.String()), nil
	case reflect.Bool:
		return rt.NewBool(rval.Bool()), nil
	}

	return nil, newGoToJsErr(rtype.Name(), nil)
}

// withJSObject creates a JS object and ensures it's freed on error.
func withJSObject(rt *Runtime, fn func(*Value) error) (*Value, error) {
	obj := rt.NewObject()
	if err := fn(obj); err != nil {
		obj.Free()

		return nil, err
	}

	return obj, nil
}

// addStructFieldsToObject sets properties of obj from the fields of the
// struct rval. Fields promoted through a nil embedded pointer are skipped.
func (tracker *convTracker) addStructFieldsToObject(
	rt *Runtime,
	obj *Value,
	rtype reflect.Type,
	rval reflect.Value,
) error {
	for _, field := range structFieldsOf(rtype).list {
		fieldValue, err := rval.FieldByIndexErr(field.index)
		if err != nil {
			continue
		}

		prop, err := tracker.toJsValue(rt, fieldValue.Interface())
		if err != nil {
			return err
		}

		if err := setTake(obj, field.name, prop); err != nil {
			return err
		}
	}

	return nil
}

// addStructMethodsToObject adds exported struct methods as JS functions.
func (tracker *convTracker) addStructMethodsToObject(
	rt *Runtime,
	obj *Value,
	rtype reflect.Type,
	rval reflect.Value,
) error {
	for i := range rtype.NumMethod() {
		method := rtype.Method(i)
		methodValue := rval.Method(i)

		jsMethod, err := funcToJS(rt, methodValue.Interface())
		if err != nil {
			return newGoToJsErr(
				goTypeName(rtype)+"."+method.Name,
				err,
				"struct method",
			)
		}

		if err := setTake(obj, method.Name, jsMethod); err != nil {
			return err
		}
	}

	return nil
}

// arrayLikeToJS is a helper that converts arrays and slices to JS arrays.
func (tracker *convTracker) arrayLikeToJS(
	rt *Runtime,
	rval reflect.Value,
	typeName string,
) (*Value, error) {
	arr := rt.NewArray()

	for i := range rval.Len() {
		elem := rval.Index(i)

		jsElem, err := tracker.toJsValue(rt, elem.Interface())
		if err != nil {
			arr.Free()

			return nil, newGoToJsErr(typeName+": "+goTypeName(rval.Type()), err)
		}

		if err := setIndexTake(arr, uint32(i), jsElem); err != nil {
			arr.Free()

			return nil, err
		}
	}

	return arr, nil
}

// toJsValue converts any Go value to a QuickJS Value using the conversion context.
func (tracker *convTracker) toJsValue(rt *Runtime, v any) (*Value, error) {
	if v == nil {
		return rt.NewNull(), nil
	}

	if jsVal, ok := v.(*Value); ok {
		if err := jsVal.check(); err != nil {
			return nil, err
		}

		return jsVal.Clone(), nil
	}

	if err, ok := v.(error); ok {
		return rt.NewError(err), nil
	}

	switch n := v.(type) {
	case *big.Int:
		if n == nil {
			return rt.NewNull(), nil
		}

		return rt.newBigInt(n)
	case big.Int:
		return rt.newBigInt(&n)
	}

	if jsVal := tryConvertBuiltinTypes(rt, v); jsVal != nil {
		return jsVal, nil
	}

	if jsVal := tryConvertNumeric(rt, v); jsVal != nil {
		return jsVal, nil
	}

	return tracker.convertReflectValue(rt, v)
}

package qngg

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"time"
)

// toGo converts a JS value to T. A sample of T's dynamic type may be given
// when T is an interface.
func toGo[T any](input *Value, samples ...T) (T, error) {
	result, err := toGoValue(newConvTracker(), input, targetOf(samples...))
	v, _ := result.(T)

	return v, err
}

var (
	valueType    = reflect.TypeFor[Value]()
	valuePtrType = reflect.TypeFor[*Value]()
)

// toGoValue converts a JS value to the Go type target, or to whatever Go
// value fits it best when target is nil or an interface.
func toGoValue(tracker *convTracker, input *Value, target reflect.Type) (result any, err error) {
	switch {
	case target == nil:
	case target == valuePtrType:
		return input, nil
	case target == valueType:
		return *input, nil
	case target.Kind() == reflect.Interface:
		target = nil
	}

	defer func() {
		result, err = convertResult("ToGoValue", result, err, target)
	}()

	kind := input.kind()

	// A proxy value converts back to the Go value it wraps.
	if kind == kindProxyValue {
		result, _ = input.rt.registry.Get(input.proxyID())

		return result, nil
	}

	var ok bool
	if result, ok, err = jsPrimitivesToGo(input, kind, target); ok {
		return result, err
	}

	switch kind {
	case kindDate:
		result, err = jsTimeToGo(input)
	case kindRegExp:
		result = input.String()
	case kindArrayBuffer:
		result, err = jsArrayBufferToGo(input)
	case kindTypedArray, kindDataView:
		result, err = jsTypedArrayToGo(input)
	case kindSet:
		result, err = jsSetToGo(tracker, input, target)
	case kindArray:
		result, err = jsArrayToGo(tracker, input, target)
	default:
		result, err = jsObjectToGo(tracker, input, target)
	}

	return result, err
}

// jsNumberToGo converts JavaScript numbers to Go numeric types.
func jsNumberToGo(input *Value, target reflect.Type) (result any, err error) {
	defer func() {
		result, err = convertResult("JsNumberToGo", result, err, target)
	}()

	if input.IsBigInt() {
		return jsBigIntToGo(input, target)
	}

	if err := canConvertToGoNumber(input); err != nil {
		return nil, err
	}

	floatVal := input.Float64()

	if target == nil {
		// Prefer int64 for whole numbers
		if isFloatWholeNumber(floatVal) {
			return int64(floatVal), nil
		}

		return floatVal, nil
	}

	if isNumericType(target) {
		return newJsNumericToGoConverter(target).Convert(floatVal)
	}

	return nil, newInvalidGoTypeErr("numeric type", zeroOf(target))
}

var (
	bigIntType    = reflect.TypeFor[big.Int]()
	bigIntPtrType = reflect.TypeFor[*big.Int]()
)

// jsBigIntToGo converts a JS BigInt into the Go *big.Int/big.Int.
func jsBigIntToGo(input *Value, target reflect.Type) (result any, err error) {
	defer func() {
		result, err = convertResult("JsBigIntToGo", result, err, target)
	}()

	bigInt := input.BigInt()
	if bigInt == nil {
		return nil, newJsToGoErr(input, nil, "BigInt")
	}

	switch target {
	case nil, bigIntPtrType:
		return bigInt, nil
	case bigIntType:
		return *bigInt, nil
	}

	result, err = bigIntToGoInteger(bigInt, target)
	if err != nil {
		return nil, fmt.Errorf("cannot convert BigInt %s: %w", bigInt, err)
	}

	return result, nil
}

// bigIntToGoInteger converts n to the integer type t, if it fits.
func bigIntToGoInteger(n *big.Int, t reflect.Type) (any, error) {
	out := reflect.New(t).Elem()

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !n.IsInt64() || out.OverflowInt(n.Int64()) {
			return nil, fmt.Errorf("out of range for %s", t)
		}

		out.SetInt(n.Int64())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if !n.IsUint64() || out.OverflowUint(n.Uint64()) {
			return nil, fmt.Errorf("out of range for %s", t)
		}

		out.SetUint(n.Uint64())
	default:
		return nil, newInvalidGoTypeErr("*big.Int, big.Int or an integer", out.Interface())
	}

	return out.Interface(), nil
}

// jsTimeToGo converts a JS Date object into a time.Time value in UTC.
func jsTimeToGo(input *Value) (time.Time, error) {
	t, err := input.Time()
	if err != nil {
		return time.Time{}, newInvokeErr(input, err)
	}

	return t.UTC(), nil
}

// jsArrayBufferToGo converts an ArrayBuffer to a []byte.
func jsArrayBufferToGo(input *Value) ([]byte, error) {
	if !input.IsArrayBuffer() {
		return nil, errNotArrayBuffer
	}

	return input.Bytes()
}

// jsTypedArrayToGo returns a copy of the bytes viewed by a TypedArray or
// DataView.
func jsTypedArrayToGo(input *Value) ([]byte, error) {
	buffer, err := input.Get("buffer")
	if err != nil {
		return nil, err
	}
	defer buffer.Free()

	if buffer.IsUndefined() || buffer.IsNull() {
		return nil, errMissingBufferProperty
	}

	if !buffer.IsArrayBuffer() {
		return nil, errNotByteArray
	}

	offset, length := uint(propInt(input, "byteOffset")), uint(propInt(input, "byteLength"))

	fullBytes, err := buffer.Bytes()
	if err != nil {
		return nil, err
	}

	if offset+length > uint(len(fullBytes)) {
		return nil, newMaxLengthExceededErr(length, int64(len(fullBytes)), 0)
	}

	return fullBytes[offset : offset+length], nil
}

func jsSetToGo(tracker *convTracker, input *Value, target reflect.Type) (any, error) {
	if !input.IsSet() {
		return nil, newJsToGoErr(input, nil, "Set")
	}

	arrayVal, err := input.rt.arrayFrom(input)
	if err != nil {
		return nil, err
	}
	defer arrayVal.Free()

	return jsArrayToGo(tracker, arrayVal, target)
}

func jsArrayToGo(tracker *convTracker, input *Value, target reflect.Type) (any, error) {
	return (&jsArrayToGoConverter{tracker: tracker, input: input, targetType: target}).Convert()
}

func mapEntryToGo(
	tracker *convTracker,
	jsKey, jsValue *Value,
	goKeyType, goValueType reflect.Type,
) (k, v reflect.Value, err error) {
	goKeyFromJs, keyErr := toGoValue(tracker, jsKey, goKeyType)
	if keyErr != nil {
		return k, v, newJsToGoErr(jsKey, keyErr, "map key")
	}

	k = reflect.ValueOf(goKeyFromJs).Convert(goKeyType)
	goValFromJs, valErr := toGoValue(tracker, jsValue, goValueType)

	switch {
	case valErr != nil:
		return k, v, newJsToGoErr(jsValue, valErr, "map value")
	case goValFromJs == nil:
		v = reflect.Zero(goValueType)
	default:
		v = reflect.ValueOf(goValFromJs).Convert(goValueType)
	}

	return k, v, nil
}

func jsObjectOrMapToGoMap(tracker *convTracker, input *Value, target reflect.Type) (result any, err error) {
	if !input.IsObject() {
		return nil, newInvalidJsInputErr("object", input)
	}

	defer func() {
		result, err = convertResult("JsObjectOrMapToGoMap", result, err, target)
	}()

	if tracker.track(input.raw()) {
		return nil, newJsToGoErr(input, nil, "circular reference")
	}
	defer tracker.untrack(input.raw())

	mapType := target
	if mapType == nil {
		mapType = reflect.TypeFor[map[string]any]()
	}

	resultMap := reflect.MakeMap(mapType)
	targetKeyType := mapType.Key()
	targetValueType := mapType.Elem()

	err = input.entries(func(key, value *Value) error {
		targetKey, targetValue, err := mapEntryToGo(tracker, key, value, targetKeyType, targetValueType)
		if err != nil {
			return err
		}

		resultMap.SetMapIndex(targetKey, targetValue)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return resultMap.Interface(), nil
}

// jsObjectOrMapToGoStruct converts a JS object to target, a struct type
// or a pointer to one.
func jsObjectOrMapToGoStruct(tracker *convTracker, input *Value, target reflect.Type) (result any, err error) {
	if !input.IsObject() {
		return nil, newInvalidJsInputErr("object", input)
	}

	defer func() {
		result, err = convertResult("JsObjectOrMapToGoStruct", result, err, target)
	}()

	if tracker.track(input.raw()) {
		return nil, newJsToGoErr(input, nil, "circular reference")
	}
	defer tracker.untrack(input.raw())

	structType := target
	if structType.Kind() == reflect.Pointer {
		structType = structType.Elem()
	}

	structPtr := reflect.New(structType)
	if err := processObjectFields(tracker, input, structPtr.Elem(), structFieldsOf(structType)); err != nil {
		return nil, err
	}

	if target.Kind() == reflect.Pointer {
		return structPtr.Interface(), nil
	}

	return structPtr.Elem().Interface(), nil
}

// processObjectFields processes each field in the JavaScript object.
func processObjectFields(
	tracker *convTracker,
	obj *Value,
	structValue reflect.Value,
	fields *structFields,
) error {
	return obj.entries(func(key, value *Value) error {
		if value.IsNull() || value.IsUndefined() {
			return nil
		}

		i, ok := fields.byName[key.String()]
		if !ok {
			return nil
		}

		field := &fields.list[i]

		return setFieldValue(tracker, fieldByIndexAlloc(structValue, field.index), value, field)
	})
}

// fieldByIndexAlloc returns the field at the index path from structValue,
// allocating nil embedded pointers on the way.
func fieldByIndexAlloc(structValue reflect.Value, index []int) reflect.Value {
	fieldValue := structValue
	for _, i := range index {
		if fieldValue.Kind() == reflect.Pointer {
			if fieldValue.IsNil() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}

			fieldValue = fieldValue.Elem()
		}

		fieldValue = fieldValue.Field(i)
	}

	return fieldValue
}

// setFieldValue sets a struct field from a JS value, through JSON if the
// field implements json.Unmarshaler.
func setFieldValue(tracker *convTracker, fieldValue reflect.Value, jsValue *Value, field *structField) error {
	if implementsJSONUnmarshaler(field.Type) {
		jsonStr, err := jsValue.jsonStringify()
		if err != nil {
			return newJsStringifyErr("object/map field "+field.Name, err)
		}

		if err := unmarshalJSON(jsonStr, fieldValue.Addr().Interface()); err != nil {
			return fmt.Errorf("cannot unmarshal json: %w, %s=%s", err, field.Name, jsonStr)
		}

		return nil
	}

	converted, err := toGoValue(tracker, jsValue, field.Type)
	if err != nil {
		return newJsToGoErr(
			jsValue, err,
			fmt.Sprintf("object/map field '%s':", field.Name),
		)
	}

	fieldValue.Set(reflect.ValueOf(converted))

	return nil
}

func jsObjectToGo(tracker *convTracker, input *Value, target reflect.Type) (any, error) {
	if !input.IsObject() {
		return nil, newInvalidJsInputErr("object", input)
	}

	switch {
	case target == nil || target.Kind() == reflect.Map:
		return jsObjectOrMapToGoMap(tracker, input, target)
	case isGoStruct(target):
		return jsObjectOrMapToGoStruct(tracker, input, target)
	case input.IsArray():
		return jsArrayToGo(tracker, input, target)
	}

	// Fallback for other types
	tempPtr := reflect.New(target)

	jsonString, err := input.jsonStringify()
	if err != nil {
		return nil, newJsStringifyErr("object", err)
	}

	if err = unmarshalJSON(jsonString, tempPtr.Interface()); err != nil {
		err = fmt.Errorf("can not unmarshal json: %w, input=%s", err, jsonString)
	}

	return convertResult("JsObjectToGo", tempPtr.Elem().Interface(), err, target)
}

func jsFuncToGo(input *Value, target reflect.Type) (result any, err error) {
	if !input.IsFunction() {
		return nil, newInvalidJsInputErr("function", input)
	}

	defer func() {
		result, err = convertResult("JsFuncToGo", result, err, target)
	}()

	fnType, err := createGoBindFuncType(target)
	if err != nil {
		return nil, err
	}

	// The Go function may outlive input, so it keeps its own reference,
	// released once the Go function is garbage collected. Each call
	// converts its result with a tracker of its own.
	jsFn := input.Clone()
	jsFn.freeWhenCollected()

	goFunc := func(args []reflect.Value) (results []reflect.Value) {
		return createJsFunctionHandler(jsFn, newConvTracker(), fnType, args)
	}

	return reflect.MakeFunc(fnType, goFunc).Interface(), nil
}

// createJsFunctionHandler handles the execution of JavaScript functions from Go.
func createJsFunctionHandler(
	input *Value,
	tracker *convTracker,
	fnType reflect.Type,
	args []reflect.Value,
) (results []reflect.Value) {
	results = make([]reflect.Value, fnType.NumOut())
	for i := range fnType.NumOut() {
		results[i] = reflect.Zero(fnType.Out(i))
	}

	jsResult, err := input.Call(context.Background(), input.rt.global, flattenArgs(fnType, args)...)
	if err != nil {
		results[len(results)-1] = reflect.ValueOf(
			fmt.Errorf("JS function execution failed: %w", err),
		)

		return results
	}

	defer jsResult.Free()

	return handleJsFunctionResult(jsResult, tracker, fnType, results)
}

// flattenArgs returns the arguments of a call of a Go function of type
// fnType, with the variadic slice spread out.
func flattenArgs(fnType reflect.Type, args []reflect.Value) []any {
	flat := make([]any, 0, len(args))

	for i, arg := range args {
		if fnType.IsVariadic() && i == fnType.NumIn()-1 {
			for j := range arg.Len() {
				flat = append(flat, arg.Index(j).Interface())
			}

			break
		}

		flat = append(flat, arg.Interface())
	}

	return flat
}

// handleJsFunctionResult processes the result of a JavaScript function call.
func handleJsFunctionResult(
	jsResult *Value,
	tracker *convTracker,
	fnType reflect.Type,
	results []reflect.Value,
) []reflect.Value {
	numOut := fnType.NumOut()

	if jsResult.IsUndefined() || jsResult.IsNull() {
		results[0] = reflect.Zero(fnType.Out(0))
		if numOut == 2 {
			results[1] = reflect.Zero(fnType.Out(1))
		}

		return results
	}

	goResult, err := toGoValue(tracker, jsResult, nil)
	if err != nil {
		results[len(results)-1] = reflect.ValueOf(
			fmt.Errorf("failed to convert JS function result '%s' to Go: %w", jsResult.Type(), err),
		)

		return results
	}

	// Validate that the conversion is possible before attempting it
	goResultValue := reflect.ValueOf(goResult)

	targetType := fnType.Out(0)
	if !goResultValue.CanConvert(targetType) {
		results[len(results)-1] = reflect.ValueOf(
			fmt.Errorf("failed to convert JS function return value from %T (%v) to %s", goResult, goResult, targetType),
		)

		return results
	}

	results[0] = goResultValue.Convert(targetType)
	if numOut == 2 {
		results[1] = reflect.Zero(fnType.Out(1))
	}

	return results
}

// jsPrimitivesToGo converts JavaScript primitive types to Go types.
func jsPrimitivesToGo(input *Value, kind objectKind, target reflect.Type) (val any, ok bool, err error) {
	switch {
	case input.IsString():
		val, err = jsStringToGo(input, target)
	case input.IsBigInt():
		val, err = jsBigIntToGo(input, target)
	case input.IsNumber():
		val, err = jsNumberToGo(input, target)
	case input.IsBool():
		val = input.Bool()
	case kind == kindError:
		val = input.toError()
	case input.IsNull() || input.IsUndefined():
	case input.IsSymbol():
		err = errors.New("unsupported type: Symbol")
	case kind.isFunction():
		val, err = jsFuncToGo(input, target)
	default:
		return nil, false, nil // No primitive type matched
	}

	return val, true, err
}

// jsStringToGo handles string to various type conversions.
func jsStringToGo(input *Value, targetType reflect.Type) (any, error) {
	stringVal := input.String()

	if targetType != nil {
		if isNumericType(targetType) {
			return stringToNumeric(stringVal, targetType)
		}

		if targetType.Kind() == reflect.Bool {
			return stringVal != "", nil
		}

		// Handle string-based custom types
		if targetType.Kind() == reflect.String {
			// Convert string to custom string type using reflection
			stringValue := reflect.ValueOf(stringVal)
			if stringValue.Type().ConvertibleTo(targetType) {
				return stringValue.Convert(targetType).Interface(), nil
			}
		}
	}

	return stringVal, nil
}

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
func toGo[T any](input *Value, samples ...T) (v T, err error) {
	return toGoValue(newConvTracker[uint64](), input, samples...)
}

func toGoValue[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	temp, sample := createTemp(samples...)
	switch any(sample).(type) {
	case *Value:
		tvv, _ := any(input).(T)

		return tvv, nil
	case Value:
		tv, _ := any(*input).(T)

		return tv, nil
	}

	defer func() {
		v, err = processTempValue("ToGoValue", temp, err, sample)
	}()

	kind := input.kind()

	// A proxy value converts back to the Go value it wraps.
	if kind == kindProxyValue {
		temp, _ = input.rt.registry.Get(input.proxyID())

		return v, nil
	}

	var ok bool
	if temp, ok, err = jsPrimitivesToGo(tracker, input, kind, sample); ok {
		return v, err
	}

	switch kind {
	case kindDate:
		temp, err = jsTimeToGo(input)
	case kindRegExp:
		temp = input.String()
	case kindArrayBuffer:
		temp, err = jsArrayBufferToGo(input)
	case kindTypedArray, kindDataView:
		temp, err = jsTypedArrayToGo(input)
	case kindSet:
		temp, err = jsSetToGoWithContext(tracker, input, sample)
	case kindArray:
		temp, err = jsArrayToGoWithContext(tracker, input, sample)
	default:
		temp, err = jsObjectToGoWithContext(tracker, input, sample)
	}

	return v, err
}

// jsNumberToGo converts JavaScript numbers to Go numeric types.
func jsNumberToGo[T any](input *Value, samples ...T) (v T, err error) {
	temp, sample := createTemp(samples...)

	defer func() {
		v, err = processTempValue("JsNumberToGo", temp, err, sample)
	}()

	if input.IsBigInt() {
		temp, err = jsBigIntToGo(input, sample)

		return v, err
	}

	if err := canConvertToGoNumber(input); err != nil {
		return v, err
	}

	floatVal := input.Float64()

	targetType := reflect.TypeOf(sample)
	if targetType == nil || targetType.Kind() == reflect.Interface {
		// Prefer int64 for whole numbers
		if isFloatWholeNumber(floatVal) {
			temp = int64(floatVal)
		} else {
			temp = floatVal
		}

		return v, err
	}

	if isNumericType(targetType) {
		converter := newJsNumericToGoConverter(targetType)
		temp, err = converter.Convert(floatVal)

		return v, err
	}

	return v, newInvalidGoTypeErr("numeric type", sample)
}

// jsArrayToGo handles conversion of JavaScript Array objects to Go types.
func jsArrayToGo[T any](input *Value, samples ...T) (T, error) {
	return newJsArrayToGoConverter(input, samples...).Convert()
}

// jsBigIntToGo converts a JS BigInt into the Go *big.Int/big.Int.
func jsBigIntToGo[T any](input *Value, samples ...T) (v T, err error) {
	temp, sample := createTemp(samples...)

	defer func() {
		v, err = processTempValue("JsBigIntToGo", temp, err, sample)
	}()

	bigInt := input.BigInt()
	if bigInt == nil {
		return v, newJsToGoErr(input, nil, "BigInt")
	}

	switch any(sample).(type) {
	case nil, *big.Int:
		temp = bigInt
	case big.Int:
		temp = *bigInt
	default:
		temp, err = bigIntToGoInteger(bigInt, reflect.TypeOf(sample))
		if err != nil {
			return v, fmt.Errorf("cannot convert BigInt %s: %w", bigInt, err)
		}
	}

	return v, err
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

// jsSetToGo converts JavaScript Set to Go types.
func jsSetToGo[T any](input *Value, samples ...T) (v T, err error) {
	return jsSetToGoWithContext(newConvTracker[uint64](), input, samples...)
}

func jsSetToGoWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	if !input.IsSet() {
		return v, newJsToGoErr(input, nil, "Set")
	}

	arrayVal, err := input.rt.arrayFrom(input)
	if err != nil {
		return v, err
	}
	defer arrayVal.Free()

	return jsArrayToGoWithContext(tracker, arrayVal, samples...)
}

func jsArrayToGoWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (T, error) {
	_, sample := createTemp(samples...)

	return (&jsArrayToGoConverter[T]{
		tracker:    tracker,
		input:      input,
		sample:     sample,
		targetType: reflect.TypeOf(sample),
	}).Convert()
}

func mapEntryToGoWithContext(
	ctx *convTracker[uint64],
	jsKey, jsValue *Value,
	goKeyType, goValueType reflect.Type,
) (k, v reflect.Value, err error) {
	keySample := reflect.New(goKeyType).Elem().Interface()

	goKeyFromJs, keyErr := toGoValue(ctx, jsKey, keySample)
	if keyErr != nil {
		return k, v, newJsToGoErr(jsKey, keyErr, "map key")
	}

	k = reflect.ValueOf(goKeyFromJs).Convert(goKeyType)
	valueSample := reflect.New(goValueType).Elem().Interface()
	goValFromJs, valErr := toGoValue(ctx, jsValue, valueSample)

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

func jsObjectOrMapToGoMap[T any](input *Value, samples ...T) (v T, err error) {
	return jsObjectOrMapToGoMapWithContext(newConvTracker[uint64](), input, samples...)
}

func jsObjectOrMapToGoMapWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	if !input.IsObject() {
		return v, newInvalidJsInputErr("object", input)
	}

	temp, sample, targetType := createGoObjectTarget(samples...)

	defer func() {
		v, err = processTempValue("JsObjectOrMapToGoMap", temp, err, sample)
	}()

	var ct circularTracker[uint64]
	defer ct.cleanup()

	if err := ct.trackValue(tracker, input); err != nil {
		return v, err
	}

	resultMap := reflect.MakeMap(targetType)
	targetKeyType := targetType.Key()
	targetValueType := targetType.Elem()

	err = input.entries(func(key, value *Value) error {
		targetKey, targetValue, err := mapEntryToGoWithContext(tracker, key, value, targetKeyType, targetValueType)
		if err != nil {
			return err
		}

		resultMap.SetMapIndex(targetKey, targetValue)

		return nil
	})
	if err != nil {
		return v, err
	}

	temp = resultMap.Interface()

	return v, err
}

func jsObjectOrMapToGoStruct[T any](
	input *Value,
	samples ...T,
) (v T, err error) {
	return jsObjectOrMapToGoStructWithContext(newConvTracker[uint64](), input, samples...)
}

func jsObjectOrMapToGoStructWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	if !input.IsObject() {
		return v, newInvalidJsInputErr("object", input)
	}

	temp, sample, targetType := createGoObjectTarget(samples...)

	defer func() {
		v, err = processTempValue("JsObjectOrMapToGoStruct", temp, err, sample)
	}()

	var ct circularTracker[uint64]
	defer ct.cleanup()

	if err := ct.trackValue(tracker, input); err != nil {
		return v, err
	}

	structValue, isStructPtr := setupStructValue(targetType, &temp)
	fieldMap := getFieldMap(structValue.Type())

	if err := processObjectFields(tracker, input, structValue, fieldMap); err != nil {
		return v, err
	}

	if !isStructPtr {
		temp = structValue.Interface()
	}

	return v, err
}

// setupStructValue initializes the struct value for processing.
func setupStructValue(targetType reflect.Type, temp *any) (reflect.Value, bool) {
	isStructPtr := targetType.Kind() == reflect.Pointer
	if isStructPtr {
		*temp = reflect.New(targetType.Elem()).Interface()

		return reflect.ValueOf(*temp).Elem(), true
	}

	*temp = reflect.New(targetType).Interface()

	return reflect.ValueOf(*temp).Elem(), false
}

// processObjectFields processes each field in the JavaScript object.
func processObjectFields(
	tracker *convTracker[uint64],
	obj *Value,
	structValue reflect.Value,
	fieldMap map[string]fieldPath,
) error {
	return obj.entries(func(key, value *Value) error {
		if value.IsNull() || value.IsUndefined() {
			return nil
		}

		fieldInfo, ok := fieldMap[key.String()]
		if !ok {
			return nil
		}

		fieldValue := getFieldValue(structValue, fieldInfo.indices)
		if !fieldValue.IsValid() {
			return nil
		}

		return setFieldValue(tracker, fieldValue, value, fieldInfo)
	})
}

// getFieldValue navigates to the target field through embedded structs.
func getFieldValue(structValue reflect.Value, indices []int) reflect.Value {
	fieldValue := structValue
	for i, idx := range indices {
		if fieldValue.Kind() == reflect.Pointer {
			if fieldValue.IsNil() && fieldValue.CanSet() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}

			fieldValue = fieldValue.Elem()
		}

		fieldValue = fieldValue.Field(idx)
		if i < len(indices)-1 && !fieldValue.CanSet() {
			return reflect.Value{}
		}
	}

	return fieldValue
}

// setFieldValue sets the value of a struct field, handling JSON unmarshaling.
func setFieldValue(tracker *convTracker[uint64], fieldValue reflect.Value, jsValue *Value, fieldInfo fieldPath) error {
	fieldType := fieldInfo.field.Type

	if implementsJSONUnmarshaler(fieldType) {
		return setFieldWithJSONUnmarshaler(fieldValue, jsValue, fieldInfo)
	}

	return setFieldWithDirectConversion(tracker, fieldValue, jsValue, fieldInfo, fieldType)
}

// setFieldWithJSONUnmarshaler handles fields with custom JSON unmarshaling.
func setFieldWithJSONUnmarshaler(fieldValue reflect.Value, jsValue *Value, fieldInfo fieldPath) error {
	jsonStr, err := jsValue.jsonStringify()
	if err != nil {
		return newJsStringifyErr("object/map field "+fieldInfo.field.Name, err)
	}

	if err := unmarshalJSON(jsonStr, fieldValue.Addr().Interface()); err != nil {
		return fmt.Errorf("cannot unmarshal json: %w, %s=%s", err, fieldInfo.field.Name, jsonStr)
	}

	return nil
}

// setFieldWithDirectConversion handles direct value conversion.
func setFieldWithDirectConversion(
	tracker *convTracker[uint64],
	fieldValue reflect.Value,
	jsValue *Value,
	fieldInfo fieldPath,
	fieldType reflect.Type,
) error {
	fieldSample := reflect.New(fieldType).Elem().Interface()

	converted, err := toGoValue(tracker, jsValue, fieldSample)
	if err != nil {
		return newJsToGoErr(
			jsValue, err,
			fmt.Sprintf("object/map field '%s':", fieldInfo.field.Name),
		)
	}

	fieldValue.Set(reflect.ValueOf(converted))

	return nil
}

// jsObjectToGo handles conversion of JavaScript objects to Go types.
func jsObjectToGo[T any](input *Value, samples ...T) (v T, err error) {
	return jsObjectToGoWithContext(newConvTracker[uint64](), input, samples...)
}

func jsObjectToGoWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	if !input.IsObject() {
		return v, newInvalidJsInputErr("object", input)
	}

	_, sample := createTemp(samples...)

	targetType := reflect.TypeOf(sample)
	if targetType == nil {
		targetType = reflect.TypeFor[map[string]any]()
	}

	if targetType.Kind() == reflect.Map {
		return jsObjectOrMapToGoMapWithContext(tracker, input, sample)
	}

	if isGoStruct(targetType) {
		return jsObjectOrMapToGoStructWithContext(tracker, input, sample)
	}

	if input.IsArray() {
		return jsArrayToGoWithContext(tracker, input, sample)
	}

	// Fallback for other types
	tempPtr := reflect.New(targetType).Interface()

	jsonString, err := input.jsonStringify()
	if err != nil {
		return v, newJsStringifyErr("object", err)
	}

	if err = unmarshalJSON(jsonString, tempPtr); err != nil {
		err = fmt.Errorf("can not unmarshal json: %w, input=%s", err, jsonString)

		return processTempValue("JsObjectToGo", nil, err, sample)
	}

	// Dereference the pointer to get the actual value
	temp := reflect.ValueOf(tempPtr).Elem().Interface()

	return processTempValue("JsObjectToGo", temp, err, sample)
}

// jsFuncRef holds the JavaScript function behind a Go function converted
// from it. The Go function references it, so it is collected with it.
type jsFuncRef struct {
	fn *Value
}

func jsFuncToGo[T any](input *Value, samples ...T) (v T, err error) {
	return jsFuncToGoWithContext(newConvTracker[uint64](), input, samples...)
}

func jsFuncToGoWithContext[T any](
	tracker *convTracker[uint64],
	input *Value,
	samples ...T,
) (v T, err error) {
	if !input.IsFunction() {
		return v, newInvalidJsInputErr("function", input)
	}

	temp, sample := createTemp(samples...)

	defer func() {
		v, err = processTempValue("JsFuncToGo", temp, err, sample)
	}()

	fnType, err := createGoBindFuncType(sample)
	if err != nil {
		return v, err
	}

	// The Go function may outlive input, so it keeps its own reference,
	// released once the Go function is garbage collected.
	jsFn := &jsFuncRef{fn: input.Clone()}
	input.rt.releaseWhenCollected(jsFn, jsFn.fn)

	goFunc := func(args []reflect.Value) (results []reflect.Value) {
		return createJsFunctionHandler(jsFn.fn, tracker, fnType, args)
	}

	fn := reflect.MakeFunc(fnType, goFunc)
	temp = fn.Interface()

	return v, err
}

// createJsFunctionHandler handles the execution of JavaScript functions from Go.
func createJsFunctionHandler(
	input *Value,
	tracker *convTracker[uint64],
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
	tracker *convTracker[uint64],
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

	goResult, err := toGoValue[any](tracker, jsResult)
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
func jsPrimitivesToGo[T any](
	tracker *convTracker[uint64],
	input *Value,
	kind objectKind,
	sample T,
) (val any, ok bool, err error) {
	if input.IsString() {
		result, err := jsStringToGo(input, sample)

		return result, true, err
	}

	if input.IsBigInt() {
		result, err := jsBigIntToGo(input, sample)

		return result, true, err
	}

	if input.IsNumber() {
		result, err := jsNumberToGo(input, sample)

		return result, true, err
	}

	if input.IsBool() {
		val, err := processTempValue("JsBigIntToGo", input.Bool(), nil, true)

		return val, true, err
	}

	if kind == kindError {
		return input.toError(), true, nil
	}

	if input.IsNull() || input.IsUndefined() {
		return nil, true, nil
	}

	if input.IsSymbol() {
		return nil, true, errors.New("unsupported type: Symbol")
	}

	if kind.isFunction() {
		result, err := jsFuncToGoWithContext(tracker, input, sample)

		return result, true, err
	}

	return nil, false, nil // No primitive type matched
}

// jsStringToGo handles string to various type conversions.
func jsStringToGo[T any](input *Value, sample T) (any, error) {
	stringVal := input.String()
	targetType := reflect.TypeOf(sample)

	if targetType != nil && targetType.Kind() != reflect.Interface {
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

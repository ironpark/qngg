package qngg

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"time"
)

func ToGoValue[T any](input *Value, samples ...T) (v T, err error) {
	registryID := input.GetPropertyStr("__registry_id")
	if !registryID.IsUndefined() && !registryID.IsNull() {
		registryVal, ok := input.runtime.registry.Get(uint64(registryID.Int64()))
		if ok {
			val, ok := registryVal.(T)
			if !ok {
				return v, fmt.Errorf("type assertion failed for registry value: expected %T, got %T", v, registryVal)
			}

			return val, nil
		}
	}

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

	// If JS value is a QJSProxyValue, extract the Go value from the registry
	if input.isProxyValue() {
		proxyID := input.GetPropertyStr("proxyId")
		temp, _ = input.runtime.registry.Get(uint64(proxyID.Int64()))

		return v, nil
	}

	var ok bool
	if temp, ok, err = jsPrimitivesToGo(tracker, input, sample); ok {
		return v, err
	}

	if input.isGlobalInstanceOf("Date") {
		temp, err = jsTimeToGo(input)

		return v, err
	}

	if input.isGlobalInstanceOf("RegExp") {
		temp = input.String()

		return v, err
	}

	if input.IsByteArray() {
		temp, err = jsArrayBufferToGo(input)

		return v, err
	}

	if isTypedArray(input) {
		temp, err = jsTypedArrayToGo(input)

		return v, err
	}

	if input.IsMap() {
		temp, err = jsObjectToGoWithContext(tracker, input, sample)

		return v, err
	}

	if input.IsSet() {
		temp, err = jsSetToGoWithContext(tracker, input, sample)

		return v, err
	}

	if input.IsArray() {
		temp, err = jsArrayToGoWithContext(tracker, input, sample)

		return v, err
	}

	// Fallback for all other object types
	temp, err = jsObjectToGoWithContext(tracker, input, sample)

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
	case *big.Int:
		temp = bigInt
	case big.Int:
		temp = *bigInt
	default:
		return v, newInvalidGoTypeErr("*big.Int/big.Int", sample)
	}

	return v, err
}

// jsTimeToGo converts a JS Date object into a time.Time value.
func jsTimeToGo(input *Value) (time.Time, error) {
	jsTime, err := input.InvokeJS("getTime")
	if err != nil {
		return time.Time{}, newInvokeErr(input, err)
	}
	defer jsTime.Free()

	ms := jsTime.Int64()
	seconds := ms / 1000
	nanoseconds := (ms % 1000) * int64(time.Millisecond)

	return time.Unix(seconds, nanoseconds).In(time.UTC), nil
}

// jsArrayBufferToGo converts an ArrayBuffer to a []byte.
func jsArrayBufferToGo(input *Value) ([]byte, error) {
	if !input.IsByteArray() {
		return nil, ErrNotArrayBuffer
	}

	return input.ToByteArray(), nil
}

// jsTypedArrayToGo return the underlying byte slice from a TypedArray/DataView.
func jsTypedArrayToGo(input *Value) ([]byte, error) {
	buffer := input.GetPropertyStr("buffer")
	defer buffer.Free()

	if buffer.IsUndefined() || buffer.IsNull() {
		return nil, ErrMissingBufferProperty
	}

	if buffer.IsByteArray() {
		offset := uint(input.GetPropertyStr("byteOffset").Int64())
		length := uint(input.GetPropertyStr("byteLength").Int64())

		fullBytes := buffer.ToByteArray()
		if offset+length > uint(len(fullBytes)) {
			return nil, newMaxLengthExceededErr(length, int64(len(fullBytes)), 0)
		}

		return fullBytes[offset : offset+length], nil
	}

	return nil, ErrNotByteArray
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

	setVal := input.ToSet()

	arrayVal := setVal.ToArray()
	defer arrayVal.Free()

	return jsArrayToGoWithContext(tracker, arrayVal.Value, samples...)
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

	temp, obj, sample, targetType := createGoObjectTarget(input, samples...)

	defer func() {
		v, err = processTempValue("JsObjectOrMapToGoMap", temp, err, sample)
	}()

	var ct circularTracker[uint64]
	defer ct.cleanup()

	if err := ct.trackValue(tracker, input); err != nil {
		return v, err
	}

	var forEachErr error

	resultMap := reflect.MakeMap(targetType)
	targetKeyType := targetType.Key()
	targetValueType := targetType.Elem()

	obj.ForEach(func(key, value *Value) {
		if forEachErr != nil {
			return
		}

		if targetKey, targetValue, err := mapEntryToGoWithContext(
			tracker,
			key, value,
			targetKeyType, targetValueType,
		); err != nil {
			forEachErr = err
		} else {
			resultMap.SetMapIndex(targetKey, targetValue)
		}
	})

	if forEachErr != nil {
		return v, forEachErr
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

	temp, obj, sample, targetType := createGoObjectTarget(input, samples...)

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

	if err := processObjectFields(tracker, obj, structValue, fieldMap); err != nil {
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
	obj objectOrMap,
	structValue reflect.Value,
	fieldMap map[string]fieldPath,
) error {
	var forEachErr error

	obj.ForEach(func(key, value *Value) {
		if forEachErr != nil || value.IsNull() || value.IsUndefined() {
			return
		}

		propName := key.String()

		fieldInfo, ok := fieldMap[propName]
		if !ok {
			return
		}

		fieldValue := getFieldValue(structValue, fieldInfo.indices)
		if !fieldValue.IsValid() {
			return
		}

		if err := setFieldValue(tracker, fieldValue, value, fieldInfo); err != nil {
			forEachErr = err
		}
	})

	return forEachErr
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
	jsonStr, err := jsValue.JSONStringify()
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

	jsonString, err := input.JSONStringify()
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

func JsFuncToGo[T any](input *Value, samples ...T) (v T, err error) {
	return jsFuncToGo(newConvTracker[uint64](), input, samples...)
}

func jsFuncToGo[T any](
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

	ctx := input.Runtime()
	goFunc := func(args []reflect.Value) (results []reflect.Value) {
		return createJsFunctionHandler(ctx, input, tracker, fnType, args)
	}

	fn := reflect.MakeFunc(fnType, goFunc)
	temp = fn.Interface()

	return v, err
}

// createJsFunctionHandler handles the execution of JavaScript functions from Go.
func createJsFunctionHandler(
	ctx *Runtime,
	input *Value,
	tracker *convTracker[uint64],
	fnType reflect.Type,
	args []reflect.Value,
) (results []reflect.Value) {
	results = make([]reflect.Value, fnType.NumOut())
	for i := range fnType.NumOut() {
		results[i] = reflect.Zero(fnType.Out(i))
	}

	jsArgs, err := convertArgsToJS(ctx, fnType, args, results)
	if err != nil {
		return results
	}

	defer func() {
		for _, arg := range jsArgs {
			arg.Free()
		}
	}()

	jsResult, err := ctx.Invoke(input, ctx.Global(), jsArgs...)
	if err != nil {
		results[len(results)-1] = reflect.ValueOf(
			fmt.Errorf("JS function execution failed: %w", err),
		)

		return results
	}

	defer jsResult.Free()

	return handleJsFunctionResult(jsResult, tracker, fnType, results)
}

// convertArgsToJS converts Go function arguments to JavaScript values.
func convertArgsToJS(
	ctx *Runtime,
	fnType reflect.Type,
	args []reflect.Value,
	results []reflect.Value,
) ([]*Value, error) {
	isVariadic := fnType.IsVariadic()

	numFixedArgs := fnType.NumIn()
	if isVariadic {
		numFixedArgs--
	}

	expectedArgsCount := len(args)
	if isVariadic && len(args) == numFixedArgs+1 &&
		args[numFixedArgs].Kind() == reflect.Slice {
		expectedArgsCount = numFixedArgs + args[numFixedArgs].Len()
	}

	jsArgs := make([]*Value, 0, expectedArgsCount)

	convertArg := func(argValue any, argIndex int) error {
		jsArg, convErr := ToJsValue(ctx, argValue)
		if convErr != nil {
			results[len(results)-1] = reflect.ValueOf(
				fmt.Errorf(
					"failed to convert go function argument at index %d '%T=%v' to JS: %w",
					argIndex, argValue, argValue, convErr,
				),
			)

			return convErr
		}

		jsArgs = append(jsArgs, jsArg)

		return nil
	}

	if isVariadic && len(args) == numFixedArgs+1 &&
		args[numFixedArgs].Kind() == reflect.Slice {
		variadicSlice := args[numFixedArgs]

		// Convert fixed arguments
		for i := range numFixedArgs {
			if err := convertArg(args[i].Interface(), i); err != nil {
				return nil, err
			}
		}

		// Convert variadic arguments
		for i := range variadicSlice.Len() {
			elem := variadicSlice.Index(i)
			if err := convertArg(elem.Interface(), numFixedArgs+i); err != nil {
				return nil, err
			}
		}
	} else {
		// Convert all arguments directly
		for i, arg := range args {
			if err := convertArg(arg.Interface(), i); err != nil {
				return nil, err
			}
		}
	}

	return jsArgs, nil
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

	if input.IsError() {
		return input.exception(), true, nil
	}

	if input.IsNull() || input.IsUndefined() {
		return nil, true, nil
	}

	if input.IsSymbol() {
		return nil, true, errors.New("unsupported type: Symbol")
	}

	if input.IsFunction() {
		result, err := jsFuncToGo(tracker, input, sample)

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

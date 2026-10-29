package qngg

import (
	"fmt"
	"reflect"
)

// jsArrayToGoConverter handles array conversions with better error handling and performance.
type jsArrayToGoConverter[T any] struct {
	tracker    *convTracker
	input      *Value
	targetType reflect.Type
	sample     T
}

func (ac *jsArrayToGoConverter[T]) Convert() (T, error) {
	var zero T

	jsArray := ac.input
	if !jsArray.IsArray() {
		return zero, newJsToGoErr(ac.input, newInvalidJsInputErr("array", jsArray), "Array")
	}

	jsLen := jsArray.Len()
	if ac.targetType == nil || ac.targetType.Kind() == reflect.Interface {
		return ac.convertToInterface(jsArray, jsLen)
	}

	switch ac.targetType.Kind() {
	case reflect.Slice:
		return ac.convertToSlice(jsArray, jsLen)
	case reflect.Array:
		return ac.convertToArray(jsArray, jsLen)
	default:
		return ac.convertViaJSON(jsArray)
	}
}

func (ac *jsArrayToGoConverter[T]) convertToInterface(jsArray *Value, jsLen int64) (T, error) {
	result := make([]any, 0, jsLen)

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return ac.sample, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		goElem, convErr := toGoValue[any](ac.tracker, jsElem)

		jsElem.Free()

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		result = append(result, goElem)
	}

	resultT, _ := any(result).(T)

	return resultT, nil
}

func (ac *jsArrayToGoConverter[T]) convertToSlice(jsArray *Value, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	sliceValue := reflect.MakeSlice(ac.targetType, 0, int(jsLen))

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return ac.sample, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := toGoValue(ac.tracker, jsElem, elemSample)

		jsElem.Free()

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		sliceValue = reflect.Append(sliceValue, elemValue)
	}

	sliceT, _ := reflect.TypeAssert[T](sliceValue)

	return sliceT, nil
}

func (ac *jsArrayToGoConverter[T]) convertToArray(jsArray *Value, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	goArrayLen := ac.targetType.Len()
	goArrayValue := reflect.New(ac.targetType).Elem()

	if jsLen > int64(goArrayLen) {
		return ac.sample, fmt.Errorf("JS array/set length (%d) exceeds Go array length (%d)", jsLen, goArrayLen)
	}

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return ac.sample, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := toGoValue(ac.tracker, jsElem, elemSample)

		jsElem.Free()

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		goArrayValue.Index(int(i)).Set(elemValue)
	}

	arrayT, _ := reflect.TypeAssert[T](goArrayValue)

	return arrayT, nil
}

func (ac *jsArrayToGoConverter[T]) convertElementValue(goElem any, elemType reflect.Type) reflect.Value {
	if goElem == nil {
		return reflect.Zero(elemType)
	}

	return reflect.ValueOf(goElem)
}

func (ac *jsArrayToGoConverter[T]) convertViaJSON(jsArray *Value) (T, error) {
	jsonString, err := jsArray.jsonStringify()
	if err != nil {
		return ac.sample, newJsStringifyErr("array", err)
	}

	temp := reflect.New(ac.targetType).Interface()
	if err = unmarshalJSON(jsonString, temp); err != nil {
		return ac.sample, fmt.Errorf("can not unmarshal json: %w, input=%s", err, jsonString)
	}

	arrayT, _ := reflect.TypeAssert[T](reflect.ValueOf(temp).Elem())

	return arrayT, nil
}

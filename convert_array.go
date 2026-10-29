package qngg

import (
	"fmt"
	"reflect"
)

// jsArrayToGoConverter handles array conversions with better error handling and performance.
type jsArrayToGoConverter struct {
	tracker    *convTracker
	input      *Value
	targetType reflect.Type // nil for any
}

func (ac *jsArrayToGoConverter) Convert() (any, error) {
	jsArray := ac.input
	if !jsArray.IsArray() {
		return nil, newJsToGoErr(ac.input, newInvalidJsInputErr("array", jsArray), "Array")
	}

	jsLen := jsArray.Len()
	if ac.targetType == nil {
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

func (ac *jsArrayToGoConverter) convertToInterface(jsArray *Value, jsLen int64) (any, error) {
	result := make([]any, 0, jsLen)

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return nil, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		goElem, convErr := toGoValue(ac.tracker, jsElem, nil)

		jsElem.Free()

		if convErr != nil {
			return nil, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		result = append(result, goElem)
	}

	return result, nil
}

func (ac *jsArrayToGoConverter) convertToSlice(jsArray *Value, jsLen int64) (any, error) {
	elemType := ac.targetType.Elem()
	sliceValue := reflect.MakeSlice(ac.targetType, 0, int(jsLen))

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return nil, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		goElem, convErr := toGoValue(ac.tracker, jsElem, elemType)

		jsElem.Free()

		if convErr != nil {
			return nil, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		sliceValue = reflect.Append(sliceValue, elemValue)
	}

	return sliceValue.Interface(), nil
}

func (ac *jsArrayToGoConverter) convertToArray(jsArray *Value, jsLen int64) (any, error) {
	elemType := ac.targetType.Elem()
	goArrayLen := ac.targetType.Len()
	goArrayValue := reflect.New(ac.targetType).Elem()

	if jsLen > int64(goArrayLen) {
		return nil, fmt.Errorf("JS array/set length (%d) exceeds Go array length (%d)", jsLen, goArrayLen)
	}

	for i := range jsLen {
		jsElem, err := jsArray.Index(uint32(i))
		if err != nil {
			return nil, newJsToGoErr(jsArray, err, fmt.Sprintf("array/set element at index %d", int(i)))
		}

		goElem, convErr := toGoValue(ac.tracker, jsElem, elemType)

		jsElem.Free()

		if convErr != nil {
			return nil, newJsToGoErr(
				jsArray,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		elemValue := ac.convertElementValue(goElem, elemType)
		goArrayValue.Index(int(i)).Set(elemValue)
	}

	return goArrayValue.Interface(), nil
}

func (ac *jsArrayToGoConverter) convertElementValue(goElem any, elemType reflect.Type) reflect.Value {
	if goElem == nil {
		return reflect.Zero(elemType)
	}

	return reflect.ValueOf(goElem)
}

func (ac *jsArrayToGoConverter) convertViaJSON(jsArray *Value) (any, error) {
	jsonString, err := jsArray.jsonStringify()
	if err != nil {
		return nil, newJsStringifyErr("array", err)
	}

	temp := reflect.New(ac.targetType).Interface()
	if err = unmarshalJSON(jsonString, temp); err != nil {
		return nil, fmt.Errorf("can not unmarshal json: %w, input=%s", err, jsonString)
	}

	return reflect.ValueOf(temp).Elem().Interface(), nil
}

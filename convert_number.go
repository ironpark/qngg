package qngg

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

const (
	// nanosToMillis is the conversion factor from nanoseconds to milliseconds.
	nanosToMillis = 1e6
	// uint64SignBitPosition is the bit position of the sign bit in a 64-bit unsigned integer.
	uint64SignBitPosition = 63
)

type numberType interface {
	int |
		int8 |
		int16 |
		int32 |
		int64 |
		uint |
		uint8 |
		uint16 |
		uint32 |
		uint64 |
		uintptr |
		float32 |
		float64
}

var (
	// Overflow bounds for numeric types.
	numericBounds = map[reflect.Kind][2]float64{
		reflect.Int8:    {math.MinInt8, math.MaxInt8},
		reflect.Int16:   {math.MinInt16, math.MaxInt16},
		reflect.Int32:   {math.MinInt32, math.MaxInt32},
		reflect.Int64:   {math.MinInt64, math.MaxInt64},
		reflect.Uint8:   {0, math.MaxUint8},
		reflect.Uint16:  {0, math.MaxUint16},
		reflect.Uint32:  {0, math.MaxUint32},
		reflect.Uint64:  {0, math.MaxUint64},
		reflect.Float32: {-math.MaxFloat32, math.MaxFloat32},
		reflect.Float64: {-math.MaxFloat64, math.MaxFloat64},
	}
)

// jsNumericToGoConverter handles conversion from float64 to various numeric types.
type jsNumericToGoConverter struct {
	targetType reflect.Type
	isPointer  bool
}

func newJsNumericToGoConverter(targetType reflect.Type) *jsNumericToGoConverter {
	isPointer := targetType.Kind() == reflect.Pointer
	if isPointer {
		targetType = targetType.Elem()
	}

	return &jsNumericToGoConverter{
		targetType: targetType,
		isPointer:  isPointer,
	}
}

func (nc *jsNumericToGoConverter) Convert(floatVal float64) (any, error) {
	targetKind := nc.targetType.Kind()
	if err := numericBoundsCheck(floatVal, targetKind); err != nil {
		return nil, err
	}

	result, err := floatToInt(floatVal, targetKind)
	if err != nil {
		return nil, err
	}

	if nc.isPointer {
		ptr := reflect.New(nc.targetType)
		ptr.Elem().Set(reflect.ValueOf(result))
		result = ptr.Interface()
	}

	return result, nil
}

func floatToInt(floatVal float64, targetKind reflect.Kind) (any, error) {
	var result any

	switch targetKind {
	case reflect.Int:
		result = int(floatVal)
	case reflect.Int8:
		result = int8(floatVal)
	case reflect.Int16:
		result = int16(floatVal)
	case reflect.Int32:
		result = int32(floatVal)
	case reflect.Int64:
		result = int64(floatVal)
	case reflect.Uint:
		result = uint(floatVal)
	case reflect.Uint8:
		result = uint8(floatVal)
	case reflect.Uint16:
		result = uint16(floatVal)
	case reflect.Uint32:
		result = uint32(floatVal)
	case reflect.Uint64:
		result = uint64(floatVal)
	case reflect.Uintptr:
		result = uintptr(floatVal)
	case reflect.Float32:
		result = float32(floatVal)
	case reflect.Float64:
		result = floatVal
	case reflect.Complex64:
		result = complex(float32(floatVal), 0)
	case reflect.Complex128:
		result = complex(floatVal, 0)
	default:
		return nil, fmt.Errorf("unsupported numeric type: %s", targetKind.String())
	}

	return result, nil
}

func isValid32BitFloat(floatVal float64, targetKind reflect.Kind) error {
	var bounds [2]float64
	if targetKind == reflect.Int {
		bounds = [2]float64{math.MinInt32, math.MaxInt32}
	} else {
		bounds = [2]float64{0, math.MaxUint32}
	}

	if floatVal < bounds[0] || floatVal > bounds[1] {
		return newOverflowErr(floatVal, targetKind.String())
	}

	return nil
}

func numericBoundsCheck(floatVal float64, targetKind reflect.Kind) error {
	if bounds, ok := numericBounds[targetKind]; ok {
		if floatVal < bounds[0] || floatVal > bounds[1] {
			return newOverflowErr(floatVal, targetKind.String())
		}
	}

	// Special handling for int/uint on 32-bit platforms
	isIntUint := targetKind == reflect.Int || targetKind == reflect.Uint
	if isIntUint && is32BitPlatform() {
		return isValid32BitFloat(floatVal, targetKind)
	}

	return nil
}

func stringToNumeric(s string, targetType reflect.Type) (result any, err error) {
	s = strings.TrimSpace(s)

	if targetType.Kind() == reflect.Pointer {
		elemType := targetType.Elem()

		result, err := stringToNumeric(s, elemType)
		if err != nil {
			return nil, err
		}

		ptrVal := reflect.New(elemType)
		ptrVal.Elem().Set(reflect.ValueOf(result))

		return ptrVal.Interface(), nil
	}

	if s == "" {
		return nil, errEmptyStringToNumber
	}

	// For integer types, try to parse as float first (JavaScript-style conversion)
	// then truncate to integer, which mimics JavaScript's Number() conversion
	switch targetType.Kind() {
	case reflect.Int:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return int(val), nil
		}
	case reflect.Int8:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int8(val)
			if float64(intVal) == val || (val >= -128 && val <= 127) {
				return intVal, nil
			}
		}
	case reflect.Int16:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int16(val)
			if float64(intVal) == val || (val >= -32768 && val <= 32767) {
				return intVal, nil
			}
		}
	case reflect.Int32:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			intVal := int32(val)
			if float64(intVal) == val || (val >= -2147483648 && val <= 2147483647) {
				return intVal, nil
			}
		}
	case reflect.Int64:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(val), nil
		}
	case reflect.Uint:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uint(val), nil
		}
	case reflect.Uint8:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint8(val)
			if float64(intVal) == val || (val >= 0 && val <= 255) {
				return intVal, nil
			}
		}
	case reflect.Uint16:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint16(val)
			if float64(intVal) == val || (val >= 0 && val <= 65535) {
				return intVal, nil
			}
		}
	case reflect.Uint32:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			intVal := uint32(val)
			if float64(intVal) == val || (val >= 0 && val <= 4294967295) {
				return intVal, nil
			}
		}
	case reflect.Uint64:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uint64(val), nil
		}
	case reflect.Uintptr:
		if val, err := strconv.ParseFloat(s, 64); err == nil && val >= 0 {
			return uintptr(val), nil
		}
	case reflect.Float32:
		if val, err := strconv.ParseFloat(s, 32); err == nil {
			return float32(val), nil
		}
	case reflect.Float64:
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return val, nil
		}
	}

	return nil, fmt.Errorf("cannot convert JS string %q to %s", s, targetType.String())
}

// canConvertToGoNumber performs common validation for number inputs.
func canConvertToGoNumber(input *Value) error {
	if !input.IsNumber() {
		return newJsToGoErr(input, nil, "number")
	}

	if input.IsNaN() {
		return newJsToGoErr(input, nil, "NaN")
	}

	if input.IsInfinity() {
		return newJsToGoErr(input, nil, "Infinity")
	}

	return nil
}

func isFloatWholeNumber(floatVal float64) bool {
	return floatVal == float64(int64(floatVal)) &&
		floatVal >= math.MinInt64 &&
		floatVal <= math.MaxInt64
}

// is32BitPlatform check if the platform is 32-bit by comparing the size of uintptr.
func is32BitPlatform() bool {
	return strconv.IntSize == 32
}

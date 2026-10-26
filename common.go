package qngg

import (
	"fmt"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// minMapForeachArgs is the minimum number of arguments for Map forEach callback (value, key).
	minMapForeachArgs = 2
	// nanosToMillis is the conversion factor from nanoseconds to milliseconds.
	nanosToMillis = 1e6
	// uint64ByteSize is the size of a uint64 value in bytes.
	uint64ByteSize = 8
	// uint64SignBitPosition is the bit position of the sign bit in a 64-bit unsigned integer.
	uint64SignBitPosition = 63
	// packedPtrSize is the size in bytes of a packed pointer structure.
	packedPtrSize = 8
	// nullPtr represents a null pointer value.
	nullPtr = uint32(0)
	// stringTerminator is the null terminator byte for C-style strings.
	stringTerminator = byte(0)
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

	// TypedArray types for validation.
	typedArrayTypes = []string{
		"Uint8Array",
		"Int8Array",
		"Uint16Array",
		"Int16Array",
		"Uint32Array",
		"Int32Array",
		"Float32Array",
		"Float64Array",
		"BigInt64Array",
		"BigUint64Array",
		"DataView",
	}
)

// objectOrMap interface for unified object/map handling.
type objectOrMap interface {
	IsObject() bool
	IsMap() bool
	ToMap() *Map
	JSONStringify() (string, error)
	IsNull() bool
	ForEach(callback func(*Value, *Value))
}

// fieldMapper handles struct field mapping with caching for performance.
type fieldMapper struct {
	mu    sync.RWMutex
	cache map[reflect.Type]map[string]fieldPath
}

// fieldPath stores the path to a field through embedded structs.
type fieldPath struct {
	indices []int               // Path to the field through embedded structs
	field   reflect.StructField // Field info
}

// newFieldMapper creates a new field mapper with initialized cache.
func newFieldMapper() *fieldMapper {
	return &fieldMapper{
		cache: make(map[reflect.Type]map[string]fieldPath),
	}
}

// Global field mapper instance for backward compatibility.
var globalFieldMapper = newFieldMapper()

// GetFieldMap returns or builds a field map for a struct type.
func (fm *fieldMapper) GetFieldMap(structType reflect.Type) map[string]fieldPath {
	// First try read lock for existing cache
	fm.mu.RLock()

	if fieldMap, exists := fm.cache[structType]; exists {
		fm.mu.RUnlock()

		return fieldMap
	}

	fm.mu.RUnlock()

	// Upgrade to write lock to build the map
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Double-check after acquiring write lock
	if fieldMap, exists := fm.cache[structType]; exists {
		return fieldMap
	}

	fieldMap := make(map[string]fieldPath)
	seen := make(map[string]bool)

	fm.processFields(structType, []int{}, seen, fieldMap)

	// Cache the field map
	fm.cache[structType] = fieldMap

	return fieldMap
}

// processFields recursively processes struct fields including embedded struct promotion.
func (fm *fieldMapper) processFields(
	structType reflect.Type,
	indexPrefix []int,
	seen map[string]bool,
	fieldMap map[string]fieldPath,
) {
	for i := range structType.NumField() {
		field := structType.Field(i)
		fieldIndices := append(append([]int{}, indexPrefix...), i)

		// Handle non-embedded fields
		if !field.Anonymous {
			if !field.IsExported() {
				continue
			}

			fieldName := fm.getJSONFieldName(field)
			if fieldName == "" { // Skip fields marked with json:"-"
				continue
			}

			// Respect Go's field promotion: shallowest field wins
			if !seen[fieldName] {
				fieldMap[fieldName] = fieldPath{indices: fieldIndices, field: field}
				seen[fieldName] = true
			}

			continue
		}

		// Handle embedded fields
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}

		if fieldType.Kind() == reflect.Struct {
			// Process fields from embedded struct
			fm.processFields(fieldType, fieldIndices, seen, fieldMap)
		}
	}
}

// getJSONFieldName extracts the JSON field name from struct tags.
func (fm *fieldMapper) getJSONFieldName(field reflect.StructField) string {
	fieldName := field.Name

	if tag := field.Tag.Get("json"); tag != "" {
		parts := strings.Split(tag, ",")
		if parts[0] == "-" {
			return "" // Skip this field
		}

		if parts[0] != "" {
			fieldName = parts[0]
		}
	}

	return fieldName
}

// getFieldMap provides backward compatibility for the old API.
func getFieldMap(structType reflect.Type) map[string]fieldPath {
	return globalFieldMapper.GetFieldMap(structType)
}

// convTracker tracks objects during Go-JS conversion to detect circular references.
type convTracker[T uintptr | uint64] struct {
	processing map[T]bool
}

// newConvTracker creates a new conversion context for tracking circular references.
func newConvTracker[T uintptr | uint64]() *convTracker[T] {
	return &convTracker[T]{
		processing: make(map[T]bool),
	}
}

// Track registers an object for circular reference detection.
// Returns true if the object is already being processed (circular reference detected).
func (tracker *convTracker[T]) Track(ptr T) bool {
	if tracker.processing[ptr] {
		return true
	}

	tracker.processing[ptr] = true

	return false
}

// UnTrack removes an object from circular reference tracking.
func (tracker *convTracker[T]) UnTrack(ptr T) {
	delete(tracker.processing, ptr)
}

// circularTracker manages the lifecycle of circular reference tracking for a single object.
type circularTracker[T uintptr | uint64] struct {
	ctx             *convTracker[T]
	ptr             T
	needsUnregister bool
}

// trackPtr sets up circular reference tracking for a pointer.
func (ct *circularTracker[T]) trackPtr(ctx *convTracker[T], ptr T) error {
	if ctx.Track(ptr) {
		return newGoToJsErr("", nil, "recursive pointer")
	}

	ct.ctx = ctx
	ct.ptr = ptr
	ct.needsUnregister = true

	return nil
}

// // trackValue sets up circular reference tracking for a JS value.
func (ct *circularTracker[T]) trackValue(ctx *convTracker[T], value *Value) error {
	var ptr any = value.raw()

	typeT, _ := ptr.(T)
	if ctx.Track(typeT) {
		return newJsToGoErr(value, nil, "circular reference")
	}

	ct.ctx = ctx
	ct.ptr = typeT
	ct.needsUnregister = true

	return nil
}

// cleanup removes the object from circular reference tracking.
func (ct *circularTracker[T]) cleanup() {
	if ct.needsUnregister && ct.ctx != nil {
		ct.ctx.UnTrack(ct.ptr)
	}
}

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

// jsArrayToGoConverter handles array conversions with better error handling and performance.
type jsArrayToGoConverter[T any] struct {
	tracker    *convTracker[uint64]
	input      *Value
	targetType reflect.Type
	sample     T
}

func newJsArrayToGoConverter[T any](input *Value, samples ...T) *jsArrayToGoConverter[T] {
	var sample T
	if len(samples) > 0 {
		sample = samples[0]
	}

	return &jsArrayToGoConverter[T]{
		tracker:    newConvTracker[uint64](),
		input:      input,
		targetType: reflect.TypeOf(sample),
		sample:     sample,
	}
}

func (ac *jsArrayToGoConverter[T]) Convert() (T, error) {
	var zero T

	jsArray, err := ac.input.ToArray()
	if err != nil {
		return zero, newJsToGoErr(ac.input, err, "Array")
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

func (ac *jsArrayToGoConverter[T]) convertToInterface(jsArray *Array, jsLen int64) (T, error) {
	result := make([]any, 0, jsLen)

	for i := range jsLen {
		jsElem := jsArray.Get(i)

		goElem, convErr := toGoValue[any](ac.tracker, jsElem)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
				convErr,
				fmt.Sprintf("array/set element at index %d", int(i)),
			)
		}

		result = append(result, goElem)
	}

	resultT, _ := any(result).(T)

	return resultT, nil
}

func (ac *jsArrayToGoConverter[T]) convertToSlice(jsArray *Array, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	sliceValue := reflect.MakeSlice(ac.targetType, 0, int(jsLen))

	for i := range jsLen {
		jsElem := jsArray.Get(i)
		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := toGoValue(ac.tracker, jsElem, elemSample)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
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

func (ac *jsArrayToGoConverter[T]) convertToArray(jsArray *Array, jsLen int64) (T, error) {
	elemType := ac.targetType.Elem()
	goArrayLen := ac.targetType.Len()
	goArrayValue := reflect.New(ac.targetType).Elem()

	if jsLen > int64(goArrayLen) {
		return ac.sample, fmt.Errorf("JS array/set length (%d) exceeds Go array length (%d)", jsLen, goArrayLen)
	}

	for i := range jsLen {
		jsElem := jsArray.Get(i)
		elemSample := reflect.New(elemType).Elem().Interface()
		goElem, convErr := toGoValue(ac.tracker, jsElem, elemSample)

		if !jsElem.IsFunction() {
			jsElem.Free()
		}

		if convErr != nil {
			return ac.sample, newJsToGoErr(
				jsArray.Value,
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

func (ac *jsArrayToGoConverter[T]) convertViaJSON(jsArray *Array) (T, error) {
	jsonString, err := jsArray.JSONStringify()
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

// isTypedArray returns true if the input is TypedArray or DataView.
func isTypedArray(input *Value) bool {
	return slices.ContainsFunc(typedArrayTypes, input.isGlobalInstanceOf)
}

// processTempValue validates if temp is a valid result for the given T type.
func processTempValue[T any](prefix string, temp any, err error, samples ...T) (v T, _ error) {
	if err != nil {
		return v, fmt.Errorf("[%s] %w", prefix, err)
	}

	// Handle interface{} target types by accepting any converted value
	_, sample := createTemp(samples...)

	sampleRType := reflect.TypeOf(sample)
	if sampleRType == nil || sampleRType.Kind() == reflect.Interface {
		if temp == nil {
			return v, nil
		}

		tempT, _ := temp.(T)

		return tempT, nil
	}

	tempRType := reflect.TypeOf(temp)
	if tempRType != sampleRType {
		tempRValue := reflect.ValueOf(temp)
		if tempRValue.IsValid() && tempRValue.Type().ConvertibleTo(sampleRType) {
			temp = tempRValue.Convert(sampleRType).Interface()
			tempT, _ := temp.(T)

			return tempT, nil
		}

		return v, newInvalidGoTypeErr(goTypeName(sample), temp)
	}

	valueT, _ := temp.(T)

	return valueT, nil
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
		return nil, ErrEmptyStringToNumber
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

func createGoObjectTarget[T any](input objectOrMap, samples ...T) (
	temp any,
	obj objectOrMap,
	sample T,
	target reflect.Type,
) {
	if len(samples) > 0 {
		sample = samples[0]
	}

	obj = input
	if obj.IsMap() {
		obj = obj.ToMap()
	}

	target = reflect.TypeOf(sample)
	if target == nil {
		target = reflect.TypeFor[map[string]any]()
	}

	temp = reflect.New(target).Interface()

	return temp, obj, sample, target
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

func createTemp[T any](samples ...T) (any, T) {
	var (
		tempValue any
		sample    T
	)

	if len(samples) > 0 {
		sample = samples[0]
	}

	return tempValue, sample
}

func isFloatWholeNumber(floatVal float64) bool {
	return floatVal == float64(int64(floatVal)) &&
		floatVal >= math.MinInt64 &&
		floatVal <= math.MaxInt64
}

func isGoStruct(goType reflect.Type) bool {
	return goType.Kind() == reflect.Struct ||
		(goType.Kind() == reflect.Pointer && goType.Elem().Kind() == reflect.Struct)
}

// verifyGoFunc validates that a function signature is compatible with JS conversion.
func verifyGoFunc(fnType reflect.Type, sample any) error {
	if fnType == nil || fnType.Kind() != reflect.Func {
		return newInvalidGoTypeErr("function", sample)
	}

	// Validate that all return values are convertible to JS
	for out := range fnType.Outs() {
		err := isConvertibleToJs(out, make(map[reflect.Type]bool), "func return")
		if err != nil {
			return err
		}
	}

	// Validate that all parameters are convertible from JS
	for i := range fnType.NumIn() {
		err := isConvertibleToJs(fnType.In(i), make(map[reflect.Type]bool), "func param")
		if err != nil {
			return fmt.Errorf("parameter %d error: %w", i, err)
		}
	}

	return nil
}

func createGoBindFuncType[T any](sample T) (fnType reflect.Type, err error) {
	sampleVal := reflect.ValueOf(sample)
	if sampleVal.IsValid() && sampleVal.Kind() == reflect.Func {
		fnType = sampleVal.Type()
	}

	if err = verifyGoFunc(fnType, sample); err != nil {
		return fnType, err
	}

	return fnType, nil
}

func anyToError(err any) error {
	if err == nil {
		return nil
	}

	if e, ok := err.(error); ok {
		return e
	}

	if str, ok := err.(string); ok {
		return fmt.Errorf("recovered from panic: %s", str)
	}

	return fmt.Errorf("recovered from panic: %v", err)
}

// hashBytes computes a simple hash of the byte slice.
func hashBytes(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)

	return h.Sum64()
}

// parseTimezone attempts to parse a timezone string as either an IANA location name
// or a UTC offset format (+/-HH:MM). Returns UTC location if parsing fails.
func parseTimezone(tz string) *time.Location {
	// First try to parse as IANA timezone name (e.g., "America/New_York", "Asia/Tokyo")
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}

	// If IANA parsing fails, try to parse as UTC offset (e.g., "+05:30", "-08:00")
	hasSign := tz[0] == '+' || tz[0] == '-'
	if len(tz) >= 3 && hasSign {
		// Parse formats like "+05:30", "-08:00", "+0530", "-0800"
		var sign = 1
		if tz[0] == '-' {
			sign = -1
		}

		offset := tz[1:]

		var hours, minutes int

		// Handle both "+05:30" and "+0530" formats
		switch {
		case len(offset) == 5 && offset[2] == ':':
			_, _ = fmt.Sscanf(offset, "%02d:%02d", &hours, &minutes)
		case len(offset) == 4:
			_, _ = fmt.Sscanf(offset, "%02d%02d", &hours, &minutes)
		case len(offset) == 2:
			_, _ = fmt.Sscanf(offset, "%02d", &hours)
		}

		if hours >= 0 && hours <= 23 && minutes >= 0 && minutes <= 59 {
			offsetSeconds := sign * (hours*3600 + minutes*60)

			return time.FixedZone(tz, offsetSeconds)
		}
	}

	// Fallback to UTC if parsing fails
	return time.UTC
}

// is32BitPlatform check if the platform is 32-bit by comparing the size of uintptr.
func is32BitPlatform() bool {
	return strconv.IntSize == 32
}

// channelToJSObjectValue converts a Go channel to a JavaScript object with async methods.
func channelToJSObjectValue(
	c *Runtime,
	rtype reflect.Type,
	rval reflect.Value,
) (*Value, error) {
	if rval.IsNil() {
		return c.NewNull(), nil
	}

	return withJSObject(c, func(obj *Value) error {
		// Check channel direction
		chanDir := rtype.ChanDir()
		canSend := chanDir == reflect.BothDir || chanDir == reflect.SendDir
		canRecv := chanDir == reflect.BothDir || chanDir == reflect.RecvDir
		objMethods := map[string]any{
			"close":    createChannelCloseFunc(rval),
			"length":   rval.Len,
			"capacity": rval.Cap,
		}

		obj.SetPropertyStr("type", c.NewString("channel"))
		obj.SetPropertyStr("elementType", c.NewString(goTypeName(rtype.Elem())))
		obj.SetPropertyStr("canSend", c.NewBool(canSend))
		obj.SetPropertyStr("canReceive", c.NewBool(canRecv))

		if canSend {
			objMethods["send"] = createChannelSendFunc(rval)
		}

		if canRecv {
			objMethods["receive"] = createChannelReceiveFunc(rval)
		}

		for name, method := range objMethods {
			// Skip error check since we know the function signatures are correct
			jsMethod, _ := funcToJS(c, method)
			obj.SetPropertyStr(name, jsMethod)
		}

		return nil
	})
}

// createChannelSendFunc creates 'func(T) error' for sending T to a channel.
func createChannelSendFunc(chanRValue reflect.Value) any {
	errValue := errZeroRValue
	chanElemRType := chanRValue.Type().Elem()
	funcRType := reflect.FuncOf(
		[]reflect.Type{chanElemRType}, // params
		[]reflect.Type{errRType},      // returns
		false,                         // variadic
	)

	fn := reflect.MakeFunc(funcRType, func(args []reflect.Value) []reflect.Value {
		valueToSend := args[0]
		chosen, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: chanRValue, Send: valueToSend},
			{Dir: reflect.SelectDefault},
		})

		// Default case was chosen - send would block
		if chosen == 1 {
			errValue = reflect.ValueOf(ErrChanSend)
		}

		return []reflect.Value{errValue}
	})

	// Successful send
	return fn.Interface()
}

// createChannelReceiveFunc creates a function for receiving values from a channel.
func createChannelReceiveFunc(chanRValue reflect.Value) func() (any, error) {
	return func() (any, error) {
		chosen, recv, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: chanRValue},
			{Dir: reflect.SelectDefault},
		})

		// First: Check default case was chosen - no data available
		if chosen == 1 {
			return nil, ErrChanReceive
		}

		// Receive case was chosen but channel is closed
		if !ok {
			return nil, ErrChanClosed
		}

		// Successful receive
		return recv.Interface(), nil
	}
}

// createChannelCloseFunc creates a function for closing a channel.
func createChannelCloseFunc(rval reflect.Value) func() error {
	return func() error {
		// Only close if it's a send or bidirectional channel
		if rval.Type().ChanDir() == reflect.RecvDir {
			return ErrChanCloseReceiveOnly
		}

		rval.Close()

		return nil
	}
}

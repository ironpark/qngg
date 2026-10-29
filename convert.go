package qngg

import (
	"fmt"
	"reflect"
	"sync"
)

// convTracker tracks the objects being converted, Go pointers or JS
// values, to detect circular references.
type convTracker struct {
	processing map[uint64]bool
}

func newConvTracker() *convTracker {
	return &convTracker{}
}

// track registers an object for circular reference detection.
// Returns true if the object is already being processed (circular reference detected).
func (tracker *convTracker) track(key uint64) bool {
	if tracker.processing[key] {
		return true
	}

	// Most conversions never track anything, so the map is created lazily.
	if tracker.processing == nil {
		tracker.processing = make(map[uint64]bool)
	}

	tracker.processing[key] = true

	return false
}

// untrack removes an object from circular reference tracking.
func (tracker *convTracker) untrack(key uint64) {
	delete(tracker.processing, key)
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

func createGoObjectTarget[T any](samples ...T) (
	temp any,
	sample T,
	target reflect.Type,
) {
	if len(samples) > 0 {
		sample = samples[0]
	}

	target = reflect.TypeOf(sample)
	if target == nil {
		target = reflect.TypeFor[map[string]any]()
	}

	temp = reflect.New(target).Interface()

	return temp, sample, target
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

func isGoStruct(goType reflect.Type) bool {
	return goType.Kind() == reflect.Struct ||
		(goType.Kind() == reflect.Pointer && goType.Elem().Kind() == reflect.Struct)
}

// verifyGoFunc validates that a function signature is compatible with JS conversion.
func verifyGoFunc(fnType reflect.Type, sample any) error {
	if fnType == nil || fnType.Kind() != reflect.Func {
		return newInvalidGoTypeErr("function", sample)
	}

	if err, ok := funcChecks.Load(fnType); ok {
		err, _ := err.(error)

		return err
	}

	err := checkGoFunc(fnType)
	funcChecks.Store(fnType, err)

	return err
}

// funcChecks caches the result of checkGoFunc per function type.
var funcChecks sync.Map // reflect.Type -> error

// checkGoFunc reports whether the parameters and results of a function type
// can be converted.
func checkGoFunc(fnType reflect.Type) error {
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

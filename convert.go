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

// targetOf returns the type a JS value converts to for T: the dynamic type
// of sample if given, else T, or nil when that is an interface, meaning any
// Go value fits.
func targetOf[T any](samples ...T) reflect.Type {
	var sample T
	if len(samples) > 0 {
		sample = samples[0]
	}

	return reflect.TypeOf(sample)
}

// zeroOf returns the zero value of t, or nil when t is nil, for errors that
// name the type.
func zeroOf(t reflect.Type) any {
	if t == nil {
		return nil
	}

	return reflect.Zero(t).Interface()
}

// convertResult checks that temp, converted to target, is a valid result,
// converting it when it has a different but convertible type. An error is
// wrapped with prefix.
func convertResult(prefix string, temp any, err error, target reflect.Type) (any, error) {
	if err != nil {
		return nil, fmt.Errorf("[%s] %w", prefix, err)
	}

	if target == nil || reflect.TypeOf(temp) == target {
		return temp, nil
	}

	tempValue := reflect.ValueOf(temp)
	if tempValue.IsValid() && tempValue.Type().ConvertibleTo(target) {
		return tempValue.Convert(target).Interface(), nil
	}

	return nil, newInvalidGoTypeErr(goTypeName(target), temp)
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

// createGoBindFuncType returns target if it is a function type that can be
// converted.
func createGoBindFuncType(target reflect.Type) (fnType reflect.Type, err error) {
	if target != nil && target.Kind() == reflect.Func {
		fnType = target
	}

	if err = verifyGoFunc(fnType, zeroOf(target)); err != nil {
		return fnType, err
	}

	return fnType, nil
}

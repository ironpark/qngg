package qngg

import (
	"context"
	"fmt"
	"reflect"
)

var contextType = reflect.TypeFor[context.Context]()

// funcToJS converts a Go function to a JavaScript function.
func funcToJS(rt *Runtime, v any) (_ *Value, err error) {
	if v == nil {
		return rt.NewNull(), nil
	}

	defer func() {
		if err != nil {
			err = fmt.Errorf("[FuncToJS] %w", err)
		}
	}()

	rval := reflect.ValueOf(v)
	rtype := reflect.TypeOf(v)

	if rtype.Kind() == reflect.Pointer {
		if rval.IsNil() {
			return rt.NewNull(), nil
		}

		rval = rval.Elem()
		rtype = rtype.Elem()
	}

	if rtype.Kind() != reflect.Func {
		return nil, newInvalidGoTypeErr("function", v)
	}

	if rval.IsNil() {
		return rt.NewNull(), nil
	}

	fnType := rval.Type()
	if err := verifyGoFunc(fnType, v); err != nil {
		return nil, err
	}

	// A leading context.Context parameter receives the call's context, and
	// JavaScript arguments fill the parameters after it.
	argsType, takesContext := fnType, false
	if fnType.NumIn() > 0 && fnType.In(0) == contextType {
		in := make([]reflect.Type, fnType.NumIn()-1)
		for i := range in {
			in[i] = fnType.In(i + 1)
		}

		argsType, takesContext = reflect.FuncOf(in, nil, fnType.IsVariadic()), true
	}

	return rt.NewFunction(func(call *FunctionCall) (*Value, error) {
		goArgs, err := jsFuncArgsToGo(call.Args(), argsType)
		if err != nil {
			return nil, err
		}

		if takesContext {
			goArgs = append([]reflect.Value{reflect.ValueOf(call.Context())}, goArgs...)
		}

		var results []reflect.Value
		if fnType.IsVariadic() {
			results = rval.CallSlice(goArgs)
		} else {
			results = rval.Call(goArgs)
		}

		return goFuncResultToJs(rt, results)
	}), nil
}

// jsFuncArgsToGo converts JS arguments to Go arguments in both variadic and non-variadic functions,
// filling missing arguments with zero values.
func jsFuncArgsToGo(jsArgs []*Value, fnType reflect.Type) ([]reflect.Value, error) {
	numGoArgs := fnType.NumIn()
	goArgs := make([]reflect.Value, 0, numGoArgs)
	numJSArgs := len(jsArgs)

	if fnType.IsVariadic() {
		fixedArgs := min(numGoArgs-1, numJSArgs)
		for i := range fixedArgs {
			goVal, err := jsArgToGo(jsArgs[i], fnType.In(i))
			if err != nil {
				return nil, newArgConversionErr(i, err)
			}

			goArgs = append(goArgs, goVal)
		}

		// Handle variadic slice
		variadicSlice, err := createVariadicSlice(jsArgs[fixedArgs:], fnType.In(numGoArgs-1), fixedArgs)
		if err != nil {
			return nil, err
		}

		return append(goArgs, variadicSlice), nil
	}

	// Limit JS args to Go args count to avoid index out of bounds
	argsToProcess := min(numJSArgs, numGoArgs)
	for i := range argsToProcess {
		goVal, err := jsArgToGo(jsArgs[i], fnType.In(i))
		if err != nil {
			return nil, newArgConversionErr(i, err)
		}

		goArgs = append(goArgs, goVal)
	}

	// Fill missing arguments with zero values
	for i := argsToProcess; i < numGoArgs; i++ {
		goArgs = append(goArgs, reflect.Zero(fnType.In(i)))
	}

	return goArgs, nil
}

// handlePointerArgument processes JS arguments for pointer types.
func handlePointerArgument(jsArg *Value, argType reflect.Type) (reflect.Value, error) {
	if jsArg.IsNull() || jsArg.IsUndefined() {
		return reflect.Zero(argType), nil
	}

	underlyingType := argType.Elem()

	goVal, err := toGoValue(newConvTracker(), jsArg, underlyingType)
	if err != nil {
		return reflect.Value{}, newJsToGoErr(jsArg, err, "function param pointer to "+jsArg.Type())
	}

	ptrVal := reflect.New(underlyingType)
	ptrVal.Elem().Set(reflect.ValueOf(goVal))

	return ptrVal, nil
}

// jsArgToGo converts a single JS argument to a Go value with enhanced type handling.
func jsArgToGo(jsArg *Value, argType reflect.Type) (reflect.Value, error) {
	// A ProxyValue stands for the Go value it wraps, pointers included.
	if jsArg.isProxyValue() {
		goVal, err := toGoValue(newConvTracker(), jsArg, nil)
		if err != nil {
			return reflect.Value{}, err
		}

		rv := reflect.ValueOf(goVal)
		if !rv.IsValid() {
			return reflect.Zero(argType), nil
		}

		if !rv.Type().AssignableTo(argType) {
			return reflect.Value{}, newInvalidGoTypeErr(argType.String(), goVal)
		}

		return rv, nil
	}

	if argType.Kind() == reflect.Pointer {
		return handlePointerArgument(jsArg, argType)
	}

	if goVal, ok := jsBasicToGo(jsArg, argType); ok {
		return goVal, nil
	}

	goVal, err := toGoValue(newConvTracker(), jsArg, argType)
	if err != nil {
		return reflect.Value{}, newJsToGoErr(jsArg, err, "function param "+jsArg.Type())
	}

	return reflect.ValueOf(goVal), nil
}

// createVariadicSlice creates a reflect.Value slice for variadic arguments.
// Converts remaining JS arguments to the slice element type and returns as a slice value.
func createVariadicSlice(jsArgs []*Value, sliceType reflect.Type, fixedArgsCount int) (reflect.Value, error) {
	varArgType := sliceType.Elem()
	numVarArgs := len(jsArgs)
	variadicSlice := reflect.MakeSlice(sliceType, numVarArgs, numVarArgs)

	for i, jsArg := range jsArgs {
		goVal, err := jsArgToGo(jsArg, varArgType)
		if err != nil {
			return reflect.Value{}, newArgConversionErr(fixedArgsCount+i, err)
		}

		variadicSlice.Index(i).Set(goVal)
	}

	return variadicSlice, nil
}

// goFuncResultToJs processes Go function call results and converts them to JS values.
// If last return value is a non-nil error, it's thrown in JS context.
// The remaining return values are converted to JS value or JS array if there are multiple.
func goFuncResultToJs(rt *Runtime, results []reflect.Value) (*Value, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// Check if last return value is an error
	lastIdx := len(results) - 1
	lastResult := results[lastIdx]

	// Last return is error
	if isImplementError(lastResult.Type()) {
		// Last return is non-nil error -> throw in JS context
		if !lastResult.IsNil() {
			resultErr, _ := reflect.TypeAssert[error](lastResult)

			return nil, resultErr
		}

		// Error is nil, handle remaining return values
		remaining := results[:lastIdx]

		if len(remaining) == 0 {
			return nil, nil
		}

		// Single remaining value -> return that value
		if len(remaining) == 1 {
			return rt.ToValue(remaining[0].Interface())
		}

		// Multiple remaining values -> return as JS array
		jsValues := make([]any, len(remaining))
		for i, result := range remaining {
			jsValues[i] = result.Interface()
		}

		return rt.ToValue(jsValues)
	}

	// Single return value -> return that value
	if len(results) == 1 {
		return rt.ToValue(results[0].Interface())
	}

	// Multiple return values -> return as JS array
	jsValues := make([]any, len(results))
	for i, result := range results {
		jsValues[i] = result.Interface()
	}

	return rt.ToValue(jsValues)
}

// jsBasicToGo converts a string, boolean or finite number to a Go type of
// the same basic kind, like the generic conversion but without its
// overhead. It reports false for other combinations and for values the
// generic conversion rejects, so that it reports the error.
func jsBasicToGo(jsArg *Value, argType reflect.Type) (reflect.Value, bool) {
	switch kind := argType.Kind(); {
	case kind == reflect.String && jsArg.IsString():
		return reflect.ValueOf(jsArg.String()).Convert(argType), true
	case kind == reflect.Bool && jsArg.IsBool():
		return reflect.ValueOf(jsArg.Bool()).Convert(argType), true
	case kind >= reflect.Int && kind <= reflect.Float64 && canConvertToGoNumber(jsArg) == nil:
		// What jsNumericToGoConverter.Convert does, without allocating it.
		f := jsArg.Float64()
		if numericBoundsCheck(f, kind) != nil {
			return reflect.Value{}, false
		}

		n, err := floatToInt(f, kind)
		if err != nil {
			return reflect.Value{}, false
		}

		return reflect.ValueOf(n).Convert(argType), true
	}

	return reflect.Value{}, false
}

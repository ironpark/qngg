package qngg

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJsStringifyErr(t *testing.T) {
	t.Run("basic error wrapping", func(t *testing.T) {
		originalErr := errors.New("stringify failed")
		result := newJsStringifyErr("test", originalErr)

		assert.Error(t, result)
		assert.Contains(t, result.Error(), "js test:")
		assert.Contains(t, result.Error(), "stringify failed")
		assert.True(t, errors.Is(result, originalErr))
	})

	t.Run("different kinds", func(t *testing.T) {
		originalErr := errors.New("conversion error")

		result1 := newJsStringifyErr("array", originalErr)
		assert.Contains(t, result1.Error(), "js array:")

		result2 := newJsStringifyErr("object", originalErr)
		assert.Contains(t, result2.Error(), "js object:")
	})
}

func TestNewProxyErr(t *testing.T) {
	result := newProxyErr("boom")
	assert.Contains(t, result.Error(), "panic in Go function: boom")
	assert.Contains(t, result.Error(), "TestNewProxyErr", "the stack is included")
}

func TestNewJsToGoErr_JSONStringifyFailure(t *testing.T) {
	// This test requires a runtime to create a Value that fails JSONStringify
	rt, err := New(Config{})
	require.NoError(t, err)
	defer rt.Close()

	// Create a Value that will fail JSONStringify (circular reference)
	result, err := rt.Eval(context.Background(), "test.js", `
		const obj = {};
		obj.self = obj; // circular reference
		obj
	`)
	require.NoError(t, err)
	defer result.Free()

	// This should trigger the JSONStringify failure path
	jsErr := newJsToGoErr(result, errors.New("conversion failed"), "test details")

	assert.Error(t, jsErr)
	assert.Contains(t, jsErr.Error(), "conversion failed")
	assert.Contains(t, jsErr.Error(), "test details")
	// Should contain fallback string representation
	assert.Contains(t, jsErr.Error(), "[object Object]")
}

func TestNewJsToGoErr_EmptyErrorConditions(t *testing.T) {
	rt, err := New(Config{})
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt

	t.Run("with nil error", func(t *testing.T) {
		value := ctx.NewString("test")
		defer value.Free()

		result := newJsToGoErr(value, nil, "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "cannot convert JS detail")
		assert.NotContains(t, result.Error(), ":")
	})

	t.Run("with undefined value", func(t *testing.T) {
		value := ctx.NewUndefined()
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test"), "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "Undefined")
	})

	t.Run("with null value", func(t *testing.T) {
		value := ctx.NewNull()
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test"), "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "Null")
	})
}

func createCircularValue(ctx *Runtime) *Value {
	result, err := ctx.Eval(context.Background(), "test.js", `
		const obj = {};
		obj.self = obj;
		obj
	`)
	if err != nil {
		panic(err)
	}
	return result
}

func TestNewJsToGoErr(t *testing.T) {
	rt, err := New(Config{})
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt

	t.Run("JSONStringifyFailure", func(t *testing.T) {
		// Create a value that will cause JSONStringify to fail
		circularValue := createCircularValue(ctx)
		defer circularValue.Free()

		// This should hit the JSONStringify error path and use fallback
		result := newJsToGoErr(circularValue, errors.New("test error"))
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "test error")
		// Should use String() as fallback when JSONStringify fails
		assert.Contains(t, result.Error(), "[object Object]")
	})

	t.Run("JSONStringifySuccess", func(t *testing.T) {
		value := ctx.NewString("hello")
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test error"))
		assert.Error(t, result)
		assert.Contains(t, result.Error(), `"hello"`)
		assert.Contains(t, result.Error(), "test error")
	})
}

func TestNewInvalidJsInputErr_JSONStringifyFailure(t *testing.T) {
	rt, err := New(Config{})
	require.NoError(t, err)
	defer rt.Close()

	// Create a Value that will fail JSONStringify (circular reference)
	circularValue, err := rt.Eval(context.Background(), "test.js", `
		const obj = {};
		obj.self = obj; // circular reference
		obj
	`)
	require.NoError(t, err)
	defer circularValue.Free()

	// This should trigger the JSONStringify failure path in newInvalidJsInputErr
	result := newInvalidJsInputErr("array", circularValue)

	assert.Error(t, result)
	assert.Contains(t, result.Error(), "expected JS array")
	assert.Contains(t, result.Error(), "JSONStringify failed:")
	assert.Contains(t, result.Error(), "[object Object]") // Fallback String() representation
}

func TestNewInvalidJsInputErr_SuccessfulJSONStringify(t *testing.T) {
	rt, err := New(Config{})
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt

	// Create a simple value that JSONStringify will work on
	value := ctx.NewString("test string")
	defer value.Free()

	result := newInvalidJsInputErr("number", value)

	assert.Error(t, result)
	assert.Contains(t, result.Error(), "expected JS number")
	assert.Contains(t, result.Error(), `"test string"`) // Successful JSONStringify
	assert.NotContains(t, result.Error(), "JSONStringify failed")
}

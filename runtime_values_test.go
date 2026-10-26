package qngg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Basic Properties Tests
func TestContextBasicProperties(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("global_object_access", func(t *testing.T) {
		global := ctx.Global()
		assert.True(t, global.IsObject())

		// Check for standard JavaScript global objects
		console := must(global.Get("console"))
		assert.True(t, console.IsObject())
		console.Free()

		object := must(global.Get("Object"))
		assert.True(t, object.IsFunction())
		object.Free()
	})
}

// Value Creation Tests
func TestContextValueCreation(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("proxy_values", func(t *testing.T) {
		type state struct{ hits int }
		shared := &state{}

		noErr(ctx.Set("bump", func(s *state) int { s.hits++; return s.hits }))
		proxy := ctx.NewProxyValue(shared)
		defer proxy.Free()
		noErr(ctx.Global().Set("shared", proxy))

		result := must(ctx.Eval(context.Background(), "test.js", `bump(shared); bump(shared)`))
		defer result.Free()
		assert.Equal(t, int32(2), result.Int32())
		assert.Equal(t, 2, shared.hits, "the Go value itself is passed, not a copy")

		back := must(proxy.As[*state]())
		assert.Same(t, shared, back)
	})

	t.Run("primitive_values", func(t *testing.T) {
		testCases := []struct {
			name     string
			creator  func() *Value
			verifier func(*testing.T, *Value)
		}{
			{
				name:    "null",
				creator: func() *Value { return ctx.NewNull() },
				verifier: func(t *testing.T, val *Value) {
					assert.True(t, val.IsNull())
				},
			},
			{
				name:    "undefined",
				creator: func() *Value { return ctx.NewUndefined() },
				verifier: func(t *testing.T, val *Value) {
					assert.True(t, val.IsUndefined())
				},
			},
			{
				name:    "boolean_true",
				creator: func() *Value { return ctx.NewBool(true) },
				verifier: func(t *testing.T, val *Value) {
					assert.True(t, val.Bool())
				},
			},
			{
				name:    "boolean_false",
				creator: func() *Value { return ctx.NewBool(false) },
				verifier: func(t *testing.T, val *Value) {
					assert.False(t, val.Bool())
				},
			},
			{
				name:    "string",
				creator: func() *Value { return ctx.NewString("Hello, World!") },
				verifier: func(t *testing.T, val *Value) {
					assert.Equal(t, "Hello, World!", val.String())
				},
			},
			{
				name:    "int32",
				creator: func() *Value { return ctx.NewInt32(42) },
				verifier: func(t *testing.T, val *Value) {
					assert.Equal(t, int32(42), val.Int32())
				},
			},
			{
				name:    "int64",
				creator: func() *Value { return ctx.NewInt64(9876543210) },
				verifier: func(t *testing.T, val *Value) {
					assert.Equal(t, int64(9876543210), val.Int64())
				},
			},
			{
				name:    "uint32",
				creator: func() *Value { return ctx.NewUint32(42) },
				verifier: func(t *testing.T, val *Value) {
					assert.Equal(t, uint32(42), val.Uint32())
				},
			},
			{
				name:    "float64",
				creator: func() *Value { return ctx.NewFloat64(3.14159) },
				verifier: func(t *testing.T, val *Value) {
					assert.InEpsilon(t, 3.14159, val.Float64(), 0.00001)
				},
			},
			{
				name:    "bigint64",
				creator: func() *Value { return ctx.NewBigInt64(9876543210) },
				verifier: func(t *testing.T, val *Value) {
					assert.True(t, val.IsBigInt())
				},
			},
			{
				name:    "biguint64",
				creator: func() *Value { return ctx.NewBigUint64(9876543210) },
				verifier: func(t *testing.T, val *Value) {
					assert.True(t, val.IsBigInt())
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				val := tc.creator()
				defer val.Free()
				tc.verifier(t, val)
			})
		}
	})

	t.Run("date_values", func(t *testing.T) {
		tm := time.Date(2023, 10, 1, 12, 0, 0, 0, time.UTC)
		val := ctx.NewDate(tm)
		defer val.Free()
		assert.True(t, val.IsDate())
		// Compare milliseconds due to JavaScript Date precision
		assert.Equal(t, tm.UnixNano()/1e6, must(val.Time()).UnixNano()/1e6)
	})
}

// Objects and Collections Tests
func TestContextObjectsAndCollections(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("object_creation_and_properties", func(t *testing.T) {
		obj := ctx.NewObject()
		defer obj.Free()
		assert.True(t, obj.IsObject())

		noErr(obj.Set("name", ctx.NewString("John")))
		noErr(obj.Set("age", ctx.NewInt32(30)))

		assert.Equal(t, "John", must(obj.Get("name")).String())
		assert.Equal(t, int32(30), must(obj.Get("age")).Int32())
	})

	t.Run("json_parsing", func(t *testing.T) {
		jsonObj := must(ctx.ParseJSON(`{"name":"John","age":30,"isActive":true,"tags":["developer","golang"]}`))
		defer jsonObj.Free()

		assert.True(t, jsonObj.IsObject())
		assert.Equal(t, "John", must(jsonObj.Get("name")).String())
		assert.Equal(t, int32(30), must(jsonObj.Get("age")).Int32())
		assert.True(t, must(jsonObj.Get("isActive")).Bool())

		tags := must(jsonObj.Get("tags"))
		defer tags.Free()
		assert.True(t, tags.IsArray())

		assert.Equal(t, int64(2), tags.Len())

		tag0 := must(tags.Index(0))
		defer tag0.Free()
		assert.Equal(t, "developer", tag0.String())
	})
}

// Error Handling Tests
func TestContextErrorHandling(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("errors_returned_from_functions_are_thrown", func(t *testing.T) {
		testCases := []struct {
			name     string
			err      error
			wantType string
			wantMsg  string
		}{
			{"go_error", errors.New("custom error"), "Error", "Error: custom error"},
			{"syntax_error", &Error{Name: "SyntaxError", Message: "syntax error"}, "SyntaxError", "SyntaxError: syntax error"},
			{"type_error", &Error{Name: "TypeError", Message: "type error"}, "TypeError", "TypeError: type error"},
			{"reference_error", &Error{Name: "ReferenceError", Message: "reference error"}, "ReferenceError", "ReferenceError: reference error"},
			{"range_error", &Error{Name: "RangeError", Message: "range error"}, "RangeError", "RangeError: range error"},
			{"internal_error", &Error{Name: "InternalError", Message: "internal error"}, "InternalError", "InternalError: internal error"},
			{"custom_name", &Error{Name: "MyError", Message: "mine"}, "Error", "MyError: mine"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				noErr(ctx.SetFunc("fail", func(*FunctionCall) (*Value, error) { return nil, tc.err }))

				_, err := ctx.Eval(context.Background(), "throw.js", "fail()")
				var jsErr *Error
				require.ErrorAs(t, err, &jsErr)
				assert.Contains(t, err.Error(), tc.wantMsg)

				caught := must(ctx.Eval(context.Background(), "catch.js",
					"try { fail() } catch (e) { e instanceof "+tc.wantType+" }"))
				defer caught.Free()
				assert.True(t, caught.Bool(), "thrown error should be a %s", tc.wantType)
			})
		}
	})
}

// Script Execution Tests
func TestContextScriptExecution(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("eval_basic", func(t *testing.T) {
		result := must(ctx.Eval(context.Background(), "test_eval.js", `1 + 2`))
		defer result.Free()
		assert.Equal(t, int32(3), result.Int32())
	})

	t.Run("eval_with_module_type", func(t *testing.T) {
		result := must(ctx.Eval(context.Background(), "test_module.js", `export default 42;`, AsModule()))
		defer result.Free()
		assert.Equal(t, int32(42), result.Int32())
	})

	t.Run("compile_and_execute", func(t *testing.T) {
		bytecode := must(ctx.Compile("test_compile.js", `2 * 3`))
		assert.NotEmpty(t, bytecode)

		result := must(ctx.EvalBytecode(context.Background(), bytecode))
		defer result.Free()
		assert.Equal(t, int32(6), result.Int32())
	})

	t.Run("load_and_import", func(t *testing.T) {
		moduleCode := `export const value = 42;`
		noErr(ctx.LoadModule(context.Background(), "test_load.js", moduleCode))

		result := must(ctx.Eval(context.Background(), "test_import.js", `
			import { value } from 'test_load.js';
			export default value`, AsModule()))
		defer result.Free()
		assert.Equal(t, int32(42), result.Int32())
	})
}

// Function Binding Tests
func TestContextFunctionBinding(t *testing.T) {
	t.Run("Synchronous_Functions", func(t *testing.T) {
		testSynchronousFunctions(t)
	})

	t.Run("Asynchronous_Functions", func(t *testing.T) {
		testAsynchronousFunctions(t)
	})
}

// Helper structure for function test cases
type functionTestCase struct {
	name              string
	fn                Function
	code              string
	expectErrorString string
	expectError       func(*testing.T, error)
	expectValue       func(*testing.T, *Value)
}

// Synchronous Functions Tests
func testSynchronousFunctions(t *testing.T) {
	t.Run("basic_function_registration", func(t *testing.T) {
		_, ctx := setupRuntime(t)

		ctx.SetFunc("testFunc", func(this *FunctionCall) (*Value, error) {
			args := this.Args()
			if len(args) > 0 {
				return this.Runtime().NewString("Hello " + args[0].String()), nil
			}
			return this.Runtime().NewString("Hello World"), nil
		})

		result := must(ctx.Eval(context.Background(), "test.js", `testFunc("John")`))
		defer result.Free()
		assert.Equal(t, "Hello John", result.String())

		result2 := must(ctx.Eval(context.Background(), "test2.js", "testFunc()"))
		defer result2.Free()
		assert.Equal(t, "Hello World", result2.String())
	})

	t.Run("error_handling", func(t *testing.T) {
		tests := createErrorHandlingTests()
		runFunctionTests(t, tests, false)
	})

	t.Run("return_values", func(t *testing.T) {
		tests := createReturnValueTests()
		runFunctionTests(t, tests, false)
	})

	t.Run("argument_handling", func(t *testing.T) {
		tests := createArgumentHandlingTests()
		runFunctionTests(t, tests, false)
	})
}

// Asynchronous Functions Tests
func testAsynchronousFunctions(t *testing.T) {
	t.Run("basic_async_function", func(t *testing.T) {
		_, ctx := setupRuntime(t)

		noErr(ctx.SetAsyncFunc("asyncTestFunc", func(this *FunctionCall) (*Value, error) {
			return this.Runtime().NewString("async result"), nil
		}))

		result := must(ctx.Eval(context.Background(), "test_async.js", `async function run() { return await asyncTestFunc(); }; run()`))
		defer result.Free()
		assert.Equal(t, "async result", result.String())
	})

	t.Run("async_error_handling", func(t *testing.T) {
		tests := createAsyncErrorHandlingTests()
		runFunctionTests(t, tests, true)
	})

	t.Run("async_return_values", func(t *testing.T) {
		tests := createAsyncReturnValueTests()
		runFunctionTests(t, tests, true)
	})

	t.Run("async_argument_handling", func(t *testing.T) {
		tests := createAsyncArgumentHandlingTests()
		runFunctionTests(t, tests, true)
	})
}

func createErrorHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name:              "return_error",
			expectErrorString: "error string",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewUndefined(), errors.New("error string")
			},
		},
		{
			name:              "return_typed_error",
			expectErrorString: "TypeError: bad type",
			fn: func(this *FunctionCall) (*Value, error) {
				return nil, &Error{Name: "TypeError", Message: "bad type"}
			},
		},
		{
			name:              "panic",
			expectErrorString: "panic in Go function: boom",
			fn: func(this *FunctionCall) (*Value, error) {
				panic("boom")
			},
		},
	}
}

func createReturnValueTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "return_string",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewString("Hello world"), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, "Hello world", val.String())
			},
		},
		{
			name: "return_number",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewInt32(12345), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, int32(12345), val.Int32())
			},
		},
		{
			name: "return_bool",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewBool(true), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.True(t, val.Bool())
			},
		},
		{
			name: "return_undefined",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewUndefined(), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.True(t, val.IsUndefined())
			},
		},
	}
}

func createArgumentHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "string_argument",
			code: `goFunc('World')`,
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewString(
					"Hello " + this.Args()[0].String(),
				), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, "Hello World", val.String())
			},
		},
		{
			name: "numeric_arguments",
			code: `goFunc(10, 20)`,
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewInt32(
					this.Args()[0].Int32() + this.Args()[1].Int32(),
				), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, int32(30), val.Int32())
			},
		},
		{
			name: "object_argument",
			code: `goFunc({"name": "John Doe", "age": 30})`,
			fn: func(this *FunctionCall) (*Value, error) {
				obj := this.Args()[0].Clone()
				defer obj.Free()
				return this.Runtime().NewString(
					"Hello " + must(obj.Get("name")).String(),
				), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, "Hello John Doe", val.String())
			},
		},
	}
}

func createAsyncErrorHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name:              "reject_string",
			expectErrorString: "throw string",
			fn: func(this *FunctionCall) (*Value, error) {
				return nil, errors.New("throw string")
			},
		},
		{
			name:              "reject_number",
			expectErrorString: "12345",
			fn: func(this *FunctionCall) (*Value, error) {
				panic(12345)
			},
		},
		{
			name:              "throw_in_async",
			expectErrorString: "syntax error",
			fn: func(this *FunctionCall) (*Value, error) {
				return nil, &Error{Name: "SyntaxError", Message: "syntax error"}
			},
		},
	}
}

func createAsyncReturnValueTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "resolve_string",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewString("Hello world"), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, "Hello world", val.String())
			},
		},
		{
			name: "resolve_number",
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewInt32(12345), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, int32(12345), val.Int32())
			},
		},
	}
}

func createAsyncArgumentHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "async_string_argument",
			code: `await goAsyncFunc('World')`,
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewString(
					"Hello " + this.Args()[0].String(),
				), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, "Hello World", val.String())
			},
		},
		{
			name: "async_numeric_arguments",
			code: `await goAsyncFunc(10, 20)`,
			fn: func(this *FunctionCall) (*Value, error) {
				return this.Runtime().NewInt32(
					this.Args()[0].Int32() + this.Args()[1].Int32(),
				), nil
			},
			expectValue: func(t *testing.T, val *Value) {
				assert.Equal(t, int32(30), val.Int32())
			},
		},
	}
}

// runFunctionTests executes a set of function test cases
func runFunctionTests(t *testing.T, tests []functionTestCase, isAsync bool) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := must(New(Config{}))
			defer rt.Close()

			code := test.code
			if test.code == "" {
				if isAsync {
					code = `await goAsyncFunc()`
				} else {
					code = `goFunc()`
				}
			}

			funcName := "goFunc"
			if isAsync {
				funcName = "goAsyncFunc"
				noErr(rt.SetAsyncFunc(funcName, test.fn))
			} else {
				noErr(rt.SetFunc(funcName, test.fn))
			}

			var val *Value
			var err error
			if isAsync {
				val, err = rt.Eval(context.Background(), test.name+".js", code, TopLevelAwait())
			} else {
				val, err = rt.Eval(context.Background(), test.name+".js", code)
			}
			defer val.Free()

			if test.expectErrorString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.expectErrorString)
			}

			if test.expectError != nil {
				test.expectError(t, err)
				assert.True(t, val.IsUndefined())
			}

			if test.expectValue != nil {
				assert.NoError(t, err)
				test.expectValue(t, val)
			}
		})
	}
}

package qngg

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type proxyFunctionTestCase struct {
	name     string
	setupFn  func(*Runtime)
	testCode string
	validate func(*testing.T, *Value, error)
}

type proxyErrorTestCase struct {
	name        string
	setupFn     func(*Runtime)
	testCode    string
	expectError bool
	errorCheck  func(*testing.T, error)
}

// setupTestRuntime creates a runtime with cleanup for proxy tests
func setupProxyTestRuntime(t *testing.T) *Runtime {
	runtime := must(New(Config{}))
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

// TestBasicFunctionCall tests basic function calls (maintains original test)
func TestBasicFunctionCall(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.SetFunc("add", func(this *FunctionCall) (*Value, error) {
		if len(this.Args()) != 2 {
			return this.Runtime().NewUndefined(), errors.New("add requires 2 arguments")
		}

		a := this.Args()[0].Int32()
		b := this.Args()[1].Int32()
		return this.Runtime().NewInt32(a + b), nil
	})

	val, err := runtime.Eval(context.Background(), "test.js", `
        const result = add(5, 7);
        result;
    `)
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, int32(12), val.Int32())
}

// TestBasicFunctionCalls tests fundamental function calling functionality
func TestBasicFunctionCalls(t *testing.T) {
	testCases := []proxyFunctionTestCase{
		{
			name: "simple_addition_function",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("add", func(this *FunctionCall) (*Value, error) {
					if len(this.Args()) != 2 {
						return this.Runtime().NewUndefined(), errors.New("add requires 2 arguments")
					}
					a := this.Args()[0].Int32()
					b := this.Args()[1].Int32()
					return this.Runtime().NewInt32(a + b), nil
				})
			},
			testCode: "add(5, 7)",
			validate: func(t *testing.T, val *Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, int32(12), val.Int32())
			},
		},
		{
			name: "string_number_concatenation",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("concat", func(this *FunctionCall) (*Value, error) {
					if len(this.Args()) != 2 {
						return this.Runtime().NewUndefined(), errors.New("concat requires 2 arguments")
					}
					str := this.Args()[0].String()
					num := this.Args()[1].Int32()
					result := fmt.Sprintf("%s-%d", str, num)
					return this.Runtime().NewString(result), nil
				})
			},
			testCode: `concat("test", 42)`,
			validate: func(t *testing.T, val *Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, "test-42", val.String())
			},
		},
		{
			name: "no_arguments_function",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getConstant", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewString("constant value"), nil
				})
			},
			testCode: "getConstant()",
			validate: func(t *testing.T, val *Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, "constant value", val.String())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime)

			val, err := runtime.Eval(context.Background(), "test.js", tc.testCode)
			defer func() {
				if val != nil {
					val.Free()
				}
			}()

			tc.validate(t, val, err)
		})
	}
}

// TestArgumentValidation tests function argument validation and type checking
func TestArgumentValidation(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Setup validation function
	runtime.SetFunc("validateArgs", func(this *FunctionCall) (*Value, error) {
		args := this.Args()

		if len(args) < 2 {
			return this.Runtime().NewUndefined(), errors.New("requires at least 2 arguments")
		}

		if !args[0].IsString() {
			return this.Runtime().NewUndefined(), errors.New("first argument must be a string")
		}

		if !args[1].IsNumber() {
			return this.Runtime().NewUndefined(), errors.New("second argument must be a number")
		}

		return this.Runtime().NewBool(true), nil
	})

	t.Run("valid_arguments", func(t *testing.T) {
		val, err := runtime.Eval(context.Background(), "test.js", `validateArgs("test", 123)`)
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})

	t.Run("insufficient_arguments", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "test.js", `validateArgs("test")`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires at least 2 arguments")
	})

	t.Run("wrong_argument_type", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "test.js", `validateArgs("test", "not a number")`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "second argument must be a number")
	})

	t.Run("missing_first_argument", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "test.js", `validateArgs()`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires at least 2 arguments")
	})
}

// TestReturnValueTypes tests different return value types from Go functions
func TestReturnValueTypes(t *testing.T) {
	testCases := []struct {
		name     string
		setupFn  func(*Runtime)
		testCode string
		validate func(*testing.T, *Value)
	}{
		{
			name: "return_string",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getString", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewString("hello world"), nil
				})
			},
			testCode: "getString()",
			validate: func(t *testing.T, val *Value) {
				assert.Equal(t, "hello world", val.String())
			},
		},
		{
			name: "return_number",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getNumber", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewInt32(42), nil
				})
			},
			testCode: "getNumber()",
			validate: func(t *testing.T, val *Value) {
				assert.Equal(t, int32(42), val.Int32())
			},
		},
		{
			name: "return_boolean_true",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getBoolTrue", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewBool(true), nil
				})
			},
			testCode: "getBoolTrue()",
			validate: func(t *testing.T, val *Value) {
				assert.True(t, val.Bool())
			},
		},
		{
			name: "return_boolean_false",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getBoolFalse", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewBool(false), nil
				})
			},
			testCode: "getBoolFalse()",
			validate: func(t *testing.T, val *Value) {
				assert.False(t, val.Bool())
			},
		},
		{
			name: "return_null",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getNull", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewNull(), nil
				})
			},
			testCode: "getNull()",
			validate: func(t *testing.T, val *Value) {
				assert.True(t, val.IsNull())
			},
		},
		{
			name: "return_undefined",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("getUndefined", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewUndefined(), nil
				})
			},
			testCode: "getUndefined()",
			validate: func(t *testing.T, val *Value) {
				assert.True(t, val.IsUndefined())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime)

			val, err := runtime.Eval(context.Background(), "test.js", tc.testCode)
			defer val.Free()

			require.NoError(t, err)
			tc.validate(t, val)
		})
	}
}

// TestComplexReturnTypes tests complex JavaScript return types (objects, arrays)
func TestComplexReturnTypes(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("return_object", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "setup.js", `
			function returnObject() {
				return {name: "test", id: 123};
			}
		`)
		require.NoError(t, err)

		val, err := runtime.Eval(context.Background(), "test.js", "returnObject()")
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsObject())

		obj := val.Clone()
		defer obj.Free()
		assert.Equal(t, "test", must(obj.Get("name")).String())
		assert.Equal(t, int32(123), must(obj.Get("id")).Int32())
	})

	t.Run("return_array", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "setup.js", `
			function returnArray() {
				return [1, 2, 3];
			}
		`)
		require.NoError(t, err)

		val, err := runtime.Eval(context.Background(), "test.js", "returnArray()")
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsArray())

		arr := val.Clone()
		defer arr.Free()
		assert.Equal(t, int32(1), must(arr.Get("0")).Int32())
		assert.Equal(t, int32(2), must(arr.Get("1")).Int32())
		assert.Equal(t, int32(3), must(arr.Get("2")).Int32())
	})

	t.Run("return_nested_object", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "setup.js", `
			function returnNestedObject() {
				return {
					user: {name: "john", age: 30},
					meta: {created: true, version: 1}
				};
			}
		`)
		require.NoError(t, err)

		val, err := runtime.Eval(context.Background(), "test.js", "returnNestedObject()")
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsObject())

		obj := val.Clone()
		defer obj.Free()

		user := must(obj.Get("user")).Clone()
		defer user.Free()
		assert.Equal(t, "john", must(user.Get("name")).String())
		assert.Equal(t, int32(30), must(user.Get("age")).Int32())

		meta := must(obj.Get("meta")).Clone()
		defer meta.Free()
		assert.True(t, must(meta.Get("created")).Bool())
		assert.Equal(t, int32(1), must(meta.Get("version")).Int32())
	})
}

// TestProxyErrorHandling tests error propagation and handling
func TestProxyErrorHandling(t *testing.T) {
	testCases := []proxyErrorTestCase{
		{
			name: "function_returns_error",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("failingFunction", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewUndefined(), errors.New("this function fails")
				})
			},
			testCode:    "failingFunction()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "this function fails")
			},
		},
		{
			name: "function_throws_error",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("throwingFunction", func(this *FunctionCall) (*Value, error) {
					return nil, &Error{Name: "TypeError", Message: "thrown error"}
				})
			},
			testCode:    "throwingFunction()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "thrown error")
			},
		},
		{
			name: "custom_error_message",
			setupFn: func(ctx *Runtime) {
				ctx.SetFunc("customError", func(this *FunctionCall) (*Value, error) {
					return this.Runtime().NewUndefined(), fmt.Errorf("custom error with code: %d", 404)
				})
			},
			testCode:    "customError()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "custom error with code: 404")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime)

			_, err := runtime.Eval(context.Background(), "test.js", tc.testCode)

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorCheck != nil {
					tc.errorCheck(t, err)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPanicRecovery(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("panic_with_string", func(t *testing.T) {
		runtime.SetFunc("panicFunction", func(this *FunctionCall) (*Value, error) {
			panic("this function panics")
		})

		_, err := runtime.Eval(context.Background(), "test.js", `
			try {
				panicFunction();
			} catch (e) {
				throw new Error("Caught: " + e.message);
			}
		`)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "this function panics")
	})

	t.Run("panic_with_error", func(t *testing.T) {
		runtime.SetFunc("panicWithError", func(this *FunctionCall) (*Value, error) {
			panic(errors.New("panic error"))
		})

		_, err := runtime.Eval(context.Background(), "test.js", "panicWithError()")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "panic error")
	})

	t.Run("panic_recovery_continues_execution", func(t *testing.T) {
		runtime.SetFunc("sometimesPanics", func(this *FunctionCall) (*Value, error) {
			if len(this.Args()) > 0 && this.Args()[0].Bool() {
				panic("intentional panic")
			}
			return this.Runtime().NewString("success"), nil
		})

		// Test that after a panic, we can still call functions
		_, err1 := runtime.Eval(context.Background(), "test1.js", "sometimesPanics(true)")
		assert.Error(t, err1)

		val2, err2 := runtime.Eval(context.Background(), "test2.js", "sometimesPanics(false)")
		defer val2.Free()
		require.NoError(t, err2)
		assert.Equal(t, "success", val2.String())
	})
}

// TestAsyncFunctions tests asynchronous function handling
func TestAsyncFunctions(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("async_function_success", func(t *testing.T) {
		noErr(runtime.SetAsyncFunc("asyncSuccess", func(this *FunctionCall) (*Value, error) {
			return this.Runtime().NewString("async success"), nil
		}))

		val, err := runtime.Eval(context.Background(), "test.js", `
			async function test() {
				const result = await asyncSuccess();
				return result;
			}
			await test();
		`, TopLevelAwait())
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "async success", val.String())
	})

	t.Run("async_function_failure", func(t *testing.T) {
		noErr(runtime.SetAsyncFunc("asyncFailure", func(*FunctionCall) (*Value, error) {
			return nil, errors.New("async failure")
		}))

		_, err := runtime.Eval(context.Background(), "test.js", `await asyncFailure();`, TopLevelAwait())
		assert.ErrorContains(t, err, "async failure")
	})

	t.Run("async_function_with_data", func(t *testing.T) {
		noErr(runtime.SetAsyncFunc("asyncWithData", func(this *FunctionCall) (*Value, error) {
			if len(this.Args()) == 0 {
				return nil, errors.New("no data provided")
			}
			return this.Runtime().NewString(fmt.Sprintf("processed: %s", this.Arg(0).String())), nil
		}))

		val, err := runtime.Eval(context.Background(), "test.js", `await asyncWithData("test data");`, TopLevelAwait())
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "processed: test data", val.String())

		_, err = runtime.Eval(context.Background(), "test.js", `await asyncWithData();`, TopLevelAwait())
		assert.ErrorContains(t, err, "no data provided")
	})

	t.Run("async_function_returns_promise", func(t *testing.T) {
		noErr(runtime.SetAsyncFunc("later", func(this *FunctionCall) (*Value, error) {
			return this.Runtime().NewString("async result"), nil
		}))

		result := must(runtime.Eval(context.Background(), "test.js", `({ promise: later() })`))
		defer result.Free()

		promise := must(result.Get("promise"))
		defer promise.Free()
		assert.True(t, promise.IsPromise())

		a := must(promise.Await(context.Background()))
		defer a.Free()
		assert.Equal(t, "async result", a.String())
	})
}

// TestThisContext tests "this" context handling
func TestThisContext(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("this_object_property_access", func(t *testing.T) {
		runtime.SetFunc("getThisProperty", func(this *FunctionCall) (*Value, error) {
			thisValue := this.This()
			if !thisValue.IsObject() {
				return this.Runtime().NewUndefined(), errors.New("'this' is not an object")
			}

			prop := must(thisValue.Get("name"))
			return prop, nil
		})

		val, err := runtime.Eval(context.Background(), "test.js", `
			const obj = { name: "test object" };
			const result = getThisProperty.call(obj);
			result;
		`)
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "test object", val.String())
	})

	t.Run("this_context_modification", func(t *testing.T) {
		runtime.SetFunc("modifyThis", func(this *FunctionCall) (*Value, error) {
			thisValue := this.This()
			if !thisValue.IsObject() {
				return this.Runtime().NewUndefined(), errors.New("'this' is not an object")
			}

			// Modify the this object
			noErr(thisValue.Set("modified", this.Runtime().NewBool(true)))
			return this.Runtime().NewString("modified"), nil
		})

		val, err := runtime.Eval(context.Background(), "test_modify.js", `
			const testObj = { name: "test" };
			modifyThis.call(testObj);
			testObj.modified;
		`)
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})

	t.Run("this_global_context", func(t *testing.T) {
		runtime.SetFunc("checkGlobalThis", func(this *FunctionCall) (*Value, error) {
			// When called without explicit this, should be global object
			return this.Runtime().NewBool(this.This().IsObject()), nil
		})

		val, err := runtime.Eval(context.Background(), "test.js", "checkGlobalThis()")
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})
}

// TestMultipleFunctionInteraction tests interaction between multiple registered functions
func TestMultipleFunctionInteraction(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Setup multiple interacting functions
	runtime.SetFunc("setGlobalValue", func(this *FunctionCall) (*Value, error) {
		if len(this.Args()) != 1 {
			return this.Runtime().NewUndefined(), errors.New("requires 1 argument")
		}

		value := this.Args()[0]
		global := this.Runtime().Global()
		noErr(global.Set("testValue", value))

		return this.Runtime().NewUndefined(), nil
	})

	runtime.SetFunc("getGlobalValue", func(this *FunctionCall) (*Value, error) {
		global := this.Runtime().Global()
		return must(global.Get("testValue")), nil
	})

	runtime.SetFunc("processGlobalValue", func(this *FunctionCall) (*Value, error) {
		global := this.Runtime().Global()
		value := must(global.Get("testValue"))

		if value.IsUndefined() {
			return this.Runtime().NewString("no value set"), nil
		}

		processed := fmt.Sprintf("processed: %s", value.String())
		return this.Runtime().NewString(processed), nil
	})

	t.Run("function_chain_interaction", func(t *testing.T) {
		_, err := runtime.Eval(context.Background(), "test.js", `setGlobalValue("global test value")`)
		require.NoError(t, err)

		val, err := runtime.Eval(context.Background(), "test.js", `getGlobalValue()`)
		defer val.Free()
		require.NoError(t, err)
		assert.Equal(t, "global test value", val.String())

		processedVal, err := runtime.Eval(context.Background(), "test.js", `processGlobalValue()`)
		defer processedVal.Free()
		require.NoError(t, err)
		assert.Equal(t, "processed: global test value", processedVal.String())
	})

	t.Run("function_state_persistence", func(t *testing.T) {
		// Set initial value
		_, err := runtime.Eval(context.Background(), "test1.js", `setGlobalValue("persistent value")`)
		require.NoError(t, err)

		// Access from different eval call
		val, err := runtime.Eval(context.Background(), "test2.js", `getGlobalValue()`)
		defer val.Free()
		require.NoError(t, err)
		assert.Equal(t, "persistent value", val.String())
	})
}

// TestProxyRegistryOperations tests the improved ProxyRegistry functionality
func TestProxyRegistryOperations(t *testing.T) {
	t.Run("registry_basic_operations", func(t *testing.T) {
		registry := newProxyRegistry()

		// Test registration
		fn1 := func() string { return "test1" }
		fn2 := func() string { return "test2" }

		id1 := registry.Register(fn1)
		id2 := registry.Register(fn2)

		assert.NotEqual(t, id1, id2)
		assert.NotZero(t, id1)
		assert.NotZero(t, id2)

		// Test retrieval
		retrieved1, ok1 := registry.Get(id1)
		assert.True(t, ok1)
		assert.NotNil(t, retrieved1)

		retrieved2, ok2 := registry.Get(id2)
		assert.True(t, ok2)
		assert.NotNil(t, retrieved2)

		// Test length
		assert.Equal(t, 2, registry.Len())
	})

	t.Run("registry_nil_handling", func(t *testing.T) {
		registry := newProxyRegistry()

		// Test nil registration
		id := registry.Register(nil)
		assert.Zero(t, id)

		// Test invalid ID retrieval
		_, ok := registry.Get(0)
		assert.False(t, ok)

		// Test non-existent ID
		_, ok = registry.Get(999)
		assert.False(t, ok)
	})

	t.Run("registry_unregister", func(t *testing.T) {
		registry := newProxyRegistry()

		fn := func() string { return "test" }
		id := registry.Register(fn)

		assert.Equal(t, 1, registry.Len())

		// Test successful unregistration
		removed := registry.Unregister(id)
		assert.True(t, removed)
		assert.Equal(t, 0, registry.Len())

		// Test retrieval after unregistration
		_, ok := registry.Get(id)
		assert.False(t, ok)

		// Test duplicate unregistration
		removed = registry.Unregister(id)
		assert.False(t, removed)

		// Test unregistration zero ID
		removed = registry.Unregister(0)
		assert.False(t, removed)
	})

	t.Run("registry_clear", func(t *testing.T) {
		registry := newProxyRegistry()

		// Register multiple functions
		for i := range 5 {
			registry.Register(func() int { return i })
		}

		assert.Equal(t, 5, registry.Len())

		// Clear registry
		registry.Clear()
		assert.Equal(t, 0, registry.Len())
	})
}

// TestFunctionWithDifferentArgTypes maintains original test compatibility
func TestFunctionWithDifferentArgTypes(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.SetFunc("concat", func(this *FunctionCall) (*Value, error) {
		if len(this.Args()) != 2 {
			return this.Runtime().NewUndefined(), errors.New("concat requires 2 arguments")
		}

		str := this.Args()[0].String()
		num := this.Args()[1].Int32()
		result := fmt.Sprintf("%s-%d", str, num)

		return this.Runtime().NewString(result), nil
	})

	val, err := runtime.Eval(context.Background(), "test.js", `
        const result = concat("test", 42);
        result;
    `)
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "test-42", val.String())
}

// TestFunctionWithThis maintains original test compatibility
func TestFunctionWithThis(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Function that uses the "this" value
	runtime.SetFunc("getThisProperty", func(this *FunctionCall) (*Value, error) {
		thisValue := this.This()
		if !thisValue.IsObject() {
			return this.Runtime().NewUndefined(), errors.New("'this' is not an object")
		}

		prop := must(thisValue.Get("name"))
		return prop, nil
	})

	val, err := runtime.Eval(context.Background(), "test.js", `
        const obj = { name: "test object" };
        const result = getThisProperty.call(obj);
        result;
    `)
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "test object", val.String())
}

// TestPanicHandling maintains original test compatibility
func TestPanicHandling(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.SetFunc("panicFunction", func(this *FunctionCall) (*Value, error) {
		panic("this function panics")
	})

	_, err := runtime.Eval(context.Background(), "test.js", `
			try {
					panicFunction();
			} catch (e) {
					throw new Error("Caught: " + e.message);
			}
	`)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "this function panics")
}

// TestMultipleFunctions maintains original test compatibility
func TestMultipleFunctions(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.SetFunc(
		"setGlobalValue",
		func(this *FunctionCall) (*Value, error) {
			if len(this.Args()) != 1 {
				return this.Runtime().NewUndefined(), errors.New("requires 1 argument")
			}

			value := this.Args()[0]
			global := this.Runtime().Global()
			noErr(global.Set("testValue", value))

			return this.Runtime().NewUndefined(), nil
		},
	)

	runtime.SetFunc(
		"getGlobalValue",
		func(this *FunctionCall) (*Value, error) {
			global := this.Runtime().Global()
			return must(global.Get("testValue")), nil
		},
	)

	_, err := runtime.Eval(context.Background(), "test.js", `
		setGlobalValue("global test value");
	`)
	require.NoError(t, err)

	val, err := runtime.Eval(context.Background(), "test.js", `
		getGlobalValue();
	`)
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "global test value", val.String())
}

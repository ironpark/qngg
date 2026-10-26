package qngg

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
)

// proxyRegistry stores Go functions that can be called from JavaScript.
// It provides thread-safe registration and retrieval of functions with automatic ID generation.
type proxyRegistry struct {
	nextID  atomic.Uint64
	mu      sync.RWMutex
	proxies map[uint64]any
}

// newProxyRegistry creates a new thread-safe proxy registry.
func newProxyRegistry() *proxyRegistry {
	return &proxyRegistry{
		proxies: make(map[uint64]any),
	}
}

// Register adds a function to the registry and returns its unique ID.
// This method is thread-safe and can be called concurrently.
func (r *proxyRegistry) Register(fn any) uint64 {
	if fn == nil {
		return 0
	}

	id := r.nextID.Add(1)
	r.mu.Lock()
	r.proxies[id] = fn
	r.mu.Unlock()

	return id
}

// Get retrieves a function by its ID.
// Returns the function and true if found, nil and false otherwise.
// This method is thread-safe and can be called concurrently.
func (r *proxyRegistry) Get(id uint64) (any, bool) {
	if id == 0 {
		return nil, false
	}

	r.mu.RLock()
	fn, ok := r.proxies[id]
	r.mu.RUnlock()

	return fn, ok
}

// Unregister removes a function from the registry by its ID.
// Returns true if the function was found and removed, false otherwise.
// This method is thread-safe and can be called concurrently.
func (r *proxyRegistry) Unregister(id uint64) bool {
	if id == 0 {
		return false
	}

	r.mu.Lock()

	_, exists := r.proxies[id]
	if exists {
		delete(r.proxies, id)
	}

	r.mu.Unlock()

	return exists
}

// Len returns the number of registered functions.
// This method is thread-safe.
func (r *proxyRegistry) Len() int {
	r.mu.RLock()
	count := len(r.proxies)
	r.mu.RUnlock()

	return count
}

// Clear removes all registered functions from the registry.
// This method is thread-safe.
func (r *proxyRegistry) Clear() {
	r.mu.Lock()
	r.proxies = make(map[uint64]any)
	r.mu.Unlock()
}

// jsFunctionProxy is the Go host function that will be imported by the WASM module.
// It corresponds to the following C declaration:
//
//	__attribute__((import_module("env"), import_name("jsFunctionProxy")))
//	extern JSValue jsFunctionProxy(JSContext *ctx, JSValueConst this, int argc, JSValueConst *argv);
//
// Parameters:
//   - ctx: 	the Go context of the runtime
//   - mem: 	the WebAssembly memory of the runtime
//   - jsCtx: 	JSContext pointer
//   - this: 	JSValueConst this (the "this" value)
//   - argc: 	int argc (number of arguments)
//   - argv: 	pointer to argv (an array of JSValueConst/JSValue, each 8 bytes - uint64)
type jsFunctionProxy = func(
	ctx context.Context,
	mem *wasmMemory,
	jsCtx uint32,
	thisVal uint64,
	argc uint32,
	argv uint32,
) (rs uint64)

// createFuncProxyWithRegistry creates a WASM function proxy that bridges JavaScript function calls to Go functions.
// It handles parameter extraction, error recovery, and result conversion between JS and Go.
func createFuncProxyWithRegistry(registry *proxyRegistry) jsFunctionProxy {
	return func(
		_ context.Context,
		mem *wasmMemory,
		_ uint32,
		thisVal uint64,
		argc uint32,
		argv uint32,
	) (rs uint64) {
		goFunc, this := getProxyFuncParams(registry, mem, thisVal, argc, argv)

		defer func() {
			if r := recover(); r != nil {
				rs = handlePanicRecovery(this, r)
			}
		}()

		result, err := goFunc(this)
		if err != nil {
			return this.runtime.ThrowError(err).raw()
		}

		return validateAndReturnResult(this, result)
	}
}

// handlePanicRecovery handles panics during Go function execution.
func handlePanicRecovery(this *This, r any) uint64 {
	recoveredErr := newProxyErr(0, r)
	if this.isAsync && this.promise != nil {
		errVal := this.runtime.NewError(recoveredErr)
		rejectErr := this.promise.Reject(errVal)

		return this.runtime.ThrowError(combineErrors(recoveredErr, rejectErr)).raw()
	}

	return this.runtime.ThrowError(recoveredErr).raw()
}

// validateAndReturnResult validates the function result and handles JavaScript exceptions.
func validateAndReturnResult(this *This, result *Value) uint64 {
	if this.runtime.hasException() {
		panic(this.runtime.exception())
	}

	if result == nil {
		return this.runtime.NewUndefined().raw()
	}

	return result.raw()
}

// getProxyFuncParams extracts and validates parameters for proxy function calls.
// It parses WASM memory to extract function ID, context, arguments, and async information.
func getProxyFuncParams(
	registry *proxyRegistry,
	mem *wasmMemory,
	thisRef uint64,
	argc uint32,
	argv uint32,
) (Function, *This) {
	args := readArgsFromWasmMem(mem, argc, argv)
	functionHandle := args[0]  // The first argument is the function ID
	jsContextHandle := args[1] // The second argument is the context ID
	isAsync := args[2]         // The third argument is the async flag

	goFunc, goContext := retrieveGoResources(registry, functionHandle, jsContextHandle)
	promise := extractPromiseIfAsync(goContext, args, isAsync)
	fnArgs := extractFunctionArguments(goContext, args)
	this := createThisContext(goContext, thisRef, fnArgs, promise, isAsync)

	return goFunc, this
}

// extractPromiseIfAsync extracts the promise handle for async functions.
func extractPromiseIfAsync(context *Runtime, args []uint64, isAsync uint64) *Value {
	if isAsync == 0 {
		return context.NewUndefined()
	}

	promiseHandle := args[3]

	return context.newValue(newRawHandle(context, promiseHandle))
}

// extractFunctionArguments extracts and clones function arguments from WASM memory.
func extractFunctionArguments(context *Runtime, args []uint64) []*Value {
	fnArgs := make([]*Value, len(args)-4)
	for i := range fnArgs {
		argHandle := args[4+i]
		arg := context.newValue(newRawHandle(context, argHandle))
		fnArgs[i] = arg.Clone()
	}

	return fnArgs
}

// createThisContext creates the This context for function execution.
func createThisContext(context *Runtime, thisRef uint64, args []*Value, promise *Value, isAsync uint64) *This {
	thisValue := context.newValue(newRawHandle(context, thisRef))
	this := &This{
		runtime: context,
		Value:   thisValue,
		args:    args,
		promise: promise,
		isAsync: isAsync != 0,
	}

	return this
}

// retrieveGoResources retrieves and validates Go function and context from the registry.
func retrieveGoResources(
	registry *proxyRegistry,
	functionHandle uint64,
	jsContextHandle uint64,
) (Function, *Runtime) {
	// Retrieve and validate function
	fn, _ := registry.Get(functionHandle)
	goFunc, _ := fn.(Function)
	jsContext, _ := registry.Get(jsContextHandle)
	goContext, _ := jsContext.(*Runtime)

	return goFunc, goContext
}

// readArgsFromWasmMem reads the argument array from WASM memory with proper validation.
// Each argument is 8 bytes (uint64) in little-endian format.
func readArgsFromWasmMem(mem *wasmMemory, argc uint32, argv uint32) []uint64 {
	args := make([]uint64, argc)
	if argc == 0 {
		return args
	}

	data := mem.MustRead(argv, uint64(argc)*8)

	for i := range args {
		// Each argument is 8 bytes in little-endian encoding
		args[i] = binary.LittleEndian.Uint64(data[i*8:])
	}

	return args
}

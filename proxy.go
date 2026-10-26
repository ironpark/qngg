package qngg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// Function is a Go function callable from JavaScript. A returned error is
// thrown as an Error; return an *Error to throw a TypeError, RangeError and
// so on. A nil value returns undefined. The returned value is handed over
// to JavaScript: do not use or free it afterwards.
type Function func(call *FunctionCall) (*Value, error)

// FunctionCall describes a call of a Function from JavaScript. Its values
// belong to the caller and are only valid until the Function returns; Clone
// them to keep them longer.
type FunctionCall struct {
	rt   *Runtime
	this *Value
	args []*Value
}

// Runtime returns the runtime the function was called in.
func (c *FunctionCall) Runtime() *Runtime { return c.rt }

// Context returns the context of the call into JavaScript that led to this
// call, such as the one passed to Runtime.Eval, or context.Background()
// outside of one.
func (c *FunctionCall) Context() context.Context {
	if ctx := c.rt.interrupt.ctx; ctx != nil {
		return ctx
	}

	return context.Background()
}

// This returns the this value of the call.
func (c *FunctionCall) This() *Value { return c.this }

// Args returns the arguments of the call.
func (c *FunctionCall) Args() []*Value { return c.args }

// Arg returns the i-th argument, or undefined if there are fewer arguments.
func (c *FunctionCall) Arg(i int) *Value {
	if i < 0 || i >= len(c.args) {
		return c.rt.borrowValue(jsUndefined)
	}

	return c.args[i]
}

// callGoFunction implements the jsFunctionProxy import. The JS wrapper made
// by QJS_CreateFunctionProxy passes the function ID, an unused context ID,
// the async flag and, for async functions, a promise with resolve/reject
// methods, followed by the call's arguments.
//
// An error is only returned when the call cannot complete because the
// runtime was interrupted, and becomes a trap that unwinds wasm.
func (r *Runtime) callGoFunction(thisRef uint64, argc, argv uint32) (uint64, error) {
	if err := r.interruptErr(); err != nil {
		return 0, err
	}

	if argc < 4 {
		return 0, errors.New("jsFunctionProxy: missing arguments")
	}

	data := r.mem.MustRead(argv, uint64(argc)*8)
	arg := func(i int) uint64 { return binary.LittleEndian.Uint64(data[i*8:]) }

	// The ID and the async flag are int-tagged values.
	entry, _ := r.registry.Get(uint64(int32(arg(0))))
	fn, _ := entry.(Function)
	async := int32(arg(2)) != 0

	values := make([]Value, argc-3)
	call := &FunctionCall{rt: r, this: &values[0], args: make([]*Value, argc-4)}
	values[0] = Value{rt: r, ref: thisRef, borrowed: true}

	for i := range call.args {
		values[i+1] = Value{rt: r, ref: arg(4 + i), borrowed: true}
		call.args[i] = &values[i+1]
	}

	result, err := r.runGoFunction(fn, call)
	if ierr := r.interruptErr(); ierr != nil {
		return 0, ierr
	}

	if async {
		return jsUndefined, r.settle(r.borrowValue(arg(3)), result, err)
	}

	if err != nil {
		return r.throw(err), nil
	}

	return result, nil
}

// runGoFunction runs fn, turning a panic into an error, and hands its result
// over as a raw value.
func (r *Runtime) runGoFunction(fn Function, call *FunctionCall) (raw uint64, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if ierr := r.interruptErr(); ierr != nil {
				err = ierr

				return
			}

			err = newProxyErr(rec)
		}
	}()

	if fn == nil {
		return 0, errors.New("jsFunctionProxy: function not found")
	}

	result, err := fn(call)
	if err != nil {
		result.Free()

		return 0, err
	}

	if result == nil {
		return jsUndefined, nil
	}

	if err := result.check(); err != nil {
		return 0, fmt.Errorf("invalid return value: %w", err)
	}

	return result.take(), nil
}

// settle resolves or rejects the promise of an async function call.
func (r *Runtime) settle(promise *Value, result uint64, callErr error) (err error) {
	defer recoverInterrupt(r, &err)

	method, arg := "resolve", r.newValue(result)
	if callErr != nil {
		method, arg = "reject", r.NewError(callErr)
	}
	defer arg.Free()

	// If the promise cannot be settled, which only happens if its methods
	// were tampered with, it never resolves; the call itself succeeded.
	if v, err := promise.Invoke(context.Background(), method, arg); err == nil {
		v.Free()
	}

	return nil
}

// throw throws err into JavaScript and returns the exception marker.
func (r *Runtime) throw(err error) uint64 {
	errVal := r.NewError(err)

	return r.call("JS_Throw", r.jsCtx, errVal.take())
}

func newProxyErr(r any) error {
	return fmt.Errorf("panic in Go function: %v\n%s", r, debug.Stack())
}

// proxyRegistry stores Go values referenced from JavaScript by ID: the Go
// functions behind JS functions and the values wrapped by ProxyValues.
type proxyRegistry struct {
	nextID  atomic.Uint64
	mu      sync.RWMutex
	proxies map[uint64]any
}

func newProxyRegistry() *proxyRegistry {
	return &proxyRegistry{proxies: make(map[uint64]any)}
}

// Register adds v and returns its ID, which is never 0.
func (r *proxyRegistry) Register(v any) uint64 {
	if v == nil {
		return 0
	}

	id := r.nextID.Add(1)

	r.mu.Lock()
	r.proxies[id] = v
	r.mu.Unlock()

	return id
}

// Get returns the value registered as id.
func (r *proxyRegistry) Get(id uint64) (any, bool) {
	r.mu.RLock()
	v, ok := r.proxies[id]
	r.mu.RUnlock()

	return v, ok
}

// Unregister removes id and reports whether it was registered.
func (r *proxyRegistry) Unregister(id uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.proxies[id]
	delete(r.proxies, id)

	return ok
}

// Len returns the number of registered values.
func (r *proxyRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.proxies)
}

// Clear removes all registered values.
func (r *proxyRegistry) Clear() {
	r.mu.Lock()
	r.proxies = make(map[uint64]any)
	r.mu.Unlock()
}

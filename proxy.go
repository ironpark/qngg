package qngg

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
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

// callFrame holds a FunctionCall and its values, in a single allocation
// for calls with up to len(vals) arguments.
type callFrame struct {
	call FunctionCall
	this Value
	vals [4]Value
	ptrs [4]*Value
}

// callGoFunction implements the jsFunctionProxy import: it calls the Go
// function registered as id with this and the argc arguments at argv. For
// async functions, resolvers points to the resolve and reject functions of
// the promise returned to JavaScript.
//
// An error is only returned when the call cannot complete because the
// runtime was interrupted, and becomes a trap that unwinds wasm.
func (r *Runtime) callGoFunction(thisRef uint64, argc, argv, id, resolvers uint32) (uint64, error) {
	if err := r.interruptErr(); err != nil {
		return 0, err
	}

	entry, _ := r.registry.Get(id)
	fn, _ := entry.(Function)

	// Like a sloppy-mode function, a Go function called without a this
	// value gets the global object.
	if tag := tagOf(thisRef); tag == tagUndefined || tag == tagNull {
		thisRef = r.global.ref
	}

	frame := &callFrame{this: Value{rt: r, ref: thisRef, borrowed: true}}
	frame.call = FunctionCall{rt: r, this: &frame.this}

	vals, ptrs := frame.vals[:0], frame.ptrs[:0]
	if int(argc) > len(frame.vals) {
		vals, ptrs = make([]Value, 0, argc), make([]*Value, 0, argc)
	}

	data := r.view(argv, 8*argc)
	for i := range argc {
		vals = append(vals, Value{rt: r, ref: binary.LittleEndian.Uint64(data[8*i:]), borrowed: true})
	}

	for i := range vals {
		ptrs = append(ptrs, &vals[i])
	}

	frame.call.args = ptrs

	result, err := r.runGoFunction(fn, &frame.call)
	if ierr := r.interruptErr(); ierr != nil {
		return 0, ierr
	}

	if resolvers != 0 {
		return jsUndefined, r.settle(resolvers, result, err)
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

// settle resolves or rejects the promise of an async function call, by
// calling one of the resolving functions at resolvers.
func (r *Runtime) settle(resolvers uint32, result uint64, callErr error) (err error) {
	defer recoverInterrupt(r, &err)

	data := r.view(resolvers, 16)

	fn, arg := binary.LittleEndian.Uint64(data), r.newValue(result)
	if callErr != nil {
		fn, arg = binary.LittleEndian.Uint64(data[8:]), r.NewError(callErr)
	}
	defer arg.Free()

	argv := r.pushValues(arg.ref)
	defer r.drop(argv)

	// Resolving functions only throw when out of memory; the call itself
	// succeeded either way.
	if v, err := r.result(r.call(xCall, r.jsCtx, fn, jsUndefined, 1, uint64(argv.ptr))); err == nil {
		v.Free()
	}

	return nil
}

// throw throws err into JavaScript and returns the exception marker.
func (r *Runtime) throw(err error) uint64 {
	errVal := r.NewError(err)

	return r.call(xThrow, r.jsCtx, errVal.take())
}

func newProxyErr(r any) error {
	return fmt.Errorf("panic in Go function: %v\n%s", r, debug.Stack())
}

// proxyRegistry stores Go values referenced from JavaScript by ID: the Go
// functions behind JS functions and the values wrapped by ProxyValues.
// Entries are removed when the JavaScript object is garbage collected.
type proxyRegistry struct {
	mu      sync.Mutex
	lastID  uint32
	proxies map[uint32]any
}

func newProxyRegistry() *proxyRegistry {
	return &proxyRegistry{proxies: make(map[uint32]any)}
}

// Register adds v and returns its ID, which is never 0.
func (r *proxyRegistry) Register(v any) uint32 {
	if v == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// IDs are only reused after wrapping around, skipping those in use.
	for {
		r.lastID++
		if _, used := r.proxies[r.lastID]; r.lastID != 0 && !used {
			break
		}
	}

	r.proxies[r.lastID] = v

	return r.lastID
}

// Get returns the value registered as id.
func (r *proxyRegistry) Get(id uint32) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	v, ok := r.proxies[id]

	return v, ok
}

// Unregister removes id and reports whether it was registered.
func (r *proxyRegistry) Unregister(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.proxies[id]
	delete(r.proxies, id)

	return ok
}

// Len returns the number of registered values.
func (r *proxyRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.proxies)
}

// Clear removes all registered values.
func (r *proxyRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	clear(r.proxies)
}

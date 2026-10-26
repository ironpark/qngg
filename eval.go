package qngg

import (
	"context"
	"errors"
)

// QuickJS eval flags.
const (
	jsEvalTypeGlobal      = 0
	jsEvalTypeModule      = 1 << 0
	jsEvalFlagStrict      = 1 << 3
	jsEvalFlagCompileOnly = 1 << 5
	jsEvalFlagAsync       = 1 << 7
)

// EvalOption configures how code is evaluated or compiled.
type EvalOption func(*evalConfig)

type evalConfig struct {
	flags uint64
}

// AsModule evaluates the code as an ES module rather than a script. Eval
// then returns the module's default export, or undefined.
func AsModule() EvalOption {
	return func(c *evalConfig) { c.flags |= jsEvalTypeModule }
}

// TopLevelAwait allows `await` at the top level of a script. Modules
// always allow it.
func TopLevelAwait() EvalOption {
	return func(c *evalConfig) { c.flags |= jsEvalFlagAsync }
}

func newEvalConfig(opts []EvalOption) evalConfig {
	c := evalConfig{flags: jsEvalTypeGlobal | jsEvalFlagStrict}
	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// evalInput is the code to evaluate: source, bytecode, or neither to load
// the file `name` from CWD.
type evalInput struct {
	name     string
	source   *string
	bytecode []byte
}

// Eval runs src, a script (or module with AsModule) named `name` for stack
// traces and relative imports, and returns its completion value. Promises
// are awaited, and pending jobs and timers run before Eval returns. A
// JavaScript exception is returned as an *Error.
//
// Cancelling ctx, or reaching its deadline, interrupts the code, after which
// the runtime fails every call with ErrInterrupted and must be closed.
func (r *Runtime) Eval(ctx context.Context, name, src string, opts ...EvalOption) (*Value, error) {
	return r.eval(ctx, evalInput{name: name, source: &src}, newEvalConfig(opts))
}

// EvalFile is like Eval for the file at path, relative to Config.CWD.
func (r *Runtime) EvalFile(ctx context.Context, path string, opts ...EvalOption) (*Value, error) {
	return r.eval(ctx, evalInput{name: path}, newEvalConfig(opts))
}

// EvalBytecode is like Eval for bytecode from Compile. Pass AsModule for
// bytecode compiled with it.
func (r *Runtime) EvalBytecode(ctx context.Context, bytecode []byte, opts ...EvalOption) (*Value, error) {
	if len(bytecode) == 0 {
		return nil, errors.New("bytecode is empty")
	}

	return r.eval(ctx, evalInput{name: "<bytecode>", bytecode: bytecode}, newEvalConfig(opts))
}

// Compile compiles src, like Eval would, into bytecode for EvalBytecode or
// LoadModuleBytecode, without running it.
func (r *Runtime) Compile(name, src string, opts ...EvalOption) ([]byte, error) {
	return r.compile(evalInput{name: name, source: &src}, newEvalConfig(opts))
}

// CompileFile is like Compile for the file at path, relative to Config.CWD.
func (r *Runtime) CompileFile(path string, opts ...EvalOption) ([]byte, error) {
	return r.compile(evalInput{name: path}, newEvalConfig(opts))
}

// LoadModule compiles the module src and registers it as `name`, so that
// later code can import it, without running it.
func (r *Runtime) LoadModule(ctx context.Context, name, src string) error {
	return r.load(ctx, evalInput{name: name, source: &src})
}

// LoadModuleFile is like LoadModule for the file at path, relative to
// Config.CWD, registered under that path.
func (r *Runtime) LoadModuleFile(ctx context.Context, path string) error {
	return r.load(ctx, evalInput{name: path})
}

// LoadModuleBytecode is like LoadModule for module bytecode from Compile
// with AsModule. The module keeps the name it was compiled with.
func (r *Runtime) LoadModuleBytecode(ctx context.Context, bytecode []byte) error {
	if len(bytecode) == 0 {
		return errors.New("bytecode is empty")
	}

	return r.load(ctx, evalInput{name: "<bytecode>", bytecode: bytecode})
}

func (r *Runtime) eval(ctx context.Context, in evalInput, config evalConfig) (result *Value, err error) {
	err = r.withEvalOptions(ctx, in, config.flags, func(opts uint64) (err error) {
		result, err = r.result(r.call("QJS_Eval", r.jsCtx, opts))

		return err
	})

	return result, err
}

func (r *Runtime) load(ctx context.Context, in evalInput) error {
	return r.withEvalOptions(ctx, in, jsEvalTypeModule|jsEvalFlagStrict, func(opts uint64) error {
		// The module value stays referenced by the context's module list;
		// it must not be freed as a value.
		if raw := r.call("QJS_Load", r.jsCtx, opts); tagOf(raw) == tagException {
			return r.takeException()
		}

		return nil
	})
}

func (r *Runtime) compile(in evalInput, config evalConfig) (bytecode []byte, err error) {
	err = r.withEvalOptions(context.Background(), in, config.flags, func(opts uint64) error {
		box := r.call("QJS_Compile2", r.jsCtx, opts)
		if box == 0 {
			return r.takeException()
		}

		bytecode = r.takeBytes(box)

		return nil
	})

	return bytecode, err
}

// withEvalOptions runs fn, a call governed by ctx, with the QJSEvalOptions
// for in.
func (r *Runtime) withEvalOptions(
	ctx context.Context, in evalInput, flags uint64, fn func(opts uint64) error,
) (err error) {
	if in.name == "" {
		return ErrInvalidFileName
	}

	if err := r.enter(ctx); err != nil {
		return err
	}
	defer r.exit()
	defer recoverInterrupt(r, &err)

	opts, free := r.newEvalOptions(in, flags)
	defer free()

	return fn(opts)
}

// newEvalOptions builds a QJSEvalOptions struct in wasm memory. free
// releases it and the buffers it points to.
func (r *Runtime) newEvalOptions(in evalInput, flags uint64) (opts uint64, free func()) {
	var buffers []uint64

	alloc := func(ptr uint64) uint64 {
		buffers = append(buffers, ptr)

		return ptr
	}

	var source, bytecode uint64
	if in.source != nil {
		source = alloc(r.newCString(*in.source))
	}

	if in.bytecode != nil {
		bytecode = alloc(r.newBuffer(in.bytecode))
	}

	file := alloc(r.newCString(in.name))
	opts = alloc(r.call("QJS_CreateEvalOption", source, bytecode, uint64(len(in.bytecode)), file, flags))

	return opts, func() {
		for _, ptr := range buffers {
			r.free(ptr)
		}
	}
}

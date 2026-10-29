package qngg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
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

// EvalFile is like Eval for the file at path, relative to Config.CWD. Path
// separators are converted to "/", the separator module names use.
func (r *Runtime) EvalFile(ctx context.Context, path string, opts ...EvalOption) (*Value, error) {
	return r.eval(ctx, evalInput{name: filepath.ToSlash(path)}, newEvalConfig(opts))
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
// LoadModuleBytecode, without running it. Since no JavaScript runs, it takes
// no context and cannot be interrupted.
func (r *Runtime) Compile(name, src string, opts ...EvalOption) ([]byte, error) {
	return r.compile(evalInput{name: name, source: &src}, newEvalConfig(opts))
}

// CompileFile is like Compile for the file at path, relative to Config.CWD.
func (r *Runtime) CompileFile(path string, opts ...EvalOption) ([]byte, error) {
	return r.compile(evalInput{name: filepath.ToSlash(path)}, newEvalConfig(opts))
}

// LoadModule compiles the module src and registers it as `name`, so that
// later code can import it, without running it.
func (r *Runtime) LoadModule(ctx context.Context, name, src string) error {
	return r.load(ctx, evalInput{name: name, source: &src})
}

// LoadModuleFile is like LoadModule for the file at path, relative to
// Config.CWD, registered under that path.
func (r *Runtime) LoadModuleFile(ctx context.Context, path string) error {
	return r.load(ctx, evalInput{name: filepath.ToSlash(path)})
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
		result, err = r.result(r.call(xEval, r.jsCtx, opts))

		return err
	})

	return result, err
}

func (r *Runtime) load(ctx context.Context, in evalInput) error {
	return r.withEvalOptions(ctx, in, jsEvalTypeModule|jsEvalFlagStrict, func(opts uint64) error {
		// The module value stays referenced by the context's module list;
		// it must not be freed as a value.
		if raw := r.call(xLoad, r.jsCtx, opts); tagOf(raw) == tagException {
			return r.takeException()
		}

		return nil
	})
}

func (r *Runtime) compile(in evalInput, config evalConfig) (bytecode []byte, err error) {
	err = r.withEvalOptions(context.Background(), in, config.flags, func(opts uint64) error {
		res := r.call(xCompile, r.jsCtx, opts)
		if res == outFailed {
			return r.takeException()
		}

		ptr, size := uint32(res>>32), uint32(res)
		bytecode = bytes.Clone(r.view(ptr, size))
		r.call(xJSFree, r.jsCtx, uint64(ptr))

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

	var source, bytecode tmp

	if in.source != nil {
		source = r.pushCString(*in.source)
		defer r.drop(source)
	}

	if in.bytecode != nil {
		bytecode = r.push(in.bytecode)
		defer r.drop(bytecode)
	}

	file := r.pushCString(in.name)
	defer r.drop(file)

	// QJSEvalOptions on wasm32: buf, bytecode_buf, bytecode_len, filename
	// and eval_flags, each 4 bytes.
	opts := r.alloc(5 * 4)
	defer r.drop(opts)

	view := r.view(opts.ptr, 5*4)
	for i, field := range [5]uint32{source.ptr, bytecode.ptr, uint32(len(in.bytecode)), file.ptr, uint32(flags)} {
		binary.LittleEndian.PutUint32(view[4*i:], field)
	}

	return fn(uint64(opts.ptr))
}

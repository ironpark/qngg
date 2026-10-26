package qngg

import (
	"context"
	"io"
	"time"
)

const (
	// jsEvalTypeGlobal evaluates code in global scope (default).
	jsEvalTypeGlobal = (0 << 0)
	// jsEvalTypeModule evaluates code as ES6 module.
	jsEvalTypeModule = (1 << 0)
	// jsEvalFlagStrict forces strict mode execution.
	jsEvalFlagStrict = (1 << 3)
	// jsEvalFlagCompileOnly returns a JS bytecode/module for JS_EvalFunction().
	jsEvalFlagCompileOnly = (1 << 5)
	// jsEvalFlagBackTraceBarrier prevents the stack frames before this eval in the Error() backtraces.
	jsEvalFlagBackTraceBarrier = (1 << 6)
	// jsEvalFlagAsync enables top-level await (global scope only).
	jsEvalFlagAsync = (1 << 7)
)

type Option struct {
	// CWD is a host directory exposed to JavaScript as "/", used for module
	// loading and the qjs:std/qjs:os file APIs. Empty means JavaScript has no
	// filesystem access at all. The directory is read-only unless WritableCWD
	// is set.
	CWD string
	// WritableCWD lets JavaScript create, modify and delete files under CWD.
	WritableCWD bool
	Context     context.Context
	// CloseOnContextDone interrupts running JavaScript when Context is done.
	// Like MaxExecutionTime, it compiles the module with interruption checks,
	// which makes compute-heavy JavaScript about 30% slower.
	CloseOnContextDone bool
	DisableBuildCache  bool
	CacheDir           string
	MemoryLimit        int
	MaxStackSize       int
	// MaxExecutionTime limits how long a single call into JavaScript (such as
	// Eval) may run, including Go functions it calls. When exceeded, the call
	// fails with ErrExecutionTimeout and the runtime must be closed. Zero means
	// no limit. See CloseOnContextDone for the cost of interruption checks.
	MaxExecutionTime time.Duration
	GCThreshold      int
	// Stdout and Stderr receive console.log/console.error and std.out/std.err
	// output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer

	// quickJSWasm replaces the embedded QuickJS build, for tests.
	quickJSWasm []byte
}

// evalConfig configures JavaScript evaluation behavior in QuickJS context.
type evalConfig struct {
	c           *Runtime
	file        string
	code        string
	bytecode    []byte
	bytecodeLen int
	flags       uint64

	// QuickJS value handles for memory management
	fileValue     *Value
	codeValue     *Value
	byteCodeValue *Value
}

// EvalOption configures evaluation behavior using functional option pattern.
type EvalOption func(*evalConfig)

// createEvalOption initializes default option with global scope and strict mode.
func createEvalOption(c *Runtime, file string, flags ...EvalOption) *evalConfig {
	evalOption := &evalConfig{
		c:     c,
		file:  file,
		flags: jsEvalTypeGlobal | jsEvalFlagStrict,
	}

	for _, flag := range flags {
		flag(evalOption)
	}

	return evalOption
}

// Code sets the JavaScript source code to evaluate.
func Code(code string) EvalOption {
	return func(o *evalConfig) {
		o.code = code
	}
}

// Bytecode sets precompiled JavaScript bytecode to execute.
func Bytecode(buf []byte) EvalOption {
	return func(o *evalConfig) {
		o.bytecode = buf
		o.bytecodeLen = len(buf)
	}
}

// TypeGlobal sets evaluation to run in global scope (default behavior).
func TypeGlobal() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalTypeGlobal
	}
}

// TypeModule sets evaluation to run as ES6 module.
func TypeModule() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalTypeModule
	}
}

// FlagAsync enables top-level await in global scripts.
// Returns a promise from JS_Eval(). Only valid with TypeGlobal.
func FlagAsync() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalFlagAsync
	}
}

// FlagStrict forces strict mode execution.
func FlagStrict() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalFlagStrict
	}
}

// FlagCompileOnly compiles code without execution.
// Returns bytecode object for later execution with JS_EvalFunction().
func FlagCompileOnly() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalFlagCompileOnly
	}
}

// FlagBacktraceBarrier excludes stack frames before this eval from error backtraces.
func FlagBacktraceBarrier() EvalOption {
	return func(o *evalConfig) {
		o.flags |= jsEvalFlagBackTraceBarrier
	}
}

// Handle creates QuickJS evaluation option handle for WASM function calls.
func (o *evalConfig) Handle() (handle uint64) {
	codeHandle := uint64(0)
	byteCodeHandle := uint64(0)
	o.fileValue = o.c.newStringHandle(o.file)

	if o.code != "" {
		o.codeValue = o.c.newStringHandle(o.code)
		codeHandle = o.codeValue.raw()
	}

	if o.bytecode != nil {
		o.byteCodeValue = o.c.newBytes(o.bytecode)
		byteCodeHandle = o.byteCodeValue.raw()
	}

	// Create QuickJS option struct via WASM call
	option := o.c.jsCall(
		"QJS_CreateEvalOption",
		codeHandle,
		byteCodeHandle,
		uint64(o.bytecodeLen),
		o.fileValue.raw(),
		o.flags,
	)

	return option.raw()
}

// Free releases QuickJS value handles to prevent memory leaks.
// Must be called after Handle() to clean up WASM memory.
func (o *evalConfig) Free() {
	// An interrupted runtime cannot run wasm; its memory goes with the store.
	if o.c.interruptErr() != nil {
		return
	}

	if o.fileValue.raw() != 0 {
		o.c.jsCall("JS_FreeValue", o.c.ctxPtr(), o.fileValue.raw())
	}

	if o.codeValue != nil && o.codeValue.raw() != 0 {
		o.c.jsCall("JS_FreeValue", o.c.ctxPtr(), o.codeValue.raw())
	}

	if o.byteCodeValue != nil && o.byteCodeValue.raw() != 0 {
		o.c.jsCall("JS_FreeValue", o.c.ctxPtr(), o.byteCodeValue.raw())
	}
}

func getRuntimeOption(options ...Option) (option Option) {
	if len(options) == 0 {
		option = Option{}
	} else {
		option = options[0]
	}

	if option.Context == nil {
		option.Context = context.Background()
	}

	return option
}

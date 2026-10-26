package qngg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ironpark/qngg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalOptions(t *testing.T) {
	t.Run("DefaultOptions", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		// Use Eval to test the default options (should be global with strict mode)
		val, err := runtime.Eval("test.js", qngg.Code("'use strict'; 'test'"))
		assert.NoError(t, err)
		assert.Equal(t, "test", val.String())
		val.Free()
	})

	t.Run("CWDOption", func(t *testing.T) {
		// Store original working directory to restore later
		originalCwd, err := os.Getwd()
		require.NoError(t, err)

		t.Cleanup(func() {
			_ = os.Chdir(originalCwd)
		})

		t.Run("default_cwd_from_os_getwd", func(t *testing.T) {
			runtime, err := qngg.New()
			require.NoError(t, err)
			runtime.Close()
		})

		t.Run("explicit_cwd_provided", func(t *testing.T) {
			tempDir := t.TempDir()
			runtime, err := qngg.New(qngg.Option{CWD: tempDir})
			require.NoError(t, err)
			runtime.Close()
		})

		t.Run("deleted_working_directory", func(t *testing.T) {
			// Create a temporary directory and change to it
			tempDir := t.TempDir()
			subDir := filepath.Join(tempDir, "workdir")
			err := os.Mkdir(subDir, 0755)
			require.NoError(t, err, "Failed to create subdirectory")

			// Change to the subdirectory
			err = os.Chdir(subDir)
			require.NoError(t, err)

			// Remove the current working directory while we're in it
			err = os.RemoveAll(subDir)
			require.NoError(t, err)

			_, err = qngg.New()
			_ = os.Chdir(originalCwd)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "failed to get runtime options")
		})
	})

	t.Run("CodeOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js", qngg.Code("42"))
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("TypeModuleOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			qngg.Code("export default 'module'"),
			qngg.TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, "module", val.String())
		val.Free()
	})

	t.Run("BytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		bytecode, err := runtime.Compile("test.js", qngg.Code("123 + 456"))
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js", qngg.Bytecode(bytecode))
		assert.NoError(t, err)
		assert.Equal(t, int32(579), val.Int32())
		val.Free()
	})

	t.Run("ModuleBytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		bytecode, err := runtime.Compile(
			"test.js",
			qngg.Code("export default 'module bytecode'"),
			qngg.TypeModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js",
			qngg.Bytecode(bytecode),
			qngg.TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, "module bytecode", val.String())
		val.Free()
	})

	t.Run("FlagAsyncOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		// Test async flag with top-level await
		val, err := runtime.Eval("test.js",
			qngg.Code("await Promise.resolve(100)"),
			qngg.FlagAsync())
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("FlagStrictOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		// In strict mode, using undeclared variables throws an error
		_, err := runtime.Eval("test.js",
			qngg.Code("undeclaredVar = 10"),
			qngg.FlagStrict())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ReferenceError")
	})

	t.Run("FlagCompileOnlyOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()
		ctx := runtime.Context()

		// We need to use Go API directly because runtime.Compile
		// already applies FlagCompileOnly
		val, err := ctx.Eval("test.js",
			qngg.Code("42"),
			qngg.FlagCompileOnly())
		assert.NoError(t, err)

		// The result should be bytecode, not the executed result
		assert.False(t, val.IsNumber())
		val.Free()
	})

	t.Run("FlagBacktraceBarrierOption", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		_, err := runtime.Eval("outer.js", qngg.Code(`
            function outer() {
                throw new Error("test error");
            }
        `))
		assert.NoError(t, err)

		_, err = runtime.Eval("without-barrier.js", qngg.Code(`outer()`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "outer.js")

		_, err = runtime.Eval("with-barrier.js",
			qngg.Code(`outer()`),
			qngg.FlagBacktraceBarrier())
		assert.Error(t, err)
	})

	t.Run("MultipleOptions", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			qngg.Code("export default await Promise.resolve('async module')"),
			qngg.TypeModule(),
			qngg.FlagStrict())
		assert.NoError(t, err)
		assert.Equal(t, "async module", val.String())
		val.Free()
	})

	t.Run("ExplicitTypeGlobal", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			qngg.Code("var x = 100; x;"),
			qngg.TypeGlobal())
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("BytecodeWithModuleAndAsync", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		bytecode, err := runtime.Compile(
			"test.js",
			qngg.Code("export default await Promise.resolve(42)"),
			qngg.TypeModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js",
			qngg.Bytecode(bytecode),
			qngg.TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("ModuleWithStrictMode", func(t *testing.T) {
		runtime := must(qngg.New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			qngg.Code("export default 'strict module'"),
			qngg.TypeModule(),
			qngg.FlagStrict())
		assert.NoError(t, err)
		assert.Equal(t, "strict module", val.String())
		val.Free()

		_, err = runtime.Eval("test.js",
			qngg.Code("export default (function() { with({}) { return 'test'; } })()"),
			qngg.TypeModule(),
			qngg.FlagStrict())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SyntaxError")
	})
}

func TestInternalOptions(t *testing.T) {
	runtime := must(qngg.New())
	defer runtime.Close()

	_, err := runtime.Compile("test.js",
		qngg.Code("42"),
		qngg.TypeDirect())
	assert.NoError(t, err)

	_, err = runtime.Compile("test.js",
		qngg.Code("42"),
		qngg.TypeIndirect())
	assert.NoError(t, err)

	_, err = runtime.Compile("test.js",
		qngg.Code("42"),
		qngg.TypeMask())
	assert.NoError(t, err)

	_, err = runtime.Compile("test.js",
		qngg.Code("42"),
		qngg.FlagUnused())
	assert.NoError(t, err)
}

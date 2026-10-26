package qngg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalOptions(t *testing.T) {
	t.Run("DefaultOptions", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		// Use Eval to test the default options (should be global with strict mode)
		val, err := runtime.Eval("test.js", Code("'use strict'; 'test'"))
		assert.NoError(t, err)
		assert.Equal(t, "test", val.String())
		val.Free()
	})

	t.Run("CWDOption", func(t *testing.T) {
		readFile := `(() => {
			const f = std.open("/hello.txt", "r");
			if (!f) return "no access";
			const s = f.readAsString(); f.close(); return s;
		})()`
		writeFile := `(() => {
			const f = std.open("/new.txt", "w");
			if (!f) return "no access";
			f.puts("written"); f.close(); return "ok";
		})()`

		eval := func(t *testing.T, option Option, code string) string {
			t.Helper()
			rt := must(New(option))
			defer rt.Close()
			val, err := rt.Eval("test.js", Code(code))
			require.NoError(t, err)
			defer val.Free()
			return val.String()
		}

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644))

		t.Run("no_filesystem_by_default", func(t *testing.T) {
			assert.Equal(t, "no access", eval(t, Option{}, readFile))
		})

		t.Run("read_only_by_default", func(t *testing.T) {
			assert.Equal(t, "hello", eval(t, Option{CWD: dir}, readFile))
			assert.Equal(t, "no access", eval(t, Option{CWD: dir}, writeFile))
			assert.NoFileExists(t, filepath.Join(dir, "new.txt"))
		})

		t.Run("writable", func(t *testing.T) {
			assert.Equal(t, "ok", eval(t, Option{CWD: dir, WritableCWD: true}, writeFile))
			assert.FileExists(t, filepath.Join(dir, "new.txt"))
		})

		t.Run("missing_directory", func(t *testing.T) {
			_, err := New(Option{CWD: filepath.Join(dir, "missing")})
			assert.Error(t, err)
		})
	})

	t.Run("StdoutOption", func(t *testing.T) {
		var out bytes.Buffer
		rt := must(New(Option{Stdout: &out}))
		defer rt.Close()
		val := must(rt.Eval("test.js", Code(`console.log("hi")`)))
		val.Free()
		assert.Equal(t, "hi\n", out.String())

		// Without a writer, output is discarded rather than inherited.
		quiet := must(New())
		defer quiet.Close()
		val = must(quiet.Eval("test.js", Code(`console.log("hi"); 1`)))
		val.Free()
	})

	t.Run("CodeOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js", Code("42"))
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("TypeModuleOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			Code("export default 'module'"),
			TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, "module", val.String())
		val.Free()
	})

	t.Run("BytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		bytecode, err := runtime.Compile("test.js", Code("123 + 456"))
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js", Bytecode(bytecode))
		assert.NoError(t, err)
		assert.Equal(t, int32(579), val.Int32())
		val.Free()
	})

	t.Run("ModuleBytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		bytecode, err := runtime.Compile(
			"test.js",
			Code("export default 'module bytecode'"),
			TypeModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js",
			Bytecode(bytecode),
			TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, "module bytecode", val.String())
		val.Free()
	})

	t.Run("FlagAsyncOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		// Test async flag with top-level await
		val, err := runtime.Eval("test.js",
			Code("await Promise.resolve(100)"),
			FlagAsync())
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("FlagStrictOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		// In strict mode, using undeclared variables throws an error
		_, err := runtime.Eval("test.js",
			Code("undeclaredVar = 10"),
			FlagStrict())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ReferenceError")
	})

	t.Run("FlagCompileOnlyOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()
		ctx := runtime

		// We need to use Go API directly because runtime.Compile
		// already applies FlagCompileOnly
		val, err := ctx.Eval("test.js",
			Code("42"),
			FlagCompileOnly())
		assert.NoError(t, err)

		// The result should be bytecode, not the executed result
		assert.False(t, val.IsNumber())
		val.Free()
	})

	t.Run("FlagBacktraceBarrierOption", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		_, err := runtime.Eval("outer.js", Code(`
            function outer() {
                throw new Error("test error");
            }
        `))
		assert.NoError(t, err)

		_, err = runtime.Eval("without-barrier.js", Code(`outer()`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "outer.js")

		_, err = runtime.Eval("with-barrier.js",
			Code(`outer()`),
			FlagBacktraceBarrier())
		assert.Error(t, err)
	})

	t.Run("MultipleOptions", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			Code("export default await Promise.resolve('async module')"),
			TypeModule(),
			FlagStrict())
		assert.NoError(t, err)
		assert.Equal(t, "async module", val.String())
		val.Free()
	})

	t.Run("ExplicitTypeGlobal", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			Code("var x = 100; x;"),
			TypeGlobal())
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("BytecodeWithModuleAndAsync", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		bytecode, err := runtime.Compile(
			"test.js",
			Code("export default await Promise.resolve(42)"),
			TypeModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.Eval("test.js",
			Bytecode(bytecode),
			TypeModule())
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("ModuleWithStrictMode", func(t *testing.T) {
		runtime := must(New())
		defer runtime.Close()

		val, err := runtime.Eval("test.js",
			Code("export default 'strict module'"),
			TypeModule(),
			FlagStrict())
		assert.NoError(t, err)
		assert.Equal(t, "strict module", val.String())
		val.Free()

		_, err = runtime.Eval("test.js",
			Code("export default (function() { with({}) { return 'test'; } })()"),
			TypeModule(),
			FlagStrict())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SyntaxError")
	})
}

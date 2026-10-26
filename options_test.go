package qngg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalOptions(t *testing.T) {
	t.Run("DefaultOptions", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		// Use Eval to test the default options (should be global with strict mode)
		val, err := runtime.Eval(context.Background(), "test.js", "'use strict'; 'test'")
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

		eval := func(t *testing.T, option Config, code string) string {
			t.Helper()
			rt := must(New(option))
			defer rt.Close()
			val, err := rt.Eval(context.Background(), "test.js", code)
			require.NoError(t, err)
			defer val.Free()
			return val.String()
		}

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644))

		t.Run("no_filesystem_by_default", func(t *testing.T) {
			assert.Equal(t, "no access", eval(t, Config{}, readFile))
		})

		t.Run("read_only_by_default", func(t *testing.T) {
			assert.Equal(t, "hello", eval(t, Config{CWD: dir}, readFile))
			assert.Equal(t, "no access", eval(t, Config{CWD: dir}, writeFile))
			assert.NoFileExists(t, filepath.Join(dir, "new.txt"))
		})

		t.Run("writable", func(t *testing.T) {
			assert.Equal(t, "ok", eval(t, Config{CWD: dir, WritableCWD: true}, writeFile))
			assert.FileExists(t, filepath.Join(dir, "new.txt"))
		})

		t.Run("missing_directory", func(t *testing.T) {
			_, err := New(Config{CWD: filepath.Join(dir, "missing")})
			assert.Error(t, err)
		})
	})

	t.Run("StdoutOption", func(t *testing.T) {
		var out bytes.Buffer
		rt := must(New(Config{Stdout: &out}))
		defer rt.Close()
		val := must(rt.Eval(context.Background(), "test.js", `console.log("hi")`))
		val.Free()
		assert.Equal(t, "hi\n", out.String())

		// Without a writer, output is discarded rather than inherited.
		quiet := must(New(Config{}))
		defer quiet.Close()
		val = must(quiet.Eval(context.Background(), "test.js", `console.log("hi"); 1`))
		val.Free()
	})

	t.Run("CodeOption", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		val, err := runtime.Eval(context.Background(), "test.js", "42")
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("TypeModuleOption", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		val, err := runtime.Eval(context.Background(), "test.js", "export default 'module'", AsModule())
		assert.NoError(t, err)
		assert.Equal(t, "module", val.String())
		val.Free()
	})

	t.Run("BytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		bytecode, err := runtime.Compile("test.js", "123 + 456")
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.EvalBytecode(context.Background(), bytecode)
		assert.NoError(t, err)
		assert.Equal(t, int32(579), val.Int32())
		val.Free()
	})

	t.Run("ModuleBytecodeCompileAndEval", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		bytecode, err := runtime.Compile("test.js", "export default 'module bytecode'", AsModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.EvalBytecode(context.Background(), bytecode, AsModule())
		assert.NoError(t, err)
		assert.Equal(t, "module bytecode", val.String())
		val.Free()
	})

	t.Run("FlagAsyncOption", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		// Test async flag with top-level await
		val, err := runtime.Eval(context.Background(), "test.js", "await Promise.resolve(100)", TopLevelAwait())
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("FlagStrictOption", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		// In strict mode, using undeclared variables throws an error
		_, err := runtime.Eval(context.Background(), "test.js", "undeclaredVar = 10")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ReferenceError")
	})

	t.Run("FlagBacktraceBarrierOption", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		_, err := runtime.Eval(context.Background(), "outer.js", `
            function outer() {
                throw new Error("test error");
            }
        `)
		assert.NoError(t, err)

		_, err = runtime.Eval(context.Background(), "without-barrier.js", `outer()`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "outer.js")

		_, err = runtime.Eval(context.Background(), "with-barrier.js", `outer()`)
		assert.Error(t, err)
	})

	t.Run("MultipleOptions", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		val, err := runtime.Eval(context.Background(), "test.js", "export default await Promise.resolve('async module')", AsModule())
		assert.NoError(t, err)
		assert.Equal(t, "async module", val.String())
		val.Free()
	})

	t.Run("ExplicitTypeGlobal", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		val, err := runtime.Eval(context.Background(), "test.js", "var x = 100; x;")
		assert.NoError(t, err)
		assert.Equal(t, int32(100), val.Int32())
		val.Free()
	})

	t.Run("BytecodeWithModuleAndAsync", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		bytecode, err := runtime.Compile("test.js", "export default await Promise.resolve(42)", AsModule())
		assert.NoError(t, err)
		assert.NotEmpty(t, bytecode)

		val, err := runtime.EvalBytecode(context.Background(), bytecode, AsModule())
		assert.NoError(t, err)
		assert.Equal(t, int32(42), val.Int32())
		val.Free()
	})

	t.Run("ModuleWithStrictMode", func(t *testing.T) {
		runtime := must(New(Config{}))
		defer runtime.Close()

		val, err := runtime.Eval(context.Background(), "test.js", "export default 'strict module'", AsModule())
		assert.NoError(t, err)
		assert.Equal(t, "strict module", val.String())
		val.Free()

		_, err = runtime.Eval(context.Background(), "test.js", "export default (function() { with({}) { return 'test'; } })()", AsModule())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SyntaxError")
	})
}

# QNGG - JavaScript in Go with QuickJS-NG and Wasmtime

<p align="center">
  <a href="https://pkg.go.dev/github.com/ironpark/qngg#section-readme" target="_blank" rel="noopener">
    <img src="https://img.shields.io/badge/go.dev-reference-blue?logo=go&logoColor=white" alt="Go.Dev reference" />
  </a>
  <a href="https://goreportcard.com/report/github.com/ironpark/qngg" target="_blank" rel="noopener">
    <img src="https://goreportcard.com/badge/github.com/ironpark/qngg" alt="go report card" />
  </a>
  <a href="https://github.com/ironpark/qngg/actions" target="_blank" rel="noopener">
    <img src="https://github.com/ironpark/qngg/actions/workflows/ci.yml/badge.svg" alt="test status" />
  </a>
  <a href="https://opensource.org/licenses/MIT" target="_blank" rel="noopener">
    <img src="https://img.shields.io/badge/license-MIT-brightgreen.svg" alt="MIT license" />
  </a>
</p>

QNGG is a CGO-Free, modern, secure JavaScript runtime for Go applications, built on the [QuickJS-NG](https://github.com/quickjs-ng/quickjs) engine and the [Wasmtime](https://wasmtime.dev/) WebAssembly runtime.

QNGG allows you to run JavaScript code safely and efficiently, with full support for ES2023 features, async/await, and Go-JS interoperability.

## About this fork

QNGG is a maintained fork of [fastschema/qjs](https://github.com/fastschema/qjs), which is no longer maintained. Compared to upstream it:

- Runs QuickJS on **Wasmtime** instead of Wazero. Wasmtime's C API is called through [purego](https://github.com/ebitengine/purego) (a vendored port of [wasmtime-go](https://github.com/bytecodealliance/wasmtime-go)), so builds still need no cgo.
- Tracks the latest **QuickJS-NG** release (currently v0.17.0), built with wasi-sdk 34.
- Requires **Go 1.27** and decodes JSON with `encoding/json/v2`.
- Is **sandboxed by default**: no filesystem, no output and interruptible calls unless configured otherwise.
- Has a smaller, redesigned API: one ownership rule for values, `context.Context` on every call that runs JavaScript, errors instead of silent failures and panics, and `Value.As[T]()` for conversions.
- Fixes memory bugs inherited from upstream, such as leaked strings, property names and eval buffers, use-after-free in `JSONStringify`, a double free in `Await`, heap corruption when converting objects, and Go functions and ProxyValues that were never released.
- Crosses the Go/WebAssembly boundary less often: Go functions are native QuickJS closures, type checks and property access take one call each, temporary buffers come from a per-runtime scratch area instead of malloc, and struct layouts are cached per type.

| `go test -bench .` (Apple M1 Ultra) | QJS on Wazero | QNGG |
| --- | --- | --- |
| RuntimeNew | 17 ms | 0.40 ms |
| EvalFactorial | 97 ms | 52 ms (39 ms with `DisableInterruption`) |
| GoFunctionCall | 168 ms | 4.8 ms |
| ValueProperties | 503 µs | 88 µs |

Coming from fastschema/qjs? See the [migration guide](docs/migration.md).

## Features

- **JavaScript ES2023** via QuickJS-NG, including modules, async/await and top-level await.
- **Sandboxed**: QuickJS runs in its own WebAssembly instance and only gets the capabilities you grant.
- **Interruptible**: cancel a call's context or give it a deadline to stop runaway scripts, even `for (;;) {}`.
- **Go-JS interoperability**: convert structs, maps, slices, functions and channels both ways, or share Go values by reference with ProxyValues.
- **No cgo**: Wasmtime is loaded at runtime through [purego](https://github.com/ebitengine/purego), so builds work with `CGO_ENABLED=0` and cross-compile without a C toolchain.
- **Fast startup**: a new runtime takes well under a millisecond, so one per request or per tenant is affordable.

QNGG fits plugin systems, user-defined rules and formulas, templating, and
running code you do not trust.

## Installation

```bash
go get github.com/ironpark/qngg
```

QNGG requires Go 1.27 or later. The Wasmtime native library is embedded, so
there is nothing else to install (see [Wasmtime native library](#wasmtime-native-library)).

## Example Usage

The snippets below assume a runtime `rt` and a context `ctx`, and return
errors from the enclosing function.

### Basic Execution

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ironpark/qngg"
)

func main() {
	rt, err := qngg.New(qngg.Config{})
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	result, err := rt.Eval(context.Background(), "main.js", `
		const person = { name: "Alice", age: 30, city: "New York" };
		Object.entries(person).map(([k, v]) => k + ": " + v).join(", ");
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer result.Free()

	fmt.Println(result.String()) // name: Alice, age: 30, city: New York
}
```

### Values and Memory

JavaScript values live in the QuickJS heap, not Go's, so the Go garbage
collector cannot release them. One rule covers every method:

- Every `*Value` returned to you is yours: `Free` it when done.
- Passing a value to a method never takes it over: you still free it.
- Values you do not own have a no-op `Free`: `Runtime.Global()`, and the
  arguments and `this` of a `Function`, which are only valid until it returns.
  `Clone` them to keep them longer.
- A value returned from a `Function` is handed to JavaScript.
- `Close` frees everything that is left, and makes further use of the runtime
  and its values fail with `ErrRuntimeClosed`. Using a freed value fails with
  `ErrFreed`.

```go
user := rt.NewObject()
defer user.Free()

name := rt.NewString("Alice")
defer name.Free()

if err := user.Set("name", name); err != nil { // name is still yours
	return err
}

tags, err := rt.Eval(ctx, "tags.js", `["admin", "editor"]`)
if err != nil {
	return err
}
defer tags.Free()

if err := user.Set("tags", tags); err != nil {
	return err
}

if err := user.Set("age", 30); err != nil { // Go values are converted
	return err
}

keys, err := user.Keys()       // [name tags age]
out, err := json.Marshal(user) // {"name":"Alice","tags":["admin","editor"],"age":30}
```

`Value.Type()` names a value's type (`"String"`, `"Array"`, `"Map"`,
`"Object"`, ...), and the `Is*` methods test for one. `String()`, `Int32()`,
`Float64()` and friends convert like JavaScript does; use `As` for strict
conversions that report errors.

### Go Functions

`Set` converts Go values, functions included, and assigns them to globals.

```go
if err := rt.Set("add", func(a, b int) int { return a + b }); err != nil {
	return err
}

err := rt.Set("lookup", func(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", errors.New("id is required")
	}
	return "user:" + id, nil
})
if err != nil {
	return err
}

result, err := rt.Eval(ctx, "main.js", `lookup(String(add(40, 2)))`)
if err != nil {
	return err
}
defer result.Free()
// result.String() == "user:42"
```

How calls are converted:

- Arguments are converted to the parameter types with the same rules as
  `As`. Missing arguments are zero values, extra ones are ignored, and
  variadic functions take the rest. An argument that does not convert, such
  as `"x"` for an `int`, throws an error into JavaScript.
- A leading `context.Context` parameter receives the context of the call
  that is running the script.
- A last `error` result is thrown into JavaScript when it is not nil. One
  other result is returned as is, and several are returned as an array.

For direct access to JavaScript values, register a `Function` instead. Its
arguments belong to the caller; return an `*Error` to throw a specific error
type.

```go
err := rt.SetFunc("describe", func(call *qngg.FunctionCall) (*qngg.Value, error) {
	if len(call.Args()) == 0 {
		return nil, &qngg.Error{Name: "TypeError", Message: "describe needs an argument"}
	}
	return call.Runtime().NewString(call.Arg(0).Type()), nil
})
// describe([1, 2]) === "Array"; describe() throws a TypeError
```

### Errors

A JavaScript exception is returned as an `*Error` with its name, message and
stack. Evaluating to an `Error` object without throwing it is not an error.

```go
_, err := rt.Eval(ctx, "main.js", `JSON.parse("{")`)

var jsErr *qngg.Error
if errors.As(err, &jsErr) {
	fmt.Println(jsErr.Name)    // SyntaxError
	fmt.Println(jsErr.Message) // Expected property name or '}' in JSON at position 1 ...
	fmt.Println(jsErr.Stack)   // the JavaScript stack trace, ending "at <eval> (main.js:1:5)"
}
```

Other errors are sentinels to test with `errors.Is`: `ErrInterrupted` when a
call's context was done, `ErrRuntimeClosed` and `ErrFreed` when a runtime or
value is used after `Close` or `Free`. Methods without an error result, such
as `Int32()`, panic with these instead, except `String()`, which returns
`<invalid qngg.Value>`.

### Timeouts and Cancellation

Every call that runs JavaScript takes a context. When it is canceled or its
deadline passes, the script is interrupted wherever it is, and the call fails
with `ErrInterrupted`. The runtime's state is then undefined, so every later
call fails too and the runtime must be closed; a `Pool` discards it for you.

```go
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

_, err := rt.Eval(ctx, "untrusted.js", `for (;;) {}`)
fmt.Println(errors.Is(err, qngg.ErrInterrupted))   // true
fmt.Println(errors.Is(err, context.DeadlineExceeded)) // true
```

Interruption checks make compute-heavy JavaScript about 30% slower. Set
`Config.DisableInterruption` if you trust the code and need the speed;
contexts are then only checked before a call starts.

### Async Operations

`Eval` awaits the promise a script returns and runs pending jobs and timers.
Go functions registered with `SetAsyncFunc` look like async functions to
JavaScript: they return a promise that settles with the Go result or error.

```go
rt.SetAsyncFunc("fetchUser", func(call *qngg.FunctionCall) (*qngg.Value, error) {
	return call.Runtime().ToValue(map[string]any{"id": call.Arg(0).Int32(), "name": "Alice"})
})

result, err := rt.Eval(ctx, "main.js", `
	const user = await fetchUser(1);
	await new Promise(resolve => setTimeout(resolve, 10));
	user.name;
`, qngg.TopLevelAwait())
// result.String() == "Alice"
```

`Value.Await` waits for a promise you got from a call:

```go
fn, _ := rt.Eval(ctx, "main.js", `async (x) => x * 2`)
defer fn.Free()

promise, _ := fn.Call(ctx, nil, 21)
defer promise.Free()

doubled, err := promise.Await(ctx)
// doubled.Int32() == 42
```

### Calling JavaScript from Go

```go
calc, err := rt.Eval(ctx, "calc.js", `({
	add: (a, b) => a + b,
	fail: () => { throw new RangeError("out of range") },
})`)
if err != nil {
	return err
}
defer calc.Free()

sum, err := calc.Invoke(ctx, "add", 1, 2) // arguments are converted
if err != nil {
	return err
}
defer sum.Free()
fmt.Println(sum.Int32()) // 3

_, err = calc.Invoke(ctx, "fail")
var jsErr *qngg.Error
if errors.As(err, &jsErr) {
	fmt.Println(jsErr.Name, jsErr.Message) // RangeError out of range
}

// Or convert a JS function into a typed Go function.
addFn, _ := calc.Get("add")
defer addFn.Free()
add, err := addFn.As[func(int, int) (int, error)]()
n, err := add(40, 2) // 42
```

### ES Modules

```go
err := rt.LoadModule(ctx, "math.js", `
	export const PI = 3.14159;
	export default function square(x) { return x * x; }
`)
if err != nil {
	return err
}

result, err := rt.Eval(ctx, "main.js", `
	import square, { PI } from "math.js";
	export default square(2) * PI;
`, qngg.AsModule())
// result.Float64() == 12.56636
```

`EvalFile` and `LoadModuleFile` read files from `Config.CWD`, and relative
imports resolve against it.

### Bytecode Compilation

Compile once, then run the bytecode in any runtime, skipping parsing:

```go
bytecode, err := rt.Compile("fib.js", `
	function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
	fib(20);
`)
if err != nil {
	return err
}

result, err := other.EvalBytecode(ctx, bytecode)
// result.Int32() == 6765
```

Pass `AsModule()` to both `Compile` and `EvalBytecode` for modules, or load
module bytecode with `LoadModuleBytecode`.

### Go-JS Conversion

`ToValue` (and `Set`) converts Go values to JavaScript, and `Value.As[T]()`
converts back to the Go type you ask for:

| Go | JavaScript |
| --- | --- |
| `bool`, numbers, `string` | boolean, number, string |
| `nil`, nil pointers, maps and slices | `null` |
| `time.Time` | Date |
| `*big.Int` | BigInt (`As` also converts BigInts to integer types that fit) |
| `[]byte` | ArrayBuffer (`As` also reads typed arrays and DataViews) |
| slices and arrays | Array (`As` also reads Sets) |
| maps | object (`As` also reads Maps) |
| structs | object with the exported fields, named by their `json` tags, and methods |
| functions | function, see [Go Functions](#go-functions) |
| `error` | Error |
| `*Value` | the value itself (cloned) |
| any value wrapped by `NewProxyValue` | an opaque object, see [ProxyValue](#proxyvalue) |

`As[any]` picks Go types for you: objects become `map[string]any`, arrays
`[]any`, numbers `int` or `float64`, and BigInts `*big.Int`.

```go
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Post struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Author  User      `json:"author"`
	Created time.Time `json:"created"`
}

if err := rt.Set("user", User{ID: 1, Name: "Alice"}); err != nil {
	return err
}

result, err := rt.Eval(ctx, "main.js", `({
	id: 10,
	title: "Hello from " + user.name,
	author: user,
	created: new Date("2026-01-02T03:04:05Z"),
})`)
if err != nil {
	return err
}
defer result.Free()

post, err := result.As[Post]()
// post.Author.Name == "Alice", post.Created.Year() == 2026
```

JavaScript functions convert to Go functions with `As`, as shown in
[Calling JavaScript from Go](#calling-javascript-from-go).

### ProxyValue

`ToValue` copies: a Go function that receives a converted struct gets a new
copy, not the original. A ProxyValue instead passes a Go value through
JavaScript by reference. JavaScript sees an opaque object it cannot read or
forge, and Go functions and `As` get the original value back. Use it for
`context.Context`, connections, or large structs that should not be copied.

```go
type Counter struct{ hits int }

counter := &Counter{}
if err := rt.Set("bump", func(c *Counter) int { c.hits++; return c.hits }); err != nil {
	return err
}

proxy := rt.NewProxyValue(counter)
defer proxy.Free()
if err := rt.Global().Set("counter", proxy); err != nil {
	return err
}

result, err := rt.Eval(ctx, "main.js", `bump(counter); bump(counter)`)
if err != nil {
	return err
}
defer result.Free()
// result.Int32() == 2, counter.hits == 2
```

### Pool

A `Runtime` must not be used by several goroutines at once. A `Pool` gives
each call its own runtime and reuses idle ones. Runtimes that were
interrupted, or whose function panicked, are discarded instead of reused.

```go
pool, err := qngg.NewPool(8, qngg.Config{}, func(rt *qngg.Runtime) error {
	// Runs once for each new runtime.
	v, err := rt.Eval(context.Background(), "setup.js",
		`globalThis.greet = (name) => "Hello, " + name + "!"`)
	if err != nil {
		return err
	}
	v.Free()
	return nil
})
if err != nil {
	log.Fatal(err)
}
defer pool.Close()

http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	err := pool.Do(ctx, func(ctx context.Context, rt *qngg.Runtime) error {
		greet, err := rt.Get("greet")
		if err != nil {
			return err
		}
		defer greet.Free()

		msg, err := greet.Call(ctx, nil, r.URL.Query().Get("name"))
		if err != nil {
			return err
		}
		defer msg.Free()

		_, err = io.WriteString(w, msg.String())
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
})
```

Values created inside `Do` must not be used after it returns.

## Architecture

```
Go application
  │  qngg API: Runtime, Value, Pool
  ▼
Wasmtime C API, loaded at runtime through purego (no cgo)
  │  one store and instance per Runtime
  ▼
qjs.wasm: QuickJS-NG compiled with wasi-sdk
  │  WASI: CWD mount, stdout/stderr, clocks
  └─ calls back into Go for Go functions and to release Go references
```

Each `Runtime` is a separate WebAssembly instance with its own linear memory,
so runtimes share nothing and a crash in one cannot affect another. The
compiled `qjs.wasm` is shared between runtimes, and optionally cached on disk
with `Config.CacheDir`.

## API Reference

See [pkg.go.dev](https://pkg.go.dev/github.com/ironpark/qngg) for the full documentation.

### Core Types

| Type | Description |
|------|-------------|
| `Runtime` | A QuickJS runtime and its global context. Not safe for concurrent use. |
| `Value` | A JavaScript value. Free the ones you get. |
| `Function` / `FunctionCall` | A Go function callable from JavaScript, and its call. |
| `Error` | A JavaScript exception, returned by calls and throwable from Go. |
| `Pool` | Runtimes for concurrent use. |
| `Config` | Runtime configuration. |

### Key Methods

```go
// Runtime
rt, err := qngg.New(qngg.Config{})             // Create a runtime
rt.Close()                                     // Free it and all its values
rt.Eval(ctx, name, src, opts...)               // Run a script (or module with AsModule)
rt.EvalFile(ctx, path, opts...)                // Run a file from Config.CWD
rt.Compile(name, src, opts...)                 // Compile to bytecode
rt.CompileFile(path, opts...)                  // Compile a file from Config.CWD
rt.EvalBytecode(ctx, bytecode, opts...)        // Run bytecode
rt.LoadModule(ctx, name, src)                  // Register a module for imports
rt.Set(name, goValue)                          // Convert and assign a global
rt.Get(name)                                   // Read a global
rt.SetFunc(name, fn), rt.SetAsyncFunc(name, fn)
rt.ToValue(goValue)                            // Convert Go to JS
rt.NewString(s), rt.NewObject(), rt.NewProxyValue(v), ...
rt.Global()                                    // The global object (owned by the runtime)

// Value
v.Get(name), v.Set(name, val), v.Index(i), v.SetIndex(i, val) // val: *Value or Go value
v.Has(name), v.Delete(name), v.Keys(), v.Len()
v.Call(ctx, this, args...), v.Invoke(ctx, method, args...), v.New(ctx, args...)
v.Await(ctx)                                   // Wait for a promise
v.As[T]()                                      // Convert JS to Go
v.String(), v.Int32(), v.Float64(), v.Bool(), v.Time(), v.Bytes(), json.Marshal(v)
v.IsObject(), v.IsArray(), v.IsFunction(), v.Type(), ...
v.Clone()                                      // A new reference to the same value
v.Free()                                       // Release the value

// Pool
pool, err := qngg.NewPool(size, config, setup)
pool.Do(ctx, func(ctx context.Context, rt *qngg.Runtime) error { ... })
pool.Close()
```

### Configuration Options

```go
type Config struct {
    CWD                 string    // Host directory mounted as "/" (default: no filesystem)
    WritableCWD         bool      // Allow JavaScript to write under CWD (default: read-only)
    Stdout, Stderr      io.Writer // Output of console.log and friends (default: discarded)
    MemoryLimit         int       // QuickJS heap limit in bytes
    MaxStackSize        int       // QuickJS stack limit in bytes
    GCThreshold         int       // GC trigger threshold in bytes
    DisableInterruption bool      // ~30% faster compute, but contexts can no longer stop running code
    CacheDir            string    // On-disk cache of the compiled QuickJS WebAssembly module
    DisableBuildCache   bool      // Recompile the QuickJS module for every runtime
}
```

### Wasmtime native library

QNGG runs QuickJS-NG on [Wasmtime](https://wasmtime.dev/). The Wasmtime C API shared
library for the target platform (macOS, Linux and Windows on amd64/arm64) is
embedded in the binary, zstd-compressed, and extracted on first use into the
user cache directory (`os.UserCacheDir()/qngg/wasmtime`, falling back to the
system temp directory).

- Set `QNGG_WASMTIME_LIB` to the path of a Wasmtime C API shared library to load
  it instead of the embedded one. Its version must match the bindings (currently
  Wasmtime v50.0.0).
- Build with `-tags wasmtime_noembed` to leave the embedded library out of the
  binary (about 6-7 MB per platform) and rely on `QNGG_WASMTIME_LIB`.
- Linux builds require glibc; musl-based systems such as Alpine need a musl
  build of Wasmtime provided through `QNGG_WASMTIME_LIB`.

## Performance & Security

**Optimization Tips:**
1. Use a `Pool` for concurrent applications.
2. Compile frequently-used scripts to bytecode.
3. Use ProxyValue for large objects or shared state to avoid conversion.
4. Set `CacheDir` so the compiled QuickJS WebAssembly module is reused across processes.
5. Set `DisableInterruption` for trusted, compute-heavy code.

**Security**

- **Sandboxed execution**: QuickJS runs inside WebAssembly linear memory, so JavaScript cannot touch Go memory.
- **Filesystem**: none by default. With `CWD` set, only that directory is visible, as `/`, and it is read-only unless `WritableCWD` is set. WASI enforces this, so `qjs:std`/`qjs:os` cannot reach anything else.
- **Environment and output**: no environment variables are passed, and output is discarded unless `Stdout`/`Stderr` are set.
- **No network access**: no sockets are exposed to JavaScript.
- **Go references**: ProxyValues and Go functions are native objects that JavaScript cannot forge, and the Go values behind them are released when JavaScript garbage collects them.
- **Interruptible**: calls stop when their context is done, even in the middle of an infinite loop.
- **Isolation between users**: runtimes in a `Pool` keep their global state between calls. Use separate runtimes, or reset the globals you set, when running code for different tenants.
- **No CGO**: the Wasmtime native library is loaded at runtime through purego.

## Building from Source

### Prerequisites

- Go 1.27+
- WASI SDK (for WebAssembly compilation)
- CMake 3.16+
- Make

### Quick Build

```bash
# Clone with submodules
git clone --recursive https://github.com/ironpark/qngg.git
cd qngg

# Install WASI SDK 34 and binaryen (pick the archives for your platform)
curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-34/wasi-sdk-34.0-x86_64-linux.tar.gz | tar xz
sudo mv wasi-sdk-34.0-x86_64-linux /opt/wasi-sdk
curl -L https://github.com/WebAssembly/binaryen/releases/download/version_133/binaryen-version_133-x86_64-linux.tar.gz | tar xz

# Build WebAssembly module (QuickJS-NG v0.17.0)
make build WASM_OPT=$PWD/binaryen-version_133/bin/wasm-opt

# Run tests
go test ./...
```

## License

MIT License - see [LICENSE](LICENSE) file.

QNGG includes third-party code under its own licenses:

- [QuickJS-NG](https://github.com/quickjs-ng/quickjs), compiled into `qjs.wasm` - MIT ([LICENSE](https://github.com/quickjs-ng/quickjs/blob/master/LICENSE)).
- [wasmtime-go](https://github.com/bytecodealliance/wasmtime-go), via the purego port [wasmtime-pure-go](https://github.com/ironpark/wasmtime-pure-go) - Apache-2.0.
- [Wasmtime](https://github.com/bytecodealliance/wasmtime) shared libraries, embedded by wasmtime-pure-go - Apache-2.0 WITH LLVM-exception.

## Acknowledgments

Built on the shoulders of giants:

- **[fastschema/qjs](https://github.com/fastschema/qjs)** - The project QNGG is forked from.
- **[QuickJS](https://bellard.org/quickjs/)** by Fabrice Bellard - The elegant JavaScript engine.
- **[Wasmtime](https://wasmtime.dev/)** - Fast and secure WebAssembly runtime.
- **[wasmtime-go](https://github.com/bytecodealliance/wasmtime-go)** - Go bindings for Wasmtime, vendored and ported to purego.
- **[purego](https://github.com/ebitengine/purego)** - Calling C from Go without cgo.
- **[QuickJS-NG](https://github.com/quickjs-ng/quickjs)** - Maintained QuickJS fork.

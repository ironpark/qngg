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
- Fixes memory bugs inherited from upstream, such as leaked strings, property names and eval buffers, use-after-free in `JSONStringify` and a double free in `Await`.

| `go test -bench .` (Apple M1 Ultra) | QJS on Wazero | QNGG |
| --- | --- | --- |
| RuntimeNew | 17 ms | 0.36 ms |
| EvalFactorial | 97 ms | 52 ms (40 ms with `DisableInterruption`) |
| GoFunctionCall | 168 ms | 11 ms |
| ValueProperties | 503 µs | 220 µs |

### Migrating from fastschema/qjs

The API changed in ways the compiler will point out:

| fastschema/qjs | QNGG |
| --- | --- |
| `qjs.New(qjs.Option{...})` | `qngg.New(qngg.Config{...})` |
| `rt.Context().X(...)` | `rt.X(...)`: `Context` was merged into `Runtime` |
| `rt.Eval(name, qjs.Code(src), qjs.TypeModule())` | `rt.Eval(ctx, name, src, qngg.AsModule())` |
| `rt.Eval(name)` / `qjs.Bytecode(b)` | `rt.EvalFile(ctx, path)` / `rt.EvalBytecode(ctx, b)` |
| `rt.Load(name, ...)` | `rt.LoadModule`, `LoadModuleFile`, `LoadModuleBytecode` |
| `qjs.FlagAsync()` | `qngg.TopLevelAwait()` |
| `Option.Context`, `CloseOnContextDone`, `MaxExecutionTime` | the `ctx` of each call: cancel it or give it a deadline |
| `v.GetPropertyStr(n)`, `SetPropertyStr`, `GetPropertyIndex`, ... | `v.Get(n)`, `Set`, `Index`, `SetIndex`, `GetKey`, `SetKey`, `Has`, `Delete`, `Keys`: all return errors |
| `v.InvokeJS(name, args...)`, `rt.Invoke(fn, this, args...)` | `v.Invoke(ctx, name, args...)`, `fn.Call(ctx, this, args...)` |
| `v.CallConstructor(args...)` | `v.New(ctx, args...)` |
| `v.JSONStringify()` | `json.Marshal(v)`: `Value` implements `json.Marshaler` |
| `v.DateTime()`, `v.ToByteArray()` | `v.Time()`, `v.Bytes()` |
| `qjs.ToJsValue(ctx, x)`, `qjs.ToGoValue[T](v)`, `qjs.JsFuncToGo[F](v)` | `rt.ToValue(x)`, `v.As[T]()`, `v.As[F]()` |
| `func(this *qjs.This) (*qjs.Value, error)` | `func(call *qngg.FunctionCall) (*qngg.Value, error)` |
| `SetAsyncFunc(name, func(this *qjs.This) { this.Promise().Resolve(v) })` | `SetAsyncFunc(name, fn)`: the promise settles with `fn`'s result or error |
| `rt.ThrowTypeError(...)` | `return nil, &qngg.Error{Name: "TypeError", Message: ...}` |
| `pool.Get()` / `pool.Put(rt)` | `pool.Do(ctx, func(rt *qngg.Runtime) error { ... })` |
| `Array`, `Map`, `Set` wrappers | plain `*Value`s: `Index`, `Invoke(ctx, "get", k)`, `Keys`, ... |

Behavior changes to watch for:

- **Ownership**: passing a value to a method no longer takes it over. Free what you create even after `Set`ting it on an object.
- **Sandbox**: JavaScript gets no filesystem unless `Config.CWD` is set, read-only unless `WritableCWD` is set, and its output is discarded unless `Stdout`/`Stderr` are set.
- **Errors as values**: evaluating to an `Error` object returns the object, not a Go error. Only thrown exceptions are errors, as `*qngg.Error`.
- **No hidden properties**: `ToValue` no longer adds `__go_type`/`__registry_id` to objects, so `As` converts them structurally. Use `NewProxyValue` to pass a Go value through JavaScript unchanged.

## Features

- **JavaScript ES2023** via QuickJS-NG, including modules, async/await and top-level await.
- **Sandboxed**: QuickJS runs in its own WebAssembly instance and only gets the capabilities you grant.
- **Interruptible**: cancel a call's context or give it a deadline to stop runaway scripts, even `for (;;) {}`.
- **Go-JS interoperability**: convert structs, maps, slices, functions and channels both ways, or share Go values by reference with ProxyValues.
- **No cgo**: Wasmtime is loaded at runtime through [purego](https://github.com/ebitengine/purego), so builds work with `CGO_ENABLED=0` and cross-compile without a C toolchain.

## Example Usage

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

### Go Functions

`Set` converts Go values, functions included, and assigns them to globals. A
leading `context.Context` parameter receives the context of the call, and a
returned error is thrown into JavaScript.

```go
rt.Set("add", func(a, b int) int { return a + b })
rt.Set("lookup", func(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", errors.New("id is required")
	}
	return "user:" + id, nil
})

result, err := rt.Eval(ctx, "main.js", `lookup(String(add(40, 2)))`)
// result.String() == "user:42"
```

For direct access to JavaScript values, register a `Function`. Its arguments
belong to the caller; return an `*Error` to throw a specific error type.

```go
rt.SetFunc("describe", func(call *qngg.FunctionCall) (*qngg.Value, error) {
	if len(call.Args()) == 0 {
		return nil, &qngg.Error{Name: "TypeError", Message: "describe needs an argument"}
	}
	return call.Runtime().NewString(call.Arg(0).Type()), nil
})
```

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

`ToValue` (and `Set`) converts Go values to JavaScript: structs become
objects using their `json` tags and methods, maps become objects, slices
become arrays, functions become callable functions and `[]byte` becomes an
ArrayBuffer. `Value.As[T]()` converts back.

```go
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Post struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Author  User   `json:"author"`
	Content string `json:"content"`
}

rt.Set("user", User{ID: 1, Name: "Alice"})

result, err := rt.Eval(ctx, "main.js", `({
	id: 10,
	title: "Hello from " + user.name,
	author: user,
	content: "Written in JavaScript",
})`)
if err != nil {
	return err
}
defer result.Free()

post, err := result.As[Post]()
// post.Author.Name == "Alice"
```

### ProxyValue

A ProxyValue passes a Go value through JavaScript by reference: Go
functions receive the original value, not a converted copy. Use it for
`context.Context`, connections, or large structs that should not be copied.

```go
type Counter struct{ hits int }

counter := &Counter{}
rt.Set("bump", func(c *Counter) int { c.hits++; return c.hits })

proxy := rt.NewProxyValue(counter)
defer proxy.Free()
rt.Global().Set("counter", proxy)

rt.Eval(ctx, "main.js", `bump(counter); bump(counter)`)
// counter.hits == 2
```

### Pool

A `Runtime` must not be used by several goroutines at once. A `Pool` gives
each call its own runtime and reuses idle ones. Runtimes that were
interrupted, or whose function panicked, are discarded instead of reused.

```go
pool, err := qngg.NewPool(8, qngg.Config{}, func(rt *qngg.Runtime) error {
	// Runs once for each new runtime.
	_, err := rt.Eval(context.Background(), "setup.js",
		`globalThis.greet = (name) => "Hello, " + name + "!"`)
	return err
})
if err != nil {
	log.Fatal(err)
}
defer pool.Close()

http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	err := pool.Do(ctx, func(rt *qngg.Runtime) error {
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

## Installation

```bash
go get github.com/ironpark/qngg
```


```go
import "github.com/ironpark/qngg"
```

**Compatible with Go 1.27+**

## Architecture

```
┌─────────────┐      ┌─────────────┐      ┌─────────────┐
│   Your Go   │      │  Wasmtime   │      │   QuickJS   │
│ Application │ ---> │ WebAssembly │ ---> │ JavaScript  │
│             │      │  Runtime    │      │   Engine    │
└─────────────┘      └─────────────┘      └─────────────┘
       ^                    ^                     ^
       │                    │                     │
   Structured           Sandboxed              ES2023
      Data              Execution            JavaScript
       │                    │                     │
       └────────────────────┴─────────────────────┘
                           QNGG
```

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
rt.EvalBytecode(ctx, bytecode, opts...)        // Run bytecode
rt.LoadModule(ctx, name, src)                  // Register a module for imports
rt.Set(name, goValue)                          // Convert and assign a global
rt.Get(name)                                   // Read a global
rt.SetFunc(name, fn), rt.SetAsyncFunc(name, fn)
rt.ToValue(goValue)                            // Convert Go to JS
rt.NewString(s), rt.NewObject(), rt.NewProxyValue(v), ...
rt.Global()                                    // The global object (owned by the runtime)

// Value
v.Get(name), v.Set(name, val), v.Index(i), v.SetIndex(i, val)
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
pool.Do(ctx, func(rt *qngg.Runtime) error { ... })
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
    CacheDir            string    // Directory for compiled module cache files
    DisableBuildCache   bool      // Do not reuse in-memory compiled modules
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
4. Set `CacheDir` so compiled modules are reused across processes.
5. Set `DisableInterruption` for trusted, compute-heavy code.

**Security**

- **Sandboxed execution**: QuickJS runs inside WebAssembly linear memory, so JavaScript cannot touch Go memory.
- **Filesystem**: none by default. With `CWD` set, only that directory is visible, as `/`, and it is read-only unless `WritableCWD` is set. WASI enforces this, so `qjs:std`/`qjs:os` cannot reach anything else.
- **Environment and output**: no environment variables are passed, and output is discarded unless `Stdout`/`Stderr` are set.
- **No network access**: no sockets are exposed to JavaScript.
- **Interruptible**: calls stop when their context is done, even in the middle of an infinite loop.
- **Isolation between users**: runtimes in a `Pool` keep their global state between calls. Use separate runtimes, or reset the globals you set, when running code for different tenants.
- **No CGO**: the Wasmtime native library is loaded at runtime through purego.

### Memory Management

Values live in QuickJS's heap, not Go's, so they must be released with
`Free`. The rule is simple:

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
obj := rt.NewObject()
defer obj.Free()

name := rt.NewString("Alice")
defer name.Free()

if err := obj.Set("name", name); err != nil { // name is still yours
	return err
}
```

**Choose QNGG when you need:**
- Modern JavaScript (ES2023) in Go.
- No CGO, cross-compiles with a plain Go toolchain.
- Plugin systems and user-generated code.
- Fast startup: a new runtime takes well under a millisecond.

## Building from Source

### Prerequisites

- Go 1.27+
- WASI SDK (for WebAssembly compilation)
- CMake 3.16+
- Make

### Quick Build

**Development Setup:**

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

**Code Standards:**
- Follow standard Go conventions (`gofmt`, `golangci-lint`).
- Add tests for new features.
- Update documentation for API changes.
- Keep commit messages clear and descriptive.

## Contributing

Bug reports and pull requests are welcome on [GitHub](https://github.com/ironpark/qngg/issues). When reporting a bug, include the Go version, OS and a minimal reproduction.

## License

MIT License - see [LICENSE](LICENSE) file.

QNGG includes third-party code under its own licenses:

- [QuickJS-NG](https://github.com/quickjs-ng/quickjs), compiled into `qjs.wasm` - MIT ([LICENSE](https://github.com/quickjs-ng/quickjs/blob/master/LICENSE)).
- [wasmtime-go](https://github.com/bytecodealliance/wasmtime-go), vendored in `internal/wasmtime` - Apache-2.0 ([LICENSE-wasmtime-go](internal/wasmtime/LICENSE-wasmtime-go)).
- [Wasmtime](https://github.com/bytecodealliance/wasmtime) shared libraries, embedded in `internal/wasmtime/embedded` - Apache-2.0 WITH LLVM-exception ([LICENSE-wasmtime](internal/wasmtime/embedded/LICENSE-wasmtime)).

## Acknowledgments

Built on the shoulders of giants:

- **[fastschema/qjs](https://github.com/fastschema/qjs)** - The project QNGG is forked from.
- **[QuickJS](https://bellard.org/quickjs/)** by Fabrice Bellard - The elegant JavaScript engine.
- **[Wasmtime](https://wasmtime.dev/)** - Fast and secure WebAssembly runtime.
- **[wasmtime-go](https://github.com/bytecodealliance/wasmtime-go)** - Go bindings for Wasmtime, vendored and ported to purego.
- **[purego](https://github.com/ebitengine/purego)** - Calling C from Go without cgo.
- **[QuickJS-NG](https://github.com/quickjs-ng/quickjs)** - Maintained QuickJS fork.

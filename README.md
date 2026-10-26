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
- Fixes memory bugs inherited from upstream: string conversions leaked their C buffers, `JSONStringify` returned freed memory and `ToByteArray` freed memory owned by the ArrayBuffer.

Migrating from `github.com/fastschema/qjs`:

- Import `github.com/ironpark/qngg`; the package is named `qngg`.
- `Context` was merged into `Runtime`: call its methods on the runtime, and use `Value.Runtime()`/`This.Runtime()` instead of `Context()`.
- Low-level plumbing is no longer exported: `Handle`, `Mem`, raw `Call`/`Malloc`/`Free*`, the `Js*ToGo`/`Go*ToJs` helpers and the `ProxyFunction`, `StartFunctionName` and `QuickJSWasmBytes` options. Use `ToGoValue`, `ToJsValue` and `JsFuncToGo`.
- `SetProperty`, `SetPropertyStr` and `SetPropertyIndex` take ownership of the value; do not `Free` it afterwards.
- `Min` was removed; use the built-in `min`.
- `Option.MaxExecutionTime` is now a `time.Duration` and is enforced (it was an ignored `int`).
- JavaScript gets no filesystem unless `Option.CWD` is set, and that directory is read-only unless `Option.WritableCWD` is set (the process working directory used to be mounted read-write).
- Output is discarded unless `Option.Stdout`/`Option.Stderr` are set (it used to go to the process's stdout/stderr).
- `Pool.Put` closes runtimes that were closed or interrupted instead of reusing them.

| `go test -bench .` (Apple M1 Ultra) | QJS on Wazero | QNGG |
| --- | --- | --- |
| RuntimeNew | 17 ms | 0.34 ms |
| EvalFactorial | 97 ms | 39 ms |
| GoFunctionCall | 168 ms | 25 ms |
| ValueProperties | 503 µs | 153 µs |

## Features

- **JavaScript ES6+ Support**: Full ES2023 compatibility via QuickJS (NG fork).
- **WebAssembly Execution**: Secure, sandboxed runtime using Wasmtime.
- **Go-JS Interoperability**: Seamless data conversion between Go and JavaScript.
- **ProxyValue Support**: Zero-copy sharing of Go values with JavaScript via lightweight proxies.
- **Function Binding**: Expose Go functions to JavaScript and vice versa.
- **Async/Await**: Full support for asynchronous JavaScript execution.
- **Memory Safety**: Memory-safe execution environment with configurable limits.
- **No CGO Dependencies**: Wasmtime is loaded at runtime through [purego](https://github.com/ebitengine/purego), so builds work with `CGO_ENABLED=0` and cross-compile without a C toolchain.

## Example Usage

### Basic Execution

```go
rt, err := qngg.New()
if err != nil {
	log.Fatal(err)
}

defer rt.Close()

result, err := rt.Eval("test.js", qngg.Code(`
	const person = {
		name: "Alice",
		age: 30,
		city: "New York"
	};

	const info = Object.keys(person).map(key =>
		key + ": " + person[key]
	).join(", ");

	// The last expression is the return value
	({ person: person, info: info });
`))
if err != nil {
	log.Fatal("Eval error:", err)
}
defer result.Free()
// Output: name: Alice, age: 30, city: New York
log.Println(result.GetPropertyStr("info").String())
// Output: Alice
log.Println(result.GetPropertyStr("person").GetPropertyStr("name").String())
// Output: 30
log.Println(result.GetPropertyStr("person").GetPropertyStr("age").Int32())
```

### Go function binding

```go
rt.SetFunc("goFunction", func(this *qngg.This) (*qngg.Value, error) {
    return this.Runtime().NewString("Hello from Go!"), nil
})

result, err := rt.Eval("test.js", qngg.Code(`
	const message = goFunction();
	message;
`))
if err != nil {
	log.Fatal("Eval error:", err)
}
defer result.Free()

// Output: Hello from Go!
log.Println(result.String())
```

### HTTP Handlers in JavaScript
```go
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/ironpark/qngg"
)

func must[T any](val T, err error) T {
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	return val
}

const script = `
// JS handlers for HTTP routes
const about = () => {
	return "QuickJS in Go - Hello World!";
};

const contact = () => {
	return "Contact us at contact@example.com";
};

export default { about, contact };
`

func main() {
	rt := must(qngg.New())
	defer rt.Close()

	// Precompile the script to bytecode
	byteCode := must(rt.Compile("script.js", qngg.Code(script), qngg.TypeModule()))
	// Use a pool of runtimes for concurrent requests
	pool := qngg.NewPool(3, qngg.Option{}, func(r *qngg.Runtime) error {
		results := must(r.Eval("script.js", qngg.Bytecode(byteCode), qngg.TypeModule()))
		// Store the exported functions in the global object for easy access
		r.Global().SetPropertyStr("handlers", results)
		return nil
	})

	// Register HTTP handlers based on JS functions
	val := must(rt.Eval("script.js", qngg.Bytecode(byteCode), qngg.TypeModule()))
	methodNames := must(val.GetOwnPropertyNames())
	val.Free()
	for _, methodName := range methodNames {
		http.HandleFunc("/"+methodName, func(w http.ResponseWriter, r *http.Request) {
			runtime := must(pool.Get())
			defer pool.Put(runtime)

			// Call the corresponding JS function
			handlers := runtime.Global().GetPropertyStr("handlers")
			defer handlers.Free()
			result := must(handlers.InvokeJS(methodName))
			fmt.Fprint(w, result.String())
			result.Free()
		})
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from Go's HTTP server!")
	})

	log.Println("Server listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server error: %v\n", err)
	}
}
```

### Async operations

**Awaiting a promise**

```go
rt.SetAsyncFunc("asyncFunction", func(this *qngg.This) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		result := this.Runtime().NewString("Async result from Go!")
		this.Promise().Resolve(result)
	}()
})

result, err := rt.Eval("test.js", qngg.Code(`
async function main() {
	const result = await asyncFunction();
	return result;
}
({ main: main() });
`))

if err != nil {
	log.Fatal("Eval error:", err)
}
defer result.Free()

mainFunc := result.GetPropertyStr("main")

// Wait for the promise to resolve
val, err := mainFunc.Await()
if err != nil {
	log.Fatal("Await error:", err)
}

// Output: Async result from Go!
log.Println("Awaited value:", val.String())
```

**Top level await**

```go
// asyncFunction is already defined above
result, err := rt.Eval("test.js", qngg.Code(`
	async function main() {
		const result = await asyncFunction();
		return result;
	}
	await main()
`), qngg.FlagAsync())

if err != nil {
	log.Fatal("Eval error:", err)
}

defer result.Free()
log.Println(result.String())
```

### Call JS function from Go

```go
// Call JS function from Go
result, err := rt.Eval("test.js", qngg.Code(`
	function add(a, b) {
		return a + b;
	}

	function errorFunc() {
		throw new Error("test error");
	}

	({
		addFunc: add,
		errorFunc: errorFunc
	});
`))

if err != nil {
	log.Fatal("Eval error:", err)
}
defer result.Free()

jsAddFunc := result.GetPropertyStr("addFunc")
defer jsAddFunc.Free()

goAddFunc, err := qngg.JsFuncToGo[func(int, int) (int, error)](jsAddFunc)
if err != nil {
	log.Fatal("Func conversion error:", err)
}

total, err := goAddFunc(1, 2)
if err != nil {
	log.Fatal("Func execution error:", err)
}

// Output: 3
log.Println("Addition result:", total)

jsErrorFunc := result.GetPropertyStr("errorFunc")
defer jsErrorFunc.Free()

goErrorFunc, err := qngg.JsFuncToGo[func() (any, error)](jsErrorFunc)
if err != nil {
	log.Fatal("Func conversion error:", err)
}

_, err = goErrorFunc()
if err != nil {
	// Output:
	// JS function execution failed: Error: test error
  //  at errorFunc (test.js:7:13)
	log.Println(err.Error())
}
```

### ES Modules

```go
// Load a utility module
if _, err = rt.Load("math-utils.js", qngg.Code(`
	export function add(a, b) {
		return a + b;
	}

	export function multiply(a, b) {
		return a * b;
	}

	export function power(base, exponent) {
		return Math.pow(base, exponent);
	}

	export const PI = 3.14159;
	export const E = 2.71828;
	export default {
		add,
		multiply,
		power,
		PI,
		E
	};
`)); err != nil {
	log.Fatal("Module load error:", err)
}

// Use the module
result, err := rt.Eval("use-math.js", qngg.Code(`
	import mathUtils, { add, multiply, power, PI } from 'math-utils.js';

	const calculations = {
		addition: add(10, 20),
		multiplication: multiply(6, 7),
		power: power(2, 8),
		circleArea: PI * power(5, 2),
		defaultAdd: mathUtils.add(10, 20)
	};

	export default calculations;
`), qngg.TypeModule())

if err != nil {
	log.Fatal("Module eval error:", err)
}

// Output:
// Addition: 30
// Multiplication: 42
// Power: 256
// Circle Area: 78.54
// Default Add: 30
fmt.Printf("Addition: %d\n", result.GetPropertyStr("addition").Int32())
fmt.Printf("Multiplication: %.0f\n", result.GetPropertyStr("multiplication").Float64())
fmt.Printf("Power: %.0f\n", result.GetPropertyStr("power").Float64())
fmt.Printf("Circle Area: %.2f\n", result.GetPropertyStr("circleArea").Float64())
fmt.Printf("Default Add: %.d\n", result.GetPropertyStr("defaultAdd").Int32())
result.Free()
```

### Bytecode Compilation

```go
script := `
	function fibonacci(n) {
		if (n <= 1) return n;
		return fibonacci(n - 1) + fibonacci(n - 2);
	}

	function factorial(n) {
		return n <= 1 ? 1 : n * factorial(n - 1);
	}

	const result = {
		fib10: fibonacci(10),
		fact5: factorial(5),
		timestamp: Date.now()
	};

	result;
`

// Compile the script to bytecode
bytecode, err := rt.Compile("math-functions.js", qngg.Code(script))
if err != nil {
	log.Fatal("Compilation error:", err)
}

fmt.Printf("Bytecode size: %d bytes\n", len(bytecode))

// Execute the compiled bytecode
result, err := rt.Eval("compiled-math.js", qngg.Bytecode(bytecode))
if err != nil {
	log.Fatal("Bytecode execution error:", err)
}

fmt.Printf("Fibonacci(10): %d\n", result.GetPropertyStr("fib10").Int32())
fmt.Printf("Factorial(5): %d\n", result.GetPropertyStr("fact5").Int32())
result.Free()
```

### ProxyValue Support

ProxyValue is a feature that allows you to pass Go values directly to JavaScript without full serialization, enabling efficient sharing of complex objects, functions, and resources.

ProxyValue creates a lightweight JavaScript wrapper around Go values, storing only a reference ID rather than copying the entire value. This is particularly useful for **pass-through scenarios** where JavaScript receives a Go value and passes it back to Go without needing to access its contents.

Key benefits:
- **Zero-copy data sharing** - no serialization/deserialization overhead.
- **Pass-through efficiency** - JavaScript can hold and return Go values without conversion.
- **Type preservation** - original Go types are maintained across boundaries.
- **Resource efficiency** - perfect for objects like `context.Context`, database connections, or large structs.

#### Basic ProxyValue Usage

```go
// Create a Go function that accepts context and a number
goFuncWithContext := func(c context.Context, num int) int {
	// Access context values in Go
	log.Println("Context value:", c.Value("key"))
	return num * 2
}

// Convert Go function to JavaScript function
jsFuncWithContext, err := qngg.ToJsValue(rt, goFuncWithContext)
if err != nil {
	log.Fatal("Func conversion error:", err)
}
// The global object takes ownership of jsFuncWithContext.
rt.Global().SetPropertyStr("funcWithContext", jsFuncWithContext)

// Create a helper function that returns a ProxyValue
rt.SetFunc("$context", func(this *qngg.This) (*qngg.Value, error) {
	// Create context as ProxyValue - JavaScript will never access its contents
	passContext := context.WithValue(context.Background(), "key", "value123")
	val := rt.NewProxyValue(passContext)
	return val, nil
})

// JavaScript gets context as ProxyValue and passes it to Go function
result, err := rt.Eval("test.js", qngg.Code(`
	funcWithContext($context(), 10);
`))
if err != nil {
	log.Fatal("Eval error:", err)
}
defer result.Free()

// Output: 20
log.Println("Result:", result.Int32())
```

### GO-JS Conversion

```go
package main

import (
	"fmt"
	"log"

	"github.com/ironpark/qngg"
)

type Post struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Author User   `json:"author"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// Method on User struct
func (u User) GetDisplayName() string {
	return fmt.Sprintf("%s (%d)", u.Name, u.Age)
}

func (u User) IsAdult() bool {
	return u.Age >= 18
}

func main() {
	rt, err := qngg.New()
	if err != nil {
		log.Fatalf("Failed to create QuickJS runtime: %v", err)
	}
	defer rt.Close()

	rt.Global().SetPropertyStr("goInt", rt.NewInt32(55))
	rt.Global().SetPropertyStr("goString", rt.NewString("Hello, World!"))
	jsUser, err := qngg.ToJsValue(rt, User{ID: 1, Name: "Alice", Age: 25})
	if err != nil {
		log.Fatalf("Failed to convert User to JS value: %v", err)
	}
	rt.Global().SetPropertyStr("goUser", jsUser)

	result, err := rt.Eval("test.js", qngg.Code(`
		const post = {
			id: goInt,
			name: goString,
			author: goUser,
			displayName: goUser.GetDisplayName(),
			isAdult: goUser.IsAdult()
		};
		post;
	`))
	if err != nil {
		log.Fatalf("Failed to evaluate JS code: %v", err)
	}
	defer result.Free()

	goPost, err := qngg.ToGoValue[Post](result)
	if err != nil {
		log.Fatalf("Failed to convert JS value to Post: %v", err)
	}

	// Output:
	// Post ID: 55
	// Post Name: Hello, World!
	// Author ID: 1
	// Author Name: Alice
	// Author Age: 25
	// Author Display Name: Alice (25)
	// Author Is Adult: true
	log.Printf("Post ID: %d\n", goPost.ID)
	log.Printf("Post Name: %s\n", goPost.Name)
	log.Printf("Author ID: %d\n", goPost.Author.ID)
	log.Printf("Author Name: %s\n", goPost.Author.Name)
	log.Printf("Author Age: %d\n", goPost.Author.Age)
	log.Printf("Author Display Name: %s\n", goPost.Author.GetDisplayName())
	log.Printf("Author Is Adult: %t\n", goPost.Author.IsAdult())
}
```

### Pool

```go
package main

import (
	"log"
	"sync"

	"github.com/ironpark/qngg"
)

func main() {
	setupFunc := func(rt *qngg.Runtime) error {
		rt.Eval("setup.js", qngg.Code(`
			function getMessage(workerId, taskId) {
				return "Hello from pooled runtime: " + workerId + "-" + taskId;
			}
		`))
		return nil
	}
	// Create a pool with 3 runtimes
	pool := qngg.NewPool(3, qngg.Option{}, setupFunc)
	numWorkers := 5
	numTasks := 3
	var wg sync.WaitGroup

	for workerID := range numWorkers {
		wg.Go(func() {
			for taskID := range numTasks {
				rt, err := pool.Get()
				if err != nil {
					log.Fatalf("Failed to get runtime from pool: %v", err)
				}

				rt.Global().SetPropertyStr("workerID", rt.NewInt32(int32(workerID)))
				rt.Global().SetPropertyStr("taskID", rt.NewInt32(int32(taskID)))

				// Use the runtime
				result, err := rt.Eval("pool-test.js", qngg.Code(`
					({
						message: getMessage(workerID, taskID),
						timestamp: Date.now(),
					});
				`))
				if err != nil {
					log.Fatalf("JS execution error: %v", err)
				}
				message := result.GetPropertyStr("message")
				log.Println(message.String())

				// Release values before returning the runtime to the pool,
				// where another goroutine may pick it up.
				message.Free()
				result.Free()
				pool.Put(rt)
			}
		})
	}
	wg.Wait()
}
```

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

### Core Types

| Type | Description |
|------|-------------|
| `Runtime` | Main JavaScript runtime instance |
| `Value` | JavaScript value wrapper |
| `Pool` | Runtime pool for performance |
| `ProxyRegistry` | Thread-safe registry for ProxyValue objects |

### Key Methods

```go
// Runtime
rt, err := qngg.New(options...)          // Create a runtime
rt.Close()                               // Free the runtime
rt.Eval(filename, qngg.Code(src))        // Run a script, returns the last value
rt.Load(filename, qngg.Code(src))        // Load an ES module for later imports
rt.Compile(filename, qngg.Code(src))     // Compile to bytecode
rt.Global()                              // Global object
rt.SetFunc(name, fn)                     // Bind a Go function
rt.SetAsyncFunc(name, fn)                // Bind a Go function returning a promise
rt.NewString(s), rt.NewObject(), ...     // Create JS values
rt.NewProxyValue(v)                      // Wrap a Go value as a ProxyValue

// Value
value.String(), value.Int32(), value.Bool(), ...
value.GetPropertyStr(name)               // Get a property (free the result)
value.SetPropertyStr(name, val)          // Set a property (takes ownership of val)
value.InvokeJS(name, args...)            // Call a method
value.Await()                            // Wait for a promise
value.Free()                             // Release the value

// Conversion
qngg.ToGoValue[T](value)                 // JS (or ProxyValue) to Go
qngg.ToJsValue(rt, goValue)             // Go to JS
qngg.JsFuncToGo[F](value)                // JS function to a typed Go function
```

### Configuration Options

```go
type Option struct {
    CWD                string          // Host directory mounted as "/" (default: no filesystem)
    WritableCWD        bool            // Allow JavaScript to write under CWD (default: read-only)
    Context            context.Context // Context passed to Go functions called from JS
    CloseOnContextDone bool            // Interrupt running JavaScript when Context is done
    MemoryLimit        int             // QuickJS heap limit in bytes
    MaxStackSize       int             // QuickJS stack limit in bytes
    GCThreshold        int             // GC trigger threshold in bytes
    CacheDir           string          // Directory for compiled module cache files
    DisableBuildCache  bool            // Do not reuse in-memory compiled modules
    Stdout, Stderr     io.Writer       // Output of console.log and friends (default: discarded)
    MaxExecutionTime   time.Duration   // Time limit for each call into JavaScript, such as Eval
}
```

`MaxExecutionTime` and `CloseOnContextDone` stop running JavaScript, including
infinite loops. Calls then fail with `ErrExecutionTimeout` or the context's
error, and the runtime must be closed. Both compile QuickJS with interruption
checks, which makes compute-heavy JavaScript about 30% slower.

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
1. Use runtime pools for concurrent applications.
2. Compile frequently-used scripts to bytecode.
3. Use ProxyValue for large objects or shared state to avoid serialization overhead.
4. Set `CacheDir` so compiled modules are reused across processes.
5. Set appropriate memory limits.

**Security**

- **Sandboxed execution**: QuickJS runs inside WebAssembly linear memory, so JavaScript cannot touch Go memory.
- **Filesystem**: none by default. With `CWD` set, only that directory is visible, as `/`, and it is read-only unless `WritableCWD` is set. WASI enforces this, so `qjs:std`/`qjs:os` cannot reach anything else.
- **Environment and output**: no environment variables are passed, and output is discarded unless `Stdout`/`Stderr` are set.
- **No network access**: no sockets are exposed to JavaScript.
- **Interruptible**: `MaxExecutionTime` and `CloseOnContextDone` stop runaway scripts.
- **No CGO**: the Wasmtime native library is loaded at runtime through purego.

### Memory Management

**Rules:**
- Call `Free()` on values you get back (`Eval`, `GetPropertyStr`, `InvokeJS`, ...).
- Do not `Free()` a value after passing it to `SetProperty`, `SetPropertyStr` or `SetPropertyIndex`; the object owns it.
- Do not free functions registered on the global object.
- Free values before returning a runtime to a `Pool`.
- Call `rt.Close()` when done with a runtime.

```go
result, err := rt.Eval("script.js", qngg.Code(src))
if err != nil {
	return err
}
defer result.Free()
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

# Migrating from fastschema/qjs

QNGG started as a fork of [fastschema/qjs](https://github.com/fastschema/qjs).
Its API was redesigned around one ownership rule for values and a
`context.Context` on every call that runs JavaScript, so most code needs
changes. This guide lists them.

## Module path

Replace the module and package name:

```bash
go get github.com/ironpark/qngg
```

```go
import "github.com/ironpark/qngg" // was "github.com/fastschema/qjs"
```

QNGG requires Go 1.27 or later.

## Before and after

```go
// fastschema/qjs
rt, err := qjs.New()
if err != nil {
	log.Fatal(err)
}
defer rt.Close()
ctx := rt.Context()

ctx.SetFunc("greet", func(this *qjs.This) (*qjs.Value, error) {
	return this.Context().NewString("Hello, " + this.Args()[0].String()), nil
})

result, err := ctx.Eval("main.js", qjs.Code(`({ message: greet("Alice") })`))
if err != nil {
	log.Fatal(err)
}
defer result.Free()
log.Println(result.GetPropertyStr("message").String())
```

```go
// QNGG
rt, err := qngg.New(qngg.Config{})
if err != nil {
	log.Fatal(err)
}
defer rt.Close()

err = rt.SetFunc("greet", func(call *qngg.FunctionCall) (*qngg.Value, error) {
	return call.Runtime().NewString("Hello, " + call.Arg(0).String()), nil
})
if err != nil {
	log.Fatal(err)
}

result, err := rt.Eval(context.Background(), "main.js", `({ message: greet("Alice") })`)
if err != nil {
	log.Fatal(err)
}
defer result.Free()

message, err := result.Get("message") // a new value: free it too
if err != nil {
	log.Fatal(err)
}
defer message.Free()
log.Println(message.String())
```

## API changes

These changes break the build, so the compiler points them out:

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
| `pool.Get()` / `pool.Put(rt)` | `pool.Do(ctx, func(ctx context.Context, rt *qngg.Runtime) error { ... })` |
| `Array`, `Map`, `Set` wrappers | plain `*Value`s: `Index`, `Invoke(ctx, "get", k)`, `Keys`, ... |

## Behavior changes

These compile but behave differently:

- **Ownership**: passing a value to a method no longer takes it over. Free what you create even after `Set`ting it on an object.
- **Sandbox**: JavaScript gets no filesystem unless `Config.CWD` is set, read-only unless `WritableCWD` is set, and its output is discarded unless `Stdout`/`Stderr` are set.
- **Errors as values**: evaluating to an `Error` object returns the object, not a Go error. Only thrown exceptions are errors, as `*qngg.Error`.
- **`Type()`**: plain objects report `"Object"` instead of `"unknown"`, and RegExps, typed arrays and DataViews report `"RegExp"`, `"TypedArray"` and `"DataView"`.
- **BigInt**: `ToValue` converts `*big.Int` and `big.Int` to BigInts instead of plain objects, and `As` converts BigInts to integer types when they fit and to `*big.Int` for `any`.
- **No hidden properties**: `ToValue` no longer adds `__go_type`/`__registry_id` to objects, so `As` converts them structurally. Use `NewProxyValue` to pass a Go value through JavaScript unchanged.

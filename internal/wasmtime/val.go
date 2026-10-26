package wasmtime

import (
	"runtime"
)

// Val is a primitive numeric value.
// Moreover, in the definition of programs, immutable sequences of values occur to represent more complex data, such as text strings or other vectors.
type Val struct {
	kind wasmtime_valkind_t
	val  interface{}
}

// ValI32 converts a go int32 to a i32 Val
func ValI32(val int32) Val {
	return Val{kind: WASMTIME_I32, val: val}
}

// ValI64 converts a go int64 to a i64 Val
func ValI64(val int64) Val {
	return Val{kind: WASMTIME_I64, val: val}
}

// ValF32 converts a go float32 to a f32 Val
func ValF32(val float32) Val {
	return Val{kind: WASMTIME_F32, val: val}
}

// ValF64 converts a go float64 to a f64 Val
func ValF64(val float64) Val {
	return Val{kind: WASMTIME_F64, val: val}
}

// ValFuncref converts a Func to a funcref Val
//
// Note that `f` can be `nil` to represent a null `funcref`.
func ValFuncref(f *Func) Val {
	return Val{kind: WASMTIME_FUNCREF, val: f}
}

// mkValGC is overridden in val_feat_gc.go when GC support is compiled in to
// handle GC kinds such as `externref`.
var mkValGC = func(store Storelike, src *wasmtime_val_t) Val {
	panic("failed to get kind of `Val`")
}

// valKindGC is overridden in val_feat_gc.go to translate GC value kinds (such
// as `externref`) to their `ValKind` counterpart.
var valKindGC = func(kind wasmtime_valkind_t) ValKind {
	panic("failed to get kind of `Val`")
}

// valInitializeGC is overridden in val_feat_gc.go to populate a
// `wasmtime_val_t` for GC-typed values (such as `externref`).
var valInitializeGC = func(v Val, store Storelike, ptr *wasmtime_val_t) {
	panic("failed to get kind of `Val`")
}

// initializeExternrefArg is overridden in val_feat_gc.go to wrap arbitrary Go
// values into an `externref` argument when calling host->wasm. Without GC
// support, no externref values can flow into wasm, so this panics.
var initializeExternrefArg = func(store Storelike, val interface{}, dst *wasmtime_val_t) Val {
	panic("externref support is not available in this build")
}

func mkVal(store Storelike, src *wasmtime_val_t) Val {
	switch src.kind {
	case WASMTIME_I32:
		return ValI32(int32(go_wasmtime_val_i32_get(src)))
	case WASMTIME_I64:
		return ValI64(int64(go_wasmtime_val_i64_get(src)))
	case WASMTIME_F32:
		return ValF32(float32(go_wasmtime_val_f32_get(src)))
	case WASMTIME_F64:
		return ValF64(float64(go_wasmtime_val_f64_get(src)))
	case WASMTIME_FUNCREF:
		val := go_wasmtime_val_funcref_get(src)
		if val.store_id == 0 {
			return ValFuncref(nil)
		} else {
			return ValFuncref(mkFunc(val))
		}
	}
	return mkValGC(store, src)
}

func takeVal(store Storelike, src *wasmtime_val_t) Val {
	ret := mkVal(store, src)
	wasmtime_val_unroot(src)
	runtime.KeepAlive(store)
	return ret
}

// Kind returns the kind of value that this `Val` contains.
func (v Val) Kind() ValKind {
	switch v.kind {
	case WASMTIME_I32:
		return KindI32
	case WASMTIME_I64:
		return KindI64
	case WASMTIME_F32:
		return KindF32
	case WASMTIME_F64:
		return KindF64
	case WASMTIME_FUNCREF:
		return KindFuncref
	}
	return valKindGC(v.kind)
}

// I32 returns the underlying 32-bit integer if this is an `i32`, or panics.
func (v Val) I32() int32 {
	if v.Kind() != KindI32 {
		panic("not an i32")
	}
	return v.val.(int32)
}

// I64 returns the underlying 64-bit integer if this is an `i64`, or panics.
func (v Val) I64() int64 {
	if v.Kind() != KindI64 {
		panic("not an i64")
	}
	return v.val.(int64)
}

// F32 returns the underlying 32-bit float if this is an `f32`, or panics.
func (v Val) F32() float32 {
	if v.Kind() != KindF32 {
		panic("not an f32")
	}
	return v.val.(float32)
}

// F64 returns the underlying 64-bit float if this is an `f64`, or panics.
func (v Val) F64() float64 {
	if v.Kind() != KindF64 {
		panic("not an f64")
	}
	return v.val.(float64)
}

// Funcref returns the underlying function if this is a `funcref`, or panics.
//
// Note that a null `funcref` is returned as `nil`.
func (v Val) Funcref() *Func {
	if v.Kind() != KindFuncref {
		panic("not a funcref")
	}
	return v.val.(*Func)
}

// Get returns the underlying 64-bit float if this is an `f64`, or panics.
func (v Val) Get() interface{} {
	return v.val
}

func (v Val) initialize(store Storelike, ptr *wasmtime_val_t) {
	ptr.kind = v.kind
	switch v.kind {
	case WASMTIME_I32:
		go_wasmtime_val_i32_set(ptr, int32(v.val.(int32)))
	case WASMTIME_I64:
		go_wasmtime_val_i64_set(ptr, int64(v.val.(int64)))
	case WASMTIME_F32:
		go_wasmtime_val_f32_set(ptr, float32(v.val.(float32)))
	case WASMTIME_F64:
		go_wasmtime_val_f64_set(ptr, float64(v.val.(float64)))
	case WASMTIME_FUNCREF:
		val := v.val.(*Func)
		if val != nil {
			go_wasmtime_val_funcref_set(ptr, val.val)
		} else {
			empty := wasmtime_func_t{}
			go_wasmtime_val_funcref_set(ptr, empty)
		}
	default:
		valInitializeGC(v, store, ptr)
	}
}

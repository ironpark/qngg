package wasmtime

import (
	"runtime"
	"unsafe"
)

// # Naming
//
// This package is a purego port of github.com/bytecodealliance/wasmtime-go.
// To keep the port close to the original cgo code, C types, functions and
// constants keep their C names: `C.wasm_engine_t` became `wasm_engine_t`,
// `C.wasmtime_func_call(...)` became `wasmtime_func_call(...)` and so on.
// C functions are Go function variables bound in `load.go`.
//
// C scalar types map as follows: size_t => uintptr, bool => bool,
// int => int32, uint32_t => uint32, float => float32, double => float64.
// Enums in the Wasmtime C API are all `uint8_t`.
//
// Pointers to the opaque types below always point at memory owned by Wasmtime
// and are never dereferenced from Go. Value types (vectors, `wasmtime_val_t`,
// `wasmtime_extern_t`, ...) mirror the C layout exactly and may live in Go
// memory; only pass pointers to them to C for the duration of a call.
//
// # What's up with `ptr()` methods?
//
// We use `runtime.SetFinalizer` to free all objects we allocate from C. This
// is intended to make usage of the API much simpler since you don't have to
// close/free anything. The tricky part here though is laid out in
// `runtime.SetFinalizer`'s documentation which is that if you read a
// non-gc-value (like a C pointer) from a GC object then after the value is
// read the GC value might get garbage collected. This is quite bad for us
// because the garbage collection will free the C pointer, making the C pointer
// actually invalid.
//
// The solution is to add `runtime.KeepAlive` calls after C function calls to
// ensure that the GC object lives at least as long as the C function call
// itself. This is naturally quite error-prone, so the goal here with `ptr()`
// methods is to make us a bit more resilient to these sorts of errors and
// expose segfaults during development.
//
// Each `ptr()` method has the basic structure of doing these steps:
//
// 1. First it reads the pointer value from the GC object
// 2. Next it conditionally calls `runtime.GC()`, depending on build flags
// 3. Finally it returns the original pointer value
//
// The goal here is to as aggressively as we can collect GC objects when
// testing and trigger finalizers as frequently as we can. This naturally
// slows things down quite a bit, so conditional compilation (with the `debug`
// tag) is used to enable this.

// Opaque C types.
type (
	wasm_config_t                     struct{ _ [0]byte }
	wasm_engine_t                     struct{ _ [0]byte }
	wasm_exporttype_t                 struct{ _ [0]byte }
	wasm_externtype_t                 struct{ _ [0]byte }
	wasm_frame_t                      struct{ _ [0]byte }
	wasm_functype_t                   struct{ _ [0]byte }
	wasm_globaltype_t                 struct{ _ [0]byte }
	wasm_importtype_t                 struct{ _ [0]byte }
	wasm_memorytype_t                 struct{ _ [0]byte }
	wasm_tabletype_t                  struct{ _ [0]byte }
	wasm_trap_t                       struct{ _ [0]byte }
	wasm_valtype_t                    struct{ _ [0]byte }
	wasm_extern_t                     struct{ _ [0]byte }
	wasi_config_t                     struct{ _ [0]byte }
	wasmtime_caller_t                 struct{ _ [0]byte }
	wasmtime_context_t                struct{ _ [0]byte }
	wasmtime_error_t                  struct{ _ [0]byte }
	wasmtime_linker_t                 struct{ _ [0]byte }
	wasmtime_module_t                 struct{ _ [0]byte }
	wasmtime_store_t                  struct{ _ [0]byte }
	wasmtime_component_t              struct{ _ [0]byte }
	wasmtime_component_export_index_t struct{ _ [0]byte }
	wasmtime_component_extern_t       struct{ _ [0]byte }
	wasmtime_component_linker_t       struct{ _ [0]byte }
	wasmtime_component_type_t         struct{ _ [0]byte }
)

// C enums, all `uint8_t` in the Wasmtime C API.
type (
	wasm_valkind_t                    = uint8
	wasm_mutability_t                 = uint8
	wasmtime_valkind_t                = uint8
	wasmtime_extern_kind_t            = uint8
	wasmtime_strategy_t               = uint8
	wasmtime_opt_level_t              = uint8
	wasmtime_profiling_strategy_t     = uint8
	wasmtime_component_item_kind_t    = uint8
	wasmtime_component_valtype_kind_t = uint8
)

const (
	WASM_I32       = 0
	WASM_I64       = 1
	WASM_F32       = 2
	WASM_F64       = 3
	WASM_EXTERNREF = 128
	WASM_FUNCREF   = 129

	WASM_CONST = 0
	WASM_VAR   = 1

	WASMTIME_I32       = 0
	WASMTIME_I64       = 1
	WASMTIME_F32       = 2
	WASMTIME_F64       = 3
	WASMTIME_V128      = 4
	WASMTIME_FUNCREF   = 5
	WASMTIME_EXTERNREF = 6
	WASMTIME_ANYREF    = 7
	WASMTIME_EXNREF    = 8

	WASMTIME_EXTERN_FUNC   = 0
	WASMTIME_EXTERN_GLOBAL = 1
	WASMTIME_EXTERN_TABLE  = 2
	WASMTIME_EXTERN_MEMORY = 3

	WASMTIME_STRATEGY_AUTO      = 0
	WASMTIME_STRATEGY_CRANELIFT = 1

	WASMTIME_OPT_LEVEL_NONE           = 0
	WASMTIME_OPT_LEVEL_SPEED          = 1
	WASMTIME_OPT_LEVEL_SPEED_AND_SIZE = 2

	WASMTIME_PROFILING_STRATEGY_NONE    = 0
	WASMTIME_PROFILING_STRATEGY_JITDUMP = 1

	WASMTIME_COMPONENT_ITEM_COMPONENT          = 0
	WASMTIME_COMPONENT_ITEM_COMPONENT_INSTANCE = 1
	WASMTIME_COMPONENT_ITEM_MODULE             = 2
	WASMTIME_COMPONENT_ITEM_COMPONENT_FUNC     = 3
	WASMTIME_COMPONENT_ITEM_RESOURCE           = 4
	WASMTIME_COMPONENT_ITEM_CORE_FUNC          = 5
	WASMTIME_COMPONENT_ITEM_TYPE               = 6

	WASMTIME_COMPONENT_VALTYPE_BOOL          = 0
	WASMTIME_COMPONENT_VALTYPE_S8            = 1
	WASMTIME_COMPONENT_VALTYPE_S16           = 2
	WASMTIME_COMPONENT_VALTYPE_S32           = 3
	WASMTIME_COMPONENT_VALTYPE_S64           = 4
	WASMTIME_COMPONENT_VALTYPE_U8            = 5
	WASMTIME_COMPONENT_VALTYPE_U16           = 6
	WASMTIME_COMPONENT_VALTYPE_U32           = 7
	WASMTIME_COMPONENT_VALTYPE_U64           = 8
	WASMTIME_COMPONENT_VALTYPE_F32           = 9
	WASMTIME_COMPONENT_VALTYPE_F64           = 10
	WASMTIME_COMPONENT_VALTYPE_CHAR          = 11
	WASMTIME_COMPONENT_VALTYPE_STRING        = 12
	WASMTIME_COMPONENT_VALTYPE_LIST          = 13
	WASMTIME_COMPONENT_VALTYPE_RECORD        = 14
	WASMTIME_COMPONENT_VALTYPE_TUPLE         = 15
	WASMTIME_COMPONENT_VALTYPE_VARIANT       = 16
	WASMTIME_COMPONENT_VALTYPE_ENUM          = 17
	WASMTIME_COMPONENT_VALTYPE_OPTION        = 18
	WASMTIME_COMPONENT_VALTYPE_RESULT        = 19
	WASMTIME_COMPONENT_VALTYPE_FLAGS         = 20
	WASMTIME_COMPONENT_VALTYPE_OWN           = 21
	WASMTIME_COMPONENT_VALTYPE_BORROW        = 22
	WASMTIME_COMPONENT_VALTYPE_FUTURE        = 23
	WASMTIME_COMPONENT_VALTYPE_STREAM        = 24
	WASMTIME_COMPONENT_VALTYPE_ERROR_CONTEXT = 25
	WASMTIME_COMPONENT_VALTYPE_MAP           = 26
)

// Vector types, all laid out as `{ size_t size; T *data; }`.
type (
	wasm_byte_vec_t struct {
		size uintptr
		data *byte
	}
	wasm_valtype_vec_t struct {
		size uintptr
		data **wasm_valtype_t
	}
	wasm_importtype_vec_t struct {
		size uintptr
		data **wasm_importtype_t
	}
	wasm_exporttype_vec_t struct {
		size uintptr
		data **wasm_exporttype_t
	}
	wasm_frame_vec_t struct {
		size uintptr
		data **wasm_frame_t
	}
	wasm_extern_vec_t struct {
		size uintptr
		data **wasm_extern_t
	}
)

type wasm_limits_t struct {
	min uint32
	max uint32
}

type wasmtime_func_t struct {
	store_id  uint64
	__private uintptr
}

type wasmtime_table_t struct {
	store_id   uint64
	__private1 uint32
	__private2 uint32
	__private3 uintptr
}

type wasmtime_memory_t struct {
	store_id   uint64
	__private1 uint32
	__private2 uint32
	__private3 uintptr
}

type wasmtime_global_t struct {
	store_id   uint64
	__private1 uint32
	__private2 uint32
	__private3 uintptr
}

type wasmtime_instance_t struct {
	store_id  uint64
	__private uintptr
}

type wasmtime_externref_t struct {
	store_id   uint64
	__private1 uint32
	__private2 uint32
	__private3 uintptr
}

type wasmtime_component_instance_t struct {
	store_id  uint64
	__private uint32
	_         uint32
}

// wasmtime_val_t is `{ uint8_t kind; wasmtime_valunion_t of; }` where the
// union is 24 bytes, 8-byte aligned.
type wasmtime_val_t struct {
	kind wasmtime_valkind_t
	_    [7]byte
	of   [3]uint64
}

// wasmtime_extern_t is `{ uint8_t kind; wasmtime_extern_union_t of; }` where
// the union is 24 bytes, 8-byte aligned.
type wasmtime_extern_t struct {
	kind wasmtime_extern_kind_t
	_    [7]byte
	of   [3]uint64
}

// wasmtime_component_valtype_t is `{ uint8_t kind; union { T *ptr; } of; }`.
type wasmtime_component_valtype_t struct {
	kind wasmtime_component_valtype_kind_t
	_    [7]byte
	of   uintptr
}

// wasmtime_component_item_t is `{ uint8_t kind; union of; }` where the union
// is either a pointer or a `wasmtime_component_valtype_t`.
type wasmtime_component_item_t struct {
	kind wasmtime_component_item_kind_t
	_    [7]byte
	of   [2]uint64
}

// Replacements for the union accessors in the original `shims.c`. Setters
// zero the whole union first so no bytes of a previous value linger.

func unionGet[T any](of *[3]uint64) T {
	return *(*T)(unsafe.Pointer(of))
}

func unionSet[T any](of *[3]uint64, v T) {
	*of = [3]uint64{}
	*(*T)(unsafe.Pointer(of)) = v
}

func go_wasmtime_val_i32_get(v *wasmtime_val_t) int32      { return unionGet[int32](&v.of) }
func go_wasmtime_val_i32_set(v *wasmtime_val_t, i int32)   { unionSet(&v.of, i) }
func go_wasmtime_val_i64_get(v *wasmtime_val_t) int64      { return unionGet[int64](&v.of) }
func go_wasmtime_val_i64_set(v *wasmtime_val_t, i int64)   { unionSet(&v.of, i) }
func go_wasmtime_val_f32_get(v *wasmtime_val_t) float32    { return unionGet[float32](&v.of) }
func go_wasmtime_val_f32_set(v *wasmtime_val_t, f float32) { unionSet(&v.of, f) }
func go_wasmtime_val_f64_get(v *wasmtime_val_t) float64    { return unionGet[float64](&v.of) }
func go_wasmtime_val_f64_set(v *wasmtime_val_t, f float64) { unionSet(&v.of, f) }

func go_wasmtime_val_funcref_get(v *wasmtime_val_t) wasmtime_func_t {
	return unionGet[wasmtime_func_t](&v.of)
}

func go_wasmtime_val_funcref_set(v *wasmtime_val_t, f wasmtime_func_t) { unionSet(&v.of, f) }

func go_wasmtime_val_externref_get(v *wasmtime_val_t) wasmtime_externref_t {
	return unionGet[wasmtime_externref_t](&v.of)
}

func go_wasmtime_val_externref_set(v *wasmtime_val_t, r wasmtime_externref_t) {
	unionSet(&v.of, r)
}

func go_wasmtime_extern_func_get(e *wasmtime_extern_t) wasmtime_func_t {
	return unionGet[wasmtime_func_t](&e.of)
}

func go_wasmtime_extern_func_set(e *wasmtime_extern_t, f wasmtime_func_t) { unionSet(&e.of, f) }

func go_wasmtime_extern_memory_get(e *wasmtime_extern_t) wasmtime_memory_t {
	return unionGet[wasmtime_memory_t](&e.of)
}

func go_wasmtime_extern_memory_set(e *wasmtime_extern_t, m wasmtime_memory_t) { unionSet(&e.of, m) }

func go_wasmtime_extern_table_get(e *wasmtime_extern_t) wasmtime_table_t {
	return unionGet[wasmtime_table_t](&e.of)
}

func go_wasmtime_extern_table_set(e *wasmtime_extern_t, t wasmtime_table_t) { unionSet(&e.of, t) }

func go_wasmtime_extern_global_get(e *wasmtime_extern_t) wasmtime_global_t {
	return unionGet[wasmtime_global_t](&e.of)
}

func go_wasmtime_extern_global_set(e *wasmtime_extern_t, g wasmtime_global_t) { unionSet(&e.of, g) }

// emptyByte backs the data pointer handed to C for empty strings and slices,
// since Wasmtime builds Rust slices out of `(ptr, len)` pairs and those must
// never be null.
var emptyByte byte

// goStrPtr returns a pointer to the bytes of `s`, never nil. This replaces
// cgo's `_GoStringPtr`; `len(s)` replaces `_GoStringLen`.
func goStrPtr(s string) *byte {
	if len(s) == 0 {
		return &emptyByte
	}
	return unsafe.StringData(s)
}

// cString returns a pointer to a NUL-terminated copy of `s`, valid for as long
// as the returned pointer is kept alive.
func cString(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// goStringN copies `n` bytes at `p` into a Go string. This replaces cgo's
// `C.GoStringN`.
func goStringN(p *byte, n int) string {
	if p == nil || n <= 0 {
		return ""
	}
	return string(unsafe.Slice(p, n))
}

// goBytes copies `n` bytes at `p` into a Go slice. This replaces cgo's
// `C.GoBytes`.
func goBytes(p *byte, n int) []byte {
	if p == nil || n <= 0 {
		return []byte{}
	}
	return append([]byte(nil), unsafe.Slice(p, n)...)
}

// cSlice views `n` values of type T in C memory at `p`. When `n` is zero
// Wasmtime may pass a dangling non-null pointer, which must never be turned
// into a Go pointer, so nil is returned instead.
func cSlice[T any](p, n uintptr) []T {
	if n == 0 {
		return nil
	}
	return unsafe.Slice(*(**T)(unsafe.Pointer(&p)), n)
}

// Convert a Go string into an owned `wasm_byte_vec_t`
func stringToByteVec(s string) wasm_byte_vec_t {
	vec := wasm_byte_vec_t{}
	wasm_byte_vec_new(&vec, uintptr(len(s)), goStrPtr(s))
	runtime.KeepAlive(s)
	return vec
}

package wasmtime

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// Go replacements for the C shims of the original cgo bindings (`shims.c`).
//
// Wasmtime calls back into Go through purego callbacks. purego can only ever
// create a bounded number of callbacks per process, so a fixed set is created
// once here and the per-function state is looked up through the `env` value
// Wasmtime hands back, exactly like the cgo version did.
//
// Every callback returns a uintptr, even the ones standing in for C functions
// returning `void`: Windows callbacks must return a pointer-sized value, and
// the extra return value is ignored by C callers elsewhere.

var (
	trampolineNewCallback = purego.NewCallback(func(
		env uintptr,
		caller *wasmtime_caller_t,
		args uintptr,
		nargs uintptr,
		results uintptr,
		nresults uintptr,
	) *wasm_trap_t {
		return goTrampolineNew(caller, env, args, nargs, results, nresults)
	})

	trampolineWrapCallback = purego.NewCallback(func(
		env uintptr,
		caller *wasmtime_caller_t,
		args uintptr,
		nargs uintptr,
		results uintptr,
		nresults uintptr,
	) *wasm_trap_t {
		return goTrampolineWrap(caller, env, args, nargs, results, nresults)
	})

	finalizeStoreCallback     = voidCallback(goFinalizeStore)
	finalizeFuncNewCallback   = voidCallback(goFinalizeFuncNew)
	finalizeFuncWrapCallback  = voidCallback(goFinalizeFuncWrap)
	finalizeExternrefCallback = voidCallback(goFinalizeExternref)
)

// voidCallback creates a callback for a C `void (*)(void *)` function.
func voidCallback(f func(env uintptr)) uintptr {
	return purego.NewCallback(func(env uintptr) uintptr {
		f(env)
		return 0
	})
}

func go_store_new(engine *wasm_engine_t, env uintptr) *wasmtime_store_t {
	return wasmtime_store_new(engine, env, finalizeStoreCallback)
}

func go_func_new(
	store *wasmtime_context_t,
	ty *wasm_functype_t,
	env uintptr,
	wrap int32,
	ret *wasmtime_func_t,
) {
	callback := trampolineNewCallback
	if wrap != 0 {
		callback = trampolineWrapCallback
	}
	wasmtime_func_new(store, ty, callback, env, 0, ret)
}

func go_linker_define_func(
	linker *wasmtime_linker_t,
	module *byte,
	module_len uintptr,
	name *byte,
	name_len uintptr,
	ty *wasm_functype_t,
	wrap int32,
	env uintptr,
) *wasmtime_error_t {
	cb := trampolineNewCallback
	finalizer := finalizeFuncNewCallback
	if wrap != 0 {
		cb = trampolineWrapCallback
		finalizer = finalizeFuncWrapCallback
	}
	return wasmtime_linker_define_func(linker, module, module_len, name, name_len, ty, cb, env, finalizer)
}

func go_externref_new(cx *wasmtime_context_t, env uintptr, ref *wasmtime_externref_t) bool {
	return wasmtime_externref_new(cx, env, finalizeExternrefCallback, ref)
}

func go_component_item_kind(it *wasmtime_component_item_t) uint8 {
	return it.kind
}

func go_component_item_get_type(it *wasmtime_component_item_t, out *wasmtime_component_valtype_t) {
	wasmtime_component_valtype_clone((*wasmtime_component_valtype_t)(unsafe.Pointer(&it.of)), out)
}

func go_component_valtype_kind(vt *wasmtime_component_valtype_t) uint8 {
	return vt.kind
}

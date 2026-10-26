package wasmtime

import "runtime"

// GC will clean up any `externref` values that are no longer actually
// referenced.
//
// This function is not required to be called for correctness, it's only an
// optimization if desired to clean out any extra `externref` values.
func (store *Store) GC() {
	wasmtime_context_gc(store.Context())
	runtime.KeepAlive(store)
}

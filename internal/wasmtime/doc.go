/*
Package wasmtime is a WebAssembly runtime for Go powered by Wasmtime.

This is a vendored port of github.com/bytecodealliance/wasmtime-go (v50) that
calls the Wasmtime C API through github.com/ebitengine/purego instead of cgo,
so it builds with CGO_ENABLED=0 and cross-compiles without a C toolchain. The
public API matches upstream; see https://pkg.go.dev/github.com/bytecodealliance/wasmtime-go
and the Rust documentation at https://docs.rs/wasmtime.

# Native library

The Wasmtime C API shared library is loaded by the first call to LoadError,
which must precede any other use of the package. By default the library for
the target platform is embedded in the binary (see the embedded package) and
extracted on first use into the user cache directory.
Set the QJS_WASMTIME_LIB environment variable to load a library from a
different path instead, and build with the `wasmtime_noembed` tag to leave the
embedded copy out of the binary. The library version must match
embedded.Version.

Loading never panics: LoadError returns a descriptive error when the library
is unavailable.

# Differences from upstream

  - Storelike.Context and AsExtern return this package's mirrors of the C
    types instead of cgo types.
  - Linux builds need glibc; the embedded libraries do not support musl.

# Updating Wasmtime

Run embedded/update.sh with the new version, bump embedded.Version, then
regenerate ffi_bindings.go with gen/gen_bindings.py against the headers of
the same release and re-check the struct layouts in ffi.go.
*/
package wasmtime

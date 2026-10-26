// Package embedded carries a zstd-compressed Wasmtime C API shared library for
// the target platform so the wasmtime bindings work without a system install.
//
// Build with the `wasmtime_noembed` tag to leave the library out of the binary;
// the bindings then have to be pointed at a library on disk instead.
package embedded

// Version is the Wasmtime release the embedded libraries were taken from.
const Version = "v50.0.0"

// Digest returns a short hex prefix of the SHA-256 of the compressed library,
// precomputed by update.sh so it doesn't have to be hashed at startup.
func Digest() string {
	return digest
}

// Compressed returns the zstd-compressed shared library, or nil when none is
// embedded for this platform.
func Compressed() []byte {
	return compressed
}

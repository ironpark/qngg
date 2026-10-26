//go:build linux && amd64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed linux_amd64/libwasmtime.so.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "33a10dccfe8ae03c"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "libwasmtime.so"

//go:build linux && arm64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed linux_arm64/libwasmtime.so.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "fa511e72e13617d0"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "libwasmtime.so"

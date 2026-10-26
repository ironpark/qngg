//go:build darwin && arm64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed darwin_arm64/libwasmtime.dylib.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "ae555f8e3b01a9d3"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "libwasmtime.dylib"

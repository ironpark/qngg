//go:build darwin && amd64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed darwin_amd64/libwasmtime.dylib.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "7f81eece9439f7f6"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "libwasmtime.dylib"

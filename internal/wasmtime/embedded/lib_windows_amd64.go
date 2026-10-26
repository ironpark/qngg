//go:build windows && amd64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed windows_amd64/wasmtime.dll.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "78fa1e1df4df937b"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "wasmtime.dll"

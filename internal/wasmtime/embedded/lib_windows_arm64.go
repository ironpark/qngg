//go:build windows && arm64 && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed windows_arm64/wasmtime.dll.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "e668932a268897f7"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "wasmtime.dll"

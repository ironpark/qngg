//go:build wasmtime_noembed || !((darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && (amd64 || arm64)))

package embedded

var compressed []byte

const digest = ""

// Name is empty when no Wasmtime shared library is embedded for this platform.
const Name = ""

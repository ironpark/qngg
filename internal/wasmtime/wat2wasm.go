package wasmtime

import (
	"runtime"
)

// Wat2Wasm converts the text format of WebAssembly to the binary format.
//
// Takes the text format in-memory as input, and returns either the binary
// encoding of the text format or an error if parsing fails.
func Wat2Wasm(wat string) ([]byte, error) {
	retVec := wasm_byte_vec_t{}
	err := wasmtime_wat2wasm(
		goStrPtr(wat),
		uintptr(len(wat)),
		&retVec,
	)
	runtime.KeepAlive(wat)

	if err == nil {
		ret := goBytes(retVec.data, int(retVec.size))
		wasm_byte_vec_delete(&retVec)
		return ret, nil
	}

	return nil, mkError(err)
}

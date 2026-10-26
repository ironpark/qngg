//go:build windows

package wasmtime

import "syscall"

func openLibrary(path string) (uintptr, error) {
	handle, err := syscall.LoadLibrary(path)
	return uintptr(handle), err
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(handle), name)
}

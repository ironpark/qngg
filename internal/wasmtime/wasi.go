package wasmtime

import (
	"errors"
	"runtime"
)

type WasiConfig struct {
	_ptr *wasi_config_t
}

func NewWasiConfig() *WasiConfig {
	ptr := wasi_config_new()
	config := &WasiConfig{_ptr: ptr}
	runtime.SetFinalizer(config, func(config *WasiConfig) {
		config.Close()
	})
	return config
}

func (c *WasiConfig) ptr() *wasi_config_t {
	ret := c._ptr
	if ret == nil {
		panic("object has been closed already")
	}
	maybeGC()
	return ret
}

// Close will deallocate this WASI configuration's state explicitly.
//
// For more information see the documentation for engine.Close()
func (c *WasiConfig) Close() {
	if c._ptr == nil {
		return
	}
	runtime.SetFinalizer(c, nil)
	wasi_config_delete(c._ptr)
	c._ptr = nil

}

// SetArgv will explicitly configure the argv for this WASI configuration.
// Note that this field can only be set, it cannot be read
func (c *WasiConfig) SetArgv(argv []string) {
	ptrs := make([]*byte, len(argv))
	for i, arg := range argv {
		ptrs[i] = cString(arg)
	}
	var argvRaw **byte
	if len(ptrs) > 0 {
		argvRaw = &ptrs[0]
	}
	wasi_config_set_argv(c.ptr(), uintptr(len(argv)), argvRaw)
	runtime.KeepAlive(c)
	runtime.KeepAlive(ptrs)
}

func (c *WasiConfig) InheritArgv() {
	wasi_config_inherit_argv(c.ptr())
	runtime.KeepAlive(c)
}

// SetEnv configures environment variables to be returned for this WASI configuration.
// The pairs provided must be an iterable list of key/value pairs of environment variables.
// Note that this field can only be set, it cannot be read
func (c *WasiConfig) SetEnv(keys, values []string) {
	if len(keys) != len(values) {
		panic("mismatched numbers of keys and values")
	}
	namePtrs := make([]*byte, len(values))
	valuePtrs := make([]*byte, len(values))
	for i := range keys {
		namePtrs[i] = cString(keys[i])
		valuePtrs[i] = cString(values[i])
	}
	var namesRaw, valuesRaw **byte
	if len(keys) > 0 {
		namesRaw = &namePtrs[0]
		valuesRaw = &valuePtrs[0]
	}
	wasi_config_set_env(c.ptr(), uintptr(len(keys)), namesRaw, valuesRaw)
	runtime.KeepAlive(c)
	runtime.KeepAlive(namePtrs)
	runtime.KeepAlive(valuePtrs)
}

func (c *WasiConfig) InheritEnv() {
	wasi_config_inherit_env(c.ptr())
	runtime.KeepAlive(c)
}

func (c *WasiConfig) SetStdinFile(path string) error {
	ok := wasi_config_set_stdin_file(c.ptr(), path)
	runtime.KeepAlive(c)
	if ok {
		return nil
	}

	return errors.New("failed to open file")
}

func (c *WasiConfig) InheritStdin() {
	wasi_config_inherit_stdin(c.ptr())
	runtime.KeepAlive(c)
}

func (c *WasiConfig) SetStdoutFile(path string) error {
	ok := wasi_config_set_stdout_file(c.ptr(), path)
	runtime.KeepAlive(c)
	if ok {
		return nil
	}

	return errors.New("failed to open file")
}

func (c *WasiConfig) InheritStdout() {
	wasi_config_inherit_stdout(c.ptr())
	runtime.KeepAlive(c)
}

func (c *WasiConfig) SetStderrFile(path string) error {
	ok := wasi_config_set_stderr_file(c.ptr(), path)
	runtime.KeepAlive(c)
	if ok {
		return nil
	}

	return errors.New("failed to open file")
}

func (c *WasiConfig) InheritStderr() {
	wasi_config_inherit_stderr(c.ptr())
	runtime.KeepAlive(c)
}

type WasiDirPerms uint8
type WasiFilePerms uint8

func (c *WasiConfig) PreopenDir(path, guestPath string, fsMutable bool) error {
	ok := wasi_config_preopen_dir(c.ptr(), path, guestPath, fsMutable)
	runtime.KeepAlive(c)
	if ok {
		return nil
	}

	return errors.New("failed to preopen directory")
}

package wasmtime

import "runtime"

// SetParallelCompilation configures whether compilation should use multiple threads
func (cfg *Config) SetParallelCompilation(enabled bool) {
	wasmtime_config_parallel_compilation_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

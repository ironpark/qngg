package wasmtime

import "runtime"

// SetWasmThreads configures whether the wasm threads proposal is enabled
func (cfg *Config) SetWasmThreads(enabled bool) {
	wasmtime_config_wasm_threads_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

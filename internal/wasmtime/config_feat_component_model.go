package wasmtime

import "runtime"

// SetWasmComponentModel configures whether the wasm component model proposal is
// enabled.
func (cfg *Config) SetWasmComponentModel(enabled bool) {
	wasmtime_config_wasm_component_model_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetConcurrencySupport configures whether support for concurrent execution of
// WebAssembly is enabled for stores using this configuration.
func (cfg *Config) SetConcurrencySupport(enabled bool) {
	wasmtime_config_concurrency_support_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

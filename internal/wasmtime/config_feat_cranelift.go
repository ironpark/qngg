package wasmtime

import (
	"runtime"
)

// SetStrategy configures what compilation strategy is used to compile wasm code
func (cfg *Config) SetStrategy(strat Strategy) {
	wasmtime_config_strategy_set(cfg.ptr(), wasmtime_strategy_t(strat))
	runtime.KeepAlive(cfg)
}

// SetCraneliftDebugVerifier configures whether the cranelift debug verifier will be active when
// cranelift is used to compile wasm code.
func (cfg *Config) SetCraneliftDebugVerifier(enabled bool) {
	wasmtime_config_cranelift_debug_verifier_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetCraneliftOptLevel configures the cranelift optimization level for generated code
func (cfg *Config) SetCraneliftOptLevel(level OptLevel) {
	wasmtime_config_cranelift_opt_level_set(cfg.ptr(), wasmtime_opt_level_t(level))
	runtime.KeepAlive(cfg)
}

// SetCraneliftNanCanonicalization configures whether whether Cranelift should perform a
// NaN-canonicalization pass.
//
// When Cranelift is used as a code generation backend this will configure it to replace NaNs with a single
// canonical value. This is useful for users requiring entirely deterministic WebAssembly computation.
//
// This is not required by the WebAssembly spec, so it is not enabled by default.
func (cfg *Config) SetCraneliftNanCanonicalization(enabled bool) {
	wasmtime_config_cranelift_nan_canonicalization_set(cfg.ptr(), bool(enabled))
	runtime.KeepAlive(cfg)
}

// EnableCraneliftFlag enables a target-specific flag in Cranelift.
//
// This can be used, for example, to enable SSE4.2 on x86_64 hosts. Settings can
// be explored with `wasmtime settings` on the CLI.
//
// For more information see the Rust documentation at
// https://docs.wasmtime.dev/api/wasmtime/struct.Config.html#method.cranelift_flag_enable
func (cfg *Config) EnableCraneliftFlag(flag string) {
	cstr := flag
	wasmtime_config_cranelift_flag_enable(cfg.ptr(), cstr)
	runtime.KeepAlive(cfg)
}

// SetCraneliftFlag sets a target-specific flag in Cranelift to the specified value.
//
// This can be used, for example, to enable SSE4.2 on x86_64 hosts. Settings can
// be explored with `wasmtime settings` on the CLI.
//
// For more information see the Rust documentation at
// https://docs.wasmtime.dev/api/wasmtime/struct.Config.html#method.cranelift_flag_set
func (cfg *Config) SetCraneliftFlag(name string, value string) {
	cstrName := name
	cstrValue := value
	wasmtime_config_cranelift_flag_set(cfg.ptr(), cstrName, cstrValue)
	runtime.KeepAlive(cfg)
}

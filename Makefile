.PHONY: build build-debug clean test lint

# Toolchain used to build qjs.wasm: wasi-sdk 34 and binaryen 133.
WASI_SDK ?= /opt/wasi-sdk
WASM_OPT ?= wasm-opt
BUILD_DIR := qjswasm/quickjs/build

CMAKE_FLAGS := \
		-DQJS_BUILD_LIBC=ON \
		-DQJS_BUILD_CLI_WITH_MIMALLOC=OFF \
		-DWASI_SDK_PREFIX=$(WASI_SDK) \
		-DCMAKE_TOOLCHAIN_FILE=$(WASI_SDK)/share/cmake/wasi-sdk-p1.cmake \
		-DCMAKE_PROJECT_INCLUDE=$(CURDIR)/qjswasm/qjswasm.cmake

define build_wasm
	@echo "Configuring and building qjs..."
	rm -rf $(BUILD_DIR)
	cmake -S qjswasm/quickjs -B $(BUILD_DIR) $(CMAKE_FLAGS) $(1)
	cmake --build $(BUILD_DIR) --target qjswasm --parallel
	$(WASM_OPT) -O3 --strip-dwarf --strip-producers $(BUILD_DIR)/qjswasm -o qjs.wasm
endef

build:
	$(call build_wasm,)

build-debug:
	$(call build_wasm,-DQJS_DEBUG_RUNTIME_ADDRESS=ON)

clean:
	rm -rf $(BUILD_DIR)

test:
	./test.sh

lint:
	golangci-lint run

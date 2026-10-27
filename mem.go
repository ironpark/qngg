package qngg

import wasmtime "github.com/ironpark/wasmtime-pure-go"

// wasmMemory gives Go access to the linear memory of the QuickJS module.
type wasmMemory struct {
	store *wasmtime.Store
	mem   *wasmtime.Memory
	// data caches a view of the linear memory. The engine is configured so
	// memory never moves, so only the length can become outdated when wasm
	// grows the memory; it is refreshed whenever an access falls outside it.
	data []byte
}

// Size returns the current size of the memory in bytes.
func (m *wasmMemory) Size() uint32 {
	m.data = m.mem.UnsafeData(m.store)

	return uint32(len(m.data))
}

// view returns the `size` bytes of linear memory at `addr`, backed by the
// memory itself, or false if the range is out of bounds.
func (m *wasmMemory) view(addr, size uint32) ([]byte, bool) {
	end := uint64(addr) + uint64(size)
	if end > uint64(len(m.data)) {
		m.data = m.mem.UnsafeData(m.store)
		if end > uint64(len(m.data)) {
			return nil, false
		}
	}

	return m.data[addr:end:end], true
}

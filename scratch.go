package qngg

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Temporary buffers for the arguments and results of C calls, such as
// property names and argument arrays, come from a scratch stack in wasm
// memory allocated once per runtime, which saves a malloc and a free call
// per buffer. Calls nest when JavaScript calls back into Go, so buffers must
// be released in the reverse order of their allocation, which deferring the
// release right after the allocation ensures. Buffers that do not fit fall
// back to malloc.

const (
	// scratchSize is the size of the scratch stack.
	scratchSize = 64 << 10
	// outBufSize is the size of the buffers receiving strings and bytes from
	// C helpers; larger results are returned in memory of their own.
	outBufSize = 4 << 10
	// outFailed is the result of a C helper returning a buffer that failed,
	// leaving an exception pending.
	outFailed = math.MaxUint64
)

type scratch struct {
	base uint32 // address of the stack in wasm memory
	top  uint32 // offset of the first free byte
}

// tmp is a temporary buffer from Runtime.alloc.
type tmp struct {
	ptr uint32
	top uint32 // scratch top to restore on drop
	// heap is set for buffers that did not fit and were malloc'd
	heap bool
}

// alloc returns a temporary buffer of n bytes, aligned for 64-bit values.
// The caller releases it with drop.
func (r *Runtime) alloc(n uint32) tmp {
	s := &r.scratch

	start := (s.top + 7) &^ 7
	if uint64(start)+uint64(n) <= scratchSize {
		t := tmp{ptr: s.base + start, top: s.top}
		s.top = start + n

		return t
	}

	return tmp{ptr: uint32(r.malloc(uint64(max(n, 1)))), heap: true}
}

// drop releases a buffer from alloc.
func (r *Runtime) drop(t tmp) {
	if t.heap {
		r.free(uint64(t.ptr))

		return
	}

	r.scratch.top = t.top
}

// view returns the n bytes of wasm memory at ptr. They are only valid until
// the next call into wasm.
func (r *Runtime) view(ptr, n uint32) []byte {
	buf, ok := r.mem.view(ptr, n)
	if !ok {
		panic(fmt.Errorf("%w: %d bytes at %#x", errIndexOutOfRange, n, ptr))
	}

	return buf
}

// push copies b into a temporary buffer, without a NUL terminator.
func (r *Runtime) push[T string | []byte](b T) tmp {
	t := r.alloc(uint32(len(b)))
	copy(r.view(t.ptr, uint32(len(b))), b)

	return t
}

// pushCString copies s into a temporary buffer as a NUL-terminated string.
func (r *Runtime) pushCString(s string) tmp {
	t := r.alloc(uint32(len(s) + 1))
	buf := r.view(t.ptr, uint32(len(s)+1))
	buf[copy(buf, s)] = 0

	return t
}

// pushValues copies JSValues into a temporary array.
func (r *Runtime) pushValues(raws ...uint64) tmp {
	t := r.alloc(uint32(8 * len(raws)))
	buf := r.view(t.ptr, uint32(8*len(raws)))

	for i, raw := range raws {
		binary.LittleEndian.PutUint64(buf[8*i:], raw)
	}

	return t
}

// callOut calls a C helper taking the context and args, and returning
// data in a buffer passed as its last two arguments (see helpers.c). It
// calls fn with the data, which is only valid during the call. release
// frees data that did not fit in the buffer. It reports false if the
// helper failed, leaving an exception pending.
func (r *Runtime) callOut(x export, release func(ptr uint32), fn func([]byte), args ...uint64) bool {
	buf := r.alloc(outBufSize)
	defer r.drop(buf)

	var argv [6]uint64

	argv[0] = r.jsCtx
	n := 1 + copy(argv[1:], args)
	argv[n], argv[n+1] = uint64(buf.ptr), outBufSize

	res := r.call(x, argv[:n+2]...)
	if res == outFailed {
		return false
	}

	ptr, size := uint32(res>>32), uint32(res)
	if ptr == 0 {
		fn(r.view(buf.ptr, size))

		return true
	}

	fn(r.view(ptr, size))
	release(ptr)

	return true
}

// callString is callOut for helpers returning strings from JS_ToCString and
// friends.
func (r *Runtime) callString(x export, arg uint64) (s string, ok bool) {
	ok = r.callOut(x, r.freeCString, func(b []byte) { s = string(b) }, arg)

	return s, ok
}

func (r *Runtime) freeCString(ptr uint32) {
	r.call(xFreeCString, r.jsCtx, uint64(ptr))
}

func (r *Runtime) freePtr(ptr uint32) {
	r.free(uint64(ptr))
}

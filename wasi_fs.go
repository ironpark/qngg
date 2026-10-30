package qngg

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"weak"

	wasmtime "github.com/ironpark/wasmtime-pure-go"
)

// WASI preview1 ABI values, independent of the host OS:
// https://github.com/WebAssembly/WASI/blob/snapshot-01/phases/snapshot/docs.md
const (
	wasiAcces       = 2
	wasiBadf        = 8
	wasiBusy        = 10
	wasiExist       = 20
	wasiFault       = 21
	wasiIntr        = 27
	wasiInval       = 28
	wasiIO          = 29
	wasiIsdir       = 31
	wasiMfile       = 33
	wasiNametoolong = 37
	wasiNoent       = 44
	wasiNospc       = 51
	wasiNotdir      = 54
	wasiNotempty    = 55
	wasiNotsup      = 58
	wasiRofs        = 69
	wasiSpipe       = 70
	wasiNotcapable  = 76

	wasiFiletypeDirectory = 3
	wasiFiletypeRegular   = 4
	wasiFiletypeSymlink   = 7

	wasiOflagCreat     = 1
	wasiOflagDirectory = 2
	wasiOflagExcl      = 4
	wasiOflagTrunc     = 8

	wasiFdflagAppend   = 1
	wasiFdflagNonblock = 4

	wasiRead      = uint64(1 << 1)
	wasiSeek      = uint64(1 << 2)
	wasiWrite     = uint64(1 << 6)
	wasiAllRights = uint64(1<<30 - 1)

	wasiFDWrite = "fd_write"
	wasiFDTell  = "fd_tell"
)

type wasiFile struct {
	file    File
	path    string
	isDir   bool
	rights  uint64
	flags   uint16
	entries []os.FileInfo // snapshot for fd_readdir cookies
}

type wasiFS struct {
	fs         FS
	writable   bool
	files      map[uint32]*wasiFile
	nextFD     uint32
	stdout     io.Writer
	stderr     io.Writer
	stdClosed  [3]bool
	clockStart time.Time
	memory     *wasmtime.Memory // looked up on the first call
}

func newWasiFS(fsys FS, writable bool, stdout, stderr io.Writer) (*wasiFS, error) {
	root, err := fsys.Open(string(filepath.Separator))
	if err != nil {
		return nil, fmt.Errorf("open filesystem root: %w", err)
	}

	info, err := root.Stat()
	if err != nil || !info.IsDir() {
		_ = root.Close()

		if err == nil {
			err = syscall.ENOTDIR
		}

		return nil, fmt.Errorf("stat filesystem root: %w", err)
	}

	s := &wasiFS{
		fs: fsys, writable: writable, nextFD: 4, stdout: stdout, stderr: stderr,
		clockStart: time.Now(),
		files:      map[uint32]*wasiFile{3: {file: root, path: "/", isDir: true, rights: wasiAllRights}},
	}
	// Linker callbacks use a weak Runtime reference, so abandoned runtimes
	// can be collected. Their outstanding backend handles need cleanup too.
	runtime.SetFinalizer(s, (*wasiFS).close)

	return s, nil
}

func (s *wasiFS) close() {
	for _, f := range s.files {
		_ = f.file.Close()
	}

	clear(s.files)
}

// defineFilesystem replaces filesystem and polling imports. Clocks share
// the polling time base. Native WASI must never receive a virtual fd.
func (r *Runtime) defineFilesystem(module *wasmtime.Module) error {
	var err error

	r.filesystem, err = newWasiFS(r.config.FS, r.config.WritableFS,
		cmp.Or[io.Writer](r.config.Stdout, io.Discard), cmp.Or[io.Writer](r.config.Stderr, io.Discard))
	if err != nil {
		return err
	}

	r.linker.AllowShadowing(true)
	defer r.linker.AllowShadowing(false)

	weakRuntime := weak.Make(r)

	for _, imp := range module.Imports() {
		namePtr := imp.Name()
		if imp.Module() != "wasi_snapshot_preview1" || namePtr == nil {
			continue
		}

		name := *namePtr
		if !strings.HasPrefix(name, "fd_") && !strings.HasPrefix(name, "path_") &&
			name != "poll_oneoff" && name != "clock_time_get" {
			continue
		}

		ty := imp.Type().FuncType()
		params := ty.Params()

		err = r.linker.FuncNewUnchecked(imp.Module(), name, ty,
			func(caller *wasmtime.Caller, vals []wasmtime.ValRaw) *wasmtime.Trap {
				rt := weakRuntime.Value()
				if rt == nil {
					return wasmtime.NewTrap("qjs runtime has been released")
				}

				trap := rt.callFilesystem(caller, vals, params, name)

				runtime.KeepAlive(rt)

				return trap
			})
		if err != nil {
			return fmt.Errorf("link %s: %w", name, err)
		}
	}

	return nil
}

// callFilesystem runs the WASI import name with the raw arguments vals.
func (r *Runtime) callFilesystem(
	caller *wasmtime.Caller, vals []wasmtime.ValRaw, params []*wasmtime.ValType, name string,
) *wasmtime.Trap {
	var args [10]uint64

	for i, param := range params {
		if param.Kind() == wasmtime.KindI32 {
			args[i] = uint64(uint32(vals[i].I32()))
		} else {
			args[i] = uint64(vals[i].I64())
		}
	}

	fsys := r.filesystem
	if fsys.memory == nil {
		fsys.memory = caller.GetExport("memory").Memory()
	}

	call := wasiFSCall{fs: fsys, mem: fsys.memory.UnsafeData(caller), args: args}
	if r.interrupt.ctx != nil {
		call.done = r.interrupt.ctx.Done()
	}

	errno := call.run(name)
	// Trap now: the epoch bump that would stop wasm runs asynchronously,
	// so wasm could otherwise finish before observing the interruption.
	if call.interrupted && !r.config.DisableInterruption {
		if _, err := r.interrupt.check(); err != nil {
			return wasmtime.NewTrap(err.Error())
		}
	}

	vals[0] = wasmtime.ValRawI32(int32(errno))

	return nil
}

type wasiFSCall struct {
	fs   *wasiFS
	mem  []byte
	args [10]uint64
	done <-chan struct{}
	// interrupted reports that a poll was cut short by done.
	interrupted bool
}

func (c *wasiFSCall) bytes(addr, size uint64) []byte {
	if addr > uint64(len(c.mem)) || size > uint64(len(c.mem))-addr {
		return nil
	}

	return c.mem[addr : addr+size : addr+size]
}

// resolve confines paths to the directory descriptor and returns the backend
// path with its info, which is nil if the path does not exist. Symbolic links
// are rejected rather than allowing a BasePathFs over OsFs to escape its base.
// As with other path-based operations, callers must not concurrently replace path
// components with symlinks in an OS-backed filesystem.
func (c *wasiFSCall) resolve(fd, addr, size uint64) (string, os.FileInfo, uint32) {
	f := c.fs.files[uint32(fd)]
	if f == nil {
		return "", nil, wasiBadf
	}

	if !f.isDir {
		return "", nil, wasiNotdir
	}

	b := c.bytes(addr, size)
	if b == nil {
		return "", nil, wasiFault
	}

	name := string(b)
	if name == "" {
		return "", nil, wasiNoent
	}

	if strings.ContainsRune(name, 0) {
		return "", nil, wasiInval
	}

	if strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return "", nil, wasiNotcapable
	}

	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", nil, wasiNotcapable
	}

	resolved := path.Join(f.path, clean)

	info, errno := c.fs.checkPath(resolved)
	if errno != 0 {
		return "", nil, errno
	}

	if strings.HasSuffix(name, "/") {
		if info == nil {
			return "", nil, wasiNoent
		}

		if !info.IsDir() {
			return "", nil, wasiNotdir
		}
	}

	return filepath.FromSlash(resolved), info, 0
}

// checkPath checks the complete backend path, including the descriptor's
// ancestors, and returns the info of its final component. Symbolic links are
// rejected, so that info is the same as Stat's.
func (s *wasiFS) checkPath(resolved string) (os.FileInfo, uint32) {
	var info os.FileInfo

	// resolved is clean, so each prefix ending before a "/" is an ancestor.
	// FromSlash maps bytes one to one, so the same prefixes apply to native.
	native := filepath.FromSlash(resolved)
	for i := 1; i <= len(resolved); i++ {
		if i < len(resolved) && resolved[i] != '/' {
			continue
		}

		last := i == len(resolved)

		var err error

		info, _, err = lstatIfPossible(s.fs, native[:i])
		if errors.Is(err, fs.ErrNotExist) && last {
			return nil, 0 // creation is allowed; the operation checks existence
		}

		if err != nil {
			return nil, wasiErrno(err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return nil, wasiNotcapable
		}

		if !last && !info.IsDir() {
			return nil, wasiNotdir
		}
	}

	return info, 0
}

func wasiErrno(err error) uint32 {
	switch {
	case err == nil, errors.Is(err, io.EOF):
		return 0
	case errors.Is(err, fs.ErrNotExist):
		return wasiNoent
	case errors.Is(err, fs.ErrExist):
		return wasiExist
	case errors.Is(err, fs.ErrPermission):
		return wasiAcces
	case errors.Is(err, fs.ErrClosed), errors.Is(err, syscall.EBADF):
		return wasiBadf
	case errors.Is(err, fs.ErrInvalid), errors.Is(err, syscall.EINVAL):
		return wasiInval
	case errors.Is(err, syscall.ENOTDIR):
		return wasiNotdir
	case errors.Is(err, syscall.EISDIR):
		return wasiIsdir
	case errors.Is(err, syscall.ENOTEMPTY):
		return wasiNotempty
	case errors.Is(err, syscall.ENOSPC):
		return wasiNospc
	case errors.Is(err, syscall.EROFS):
		return wasiRofs
	default:
		return wasiIO
	}
}

func wasiFiletype(info os.FileInfo) byte {
	switch {
	case info.IsDir():
		return wasiFiletypeDirectory
	case info.Mode().IsRegular():
		return wasiFiletypeRegular
	case info.Mode()&os.ModeSymlink != 0:
		return wasiFiletypeSymlink
	default:
		return 0
	}
}

func wasiFilestat(b []byte, info os.FileInfo) {
	clear(b)
	b[16] = wasiFiletype(info)
	binary.LittleEndian.PutUint64(b[24:], 1) // nlink
	binary.LittleEndian.PutUint64(b[32:], uint64(info.Size()))
	// Portable FileInfo only provides modification time.
	mtime := uint64(info.ModTime().UnixNano())
	binary.LittleEndian.PutUint64(b[40:], mtime)
	binary.LittleEndian.PutUint64(b[48:], mtime)
	binary.LittleEndian.PutUint64(b[56:], mtime)
}

// wasiRight returns the right needed to read or write a descriptor.
func wasiRight(write bool) uint64 {
	if write {
		return wasiWrite
	}

	return wasiRead
}

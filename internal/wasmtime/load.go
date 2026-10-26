package wasmtime

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ironpark/qngg/internal/atomicfile"
	"github.com/ironpark/qngg/internal/wasmtime/embedded"
	"github.com/klauspost/compress/zstd"
)

// Version is the Wasmtime release this package binds.
const Version = embedded.Version

// LibraryPathEnv names an environment variable that, when set, points at a
// Wasmtime C API shared library to load instead of the embedded one.
const LibraryPathEnv = "QNGG_WASMTIME_LIB"

// # How the native library is bound
//
// Every C function used by this package is declared as a Go function variable
// and registered through `bind` from a package-level variable initializer, so
// the full symbol table is known once the package is initialized. The first
// call to `LoadError` then opens the shared library and wires every variable
// to its symbol with `purego.RegisterFunc`. Loading lazily keeps programs that
// import the package but never use it from paying for it at startup.
//
// Loading never panics. If the library cannot be found or opened the bound
// variables stay nil and `LoadError` reports why. Until `LoadError` has been
// called every bound function is nil.

type symbol struct {
	fptr any
	name string
}

var (
	symbols  []symbol
	libPath  string
	loadOnce = sync.OnceValue(load)
)

// bind records that the function variable pointed to by `fptr` must be bound
// to the C symbol `name` once the library is loaded. If `fptr` is a *uintptr
// it receives the raw symbol address instead, for use with purego.SyscallN on
// hot paths where the reflection behind purego.RegisterFunc is too costly.
func bind(fptr any, name string) struct{} {
	symbols = append(symbols, symbol{fptr: fptr, name: name})
	return struct{}{}
}

// LoadError loads the Wasmtime shared library on first use and returns the
// error encountered while doing so, or nil if it was loaded successfully. It
// must be called before anything else in this package.
func LoadError() error {
	return loadOnce()
}

// LibraryPath returns the path of the loaded Wasmtime shared library.
func LibraryPath() string {
	loadOnce()
	return libPath
}

func load() error {
	path, err := resolveLibrary()
	if err != nil {
		return fmt.Errorf("wasmtime: %w", err)
	}

	handle, err := openLibrary(path)
	if err != nil {
		return fmt.Errorf("wasmtime: failed to open %s: %w", path, err)
	}

	var missing []string
	for _, s := range symbols {
		addr, err := lookupSymbol(handle, s.name)
		if err != nil || addr == 0 {
			missing = append(missing, s.name)
			continue
		}
		if raw, ok := s.fptr.(*uintptr); ok {
			*raw = addr
			continue
		}
		purego.RegisterFunc(s.fptr, addr)
	}

	libPath = path
	if len(missing) > 0 {
		return fmt.Errorf("wasmtime: %s is missing symbols %v", path, missing)
	}

	return nil
}

// resolveLibrary returns the path of the shared library to load: the one named
// by LibraryPathEnv if set, otherwise the embedded library extracted to disk.
func resolveLibrary() (string, error) {
	if path := os.Getenv(LibraryPathEnv); path != "" {
		return path, nil
	}

	compressed := embedded.Compressed()
	if len(compressed) == 0 {
		return "", fmt.Errorf(
			"no embedded library for this platform; set %s to the path of a Wasmtime %s C API shared library",
			LibraryPathEnv, embedded.Version,
		)
	}

	return extractLibrary(compressed)
}

// extractLibrary decompresses the embedded library into a per-version cache
// directory, reusing a previous extraction when one is present.
func extractLibrary(compressed []byte) (string, error) {
	dirName := embedded.Version + "-" + embedded.Digest()

	var errs []error
	for _, base := range cacheBases() {
		dir := filepath.Join(base, "qngg", "wasmtime", dirName)
		path := filepath.Join(dir, embedded.Name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}

		if err := writeLibrary(dir, path, compressed); err != nil {
			errs = append(errs, err)
			continue
		}

		return path, nil
	}

	return "", fmt.Errorf("failed to extract embedded library: %w", errors.Join(errs...))
}

func cacheBases() []string {
	var bases []string
	if dir, err := os.UserCacheDir(); err == nil {
		bases = append(bases, dir)
	}

	return append(bases, os.TempDir())
}

// writeLibrary decompresses the library to `path` atomically, so concurrent
// processes never observe a partial file.
func writeLibrary(dir, path string, compressed []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dec, err := zstd.NewReader(bytes.NewReader(compressed), zstd.WithDecoderMaxWindow(1<<27))
	if err != nil {
		return err
	}
	defer dec.Close()

	return atomicfile.Write(path, 0o755, func(w io.Writer) error {
		_, err := dec.WriteTo(w)
		return err
	})
}

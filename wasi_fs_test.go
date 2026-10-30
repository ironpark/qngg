package qngg

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wasmTestBackend accepts the absolute paths used by the WASI bridge while
// keeping test files under an os.Root. Other adapter tests use os.Root directly.
type wasmTestBackend struct{ *os.Root }

func relativeTestPath(name string) string {
	name = strings.TrimPrefix(name, string(filepath.Separator))
	if name == "" {
		return "."
	}
	return name
}
func (s *wasmTestBackend) Open(name string) (*os.File, error) {
	return s.Root.Open(relativeTestPath(name))
}
func (s *wasmTestBackend) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return s.Root.OpenFile(relativeTestPath(name), flag, perm)
}
func (s *wasmTestBackend) Stat(name string) (os.FileInfo, error) {
	return s.Root.Stat(relativeTestPath(name))
}
func (s *wasmTestBackend) Lstat(name string) (os.FileInfo, error) {
	return s.Root.Lstat(relativeTestPath(name))
}

func TestWASIFilesystemMemory(t *testing.T) {
	root := newTestRoot(t)
	require.NoError(t, root.Mkdir("dir", 0o755))
	rt, err := New(Config{FS: AdaptFS(&wasmTestBackend{Root: root})})
	require.NoError(t, err)
	defer rt.Close()

	c := wasiFSCall{fs: rt.filesystem, mem: make([]byte, 128)}
	copy(c.mem, "dir")
	c.args = [10]uint64{3, 0, 0, 3, 2, wasiRead, 0, 0, 120}
	require.EqualValues(t, 0, c.run("path_open"))
	fd := binary.LittleEndian.Uint32(c.mem[120:])
	copy(c.mem, "../outside")
	c.args = [10]uint64{uint64(fd), 0, 0, 10, 0, wasiRead, 0, 0, 120}
	require.EqualValues(t, wasiNotcapable, c.run("path_open"))
	c.args = [10]uint64{3, 0, 127, 10, 0, wasiRead, 0, 0, 120}
	require.EqualValues(t, wasiFault, c.run("path_open"))
	c.args = [10]uint64{3, 127}
	require.EqualValues(t, wasiFault, c.run("fd_fdstat_get"))
	c.args = [10]uint64{12345, 0}
	require.EqualValues(t, wasiBadf, c.run("fd_fdstat_get"))
}

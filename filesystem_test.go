package qngg

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdaptFSDelegation(t *testing.T) {
	root := newTestRoot(t)
	fsys := AdaptFS(root)
	require.Equal(t, root.Name(), fsys.Name())
	require.NoError(t, fsys.MkdirAll("nested/dir", 0o755))
	f, err := fsys.Create("nested/dir/file")
	require.NoError(t, err)
	// The adapter returns the original concrete file, preserving every file
	// operation and avoiding another wrapper with a reduced method set.
	require.IsType(t, (*os.File)(nil), f)
	_, err = f.WriteString("original")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	b, err := root.ReadFile("nested/dir/file")
	require.NoError(t, err)
	require.Equal(t, "original", string(b))

	f, err = fsys.OpenFile("nested/dir/file", os.O_RDWR, 0o600)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte("O"), 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	f, err = fsys.Open("nested/dir/file")
	require.NoError(t, err)
	b, err = io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "Original", string(b))
	require.NoError(t, f.Close())

	modified := time.Unix(1700000000, 0)
	require.NoError(t, fsys.Chtimes("nested/dir/file", modified, modified))
	info, err := root.Stat("nested/dir/file")
	require.NoError(t, err)
	require.Equal(t, modified, info.ModTime())
	require.NoError(t, fsys.Chmod("nested/dir/file", 0o644))
	require.NoError(t, fsys.Rename("nested/dir/file", "nested/dir/renamed"))
	_, err = root.Stat("nested/dir/file")
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, fsys.Remove("nested/dir/renamed"))
	require.NoError(t, fsys.Mkdir("empty", 0o755))
	require.NoError(t, fsys.RemoveAll("nested"))
	_, err = root.Stat("nested")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestAdaptFSOpenErrorsAndNil(t *testing.T) {
	root := newTestRoot(t)
	fsys := AdaptFS(root)
	for _, open := range []func() (File, error){
		func() (File, error) { return fsys.Create("missing/file") },
		func() (File, error) { return fsys.Open("missing") },
		func() (File, error) { return fsys.OpenFile("missing", os.O_RDONLY, 0) },
	} {
		file, err := open()
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.Nil(t, file, "an open error must not return an interface holding a nil *os.File")
	}
	var backend FSBackend[*os.File]
	require.Nil(t, AdaptFS(backend))
}

type lstatTestBackend struct {
	FSBackend[*os.File]
	info   os.FileInfo
	err    error
	called string
}

func (s *lstatTestBackend) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	s.called = name
	return s.info, true, s.err
}

type standardLstatTestBackend struct {
	FSBackend[*os.File]
	called string
	err    error
}

func (s *standardLstatTestBackend) Lstat(name string) (os.FileInfo, error) {
	s.called = name
	if s.err != nil {
		return nil, s.err
	}
	return s.Stat(name)
}

func TestAdaptFSLstatDelegation(t *testing.T) {
	root := newTestRoot(t)
	info, err := root.Stat(".")
	require.NoError(t, err)
	backend := &lstatTestBackend{FSBackend: root, info: info}
	fsys := AdaptFS(backend)
	lstater, ok := fsys.(Lstater)
	require.True(t, ok)
	got, supported, err := lstater.LstatIfPossible("sentinel")
	require.NoError(t, err)
	require.True(t, supported)
	require.Same(t, info, got)
	require.Equal(t, "sentinel", backend.called)
	backend.err = fs.ErrPermission
	_, _, err = lstater.LstatIfPossible("denied")
	require.True(t, errors.Is(err, backend.err))

	standard := &standardLstatTestBackend{FSBackend: root}
	lstater = AdaptFS(standard).(Lstater)
	got, supported, err = lstater.LstatIfPossible(".")
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, info.Mode(), got.Mode())
	require.Equal(t, ".", standard.called)
	standard.err = fs.ErrPermission
	_, _, err = lstater.LstatIfPossible("denied")
	require.ErrorIs(t, err, fs.ErrPermission)

	// Embedding only FSBackend hides os.Root's optional Lstat method.
	statOnly := struct{ FSBackend[*os.File] }{root}
	lstater = AdaptFS(statOnly).(Lstater)
	got, supported, err = lstater.LstatIfPossible(".")
	require.NoError(t, err)
	require.False(t, supported)
	require.Equal(t, info.Mode(), got.Mode())
	_, supported, err = lstater.LstatIfPossible("missing")
	require.False(t, supported)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

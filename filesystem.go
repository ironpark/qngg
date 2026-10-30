package qngg

import (
	"io"
	"os"
	"time"
)

// File is an open file in a filesystem. It has the same method set as
// afero.File, using only standard-library types.
type File interface { //nolint:interfacebloat // preserve the complete file method set for backend compatibility
	io.Closer
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Writer
	io.WriterAt

	Name() string
	Readdir(count int) ([]os.FileInfo, error)
	Readdirnames(n int) ([]string, error)
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(size int64) error
	WriteString(s string) (int, error)
}

// FS is a filesystem exposed through Config.FS. Paths use the host's path
// separators, with the filesystem root addressed as "/" ("\\" on Windows).
// Implementations shared by several runtimes must support concurrent use.
type FS interface {
	FSBackend[File]
}

// FSBackend describes a filesystem whose open methods return F. Its method
// set matches afero.Fs; AdaptFS bridges the differing file return types.
type FSBackend[F File] interface { //nolint:interfacebloat // preserve the complete filesystem method set for delegation
	Create(name string) (F, error)
	Mkdir(name string, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	Open(name string) (F, error)
	OpenFile(name string, flag int, perm os.FileMode) (F, error)
	Remove(name string) error
	RemoveAll(path string) error
	Rename(oldname, newname string) error
	Stat(name string) (os.FileInfo, error)
	Name() string
	Chmod(name string, mode os.FileMode) error
	Chown(name string, uid, gid int) error
	Chtimes(name string, atime, mtime time.Time) error
}

// Lstater optionally exposes symbolic-link metadata. The boolean reports
// whether lstat was supported; false means Stat was used instead. Backends
// that support symbolic links should implement this or Lstat(string).
type Lstater interface {
	LstatIfPossible(name string) (os.FileInfo, bool, error)
}

// AdaptFS adapts a backend such as afero.Fs without requiring an afero
// dependency in qngg. Every operation is delegated to the original backend;
// returned files are the original handles, and optional lstat support is
// preserved. The caller retains ownership of the backend.
//
//	qngg.Config{FS: qngg.AdaptFS(afero.NewMemMapFs()), WritableFS: true}
func AdaptFS[F File](backend FSBackend[F]) FS {
	if backend == nil {
		return nil
	}

	return &fsAdapter[F]{FSBackend: backend}
}

type fsAdapter[F File] struct {
	FSBackend[F]
}

func (s *fsAdapter[F]) Create(name string) (File, error) {
	return asFile(s.FSBackend.Create(name))
}

func (s *fsAdapter[F]) Open(name string) (File, error) {
	return asFile(s.FSBackend.Open(name))
}

func (s *fsAdapter[F]) OpenFile(name string, flag int, perm os.FileMode) (File, error) {
	return asFile(s.FSBackend.OpenFile(name, flag, perm))
}

// asFile converts a backend file to File, returning a nil interface rather
// than a typed nil on error.
func asFile[F File](f F, err error) (File, error) {
	if err != nil {
		return nil, err
	}

	return f, nil
}

func (s *fsAdapter[F]) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	return lstatIfPossible(s.FSBackend, name)
}

func lstatIfPossible(backend interface {
	Stat(name string) (os.FileInfo, error)
}, name string) (os.FileInfo, bool, error) {
	if lstater, ok := backend.(Lstater); ok {
		return lstater.LstatIfPossible(name) //nolint:wrapcheck // preserve backend errors
	}

	if lstater, ok := backend.(interface {
		Lstat(name string) (os.FileInfo, error)
	}); ok {
		info, err := lstater.Lstat(name)

		return info, true, err //nolint:wrapcheck // preserve backend errors
	}

	info, err := backend.Stat(name)

	return info, false, err //nolint:wrapcheck // preserve backend errors
}

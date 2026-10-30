package qngg

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *wasiFSCall) run(name string) uint32 {
	a := c.args

	var f *wasiFile

	if strings.HasPrefix(name, "fd_") {
		if a[0] < 3 {
			return c.standardStream(name)
		}

		if f = c.fs.files[uint32(a[0])]; f == nil {
			return wasiBadf
		}
	}

	switch name {
	case "clock_time_get":
		return c.clockTime()
	case "poll_oneoff":
		return c.poll()
	case "fd_prestat_get", "fd_prestat_dir_name":
		return c.prestat(name)
	case "fd_close":
		err := f.file.Close()

		delete(c.fs.files, uint32(a[0]))

		return wasiErrno(err)
	case "fd_fdstat_get":
		return c.fdstat(f)
	case "fd_fdstat_set_flags":
		if a[1]&^uint64(wasiFdflagAppend|wasiFdflagNonblock) != 0 { // NONBLOCK: regular files
			return wasiNotsup
		}

		f.flags = uint16(a[1])

		return 0
	case "fd_read", wasiFDWrite:
		return c.readWrite(f, name == wasiFDWrite)
	case "fd_seek", wasiFDTell:
		return c.seek(f, name == wasiFDTell)
	case "fd_readdir":
		return c.readdir(f)
	case "fd_filestat_get":
		b := c.bytes(a[1], 64)
		if b == nil {
			return wasiFault
		}

		info, err := f.file.Stat()
		if err != nil {
			return wasiErrno(err)
		}

		wasiFilestat(b, info)

		return 0
	case "fd_sync", "fd_datasync":
		return wasiErrno(f.file.Sync())
	case "fd_filestat_set_size":
		if !c.fs.writable || f.rights&wasiWrite == 0 {
			return wasiNotcapable
		}

		if int64(a[1]) < 0 {
			return wasiInval
		}

		return wasiErrno(f.file.Truncate(int64(a[1])))
	case "path_open":
		return c.open()
	case "path_filestat_get":
		return c.pathStat()
	case "path_create_directory", "path_unlink_file", "path_remove_directory":
		return c.modifyPath(name)
	case "path_rename":
		return c.rename()
	case "path_filestat_set_times":
		return c.setTimes()
	default:
		return wasiNotsup
	}
}

func (c *wasiFSCall) pathStat() uint32 {
	a := c.args

	b := c.bytes(a[4], 64)
	if b == nil {
		return wasiFault
	}

	_, info, errno := c.resolve(a[0], a[2], a[3])
	if errno != 0 {
		return errno
	}

	if info == nil {
		return wasiNoent
	}

	wasiFilestat(b, info)

	return 0
}

func (c *wasiFSCall) fdstat(f *wasiFile) uint32 {
	b := c.bytes(c.args[1], 24)
	if b == nil {
		return wasiFault
	}

	info, err := f.file.Stat()
	if err != nil {
		return wasiErrno(err)
	}

	clear(b)
	b[0] = wasiFiletype(info)
	binary.LittleEndian.PutUint16(b[2:], f.flags)
	binary.LittleEndian.PutUint64(b[8:], f.rights)
	binary.LittleEndian.PutUint64(b[16:], wasiAllRights)

	return 0
}

func (c *wasiFSCall) prestat(name string) uint32 {
	a := c.args
	if a[0] != 3 {
		return wasiBadf
	}

	if name == "fd_prestat_get" {
		b := c.bytes(a[1], 8)
		if b == nil {
			return wasiFault
		}

		clear(b)
		binary.LittleEndian.PutUint32(b[4:], 1) // directory named "/"

		return 0
	}

	if a[2] < 1 {
		return wasiNametoolong
	}

	b := c.bytes(a[1], a[2])
	if b == nil {
		return wasiFault
	}

	b[0] = '/'

	return 0
}

func (c *wasiFSCall) open() uint32 {
	a := c.args

	result := c.bytes(a[8], 4)
	if result == nil {
		return wasiFault
	}

	oflags := a[4]
	if oflags&^uint64(wasiOflagCreat|wasiOflagDirectory|wasiOflagExcl|wasiOflagTrunc) != 0 || a[1]&^uint64(1) != 0 {
		return wasiInval
	}

	if a[7]&^uint64(wasiFdflagAppend|wasiFdflagNonblock) != 0 {
		return wasiNotsup
	}

	write := a[5]&wasiWrite != 0
	if !c.fs.writable && (write || oflags&(wasiOflagCreat|wasiOflagTrunc) != 0) {
		return wasiNotcapable
	}

	p, info, errno := c.resolve(a[0], a[2], a[3])
	if errno != 0 {
		return errno
	}

	flags := os.O_RDONLY
	if write {
		flags = os.O_WRONLY
		if a[5]&wasiRead != 0 {
			flags = os.O_RDWR
		}
	}

	if oflags&wasiOflagCreat != 0 {
		flags |= os.O_CREATE
	}

	if oflags&wasiOflagExcl != 0 {
		flags |= os.O_EXCL
	}

	if oflags&wasiOflagTrunc != 0 {
		flags |= os.O_TRUNC
	}

	isDir := info != nil && info.IsDir()
	if oflags&wasiOflagDirectory != 0 {
		if info == nil {
			return wasiNoent
		}

		if !isDir {
			return wasiNotdir
		}
	}

	if isDir && (write || oflags&wasiOflagTrunc != 0) {
		return wasiIsdir
	}

	file, err := c.fs.fs.OpenFile(p, flags, 0o666)
	if err != nil {
		return wasiErrno(err)
	}
	// Reuse closed descriptors and avoid wrapping onto the standard streams.
	fd := c.fs.nextFD
	for c.fs.files[fd] != nil {
		fd++
		if fd < 4 {
			fd = 4
		}

		if fd == c.fs.nextFD {
			_ = file.Close()

			return wasiMfile
		}
	}

	c.fs.files[fd] = &wasiFile{file: file, path: filepath.ToSlash(p), isDir: isDir, rights: a[5], flags: uint16(a[7])}
	c.fs.nextFD = fd // search from this descriptor next time
	binary.LittleEndian.PutUint32(result, fd)

	return 0
}

func (c *wasiFSCall) readWrite(f *wasiFile, write bool) uint32 {
	if (write && !c.fs.writable) || f.rights&wasiRight(write) == 0 {
		return wasiNotcapable
	}

	if f.isDir {
		return wasiIsdir
	}

	if !write {
		return c.transfer(f.file, nil)
	}

	if f.flags&wasiFdflagAppend != 0 {
		if _, err := f.file.Seek(0, io.SeekEnd); err != nil {
			return wasiErrno(err)
		}
	}

	return c.transfer(nil, f.file)
}

func (c *wasiFSCall) transfer(reader io.Reader, writer io.Writer) uint32 {
	a := c.args

	iovs, result := c.bytes(a[1], a[2]*8), c.bytes(a[3], 4)
	if iovs == nil || result == nil {
		return wasiFault
	}
	// Validate every buffer before performing I/O.
	for i := 0; i < len(iovs); i += 8 {
		addr, size := binary.LittleEndian.Uint32(iovs[i:]), binary.LittleEndian.Uint32(iovs[i+4:])
		if c.bytes(uint64(addr), uint64(size)) == nil {
			return wasiFault
		}
	}

	var total uint32

	for i := 0; i < len(iovs); i += 8 {
		addr, size := binary.LittleEndian.Uint32(iovs[i:]), binary.LittleEndian.Uint32(iovs[i+4:])
		b := c.bytes(uint64(addr), uint64(size))

		var (
			n   int
			err error
		)
		if writer != nil {
			n, err = writer.Write(b)
		} else {
			n, err = reader.Read(b)
		}

		total += uint32(n)
		if err != nil || n != len(b) {
			if total == 0 && wasiErrno(err) != 0 {
				return wasiErrno(err)
			}

			break
		}
	}

	binary.LittleEndian.PutUint32(result, total)

	return 0
}

func (c *wasiFSCall) standardStream(name string) uint32 {
	fd := c.args[0]
	if c.fs.stdClosed[fd] {
		return wasiBadf
	}

	switch name {
	case "fd_close":
		c.fs.stdClosed[fd] = true

		return 0
	case "fd_read":
		if fd != 0 {
			return wasiBadf
		}

		return c.transfer(strings.NewReader(""), nil)
	case wasiFDWrite:
		switch fd {
		case 1:
			return c.transfer(nil, c.fs.stdout)
		case 2:
			return c.transfer(nil, c.fs.stderr)
		default:
			return wasiBadf
		}
	case "fd_fdstat_get":
		b := c.bytes(c.args[1], 24)
		if b == nil {
			return wasiFault
		}

		clear(b)
		binary.LittleEndian.PutUint64(b[8:], wasiRight(fd != 0))

		return 0
	case "fd_seek", wasiFDTell:
		return wasiSpipe
	case "fd_sync", "fd_datasync", "fd_fdstat_set_flags":
		return 0
	default:
		return wasiBadf
	}
}

func (c *wasiFSCall) seek(f *wasiFile, tell bool) uint32 {
	offset, whence, out := int64(c.args[1]), int(c.args[2]), c.args[3]
	if tell {
		offset, whence, out = 0, io.SeekCurrent, c.args[1]
	}

	if f.rights&wasiSeek == 0 {
		return wasiNotcapable
	}

	result := c.bytes(out, 8)
	if result == nil {
		return wasiFault
	}

	if whence < 0 || whence > 2 {
		return wasiInval
	}

	pos, err := f.file.Seek(offset, whence)
	if err != nil {
		return wasiErrno(err)
	}

	binary.LittleEndian.PutUint64(result, uint64(pos))

	return 0
}

func (c *wasiFSCall) readdir(f *wasiFile) uint32 {
	a := c.args

	b, result := c.bytes(a[1], a[2]), c.bytes(a[4], 4)
	if b == nil || result == nil {
		return wasiFault
	}

	if !f.isDir {
		return wasiNotdir
	}

	if f.entries == nil {
		var err error

		f.entries, err = f.file.Readdir(-1)
		if err != nil {
			return wasiErrno(err)
		}
	}

	used := 0
	for i := a[3]; i < uint64(len(f.entries)) && used < len(b); i++ {
		entry := f.entries[i]

		var header [24]byte
		binary.LittleEndian.PutUint64(header[:], i+1)
		binary.LittleEndian.PutUint32(header[16:], uint32(len(entry.Name())))
		header[20] = wasiFiletype(entry)
		used += copy(b[used:], header[:])
		used += copy(b[used:], entry.Name())
	}

	binary.LittleEndian.PutUint32(result, uint32(used))

	return 0
}

func (c *wasiFSCall) modifyPath(name string) uint32 {
	if !c.fs.writable {
		return wasiNotcapable
	}

	p, info, errno := c.resolve(c.args[0], c.args[1], c.args[2])
	if errno != 0 {
		return errno
	}

	if name == "path_create_directory" {
		return wasiErrno(c.fs.fs.Mkdir(p, 0o777))
	}

	if info == nil {
		return wasiNoent
	}

	if name == "path_remove_directory" && !info.IsDir() {
		return wasiNotdir
	}

	if name == "path_unlink_file" && info.IsDir() {
		return wasiIsdir
	}

	if isRootPath(p) {
		return wasiBusy
	}

	if info.IsDir() {
		if errno := c.fs.checkEmptyDirectory(p); errno != 0 {
			return errno
		}
	}

	return wasiErrno(c.fs.fs.Remove(p))
}

func (s *wasiFS) checkEmptyDirectory(p string) uint32 {
	dir, err := s.fs.Open(p)
	if err != nil {
		return wasiErrno(err)
	}
	defer dir.Close()

	names, err := dir.Readdirnames(1)
	if len(names) != 0 {
		return wasiNotempty
	}

	return wasiErrno(err)
}

func (c *wasiFSCall) rename() uint32 {
	if !c.fs.writable {
		return wasiNotcapable
	}

	a := c.args

	oldPath, oldInfo, errno := c.resolve(a[0], a[1], a[2])
	if errno != 0 {
		return errno
	}

	newPath, newInfo, errno := c.resolve(a[3], a[4], a[5])
	if errno != 0 {
		return errno
	}

	if isRootPath(oldPath) || isRootPath(newPath) {
		return wasiBusy
	}

	if oldInfo == nil {
		return wasiNoent
	}

	if oldPath == newPath {
		return 0
	}

	if errno := c.fs.checkRename(oldPath, oldInfo, newPath, newInfo); errno != 0 {
		return errno
	}

	if err := c.fs.fs.Rename(oldPath, newPath); err != nil {
		return wasiErrno(err)
	}

	oldPath, newPath = filepath.ToSlash(oldPath), filepath.ToSlash(newPath)

	oldPrefix := oldPath + "/"
	for _, f := range c.fs.files {
		if f.path == oldPath || strings.HasPrefix(f.path, oldPrefix) {
			f.path = newPath + strings.TrimPrefix(f.path, oldPath)
		}
	}

	return 0
}

// isRootPath reports whether p, as returned by resolve, is the filesystem root.
func isRootPath(p string) bool {
	return p == string(filepath.Separator)
}

func (s *wasiFS) checkRename(oldPath string, oldInfo os.FileInfo, newPath string, newInfo os.FileInfo) uint32 {
	if oldInfo.IsDir() && strings.HasPrefix(filepath.ToSlash(newPath), filepath.ToSlash(oldPath)+"/") {
		return wasiInval
	}

	if newInfo == nil {
		return 0
	}

	if oldInfo.IsDir() && !newInfo.IsDir() {
		return wasiNotdir
	}

	if !oldInfo.IsDir() && newInfo.IsDir() {
		return wasiIsdir
	}

	if newInfo.IsDir() {
		return s.checkEmptyDirectory(newPath)
	}

	return 0
}

func (c *wasiFSCall) setTimes() uint32 {
	if !c.fs.writable {
		return wasiNotcapable
	}

	a := c.args

	p, info, errno := c.resolve(a[0], a[2], a[3])
	if errno != 0 {
		return errno
	}

	flags := a[6]
	if flags&^uint64(15) != 0 || flags&3 == 3 || flags&12 == 12 {
		return wasiInval
	}

	if info == nil {
		return wasiNoent
	}

	atime, mtime := info.ModTime(), info.ModTime()
	now := time.Now()

	if flags&1 != 0 {
		atime = time.Unix(0, int64(a[4]))
	} else if flags&2 != 0 {
		atime = now
	}

	if flags&4 != 0 {
		mtime = time.Unix(0, int64(a[5]))
	} else if flags&8 != 0 {
		mtime = now
	}

	return wasiErrno(c.fs.fs.Chtimes(p, atime, mtime))
}

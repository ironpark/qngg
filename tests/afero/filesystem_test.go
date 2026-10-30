package aferotest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ironpark/qngg"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func evalFS(t *testing.T, rt *qngg.Runtime, code string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, err := rt.Eval(ctx, "test.js", code)
	require.NoError(t, err)
	defer v.Free()
	return v.String()
}

func TestAferoFileAPIs(t *testing.T) {
	fsys := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fsys, "/hello.txt", []byte("hello"), 0o644))
	var stdout, stderr bytes.Buffer
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys), WritableFS: true, Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err)
	defer rt.Close()

	require.Equal(t, "hello", evalFS(t, rt, `(() => {
		const f = std.open('/hello.txt', 'r');
		if (!f) throw Error('open failed');
		const text = f.readAsString(); f.close(); return text;
	})()`))
	require.Equal(t, "0", evalFS(t, rt, `(() => {
		const f = std.open('hello.txt', 'w');
		if (!f) throw Error('write open failed');
		f.puts('새 내용'); f.flush(); return f.close();
	})()`))
	b, err := afero.ReadFile(fsys, "/hello.txt")
	require.NoError(t, err)
	require.Equal(t, "새 내용", string(b))

	require.Equal(t, "새 내용!", evalFS(t, rt, `(() => {
		const f = std.open('/hello.txt', 'a'); f.seek(0, std.SEEK_SET);
		f.puts('!'); f.close();
		return std.loadFile('/hello.txt');
	})()`))
	require.Equal(t, "3,1,2,3,0", evalFS(t, rt, `(() => {
		const fd = os.open('/binary', os.O_CREAT | os.O_RDWR | os.O_TRUNC);
		if (fd < 0) throw Error('os.open: ' + fd);
		const data = new Uint8Array([1,2,3]);
		if (os.write(fd, data.buffer, 0, 3) !== 3) throw Error('write failed');
		if (os.seek(fd, 0, std.SEEK_SET) !== 0) throw Error('seek failed');
		const result = new Uint8Array(3);
		const n = os.read(fd, result.buffer, 0, 3);
		return [n, ...result, os.close(fd)].join(',');
	})()`))
	require.Equal(t, "0,0,true,0,0,0", evalFS(t, rt, `(() => {
		const created = os.mkdir('/dir');
		const renamed = os.rename('/binary', '/dir/moved');
		const [entries, errno] = os.readdir('/dir');
		const [stat, statErr] = os.stat('/dir/moved');
		if (errno || statErr || stat.size !== 3) throw Error('directory/stat failed');
		const removed = os.remove('/dir/moved');
		const removedDir = os.remove('/dir');
		return [created, renamed, entries.includes('moved'), errno, removed, removedDir].join(',');
	})()`))
	require.Equal(t, "0,1700000000000", evalFS(t, rt, `(() => {
		const result = os.utimes('/hello.txt', 1700000000000, 1700000000000);
		const [stat, errno] = os.stat('/hello.txt');
		if (errno) throw Error('stat failed');
		return [result, stat.mtime].join(',');
	})()`))
	require.Equal(t, "true,true,true,true", evalFS(t, rt, `(() => {
		const badRead = os.open('/missing', os.O_RDONLY) < 0;
		const exclusive = os.open('/hello.txt', os.O_CREAT | os.O_EXCL | os.O_WRONLY) < 0;
		const invalid = os.close(12345) < 0;
		const notDir = os.readdir('/hello.txt')[1] !== 0;
		return [badRead, exclusive, invalid, notDir].join(',');
	})()`))

	evalFS(t, rt, `console.log('out'); std.err.puts('err'); std.err.flush();`)
	require.Equal(t, "out\n", stdout.String())
	require.Equal(t, "err", stderr.String())
}

func TestAferoReadOnly(t *testing.T) {
	for _, backendReadOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(backendReadOnly), func(t *testing.T) {
			base := afero.NewMemMapFs()
			require.NoError(t, afero.WriteFile(base, "/hello", []byte("unchanged"), 0o644))
			var fsys afero.Fs = base
			if backendReadOnly {
				fsys = afero.NewReadOnlyFs(base)
			}
			rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys), WritableFS: backendReadOnly})
			require.NoError(t, err)
			defer rt.Close()
			require.Equal(t, "unchanged", evalFS(t, rt, `std.loadFile('/hello')`))
			require.Equal(t, "true", evalFS(t, rt, `(() => {
				const failures = [
					std.open('/hello', 'w') === null,
					std.open('/hello', 'a') === null,
					os.open('/new', os.O_CREAT | os.O_WRONLY) < 0,
					os.mkdir('/dir') < 0,
					os.remove('/hello') < 0,
					os.rename('/hello', '/renamed') < 0,
					os.utimes('/hello', 0, 0) < 0,
				];
				return failures.every(Boolean);
			})()`))
			b, err := afero.ReadFile(base, "/hello")
			require.NoError(t, err)
			require.Equal(t, "unchanged", string(b))
		})
	}
}

func TestAferoPolling(t *testing.T) {
	fsys := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fsys, "/hello", []byte("hello"), 0o644))
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys), WritableFS: true})
	require.NoError(t, err)
	defer rt.Close()
	require.Equal(t, "104", evalFS(t, rt, `(() => {
		const fd = os.open('/hello', os.O_RDONLY);
		return new Promise(resolve => os.setReadHandler(fd, () => {
			os.setReadHandler(fd, null);
			const b = new Uint8Array(1);
			os.read(fd, b.buffer, 0, 1); os.close(fd); resolve(b[0]);
		}));
	})()`))
	require.Equal(t, "1", evalFS(t, rt, `(() => {
		const fd = os.open('/written', os.O_CREAT | os.O_WRONLY);
		return new Promise(resolve => os.setWriteHandler(fd, () => {
			os.setWriteHandler(fd, null);
			const n = os.write(fd, new Uint8Array([42]).buffer, 0, 1);
			os.close(fd); resolve(n);
		}));
	})()`))
	require.Equal(t, "true", evalFS(t, rt, `(async () => {
		const before = os.now(), wall = Date.now();
		await os.sleepAsync(5);
		os.sleep(2);
		return os.now() > before && Date.now() >= wall;
	})()`))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = rt.Eval(ctx, "sleep.js", `os.sleep(30000)`)
	require.ErrorIs(t, err, qngg.ErrInterrupted)
}

func TestAferoModules(t *testing.T) {
	fsys := afero.NewMemMapFs()
	require.NoError(t, fsys.MkdirAll("/app/lib", 0o755))
	for name, source := range map[string]string{
		"/app/main.js":         `import {answer} from './lib/answer.js'; export default answer;`,
		"/app/lib/answer.js":   `import data from './answer.json' with {type:'json'}; export const answer = data.value;`,
		"/app/lib/answer.json": `{"value":42}`,
		"/script.js":           `std.loadFile('/app/lib/answer.json')`,
		"/module.js":           `export const value = 7;`,
	} {
		require.NoError(t, afero.WriteFile(fsys, name, []byte(source), 0o644))
	}
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys)})
	require.NoError(t, err)
	defer rt.Close()
	v, err := rt.EvalFile(context.Background(), "/app/main.js", qngg.AsModule())
	require.NoError(t, err)
	require.Equal(t, int32(42), v.Int32())
	v.Free()
	code, err := rt.CompileFile("/script.js")
	require.NoError(t, err)
	v, err = rt.EvalBytecode(context.Background(), code)
	require.NoError(t, err)
	require.Equal(t, `{"value":42}`, v.String())
	v.Free()
	require.NoError(t, rt.LoadModuleFile(context.Background(), "/module.js"))
	v, err = rt.Eval(context.Background(), "import.js", `import {value} from '/module.js'; export default value`, qngg.AsModule())
	require.NoError(t, err)
	require.Equal(t, int32(7), v.Int32())
	v.Free()
}

func TestAferoBasePath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "secret"), []byte("secret"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "visible"), []byte("visible"), 0o644))
	require.NoError(t, os.Symlink(base, filepath.Join(root, "escape")))
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(afero.NewBasePathFs(afero.NewOsFs(), root)), WritableFS: true})
	require.NoError(t, err)
	defer rt.Close()
	require.Equal(t, "visible", evalFS(t, rt, `std.loadFile('/visible')`))
	require.Equal(t, "true", evalFS(t, rt, `[
		std.loadFile('/../secret') === null,
		std.loadFile('/escape/secret') === null,
		os.open('/escape/new', os.O_CREAT | os.O_WRONLY) < 0,
		os.rename('/visible', '/escape/moved') < 0,
	].every(Boolean)`))
	require.FileExists(t, filepath.Join(root, "visible"))
	require.NoFileExists(t, filepath.Join(base, "new"))
}

func TestAferoConfiguration(t *testing.T) {
	_, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(afero.NewMemMapFs()), CWD: t.TempDir()})
	require.ErrorContains(t, err, "mutually exclusive")
	_, err = qngg.New(qngg.Config{FS: qngg.AdaptFS(afero.NewBasePathFs(afero.NewMemMapFs(), "/missing"))})
	require.ErrorContains(t, err, "filesystem root")
}

func TestAferoDirectoryErrors(t *testing.T) {
	fsys := afero.NewMemMapFs()
	require.NoError(t, fsys.MkdirAll("/dir/child", 0o755))
	require.NoError(t, afero.WriteFile(fsys, "/file", []byte("keep"), 0o644))
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys), WritableFS: true})
	require.NoError(t, err)
	defer rt.Close()
	require.Equal(t, "true", evalFS(t, rt, `(() => {
		const fd = os.open('/dir', os.O_RDONLY);
		const b = new ArrayBuffer(1);
		const errors = [
			os.remove('/dir') < 0,
			os.rename('/dir', '/file') < 0,
			os.rename('/file', '/dir') < 0,
			os.rename('/dir', '/dir/child/nested') < 0,
			os.stat('/file/')[1] !== 0,
			os.open('/file/', os.O_RDONLY) < 0,
			os.read(fd, b, 0, 1) < 0,
		];
		os.close(fd);
		return errors.every(Boolean);
	})()`))
	info, err := fsys.Stat("/dir/child")
	require.NoError(t, err)
	require.True(t, info.IsDir())
	b, err := afero.ReadFile(fsys, "/file")
	require.NoError(t, err)
	require.Equal(t, "keep", string(b))
}

func TestAferoLargeDirectory(t *testing.T) {
	fsys := afero.NewMemMapFs()
	require.NoError(t, fsys.MkdirAll("/dir", 0o755))
	for i := range 300 {
		require.NoError(t, afero.WriteFile(fsys, fmt.Sprintf("/dir/%03d-%s", i, strings.Repeat("x", 100)), nil, 0o644))
	}
	rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys)})
	require.NoError(t, err)
	defer rt.Close()
	require.Equal(t, "300,0", evalFS(t, rt, `(() => {
		const [entries, err] = os.readdir('/dir');
		return [entries.length, err].join(',');
	})()`))
}

type trackedFS struct {
	afero.Fs
	closed chan string
}

func (s *trackedFS) Open(name string) (afero.File, error) {
	f, err := s.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &trackedFile{File: f, closed: s.closed}, nil
}

func (s *trackedFS) OpenFile(name string, flags int, perm os.FileMode) (afero.File, error) {
	f, err := s.Fs.OpenFile(name, flags, perm)
	if err != nil {
		return nil, err
	}
	return &trackedFile{File: f, closed: s.closed}, nil
}

type trackedFile struct {
	afero.File
	closed chan string
}

func (f *trackedFile) Close() error {
	f.closed <- f.Name()
	return f.File.Close()
}

func TestAferoHandleCleanup(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			closed := make(chan string, 20)
			fsys := &trackedFS{Fs: afero.NewMemMapFs(), closed: closed}
			require.NoError(t, afero.WriteFile(fsys.Fs, "/hello", []byte("hello"), 0o644))
			func() {
				rt, err := qngg.New(qngg.Config{FS: qngg.AdaptFS(fsys)})
				require.NoError(t, err)
				require.Equal(t, "true", evalFS(t, rt, `globalThis.file = std.open('/hello', 'r'); file !== null`))
				if explicit {
					require.NoError(t, rt.Close())
					require.NoError(t, rt.Close())
				}
			}()
			require.Eventually(t, func() bool {
				runtime.GC()
				return len(closed) == 2 // root and still-open hello
			}, 3*time.Second, 10*time.Millisecond)
			// The filesystem itself remains usable and caller-owned.
			b, err := afero.ReadFile(fsys.Fs, "/hello")
			require.NoError(t, err)
			require.Equal(t, "hello", string(b))
		})
	}
}

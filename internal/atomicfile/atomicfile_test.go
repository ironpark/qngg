package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	err := Write(path, 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, "content")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "content" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWriteFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	want := errors.New("boom")
	if err := Write(filepath.Join(dir, "file"), 0o644, func(io.Writer) error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, got %v", entries)
	}
}

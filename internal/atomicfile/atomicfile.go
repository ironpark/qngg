// Package atomicfile writes files so that readers, including other processes,
// never observe partially written content.
package atomicfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Write creates `path` with permissions `perm` and the content produced by
// `write`. The content is written to a temporary file in the same directory
// and renamed into place.
//
// If the rename fails because another process already created `path` (which
// Windows reports when the existing file is open), the existing file is kept
// and Write succeeds: callers use it for content-addressed caches where any
// complete copy is as good as another.
func Write(path string, perm os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := write(tmp); err != nil {
		tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmp.Name(), err)
	}

	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("failed to set permissions of %s: %w", tmp.Name(), err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return nil
		}

		return fmt.Errorf("failed to move %s into place: %w", path, err)
	}

	return nil
}

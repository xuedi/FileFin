// Package fsutil holds small filesystem helpers shared across the app.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a uniquely-named temp file in the same
// directory plus a rename, so a concurrent reader never observes a half-written file and
// a crashed write never leaves a corrupt target. The temp file is removed on every error
// path. perm is applied to the temp file before the rename. The temp lives beside the
// target (not in $TMPDIR) so the rename is same-filesystem and therefore atomic.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	return nil
}

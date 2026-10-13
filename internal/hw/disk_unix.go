//go:build linux || darwin

package hw

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
)

// FreeBytes reports the space an unprivileged user can still write on the
// filesystem that holds path. A path that does not exist yet is measured on the
// filesystem of its nearest existing ancestor, where it would be created.
func FreeBytes(path string) (uint64, error) {
	p := filepath.Clean(path)
	for {
		var st syscall.Statfs_t
		err := syscall.Statfs(p, &st)
		if err == nil {
			// Bavail excludes blocks reserved for root, which a model pull cannot use.
			// Field widths differ between Linux and macOS, hence the conversions.
			return uint64(st.Bavail) * uint64(st.Bsize), nil
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return 0, fmt.Errorf("free space of %s: %w", path, err)
		}
		p = parent
	}
}

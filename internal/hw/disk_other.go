//go:build !linux && !darwin

package hw

import (
	"errors"
	"fmt"
	"runtime"
)

// FreeBytes is not implemented on this OS; Windows support is decided in M8.
func FreeBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("free space on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}

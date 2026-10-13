//go:build !linux && !darwin

package hw

import (
	"errors"
	"fmt"
	"runtime"
)

// Detect is not implemented on this OS; Windows support is decided in M8.
func Detect() (Info, error) {
	return Info{OS: runtime.GOOS, Arch: runtime.GOARCH}, fmt.Errorf("hardware detection on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}

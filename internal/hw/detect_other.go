//go:build !linux

package hw

import (
	"errors"
	"fmt"
	"runtime"
)

// Detect is not implemented on this OS yet; macOS follows Linux in M1.
func Detect() (Info, error) {
	return Info{OS: runtime.GOOS, Arch: runtime.GOARCH}, fmt.Errorf("hardware detection on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}

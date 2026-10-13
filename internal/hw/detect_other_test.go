//go:build !linux

package hw

import (
	"errors"
	"testing"
)

func TestDetectIsUnsupportedOffLinux(t *testing.T) {
	if _, err := Detect(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Detect() error = %v, want errors.ErrUnsupported until this OS is implemented", err)
	}
}

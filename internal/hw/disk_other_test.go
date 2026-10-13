//go:build !linux && !darwin

package hw

import (
	"errors"
	"testing"
)

func TestFreeBytesIsUnsupportedHere(t *testing.T) {
	if _, err := FreeBytes("."); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("FreeBytes error = %v, want errors.ErrUnsupported", err)
	}
}

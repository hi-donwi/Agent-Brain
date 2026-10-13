//go:build darwin

package hw

import (
	"runtime"
	"testing"
)

// Runs against the real host on macOS CI runners and developer Macs.
func TestDetectOnThisMac(t *testing.T) {
	info, err := Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.Memory.TotalBytes == 0 || info.CPU.Logical == 0 || info.CPU.Model == "" {
		t.Errorf("Detect() = %+v, want memory, CPU count, and CPU model", info)
	}
	if runtime.GOARCH == "arm64" && info.Accelerator != AcceleratorMetal {
		t.Errorf("Accelerator = %q on Apple Silicon, want %q", info.Accelerator, AcceleratorMetal)
	}
	t.Logf("detected: %+v", info)
}

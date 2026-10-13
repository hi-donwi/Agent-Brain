//go:build linux

package hw

import "testing"

// Runs against the real host on Linux CI runners: a smoke test that the live
// paths and the parsers agree, not an assertion about specific hardware.
func TestDetectOnThisLinuxHost(t *testing.T) {
	info, err := Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.Memory.TotalBytes == 0 || info.Memory.AvailableBytes == 0 {
		t.Errorf("Memory = %+v, want non-zero", info.Memory)
	}
	if info.CPU.Logical == 0 {
		t.Errorf("CPU = %+v, want at least one logical CPU", info.CPU)
	}
	if info.GPUs == nil {
		t.Error("GPUs must be an empty list, not nil, so JSON shows []")
	}
	t.Logf("detected: %+v", info)
}

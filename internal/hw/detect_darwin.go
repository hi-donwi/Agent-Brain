//go:build darwin

package hw

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"time"
)

// Detect describes the running macOS host.
func Detect() (Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// sysctl exits non-zero when one name is unknown (older macOS) but still
	// prints the others, which is enough.
	sysctl, err := exec.CommandContext(ctx, "sysctl", darwinSysctlKeys...).Output()
	var exitErr *exec.ExitError
	if err != nil && !(errors.As(err, &exitErr) && len(sysctl) > 0) {
		return Info{}, err
	}
	vmStat, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		vmStat = nil
	}
	return DetectDarwin(sysctl, vmStat, runtime.GOARCH)
}

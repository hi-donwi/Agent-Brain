//go:build linux

package hw

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Detect describes the running Linux host.
func Detect() (Info, error) {
	return DetectLinux(os.DirFS("/"), runtime.GOARCH, runNvidiaSMI)
}

func runNvidiaSMI() ([]byte, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	// A wedged driver can hang nvidia-smi; detection must not hang with it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
}

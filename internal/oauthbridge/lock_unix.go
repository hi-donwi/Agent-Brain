//go:build !windows

package oauthbridge

import (
	"os"
	"syscall"
)

func lockProcessFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func terminateProcess(process *os.Process) error { return process.Signal(syscall.SIGTERM) }

package oauthbridge

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockProcessFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func terminateProcess(process *os.Process) error { return process.Kill() }

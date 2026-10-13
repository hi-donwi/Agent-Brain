package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
)

// The lokal home holds the index database, traces, and skills, not models, which
// live with their backend and are checked against model sizes separately.
const (
	homeDiskWarnBelow = 2 << 30
	homeDiskFailBelow = 512 << 20
)

// Default is the check list `lokal doctor` runs on this host.
func Default() []Check {
	return []Check{
		HardwareCheck(hw.Detect),
		HomeCheck(home.Default),
		HomeDiskCheck(home.Default, hw.FreeBytes),
	}
}

var fixHomeEnv = fmt.Sprintf("set %s to an absolute path, or unset it to use ~/.lokal", home.EnvVar)

// HardwareCheck summarises what detection found and surfaces its warnings.
func HardwareCheck(detect func() (hw.Info, error)) Check {
	return Check{ID: "hardware", Title: "Hardware", Run: func(context.Context) Outcome {
		info, err := detect()
		if errors.Is(err, errors.ErrUnsupported) {
			return Outcome{Severity: Warn, Message: err.Error() + "; the profile cannot be chosen automatically"}
		}
		if err != nil {
			return Outcome{Severity: Fail, Message: "hardware detection failed: " + err.Error()}
		}
		msg := describe(info)
		if len(info.Warnings) > 0 {
			return Outcome{Severity: Warn, Message: msg + "; " + strings.Join(info.Warnings, "; ")}
		}
		return Outcome{Severity: OK, Message: msg}
	}}
}

func describe(info hw.Info) string {
	model := info.CPU.Model
	if model == "" {
		model = "unknown CPU"
	}
	avail := "available unknown"
	if info.Memory.AvailableBytes > 0 {
		avail = HumanBytes(info.Memory.AvailableBytes) + " available"
	}
	accel := info.Accelerator
	if accel == "" {
		accel = hw.AcceleratorNone
	}
	s := fmt.Sprintf("%s, %d logical CPUs, %s memory (%s), accelerator %s",
		model, info.CPU.Logical, HumanBytes(info.Memory.TotalBytes), avail, accel)
	if info.Virtualization.Guest {
		s += fmt.Sprintf(", VM (%s)", info.Virtualization.Hypervisor)
	}
	for _, g := range info.GPUs {
		s += fmt.Sprintf(", GPU %s (%s)", g.Name, HumanBytes(g.MemoryBytes))
	}
	return s
}

// HomeCheck verifies the lokal home resolves, is a writable directory when it
// exists, and is not open to other users.
func HomeCheck(resolve func() (home.Layout, error)) Check {
	return Check{ID: "home", Title: "lokal home", Run: func(context.Context) Outcome {
		l, err := resolve()
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error(), Fix: fixHomeEnv}
		}
		st, err := os.Stat(l.Root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return Outcome{Severity: OK, Message: l.Root + " not created yet; lokal setup creates it"}
		case err != nil:
			return Outcome{Severity: Fail, Message: err.Error()}
		case !st.IsDir():
			return Outcome{Severity: Fail, Message: l.Root + " exists but is not a directory",
				Fix: "move it away, or " + fixHomeEnv}
		}
		probe, err := os.CreateTemp(l.Root, ".doctor-*")
		if err != nil {
			return Outcome{Severity: Fail, Message: l.Root + " is not writable: " + err.Error(),
				Fix: "chmod u+rwx " + l.Root}
		}
		name := probe.Name()
		probe.Close()
		os.Remove(name)
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			return Outcome{Severity: Warn,
				Message: fmt.Sprintf("%s is accessible by other users (mode %04o); it holds the gateway token and traces", l.Root, st.Mode().Perm()),
				Fix:     "chmod 700 " + l.Root}
		}
		return Outcome{Severity: OK, Message: l.Root + " is writable"}
	}}
}

// HomeDiskCheck verifies there is room for the index database and traces.
func HomeDiskCheck(resolve func() (home.Layout, error), freeBytes func(string) (uint64, error)) Check {
	return Check{ID: "disk", Title: "Free disk", Run: func(context.Context) Outcome {
		l, err := resolve()
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error(), Fix: fixHomeEnv}
		}
		free, err := freeBytes(l.Root)
		if errors.Is(err, errors.ErrUnsupported) {
			return Outcome{Severity: Warn, Message: "free disk space cannot be measured on this OS"}
		}
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error()}
		}
		msg := fmt.Sprintf("%s free on the filesystem holding %s", HumanBytes(free), l.Root)
		fix := "free disk space, or " + fixHomeEnv + " on a larger disk"
		switch {
		case free < homeDiskFailBelow:
			return Outcome{Severity: Fail, Message: msg + ", below the " + HumanBytes(homeDiskFailBelow) + " minimum", Fix: fix}
		case free < homeDiskWarnBelow:
			return Outcome{Severity: Warn, Message: msg + ", below the recommended " + HumanBytes(homeDiskWarnBelow), Fix: fix}
		}
		return Outcome{Severity: OK, Message: msg}
	}}
}

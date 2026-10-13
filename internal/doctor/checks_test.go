package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
)

const gib = 1024 * 1024 * 1024

func run(c Check) Outcome { return c.Run(context.Background()) }

func TestHardwareCheckSummarisesTheHost(t *testing.T) {
	info := hw.Info{
		OS: "linux", Arch: "amd64",
		CPU:            hw.CPU{Model: "Intel Xeon Processor (Cascadelake)", Logical: 4},
		Memory:         hw.Memory{TotalBytes: 16 * gib, AvailableBytes: 12 * gib},
		GPUs:           []hw.GPU{},
		Virtualization: hw.Virtualization{Guest: true, Hypervisor: "qemu"},
		Accelerator:    hw.AcceleratorNone,
	}

	got := run(HardwareCheck(func() (hw.Info, error) { return info, nil }))

	want := "Intel Xeon Processor (Cascadelake), 4 logical CPUs, 16.0 GiB memory (12.0 GiB available), accelerator none, VM (qemu)"
	if got.Severity != OK || got.Message != want {
		t.Fatalf("got %+v\nwant OK %q", got, want)
	}
}

func TestHardwareCheckListsGPUsAndUnknownAvailableMemory(t *testing.T) {
	info := hw.Info{
		CPU:         hw.CPU{Model: "AMD Ryzen 7 7700X 8-Core Processor", Logical: 16},
		Memory:      hw.Memory{TotalBytes: 64 * gib},
		GPUs:        []hw.GPU{{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 4090", MemoryBytes: 24 * gib}},
		Accelerator: hw.AcceleratorCUDA,
	}

	got := run(HardwareCheck(func() (hw.Info, error) { return info, nil }))

	if !strings.Contains(got.Message, "(available unknown)") || !strings.Contains(got.Message, "GPU NVIDIA GeForce RTX 4090 (24.0 GiB)") {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestHardwareCheckWarnsOnDetectionWarnings(t *testing.T) {
	info := hw.Info{CPU: hw.CPU{Model: "x", Logical: 1}, Memory: hw.Memory{TotalBytes: gib}, Warnings: []string{"nvidia-smi is installed but failed: exit status 9"}}

	got := run(HardwareCheck(func() (hw.Info, error) { return info, nil }))

	if got.Severity != Warn || !strings.Contains(got.Message, "nvidia-smi is installed but failed") {
		t.Fatalf("got %+v, want a warning carrying the detection warning", got)
	}
}

func TestHardwareCheckErrors(t *testing.T) {
	unsupported := run(HardwareCheck(func() (hw.Info, error) {
		return hw.Info{}, errors.Join(errors.New("hardware detection on plan9"), errors.ErrUnsupported)
	}))
	if unsupported.Severity != Warn {
		t.Errorf("unsupported OS: got %+v, want a warning", unsupported)
	}

	broken := run(HardwareCheck(func() (hw.Info, error) { return hw.Info{}, errors.New("read /proc/meminfo: permission denied") }))
	if broken.Severity != Fail || !strings.Contains(broken.Message, "permission denied") {
		t.Errorf("detection error: got %+v, want a failure with the cause", broken)
	}
}

func layoutAt(root string) func() (home.Layout, error) {
	return func() (home.Layout, error) {
		return home.Resolve(func(string) string { return root }, func() (string, error) { return "", nil })
	}
}

func TestHomeCheckNotCreatedYetIsFine(t *testing.T) {
	got := run(HomeCheck(layoutAt(filepath.Join(t.TempDir(), "lokal"))))

	if got.Severity != OK || !strings.Contains(got.Message, "not created yet") {
		t.Fatalf("got %+v", got)
	}
}

func TestHomeCheckWritableDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lokal")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}

	got := run(HomeCheck(layoutAt(root)))

	if got.Severity != OK {
		t.Fatalf("got %+v, want OK", got)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("the writability probe left files behind: %v", entries)
	}
}

func TestHomeCheckFailures(t *testing.T) {
	file := filepath.Join(t.TempDir(), "lokal")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := run(HomeCheck(layoutAt(file))); got.Severity != Fail || got.Fix == "" {
		t.Errorf("home is a file: got %+v, want a failure with a fix", got)
	}

	bad := func() (home.Layout, error) { return home.Layout{}, errors.New("LOKAL_HOME must be an absolute path") }
	if got := run(HomeCheck(bad)); got.Severity != Fail || !strings.Contains(got.Fix, "LOKAL_HOME") {
		t.Errorf("unresolvable home: got %+v, want a failure that points at LOKAL_HOME", got)
	}
}

func TestHomeCheckWarnsWhenOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	root := filepath.Join(t.TempDir(), "lokal")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}

	got := run(HomeCheck(layoutAt(root)))

	if got.Severity != Warn || !strings.Contains(got.Fix, "chmod 700") {
		t.Fatalf("got %+v, want a warning with a chmod fix", got)
	}
}

func TestHomeDiskCheckThresholds(t *testing.T) {
	cases := []struct {
		free uint64
		want Severity
	}{
		{10 * gib, OK},
		{1 * gib, Warn},
		{100 * 1024 * 1024, Fail},
	}
	for _, c := range cases {
		free := func(string) (uint64, error) { return c.free, nil }
		got := run(HomeDiskCheck(layoutAt(t.TempDir()), free))
		if got.Severity != c.want {
			t.Errorf("free %s: got %+v, want %s", HumanBytes(c.free), got, c.want)
		}
		if c.want != OK && got.Fix == "" {
			t.Errorf("free %s: a %s needs a fix", HumanBytes(c.free), c.want)
		}
	}
}

func TestHomeDiskCheckErrors(t *testing.T) {
	unsupported := func(string) (uint64, error) { return 0, errors.ErrUnsupported }
	if got := run(HomeDiskCheck(layoutAt(t.TempDir()), unsupported)); got.Severity != Warn {
		t.Errorf("unsupported: got %+v, want a warning", got)
	}
	broken := func(string) (uint64, error) { return 0, errors.New("statfs: input/output error") }
	if got := run(HomeDiskCheck(layoutAt(t.TempDir()), broken)); got.Severity != Fail {
		t.Errorf("statfs error: got %+v, want a failure", got)
	}
}

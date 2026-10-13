package hw

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

const kib = 1024
const mib = 1024 * 1024

func fixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func noNvidiaSMI() ([]byte, error) { return nil, exec.ErrNotFound }

func TestParseMeminfo(t *testing.T) {
	got, err := ParseMeminfo(fixture(t, "linux-kvm-x86/proc/meminfo"))
	if err != nil {
		t.Fatalf("ParseMeminfo: %v", err)
	}

	want := Memory{TotalBytes: 16384000 * kib, AvailableBytes: 14123456 * kib}
	if got != want {
		t.Fatalf("ParseMeminfo() = %+v, want %+v", got, want)
	}
}

func TestParseMeminfoWithoutMemAvailable(t *testing.T) {
	// Kernels before 3.14 have no MemAvailable; free + buffers + cache approximates it.
	in := "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 20 kB\nCached: 30 kB\n"

	got, err := ParseMeminfo(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseMeminfo: %v", err)
	}

	want := Memory{TotalBytes: 1000 * kib, AvailableBytes: 150 * kib}
	if got != want {
		t.Fatalf("ParseMeminfo() = %+v, want %+v", got, want)
	}
}

func TestParseMeminfoRequiresMemTotal(t *testing.T) {
	if _, err := ParseMeminfo(strings.NewReader("MemFree: 100 kB\n")); err == nil {
		t.Fatal("ParseMeminfo without MemTotal must fail")
	}
	if _, err := ParseMeminfo(strings.NewReader("MemTotal: lots kB\n")); err == nil {
		t.Fatal("ParseMeminfo with a non-numeric MemTotal must fail")
	}
}

func TestParseCPUInfoX86Guest(t *testing.T) {
	cpu, hypervisorFlag, err := ParseCPUInfo(fixture(t, "linux-kvm-x86/proc/cpuinfo"))
	if err != nil {
		t.Fatalf("ParseCPUInfo: %v", err)
	}

	want := CPU{Model: "Intel Xeon Processor (Cascadelake)", Logical: 4}
	if cpu != want {
		t.Fatalf("ParseCPUInfo() = %+v, want %+v", cpu, want)
	}
	if !hypervisorFlag {
		t.Fatal("the x86 guest fixture carries the hypervisor flag")
	}
}

func TestParseCPUInfoArm64WithoutModelName(t *testing.T) {
	cpu, hypervisorFlag, err := ParseCPUInfo(fixture(t, "linux-ec2-arm64/proc/cpuinfo"))
	if err != nil {
		t.Fatalf("ParseCPUInfo: %v", err)
	}

	want := CPU{Model: "arm64 (implementer 0x41, part 0xd0c)", Logical: 2}
	if cpu != want {
		t.Fatalf("ParseCPUInfo() = %+v, want %+v", cpu, want)
	}
	if hypervisorFlag {
		t.Fatal("arm64 has no hypervisor flag in cpuinfo")
	}
}

func TestParseCPUInfoBareMetal(t *testing.T) {
	cpu, hypervisorFlag, err := ParseCPUInfo(fixture(t, "linux-metal-nvidia/proc/cpuinfo"))
	if err != nil {
		t.Fatalf("ParseCPUInfo: %v", err)
	}
	if cpu.Logical != 8 || cpu.Model != "AMD Ryzen 7 7700X 8-Core Processor" {
		t.Fatalf("ParseCPUInfo() = %+v", cpu)
	}
	if hypervisorFlag {
		t.Fatal("bare metal must not report the hypervisor flag")
	}
}

func TestParseCPUInfoEmpty(t *testing.T) {
	if _, _, err := ParseCPUInfo(strings.NewReader("")); err == nil {
		t.Fatal("ParseCPUInfo with no processors must fail")
	}
}

func TestHypervisorFromDMI(t *testing.T) {
	cases := []struct {
		vendor, product, want string
	}{
		{"QEMU", "Standard PC (Q35 + ICH9, 2009)", "qemu"},
		{"Red Hat", "KVM", "kvm"},
		{"OpenStack Foundation", "OpenStack Nova", "kvm"},
		{"Amazon EC2", "c6g.large", "amazon"},
		{"VMware, Inc.", "VMware Virtual Platform", "vmware"},
		{"innotek GmbH", "VirtualBox", "oracle"},
		{"Xen", "HVM domU", "xen"},
		{"Google", "Google Compute Engine", "google"},
		{"Microsoft Corporation", "Virtual Machine", "microsoft"},
		{"Parallels Software International Inc.", "Parallels Virtual Platform", "parallels"},
		// Physical machines, including physical Microsoft hardware, are not guests.
		{"Microsoft Corporation", "Surface Laptop 5", ""},
		{"Dell Inc.", "PowerEdge R750", ""},
		{"Micro-Star International Co., Ltd.", "MS-7E26", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := HypervisorFromDMI(c.vendor, c.product); got != c.want {
			t.Errorf("HypervisorFromDMI(%q, %q) = %q, want %q", c.vendor, c.product, got, c.want)
		}
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	got, err := ParseNvidiaSMI(fixture(t, "linux-metal-nvidia/nvidia-smi.csv"))
	if err != nil {
		t.Fatalf("ParseNvidiaSMI: %v", err)
	}

	want := []GPU{
		{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 4090", MemoryBytes: 24564 * mib},
		{Vendor: "nvidia", Name: "NVIDIA RTX A4000", MemoryBytes: 16376 * mib},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseNvidiaSMI() = %+v, want %+v", got, want)
	}
}

func TestParseNvidiaSMIEmptyAndMalformed(t *testing.T) {
	got, err := ParseNvidiaSMI(strings.NewReader("\n"))
	if err != nil || len(got) != 0 {
		t.Fatalf("empty output: got %+v, %v; want no GPUs and no error", got, err)
	}
	for _, in := range []string{"just a name\n", "NVIDIA A100, lots\n"} {
		if _, err := ParseNvidiaSMI(strings.NewReader(in)); err == nil {
			t.Errorf("ParseNvidiaSMI(%q) must fail", in)
		}
	}
}

func TestDetectLinuxKVMGuest(t *testing.T) {
	got, err := DetectLinux(os.DirFS("testdata/linux-kvm-x86"), "amd64", noNvidiaSMI)
	if err != nil {
		t.Fatalf("DetectLinux: %v", err)
	}

	want := Info{
		OS:             "linux",
		Arch:           "amd64",
		CPU:            CPU{Model: "Intel Xeon Processor (Cascadelake)", Logical: 4},
		Memory:         Memory{TotalBytes: 16384000 * kib, AvailableBytes: 14123456 * kib},
		GPUs:           []GPU{},
		Virtualization: Virtualization{Guest: true, Hypervisor: "qemu"},
		Accelerator:    AcceleratorNone,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectLinux() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDetectLinuxArmCloudGuestIsFoundThroughDMI(t *testing.T) {
	got, err := DetectLinux(os.DirFS("testdata/linux-ec2-arm64"), "arm64", noNvidiaSMI)
	if err != nil {
		t.Fatalf("DetectLinux: %v", err)
	}
	if got.Virtualization != (Virtualization{Guest: true, Hypervisor: "amazon"}) {
		t.Fatalf("Virtualization = %+v, want an amazon guest", got.Virtualization)
	}
	if got.Memory.TotalBytes != 8045632*kib {
		t.Fatalf("Memory = %+v", got.Memory)
	}
}

func TestDetectLinuxBareMetalWithNvidia(t *testing.T) {
	csv, err := os.ReadFile("testdata/linux-metal-nvidia/nvidia-smi.csv")
	if err != nil {
		t.Fatal(err)
	}

	got, err := DetectLinux(os.DirFS("testdata/linux-metal-nvidia"), "amd64", func() ([]byte, error) { return csv, nil })
	if err != nil {
		t.Fatalf("DetectLinux: %v", err)
	}
	if got.Virtualization.Guest {
		t.Fatalf("bare metal reported as a guest: %+v", got.Virtualization)
	}
	if len(got.GPUs) != 2 || got.GPUs[0].MemoryBytes != 24564*mib {
		t.Fatalf("GPUs = %+v", got.GPUs)
	}
	if got.Accelerator != AcceleratorCUDA {
		t.Fatalf("Accelerator = %q, want %q with NVIDIA GPUs", got.Accelerator, AcceleratorCUDA)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", got.Warnings)
	}
}

func TestDetectLinuxReportsAFailingNvidiaSMIAsAWarning(t *testing.T) {
	// A driver/library mismatch makes nvidia-smi exit non-zero. That is worth
	// telling the user about, but it must not hide the rest of the hardware.
	failing := func() ([]byte, error) { return nil, errors.New("exit status 9") }

	got, err := DetectLinux(os.DirFS("testdata/linux-metal-nvidia"), "amd64", failing)
	if err != nil {
		t.Fatalf("DetectLinux: %v", err)
	}
	if len(got.GPUs) != 0 {
		t.Fatalf("GPUs = %+v, want none", got.GPUs)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "nvidia-smi") {
		t.Fatalf("Warnings = %v, want one about nvidia-smi", got.Warnings)
	}
}

func TestDetectLinuxWithoutDMIFallsBackToTheCPUFlag(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/meminfo": {Data: []byte("MemTotal: 2048 kB\nMemAvailable: 1024 kB\n")},
		"proc/cpuinfo": {Data: []byte("processor\t: 0\nmodel name\t: Some CPU\nflags\t\t: fpu hypervisor\n")},
	}

	got, err := DetectLinux(fsys, "amd64", noNvidiaSMI)
	if err != nil {
		t.Fatalf("DetectLinux: %v", err)
	}
	if got.Virtualization != (Virtualization{Guest: true, Hypervisor: "unknown"}) {
		t.Fatalf("Virtualization = %+v, want an unknown guest", got.Virtualization)
	}
}

func TestDetectLinuxRequiresProcFiles(t *testing.T) {
	onlyCPU := fstest.MapFS{"proc/cpuinfo": {Data: []byte("processor\t: 0\n")}}
	if _, err := DetectLinux(onlyCPU, "amd64", noNvidiaSMI); err == nil {
		t.Fatal("DetectLinux without /proc/meminfo must fail")
	}

	onlyMem := fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 2048 kB\n")}}
	if _, err := DetectLinux(onlyMem, "amd64", noNvidiaSMI); err == nil {
		t.Fatal("DetectLinux without /proc/cpuinfo must fail")
	}
}

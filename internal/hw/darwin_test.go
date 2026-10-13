package hw

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSysctl(t *testing.T) {
	got := ParseSysctl(strings.NewReader(string(readFixture(t, "darwin-m3/sysctl.txt"))))

	want := map[string]string{
		"hw.memsize":               "8589934592",
		"hw.ncpu":                  "8",
		"machdep.cpu.brand_string": "Apple M3",
		"kern.hv_vmm_present":      "0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSysctl() = %v, want %v", got, want)
	}
}

func TestParseSysctlSkipsLinesWithoutAValue(t *testing.T) {
	got := ParseSysctl(strings.NewReader("\nnot a sysctl line\nhw.ncpu: 4\n"))

	if len(got) != 1 || got["hw.ncpu"] != "4" {
		t.Fatalf("ParseSysctl() = %v, want only hw.ncpu", got)
	}
}

func TestParseVMStatAvailableMemory(t *testing.T) {
	// Available follows psutil's macOS definition: free plus inactive pages.
	got, err := ParseVMStat(strings.NewReader(string(readFixture(t, "darwin-m3/vm_stat.txt"))))
	if err != nil {
		t.Fatalf("ParseVMStat: %v", err)
	}

	want := uint64(4862+80105) * 16384
	if got != want {
		t.Fatalf("ParseVMStat() = %d, want %d", got, want)
	}
}

func TestParseVMStatRequiresThePageSize(t *testing.T) {
	if _, err := ParseVMStat(strings.NewReader("Pages free: 10.\nPages inactive: 20.\n")); err == nil {
		t.Fatal("ParseVMStat without the page-size header must fail")
	}
}

func TestDetectDarwinAppleSilicon(t *testing.T) {
	got, err := DetectDarwin(readFixture(t, "darwin-m3/sysctl.txt"), readFixture(t, "darwin-m3/vm_stat.txt"), "arm64")
	if err != nil {
		t.Fatalf("DetectDarwin: %v", err)
	}

	want := Info{
		OS:          "darwin",
		Arch:        "arm64",
		CPU:         CPU{Model: "Apple M3", Logical: 8},
		Memory:      Memory{TotalBytes: 8589934592, AvailableBytes: uint64(4862+80105) * 16384},
		GPUs:        []GPU{},
		Accelerator: AcceleratorMetal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectDarwin() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDetectDarwinIntelGuest(t *testing.T) {
	got, err := DetectDarwin(readFixture(t, "darwin-intel-vm/sysctl.txt"), readFixture(t, "darwin-intel-vm/vm_stat.txt"), "amd64")
	if err != nil {
		t.Fatalf("DetectDarwin: %v", err)
	}
	if got.Virtualization != (Virtualization{Guest: true, Hypervisor: "unknown"}) {
		t.Fatalf("Virtualization = %+v, want an unknown guest", got.Virtualization)
	}
	if got.Accelerator != AcceleratorNone {
		t.Fatalf("Accelerator = %q, want %q on an Intel Mac", got.Accelerator, AcceleratorNone)
	}
	if got.Memory.AvailableBytes != uint64(812345+700000)*4096 {
		t.Fatalf("Memory = %+v", got.Memory)
	}
}

func TestDetectDarwinWithoutVMStatWarns(t *testing.T) {
	got, err := DetectDarwin(readFixture(t, "darwin-m3/sysctl.txt"), nil, "arm64")
	if err != nil {
		t.Fatalf("DetectDarwin: %v", err)
	}
	if got.Memory.AvailableBytes != 0 || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "vm_stat") {
		t.Fatalf("got Memory %+v, Warnings %v; want unknown available memory and a vm_stat warning", got.Memory, got.Warnings)
	}
}

func TestDetectDarwinRequiresMemsizeAndNcpu(t *testing.T) {
	for _, in := range []string{"hw.ncpu: 8\n", "hw.memsize: 8589934592\n", "hw.memsize: lots\nhw.ncpu: 8\n"} {
		if _, err := DetectDarwin([]byte(in), nil, "arm64"); err == nil {
			t.Errorf("DetectDarwin(%q) must fail", in)
		}
	}
}

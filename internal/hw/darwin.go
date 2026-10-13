package hw

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// darwinSysctlKeys is what Detect asks `sysctl` for on macOS.
var darwinSysctlKeys = []string{"hw.memsize", "hw.ncpu", "machdep.cpu.brand_string", "kern.hv_vmm_present"}

// DetectDarwin builds Info from the output of `sysctl <darwinSysctlKeys>` and
// `vm_stat`. A nil vmStat leaves available memory unknown and adds a warning.
func DetectDarwin(sysctl, vmStat []byte, arch string) (Info, error) {
	kv := ParseSysctl(bytes.NewReader(sysctl))
	total, err := strconv.ParseUint(kv["hw.memsize"], 10, 64)
	if err != nil {
		return Info{}, fmt.Errorf("sysctl hw.memsize %q: %w", kv["hw.memsize"], err)
	}
	ncpu, err := strconv.Atoi(kv["hw.ncpu"])
	if err != nil {
		return Info{}, fmt.Errorf("sysctl hw.ncpu %q: %w", kv["hw.ncpu"], err)
	}

	info := Info{
		OS:          "darwin",
		Arch:        arch,
		CPU:         CPU{Model: kv["machdep.cpu.brand_string"], Logical: ncpu},
		Memory:      Memory{TotalBytes: total},
		GPUs:        []GPU{},
		Accelerator: AcceleratorNone,
	}
	if arch == "arm64" {
		info.Accelerator = AcceleratorMetal
	}
	// kern.hv_vmm_present says "inside a VM" without naming the hypervisor.
	if kv["kern.hv_vmm_present"] == "1" {
		info.Virtualization = Virtualization{Guest: true, Hypervisor: "unknown"}
	}

	if vmStat == nil {
		info.Warnings = append(info.Warnings, "vm_stat unavailable: available memory unknown")
		return info, nil
	}
	avail, err := ParseVMStat(bytes.NewReader(vmStat))
	if err != nil {
		info.Warnings = append(info.Warnings, fmt.Sprintf("vm_stat output not understood: %v", err))
		return info, nil
	}
	info.Memory.AvailableBytes = avail
	return info, nil
}

// ParseSysctl reads `sysctl name...` output ("name: value" per line). Lines
// without a value, such as errors for unknown names, are skipped.
func ParseSysctl(r io.Reader) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ": ")
		if !ok || strings.ContainsAny(key, " \t") {
			continue
		}
		kv[key] = strings.TrimSpace(val)
	}
	return kv
}

var vmStatPageSize = regexp.MustCompile(`page size of (\d+) bytes`)

// ParseVMStat returns available memory from `vm_stat`, using psutil's macOS
// definition: free plus inactive pages. vm_stat already prints "Pages free"
// without speculative pages, so they are not counted twice.
func ParseVMStat(r io.Reader) (uint64, error) {
	var pageSize uint64
	pages := map[string]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if m := vmStatPageSize.FindStringSubmatch(line); m != nil {
			pageSize, _ = strconv.ParseUint(m[1], 10, 64)
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
		if err == nil {
			pages[strings.TrimSpace(key)] = n
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if pageSize == 0 {
		return 0, errors.New("no page size header")
	}
	return (pages["Pages free"] + pages["Pages inactive"]) * pageSize, nil
}

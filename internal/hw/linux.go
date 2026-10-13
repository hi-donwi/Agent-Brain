package hw

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
)

// DetectLinux reads a Linux root filesystem (os.DirFS("/") on a live system, a
// fixture directory in tests). nvidiaSMI runs `nvidia-smi --query-gpu=name,memory.total
// --format=csv,noheader,nounits`; exec.ErrNotFound means no NVIDIA tooling.
func DetectLinux(fsys fs.FS, arch string, nvidiaSMI func() ([]byte, error)) (Info, error) {
	info := Info{OS: "linux", Arch: arch, GPUs: []GPU{}}

	mem, err := parseFile(fsys, "proc/meminfo", ParseMeminfo)
	if err != nil {
		return Info{}, err
	}
	info.Memory = mem

	var hypervisorFlag bool
	info.CPU, err = parseFile(fsys, "proc/cpuinfo", func(r io.Reader) (CPU, error) {
		cpu, flag, err := ParseCPUInfo(r)
		hypervisorFlag = flag
		return cpu, err
	})
	if err != nil {
		return Info{}, err
	}

	// DMI files are missing in containers and on some boards; that only means
	// there is no DMI evidence either way.
	vendor := readTrimmed(fsys, "sys/class/dmi/id/sys_vendor")
	product := readTrimmed(fsys, "sys/class/dmi/id/product_name")
	switch hv := HypervisorFromDMI(vendor, product); {
	case hv != "":
		info.Virtualization = Virtualization{Guest: true, Hypervisor: hv}
	case hypervisorFlag:
		info.Virtualization = Virtualization{Guest: true, Hypervisor: "unknown"}
	}

	if nvidiaSMI != nil {
		out, err := nvidiaSMI()
		switch {
		case errors.Is(err, exec.ErrNotFound):
		case err != nil:
			info.Warnings = append(info.Warnings, fmt.Sprintf("nvidia-smi is installed but failed: %v", err))
		default:
			gpus, err := ParseNvidiaSMI(bytes.NewReader(out))
			if err != nil {
				info.Warnings = append(info.Warnings, fmt.Sprintf("nvidia-smi output not understood: %v", err))
			} else {
				info.GPUs = gpus
			}
		}
	}
	return info, nil
}

func parseFile[T any](fsys fs.FS, name string, parse func(io.Reader) (T, error)) (T, error) {
	var zero T
	f, err := fsys.Open(name)
	if err != nil {
		return zero, fmt.Errorf("read /%s: %w", name, err)
	}
	defer f.Close()
	v, err := parse(f)
	if err != nil {
		return zero, fmt.Errorf("parse /%s: %w", name, err)
	}
	return v, nil
}

func readTrimmed(fsys fs.FS, name string) string {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ParseMeminfo reads /proc/meminfo. Values there are in KiB despite the "kB" label.
func ParseMeminfo(r io.Reader) (Memory, error) {
	fields := map[string]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		val := strings.TrimSuffix(strings.TrimSpace(rest), " kB")
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			if key == "MemTotal" {
				return Memory{}, fmt.Errorf("MemTotal %q: %w", strings.TrimSpace(rest), err)
			}
			continue
		}
		fields[key] = n * 1024
	}
	if err := sc.Err(); err != nil {
		return Memory{}, err
	}
	total, ok := fields["MemTotal"]
	if !ok {
		return Memory{}, errors.New("no MemTotal line")
	}
	avail, ok := fields["MemAvailable"]
	if !ok {
		// Kernels before 3.14 lack MemAvailable; this is the usual approximation.
		avail = fields["MemFree"] + fields["Buffers"] + fields["Cached"]
	}
	return Memory{TotalBytes: total, AvailableBytes: avail}, nil
}

// ParseCPUInfo reads /proc/cpuinfo. It reports the model, the number of logical
// CPUs, and whether the x86 "hypervisor" flag is set (arm64 has no such flag).
func ParseCPUInfo(r io.Reader) (CPU, bool, error) {
	var cpu CPU
	var hypervisorFlag bool
	var implementer, part, architecture string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // flags lines are long
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "processor":
			cpu.Logical++
		case "model name":
			if cpu.Model == "" {
				cpu.Model = val
			}
		case "flags":
			if !hypervisorFlag {
				hypervisorFlag = containsField(val, "hypervisor")
			}
		case "CPU implementer":
			implementer = firstNonEmpty(implementer, val)
		case "CPU part":
			part = firstNonEmpty(part, val)
		case "CPU architecture":
			architecture = firstNonEmpty(architecture, val)
		}
	}
	if err := sc.Err(); err != nil {
		return CPU{}, false, err
	}
	if cpu.Logical == 0 {
		return CPU{}, false, errors.New("no processor entries")
	}
	if cpu.Model == "" && implementer != "" {
		// arm64 kernels print no model name; the implementer and part codes
		// identify the core without a lookup table that would go stale.
		family := "arm"
		if n, err := strconv.Atoi(architecture); err == nil && n >= 8 {
			family = "arm64"
		}
		cpu.Model = fmt.Sprintf("%s (implementer %s, part %s)", family, implementer, part)
	}
	return cpu, hypervisorFlag, nil
}

// dmiPrefixes follows systemd-detect-virt's DMI vendor table. "Oracle Corporation"
// is left out on purpose: it is also the vendor of physical servers.
var dmiPrefixes = []struct{ prefix, hypervisor string }{
	{"KVM", "kvm"},
	{"OpenStack", "kvm"},
	{"KubeVirt", "kvm"},
	{"Amazon EC2", "amazon"},
	{"QEMU", "qemu"},
	{"VMware", "vmware"},
	{"VMW", "vmware"},
	{"innotek GmbH", "oracle"},
	{"VirtualBox", "oracle"},
	{"Xen", "xen"},
	{"Bochs", "bochs"},
	{"Parallels", "parallels"},
	{"BHYVE", "bhyve"},
	{"Hyper-V", "microsoft"},
	{"Apple Virtualization", "apple"},
	{"Google Compute Engine", "google"},
}

// HypervisorFromDMI names the hypervisor from /sys/class/dmi/id/sys_vendor and
// product_name, or returns "" when they describe physical hardware.
func HypervisorFromDMI(sysVendor, productName string) string {
	// Hyper-V guests report Microsoft as the vendor, as do Surface laptops; only
	// the product name tells them apart.
	if strings.HasPrefix(sysVendor, "Microsoft Corporation") && productName == "Virtual Machine" {
		return "microsoft"
	}
	for _, field := range []string{sysVendor, productName} {
		if field == "" {
			continue
		}
		for _, p := range dmiPrefixes {
			if strings.HasPrefix(field, p.prefix) {
				return p.hypervisor
			}
		}
	}
	return ""
}

// ParseNvidiaSMI reads `nvidia-smi --query-gpu=name,memory.total
// --format=csv,noheader,nounits`: one "name, MiB" line per GPU.
func ParseNvidiaSMI(r io.Reader) ([]GPU, error) {
	gpus := []GPU{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		i := strings.LastIndex(line, ",")
		if i < 0 {
			return nil, fmt.Errorf("line %q: want \"name, memory\"", line)
		}
		name := strings.TrimSpace(line[:i])
		mib, err := strconv.ParseUint(strings.TrimSpace(line[i+1:]), 10, 64)
		if err != nil || name == "" {
			return nil, fmt.Errorf("line %q: want \"name, memory in MiB\"", line)
		}
		gpus = append(gpus, GPU{Vendor: "nvidia", Name: name, MemoryBytes: mib * 1024 * 1024})
	}
	return gpus, sc.Err()
}

func containsField(s, want string) bool {
	for _, f := range strings.Fields(s) {
		if f == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(current, next string) string {
	if current != "" {
		return current
	}
	return next
}

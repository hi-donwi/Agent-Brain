// Package hw describes the machine lokal runs on: CPU, memory, accelerators, and
// whether it is a virtual machine. Setup uses it to pick a profile and doctor uses
// it to explain what the machine can run. Parsers take readers and filesystems so
// every platform is tested from fixtures on any platform.
package hw

// Info is everything lokal knows about the host.
type Info struct {
	OS             string         `json:"os"`
	Arch           string         `json:"arch"`
	CPU            CPU            `json:"cpu"`
	Memory         Memory         `json:"memory"`
	GPUs           []GPU          `json:"gpus"`
	Virtualization Virtualization `json:"virtualization"`
	// Accelerator is what local models can run on besides the CPU.
	Accelerator string `json:"accelerator"`
	// Warnings are detection problems that did not stop detection, such as an
	// installed nvidia-smi that cannot talk to its driver.
	Warnings []string `json:"warnings,omitempty"`
}

// Accelerator values. Profiles size models differently for each: unified memory
// is shared with the OS, VRAM is not, and CPU-only hosts are limited by speed
// before memory.
const (
	AcceleratorMetal = "metal" // Apple Silicon GPU over unified memory
	AcceleratorCUDA  = "cuda"  // at least one NVIDIA GPU visible to nvidia-smi
	AcceleratorNone  = "none"  // CPU only
)

// CPU is the processor model and the number of logical CPUs the OS schedules on.
type CPU struct {
	Model   string `json:"model"`
	Logical int    `json:"logical"`
}

// Memory is system RAM. On Apple Silicon it is the unified memory the GPU shares.
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// GPU is one discrete accelerator with its own memory.
type GPU struct {
	Vendor      string `json:"vendor"`
	Name        string `json:"name"`
	MemoryBytes uint64 `json:"memory_bytes"`
}

// Virtualization tells whether lokal runs inside a VM. Hypervisor uses the names
// systemd-detect-virt prints ("kvm", "qemu", "vmware", "amazon", ...), or
// "unknown" when the CPU reports a hypervisor that DMI does not identify.
type Virtualization struct {
	Guest      bool   `json:"guest"`
	Hypervisor string `json:"hypervisor,omitempty"`
}

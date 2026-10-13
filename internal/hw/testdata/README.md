# Hardware fixtures

Each `linux-*` directory mirrors the root filesystem of one machine, so tests read it
through `os.DirFS` exactly as `Detect` reads `/` on a live system. Each `darwin-*`
directory holds the output of the two commands `Detect` runs on macOS.

The Linux fixtures are **synthetic**: written by hand in the documented formats of
`/proc/meminfo`, `/proc/cpuinfo`, the DMI files under `/sys/class/dmi/id/`, and
`nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits`. Add captures
from real machines next to them (the dedicated VM first) and keep the synthetic ones as
edge cases.

| Directory | Models |
|---|---|
| `linux-kvm-x86` | QEMU/KVM guest, x86-64, 4 vCPUs with the `hypervisor` flag, 16 GiB, no GPU |
| `linux-ec2-arm64` | Cloud arm64 guest (no `model name`, no `hypervisor` flag), DMI vendor `Amazon EC2`, 8 GiB |
| `linux-metal-nvidia` | Bare-metal x86-64 workstation, 2 NVIDIA GPUs (`nvidia-smi.csv`), 64 GiB |
| `darwin-m3` | **Captured** from a real Apple M3 with 8 GB: `sysctl hw.memsize hw.ncpu machdep.cpu.brand_string kern.hv_vmm_present` and `vm_stat` |
| `darwin-intel-vm` | Synthetic Intel Mac running as a VM (`kern.hv_vmm_present: 1`), 16 GiB, 4 KiB pages |

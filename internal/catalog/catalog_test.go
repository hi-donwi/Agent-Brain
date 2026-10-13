package catalog_test

import (
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/hw"
)

func TestDefault(t *testing.T) {
	cat := catalog.Default()
	if cat == nil {
		t.Fatal("expected non-nil default catalog")
	}
	if cat.Version <= 0 {
		t.Errorf("expected positive catalog version, got %d", cat.Version)
	}
	if len(cat.Models) == 0 {
		t.Error("expected models in catalog, got none")
	}
	if len(cat.Profiles) == 0 {
		t.Error("expected profiles in catalog, got none")
	}

	for _, name := range []catalog.ProfileName{
		catalog.ProfileTiny,
		catalog.ProfileStandard,
		catalog.ProfileWorkstation,
	} {
		p, ok := cat.GetProfile(name)
		if !ok {
			t.Errorf("missing expected profile %q", name)
			continue
		}
		if p.Defaults[catalog.RoleChat] == "" {
			t.Errorf("profile %q missing default chat model", name)
		}
		if p.Defaults[catalog.RoleEmbed] == "" {
			t.Errorf("profile %q missing default embed model", name)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := []struct {
		name    string
		cat     catalog.Catalog
		wantErr string
	}{
		{
			name: "zero version",
			cat: catalog.Catalog{
				Version: 0,
			},
			wantErr: "version must be positive",
		},
		{
			name: "duplicate model id",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					validModel("m1", catalog.RoleChat),
					validModel("m1", catalog.RoleChat),
				},
			},
			wantErr: "duplicate model id",
		},
		{
			name: "empty model id",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					validModel("", catalog.RoleChat),
				},
			},
			wantErr: "model id cannot be empty",
		},
		{
			name: "unknown role",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					validModel("m1", catalog.Role("invalid_role")),
				},
			},
			wantErr: "unknown role",
		},
		{
			name: "zero download bytes",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					{
						ID:            "m1",
						Roles:         []catalog.Role{catalog.RoleChat},
						DownloadBytes: 0,
						MemoryBytes:   1000,
						MaxContext:    4096,
						Backends:      map[string]string{"ollama": "m1"},
					},
				},
			},
			wantErr: "download_bytes must be positive",
		},
		{
			name: "zero memory bytes",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					{
						ID:            "m1",
						Roles:         []catalog.Role{catalog.RoleChat},
						DownloadBytes: 1000,
						MemoryBytes:   0,
						MaxContext:    4096,
						Backends:      map[string]string{"ollama": "m1"},
					},
				},
			},
			wantErr: "memory_bytes must be positive",
		},
		{
			name: "zero max context",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					{
						ID:            "m1",
						Roles:         []catalog.Role{catalog.RoleChat},
						DownloadBytes: 1000,
						MemoryBytes:   1000,
						MaxContext:    0,
						Backends:      map[string]string{"ollama": "m1"},
					},
				},
			},
			wantErr: "max_context must be positive",
		},
		{
			name: "empty backends",
			cat: catalog.Catalog{
				Version: 1,
				Models: []catalog.Model{
					{
						ID:            "m1",
						Roles:         []catalog.Role{catalog.RoleChat},
						DownloadBytes: 1000,
						MemoryBytes:   1000,
						MaxContext:    4096,
						Backends:      nil,
					},
				},
			},
			wantErr: "must specify at least one backend",
		},
		{
			name: "duplicate profile name",
			cat: catalog.Catalog{
				Version: 1,
				Models:  []catalog.Model{validModel("m1", catalog.RoleChat)},
				Profiles: []catalog.Profile{
					{
						Name:     catalog.ProfileTiny,
						Defaults: map[catalog.Role]string{catalog.RoleChat: "m1"},
					},
					{
						Name:     catalog.ProfileTiny,
						Defaults: map[catalog.Role]string{catalog.RoleChat: "m1"},
					},
				},
			},
			wantErr: "duplicate profile name",
		},
		{
			name: "profile refers to unknown model",
			cat: catalog.Catalog{
				Version: 1,
				Models:  []catalog.Model{validModel("m1", catalog.RoleChat)},
				Profiles: []catalog.Profile{
					{
						Name:     catalog.ProfileTiny,
						Defaults: map[catalog.Role]string{catalog.RoleChat: "unknown_model"},
					},
				},
			},
			wantErr: "unknown model id",
		},
		{
			name: "profile default does not support assigned role",
			cat: catalog.Catalog{
				Version: 1,
				Models:  []catalog.Model{validModel("m1", catalog.RoleChat)},
				Profiles: []catalog.Profile{
					{
						Name: catalog.ProfileTiny,
						Defaults: map[catalog.Role]string{
							catalog.RoleEmbed: "m1", // m1 only supports chat
						},
					},
				},
			},
			wantErr: "does not support role",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cat.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestSelectProfile(t *testing.T) {
	const gib = 1024 * 1024 * 1024

	cases := []struct {
		name string
		info hw.Info
		want catalog.ProfileName
	}{
		{
			name: "Apple M3 8 GiB Metal -> tiny",
			info: hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 8 * gib},
			},
			want: catalog.ProfileTiny,
		},
		{
			name: "Apple Silicon 36 GiB Metal -> standard",
			info: hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 36 * gib},
			},
			want: catalog.ProfileStandard,
		},
		{
			name: "Apple Silicon 64 GiB Metal -> workstation",
			info: hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 64 * gib},
			},
			want: catalog.ProfileWorkstation,
		},
		{
			name: "Linux KVM 8 GiB CPU-only -> tiny",
			info: hw.Info{
				OS:             "linux",
				Arch:           "amd64",
				Accelerator:    hw.AcceleratorNone,
				Virtualization: hw.Virtualization{Guest: true, Hypervisor: "kvm"},
				Memory:         hw.Memory{TotalBytes: 8 * gib},
			},
			want: catalog.ProfileTiny,
		},
		{
			name: "Linux KVM 16 GiB CPU-only (steps down from standard) -> tiny",
			info: hw.Info{
				OS:             "linux",
				Arch:           "amd64",
				Accelerator:    hw.AcceleratorNone,
				Virtualization: hw.Virtualization{Guest: true, Hypervisor: "kvm"},
				Memory:         hw.Memory{TotalBytes: 16 * gib},
			},
			want: catalog.ProfileTiny,
		},
		{
			name: "Linux KVM 32 GiB CPU-only (steps down from standard) -> tiny",
			info: hw.Info{
				OS:             "linux",
				Arch:           "amd64",
				Accelerator:    hw.AcceleratorNone,
				Virtualization: hw.Virtualization{Guest: true, Hypervisor: "kvm"},
				Memory:         hw.Memory{TotalBytes: 32 * gib},
			},
			want: catalog.ProfileTiny,
		},
		{
			name: "Linux KVM 64 GiB CPU-only (steps down from workstation) -> standard",
			info: hw.Info{
				OS:             "linux",
				Arch:           "amd64",
				Accelerator:    hw.AcceleratorNone,
				Virtualization: hw.Virtualization{Guest: true, Hypervisor: "kvm"},
				Memory:         hw.Memory{TotalBytes: 64 * gib},
			},
			want: catalog.ProfileStandard,
		},
		{
			name: "Linux 64 GiB RAM + RTX 4090 24 GiB VRAM -> workstation",
			info: hw.Info{
				OS:          "linux",
				Arch:        "amd64",
				Accelerator: hw.AcceleratorCUDA,
				Memory:      hw.Memory{TotalBytes: 64 * gib},
				GPUs: []hw.GPU{
					{Vendor: "NVIDIA", Name: "NVIDIA GeForce RTX 4090", MemoryBytes: 24 * gib},
				},
			},
			want: catalog.ProfileWorkstation,
		},
		{
			name: "Linux 16 GiB RAM + RTX 4060 8 GiB VRAM -> standard",
			info: hw.Info{
				OS:          "linux",
				Arch:        "amd64",
				Accelerator: hw.AcceleratorCUDA,
				Memory:      hw.Memory{TotalBytes: 16 * gib},
				GPUs: []hw.GPU{
					{Vendor: "NVIDIA", Name: "NVIDIA GeForce RTX 4060", MemoryBytes: 8 * gib},
				},
			},
			want: catalog.ProfileStandard,
		},
		{
			name: "Linux 8 GiB RAM + GTX 1650 4 GiB VRAM -> tiny",
			info: hw.Info{
				OS:          "linux",
				Arch:        "amd64",
				Accelerator: hw.AcceleratorCUDA,
				Memory:      hw.Memory{TotalBytes: 8 * gib},
				GPUs: []hw.GPU{
					{Vendor: "NVIDIA", Name: "NVIDIA GeForce GTX 1650", MemoryBytes: 4 * gib},
				},
			},
			want: catalog.ProfileTiny,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := catalog.SelectProfile(tc.info)
			if got != tc.want {
				t.Fatalf("SelectProfile() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetters(t *testing.T) {
	cat := catalog.Default()

	m, ok := cat.GetModel("qwen2.5-coder-7b")
	if !ok {
		t.Fatal("expected qwen2.5-coder-7b to exist in default catalog")
	}
	if m.ID != "qwen2.5-coder-7b" {
		t.Errorf("expected id qwen2.5-coder-7b, got %s", m.ID)
	}

	_, ok = cat.GetModel("nonexistent")
	if ok {
		t.Error("expected nonexistent model to return false")
	}

	p, ok := cat.GetProfile(catalog.ProfileStandard)
	if !ok {
		t.Fatal("expected standard profile to exist")
	}
	if p.Name != catalog.ProfileStandard {
		t.Errorf("expected profile standard, got %s", p.Name)
	}

	_, ok = cat.GetProfile(catalog.ProfileName("nonexistent"))
	if ok {
		t.Error("expected nonexistent profile to return false")
	}
}

func validModel(id string, role catalog.Role) catalog.Model {
	return catalog.Model{
		ID:            id,
		Name:          "Test Model",
		Roles:         []catalog.Role{role},
		Params:        "7B",
		Quant:         "Q4_K_M",
		DownloadBytes: 1024 * 1024 * 100,
		MemoryBytes:   1024 * 1024 * 500,
		MaxContext:    4096,
		Licence:       "Apache-2.0",
		Backends:      map[string]string{"ollama": "test:latest"},
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

package setup_test

import (
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/hw"
	"github.com/hi-donwi/Agent-Brain/internal/setup"
)

const gib = 1024 * 1024 * 1024

func TestPlan_DefaultProfileAndDeduplication(t *testing.T) {
	cat := catalog.Default()
	info := hw.Info{
		OS:          "darwin",
		Arch:        "arm64",
		Accelerator: hw.AcceleratorMetal,
		Memory:      hw.Memory{TotalBytes: 8 * gib},
	}

	plan, err := setup.BuildPlan(setup.PlanInput{
		HW:            info,
		Catalog:       cat,
		FreeDiskBytes: 20 * gib,
	})
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if plan.Profile != catalog.ProfileTiny {
		t.Errorf("expected tiny profile, got %s", plan.Profile)
	}

	// Tiny profile defaults chat and code to qwen2.5-coder-1.5b.
	// Verify models are deduplicated:
	var qwenCount int
	for _, m := range plan.Models {
		if m.ModelID == "qwen2.5-coder-1.5b" {
			qwenCount++
			if len(m.Roles) < 2 {
				t.Errorf("expected qwen model to have multiple roles, got %+v", m.Roles)
			}
		}
	}
	if qwenCount != 1 {
		t.Errorf("expected qwen2.5-coder-1.5b deduplicated to 1 entry, got %d", qwenCount)
	}

	if !plan.DiskSufficient {
		t.Errorf("expected disk sufficient with 20 GiB free, got refusal: %s", plan.RefusalReason)
	}
	if plan.TotalDownloadBytes == 0 {
		t.Error("expected positive total download bytes")
	}
	if plan.DiskAfterInstall != 20*gib-plan.TotalDownloadBytes {
		t.Errorf("expected disk after install %d, got %d", 20*gib-plan.TotalDownloadBytes, plan.DiskAfterInstall)
	}
}

func TestPlan_ProfileOverride(t *testing.T) {
	cat := catalog.Default()
	info := hw.Info{
		OS:          "darwin",
		Arch:        "arm64",
		Accelerator: hw.AcceleratorMetal,
		Memory:      hw.Memory{TotalBytes: 8 * gib},
	}

	plan, err := setup.BuildPlan(setup.PlanInput{
		HW:               info,
		Catalog:          cat,
		RequestedProfile: catalog.ProfileWorkstation,
		FreeDiskBytes:    50 * gib,
	})
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if plan.Profile != catalog.ProfileWorkstation {
		t.Errorf("expected workstation profile override, got %s", plan.Profile)
	}
}

func TestPlan_AlreadyInstalledFiltering(t *testing.T) {
	cat := catalog.Default()
	info := hw.Info{
		OS:          "darwin",
		Arch:        "arm64",
		Accelerator: hw.AcceleratorMetal,
		Memory:      hw.Memory{TotalBytes: 8 * gib},
	}

	// Tiny profile uses qwen2.5-coder-1.5b and nomic-embed-text
	planAll, _ := setup.BuildPlan(setup.PlanInput{
		HW:            info,
		Catalog:       cat,
		FreeDiskBytes: 20 * gib,
	})

	planFiltered, err := setup.BuildPlan(setup.PlanInput{
		HW:                info,
		Catalog:           cat,
		InstalledModelIDs: []string{"nomic-embed-text:latest"},
		FreeDiskBytes:     20 * gib,
	})
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if planFiltered.TotalDownloadBytes >= planAll.TotalDownloadBytes {
		t.Errorf("expected reduced download size with pre-installed model: %d >= %d",
			planFiltered.TotalDownloadBytes, planAll.TotalDownloadBytes)
	}

	var foundEmbed bool
	for _, m := range planFiltered.Models {
		if m.ModelID == "nomic-embed-text" {
			foundEmbed = true
			if !m.AlreadyPresent {
				t.Error("expected nomic-embed-text to be marked AlreadyPresent")
			}
		}
	}
	if !foundEmbed {
		t.Error("expected nomic-embed-text in plan models list")
	}
}

func TestPlan_DiskSafetyMarginRefusal(t *testing.T) {
	cat := catalog.Default()
	info := hw.Info{
		OS:          "darwin",
		Arch:        "arm64",
		Accelerator: hw.AcceleratorMetal,
		Memory:      hw.Memory{TotalBytes: 8 * gib},
	}

	// Free disk is only 1 GiB, which is below the download size + 2 GiB safety margin
	plan, err := setup.BuildPlan(setup.PlanInput{
		HW:            info,
		Catalog:       cat,
		FreeDiskBytes: 1 * gib,
	})
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if plan.DiskSufficient {
		t.Error("expected DiskSufficient=false when free disk is below safety margin")
	}
	if !strings.Contains(plan.RefusalReason, "safety margin") {
		t.Errorf("expected refusal reason mentioning safety margin, got %q", plan.RefusalReason)
	}
}

func TestPlan_BackendInstructions(t *testing.T) {
	cat := catalog.Default()

	planMac, _ := setup.BuildPlan(setup.PlanInput{
		HW:            hw.Info{OS: "darwin"},
		Catalog:       cat,
		BackendReady:  false,
		FreeDiskBytes: 20 * gib,
	})
	if !strings.Contains(planMac.Backend.Instruction, "brew") {
		t.Errorf("expected brew instructions for macOS, got: %s", planMac.Backend.Instruction)
	}

	planLinux, _ := setup.BuildPlan(setup.PlanInput{
		HW:            hw.Info{OS: "linux"},
		Catalog:       cat,
		BackendReady:  false,
		FreeDiskBytes: 20 * gib,
	})
	if !strings.Contains(planLinux.Backend.Instruction, "curl") {
		t.Errorf("expected curl install script for Linux, got: %s", planLinux.Backend.Instruction)
	}

	planRunning, _ := setup.BuildPlan(setup.PlanInput{
		HW:            hw.Info{OS: "linux"},
		Catalog:       cat,
		BackendReady:  true,
		FreeDiskBytes: 20 * gib,
	})
	if !planRunning.Backend.Installed {
		t.Error("expected Backend.Installed=true when backend is ready")
	}
}

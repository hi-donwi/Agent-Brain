// Package setup calculates and executes the installation plan for lokal:
// backend verification/installation, model pulls, configuration, and doctor validation.
package setup

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
)

// DefaultSafetyMarginBytes is the minimum disk margin lokal requires keeping free (2 GiB).
const DefaultSafetyMarginBytes uint64 = 2 << 30

// BackendAction reports backend state and required installation/start steps.
type BackendAction struct {
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Instruction string `json:"instruction,omitempty"`
}

// ModelAction describes a model targeted by the setup plan.
type ModelAction struct {
	ModelID        string   `json:"model_id"`
	BackendID      string   `json:"backend_id"`
	Roles          []string `json:"roles"`
	DownloadBytes  uint64   `json:"download_bytes"`
	MemoryBytes    uint64   `json:"memory_bytes"`
	AlreadyPresent bool     `json:"already_present"`
}

// Plan is the complete specification of what setup will perform.
type Plan struct {
	Profile            catalog.ProfileName `json:"profile"`
	Backend            BackendAction       `json:"backend"`
	Models             []ModelAction       `json:"models"`
	TotalDownloadBytes uint64              `json:"total_download_bytes"`
	FreeDiskBytes      uint64              `json:"free_disk_bytes"`
	DiskAfterInstall   uint64              `json:"disk_after_install"`
	SafetyMarginBytes  uint64              `json:"safety_margin_bytes"`
	DiskSufficient     bool                `json:"disk_sufficient"`
	RefusalReason      string              `json:"refusal_reason,omitempty"`
	Config             *home.Config        `json:"config"`
}

// PlanInput supplies the state needed to construct a Plan.
type PlanInput struct {
	HW                hw.Info
	Catalog           *catalog.Catalog
	InstalledModelIDs []string
	BackendReady      bool
	RequestedProfile  catalog.ProfileName
	FreeDiskBytes     uint64
	SafetyMarginBytes uint64
}

// BuildPlan computes the setup plan as a pure function without disk or network side-effects.
func BuildPlan(input PlanInput) (Plan, error) {
	cat := input.Catalog
	if cat == nil {
		cat = catalog.Default()
	}

	margin := input.SafetyMarginBytes
	if margin == 0 {
		margin = DefaultSafetyMarginBytes
	}

	profileName := input.RequestedProfile
	if profileName == "" {
		profileName = catalog.SelectProfile(input.HW)
	}

	profile, ok := cat.GetProfile(profileName)
	if !ok {
		return Plan{}, fmt.Errorf("unknown profile %q", profileName)
	}

	// Determine backend action
	backendAction := BackendAction{
		Name:      "ollama",
		Installed: input.BackendReady,
	}
	if !input.BackendReady {
		switch input.HW.OS {
		case "darwin":
			backendAction.Instruction = "brew install ollama && brew services start ollama (or download from https://ollama.ai)"
		case "linux":
			backendAction.Instruction = "curl -fsSL https://ollama.ai/install.sh | sh"
		default:
			backendAction.Instruction = "download and install from https://ollama.ai"
		}
	}

	// Group roles by model ID to deduplicate downloads
	modelRoles := make(map[string][]string)
	for role, modelID := range profile.Defaults {
		modelRoles[modelID] = append(modelRoles[modelID], string(role))
	}

	// Sort model IDs for deterministic output
	modelIDs := make([]string, 0, len(modelRoles))
	for id := range modelRoles {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)

	var (
		modelActions       []ModelAction
		totalDownloadBytes uint64
	)

	for _, id := range modelIDs {
		m, ok := cat.GetModel(id)
		if !ok {
			return Plan{}, fmt.Errorf("catalog missing model %q for profile %q", id, profileName)
		}

		backendID := m.Backends["ollama"]
		if backendID == "" {
			backendID = m.ID
		}

		roles := modelRoles[id]
		sort.Strings(roles)

		// Check if model is already present
		alreadyPresent := false
		for _, inst := range input.InstalledModelIDs {
			if isModelMatch(inst, id, backendID) {
				alreadyPresent = true
				break
			}
		}

		action := ModelAction{
			ModelID:        m.ID,
			BackendID:      backendID,
			Roles:          roles,
			DownloadBytes:  m.DownloadBytes,
			MemoryBytes:    m.MemoryBytes,
			AlreadyPresent: alreadyPresent,
		}
		modelActions = append(modelActions, action)

		if !alreadyPresent {
			totalDownloadBytes += m.DownloadBytes
		}
	}

	plan := Plan{
		Profile:            profileName,
		Backend:            backendAction,
		Models:             modelActions,
		TotalDownloadBytes: totalDownloadBytes,
		FreeDiskBytes:      input.FreeDiskBytes,
		SafetyMarginBytes:  margin,
	}

	// Verify disk safety margin
	requiredDisk := totalDownloadBytes + margin
	if input.FreeDiskBytes > 0 && input.FreeDiskBytes < requiredDisk {
		plan.DiskSufficient = false
		plan.RefusalReason = fmt.Sprintf("plan requires %s download plus %s safety margin (%s total), but only %s is free",
			doctor.HumanBytes(totalDownloadBytes),
			doctor.HumanBytes(margin),
			doctor.HumanBytes(requiredDisk),
			doctor.HumanBytes(input.FreeDiskBytes))
	} else {
		plan.DiskSufficient = true
		if input.FreeDiskBytes >= totalDownloadBytes {
			plan.DiskAfterInstall = input.FreeDiskBytes - totalDownloadBytes
		}
	}

	// Generate target configuration
	aliases := make(map[string]string, len(profile.Defaults))
	for r, m := range profile.Defaults {
		aliases[string(r)] = m
	}
	plan.Config = home.DefaultConfig(string(profileName), aliases)

	return plan, nil
}

// WriteText prints a clean human-readable representation of the plan.
func (p Plan) WriteText(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Hardware profile: %s\n", p.Profile)
	if p.Backend.Installed {
		fmt.Fprintf(&b, "Inference backend: %s (running)\n", p.Backend.Name)
	} else {
		fmt.Fprintf(&b, "Inference backend: %s (not running)\n  Action: %s\n", p.Backend.Name, p.Backend.Instruction)
	}

	fmt.Fprintf(&b, "\nModels to install:\n")
	for _, m := range p.Models {
		status := doctor.HumanBytes(m.DownloadBytes)
		if m.AlreadyPresent {
			status = "already installed"
		}
		fmt.Fprintf(&b, "  - %s (%s) [%s]: %s\n", m.ModelID, m.BackendID, strings.Join(m.Roles, ", "), status)
	}

	fmt.Fprintf(&b, "\nDisk space:\n")
	fmt.Fprintf(&b, "  Required download:   %s\n", doctor.HumanBytes(p.TotalDownloadBytes))
	fmt.Fprintf(&b, "  Safety margin:       %s\n", doctor.HumanBytes(p.SafetyMarginBytes))
	if p.FreeDiskBytes > 0 {
		fmt.Fprintf(&b, "  Current free:        %s\n", doctor.HumanBytes(p.FreeDiskBytes))
		if p.DiskSufficient {
			fmt.Fprintf(&b, "  Free after install:  %s\n", doctor.HumanBytes(p.DiskAfterInstall))
		} else {
			fmt.Fprintf(&b, "  Status:              INSUFFICIENT DISK\n  Reason:              %s\n", p.RefusalReason)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func isModelMatch(installed, id, backendID string) bool {
	if installed == id || installed == backendID {
		return true
	}
	if strings.HasPrefix(installed, id+":") || strings.HasPrefix(installed, backendID+":") {
		return true
	}
	if strings.HasPrefix(id, installed) || strings.HasPrefix(backendID, installed) {
		return true
	}
	return false
}

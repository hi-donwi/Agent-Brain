// Package catalog defines the model catalog schema, embedded model definitions,
// and hardware-aware profile selection. Models and profiles are chosen by
// measurement on each hardware tier, and validated on load.
package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/hi-donwi/Agent-Brain/internal/hw"
)

//go:embed models.json
var embeddedJSON []byte

// Role classifies what task a model is tuned for.
type Role string

const (
	RoleChat   Role = "chat"
	RoleCode   Role = "code"
	RoleEmbed  Role = "embed"
	RoleRerank Role = "rerank"
	RoleVision Role = "vision"
)

// KnownRoles is the set of valid model roles.
var KnownRoles = map[Role]bool{
	RoleChat:   true,
	RoleCode:   true,
	RoleEmbed:  true,
	RoleRerank: true,
	RoleVision: true,
}

// ProfileName is one of the supported hardware tiers.
type ProfileName string

const (
	ProfileTiny        ProfileName = "tiny"
	ProfileStandard    ProfileName = "standard"
	ProfileWorkstation ProfileName = "workstation"
)

// Capabilities describes special model features beyond basic text completion.
type Capabilities struct {
	Tools    bool `json:"tools"`
	Thinking bool `json:"thinking"`
	Vision   bool `json:"vision"`
}

// Model describes one model entry in the catalog.
type Model struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Roles         []Role            `json:"roles"`
	Params        string            `json:"params"`
	Quant         string            `json:"quant"`
	DownloadBytes uint64            `json:"download_bytes"`
	MemoryBytes   uint64            `json:"memory_bytes"`
	MaxContext    int               `json:"max_context"`
	Licence       string            `json:"licence"`
	Capabilities  Capabilities      `json:"capabilities"`
	Backends      map[string]string `json:"backends"`
	Unmeasured    bool              `json:"unmeasured,omitempty"`
}

// SupportsRole checks if the model supports the requested role.
func (m Model) SupportsRole(r Role) bool {
	for _, role := range m.Roles {
		if role == r {
			return true
		}
	}
	return false
}

// MemoryAtCtx estimates memory in bytes for the model at a specific context length.
func (m Model) MemoryAtCtx(numCtx int) uint64 {
	if numCtx <= 0 || m.MaxContext <= 0 {
		return m.MemoryBytes
	}
	// KV cache estimation: ~4KB per token heuristic
	extra := uint64(numCtx) * 4096
	return m.MemoryBytes + extra
}

// Profile pairs hardware capability with default model assignments per role.
type Profile struct {
	Name        ProfileName     `json:"name"`
	Description string          `json:"description"`
	Defaults    map[Role]string `json:"defaults"` // role -> model id
}

// Catalog holds all known models and profiles.
type Catalog struct {
	Version  int       `json:"version"`
	Models   []Model   `json:"models"`
	Profiles []Profile `json:"profiles"`
}

var (
	defaultCatalogOnce sync.Once
	defaultCatalog     *Catalog
)

// Default returns the validated embedded catalog, cached after first load.
// It panics only if build-time embedded data is corrupt.
func Default() *Catalog {
	defaultCatalogOnce.Do(func() {
		cat, err := Load(embeddedJSON)
		if err != nil {
			panic(fmt.Sprintf("embedded catalog corrupt: %v", err))
		}
		defaultCatalog = cat
	})
	return defaultCatalog
}

// Load parses and validates a JSON catalog.
func Load(data []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parse catalog json: %w", err)
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return &cat, nil
}

// Validate checks internal consistency: unique IDs, positive sizes, known roles,
// and that profiles reference existing models supporting the specified roles.
func (c *Catalog) Validate() error {
	if c.Version <= 0 {
		return errors.New("version must be positive")
	}

	modelMap := make(map[string]Model, len(c.Models))
	for _, m := range c.Models {
		if m.ID == "" {
			return errors.New("model id cannot be empty")
		}
		if _, exists := modelMap[m.ID]; exists {
			return fmt.Errorf("duplicate model id: %s", m.ID)
		}
		if len(m.Roles) == 0 {
			return fmt.Errorf("model %s has no roles", m.ID)
		}
		for _, r := range m.Roles {
			if !KnownRoles[r] {
				return fmt.Errorf("model %s has unknown role: %s", m.ID, r)
			}
		}
		if m.DownloadBytes == 0 {
			return fmt.Errorf("model %s download_bytes must be positive", m.ID)
		}
		if m.MemoryBytes == 0 {
			return fmt.Errorf("model %s memory_bytes must be positive", m.ID)
		}
		if m.MaxContext <= 0 {
			return fmt.Errorf("model %s max_context must be positive", m.ID)
		}
		if len(m.Backends) == 0 {
			return fmt.Errorf("model %s must specify at least one backend", m.ID)
		}
		modelMap[m.ID] = m
	}

	profileMap := make(map[ProfileName]bool, len(c.Profiles))
	for _, p := range c.Profiles {
		if _, exists := profileMap[p.Name]; exists {
			return fmt.Errorf("duplicate profile name: %s", p.Name)
		}
		switch p.Name {
		case ProfileTiny, ProfileStandard, ProfileWorkstation:
		default:
			return fmt.Errorf("unknown profile name: %s", p.Name)
		}
		profileMap[p.Name] = true

		for role, modelID := range p.Defaults {
			if !KnownRoles[role] {
				return fmt.Errorf("profile %s default has unknown role: %s", p.Name, role)
			}
			target, exists := modelMap[modelID]
			if !exists {
				return fmt.Errorf("profile %s references unknown model id: %s", p.Name, modelID)
			}
			if !target.SupportsRole(role) {
				return fmt.Errorf("model %s does not support role %s for profile %s", modelID, role, p.Name)
			}
		}
	}

	return nil
}

// GetModel finds a model by its unique ID.
func (c *Catalog) GetModel(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// GetProfile finds a profile by its name.
func (c *Catalog) GetProfile(name ProfileName) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// SelectProfile picks a profile from hardware memory and accelerator capabilities.
// A CPU-only host generates significantly slower than GPU or Apple Silicon, so it
// steps down one tier from what memory alone allows (ADR-0008, architecture section Hardware profiles).
func SelectProfile(info hw.Info) ProfileName {
	const gib = 1024 * 1024 * 1024
	totalRAM := info.Memory.TotalBytes

	var raw ProfileName
	switch info.Accelerator {
	case hw.AcceleratorCUDA:
		var totalVRAM uint64
		for _, g := range info.GPUs {
			totalVRAM += g.MemoryBytes
		}
		if totalVRAM >= 24*gib || totalRAM >= 60*gib {
			raw = ProfileWorkstation
		} else if totalVRAM >= 8*gib || totalRAM >= 14*gib {
			raw = ProfileStandard
		} else {
			raw = ProfileTiny
		}
		return raw

	case hw.AcceleratorMetal:
		if totalRAM >= 60*gib {
			raw = ProfileWorkstation
		} else if totalRAM >= 14*gib {
			raw = ProfileStandard
		} else {
			raw = ProfileTiny
		}
		return raw

	default: // CPU-only
		if totalRAM >= 60*gib {
			raw = ProfileWorkstation
		} else if totalRAM >= 14*gib {
			raw = ProfileStandard
		} else {
			raw = ProfileTiny
		}

		// Step down one tier for CPU-only hosts:
		switch raw {
		case ProfileWorkstation:
			return ProfileStandard
		case ProfileStandard:
			return ProfileTiny
		default:
			return ProfileTiny
		}
	}
}

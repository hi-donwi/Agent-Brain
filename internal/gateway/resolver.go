package gateway

import (
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

// ResolvedTarget holds model routing resolution and fallback candidates.
type ResolvedTarget struct {
	BackendTag   string
	CatalogModel *catalog.Model
	IsRole       bool
	Role         string
	Fallbacks    []string
}

// ResolveModel maps a requested model or role to its concrete backend tag
// and ordered fallback candidates using the active configuration and catalog.
func ResolveModel(query string, cfg *home.Config, cat *catalog.Catalog, backendName string) ResolvedTarget {
	q := strings.TrimSpace(query)
	if q == "" {
		return ResolvedTarget{
			Fallbacks: []string{},
		}
	}

	targetID := q
	isRole := false
	roleName := ""

	// Check if query is an alias configured in config.yaml
	if cfg != nil && cfg.Aliases != nil {
		if aliasTarget, ok := cfg.Aliases[q]; ok && aliasTarget != "" {
			targetID = aliasTarget
			isRole = true
			roleName = q
		}
	}
	if !isRole && catalog.KnownRoles[catalog.Role(q)] {
		isRole = true
		roleName = q
	}

	if cat == nil {
		cat = catalog.Default()
	}

	fallbacks := []string{}
	if isRole && roleName != "" {
		// Collect candidate fallback models supporting the role
		for _, cm := range cat.Models {
			if cm.ID != targetID && cm.SupportsRole(catalog.Role(roleName)) {
				tag := cm.ID
				if btag, ok := cm.Backends[backendName]; ok && btag != "" {
					tag = btag
				}
				fallbacks = append(fallbacks, tag)
			}
		}
	}

	// Try resolving through catalog
	if cm, ok := cat.GetModel(targetID); ok {
		backendTag := cm.ID
		if tag, exists := cm.Backends[backendName]; exists && tag != "" {
			backendTag = tag
		}
		return ResolvedTarget{
			BackendTag:   backendTag,
			CatalogModel: &cm,
			IsRole:       isRole,
			Role:         roleName,
			Fallbacks:    fallbacks,
		}
	}

	// Direct pass-through
	return ResolvedTarget{
		BackendTag:   targetID,
		CatalogModel: nil,
		IsRole:       isRole,
		Role:         roleName,
		Fallbacks:    fallbacks,
	}
}

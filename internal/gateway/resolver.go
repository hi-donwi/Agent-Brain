package gateway

import (
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
)

// ResolvedTarget holds model routing resolution.
type ResolvedTarget struct {
	BackendTag   string
	CatalogModel *catalog.Model
	IsRole       bool
}

// ResolveModel maps a requested model or role to its concrete backend tag
// using the active configuration and catalog.
func ResolveModel(query string, cfg *home.Config, cat *catalog.Catalog, backendName string) ResolvedTarget {
	q := strings.TrimSpace(query)
	if q == "" {
		return ResolvedTarget{}
	}

	targetID := q
	isRole := false

	// Check if query is an alias configured in config.yaml
	if cfg != nil && cfg.Aliases != nil {
		if aliasTarget, ok := cfg.Aliases[q]; ok && aliasTarget != "" {
			targetID = aliasTarget
			isRole = true
		}
	}

	if cat == nil {
		cat = catalog.Default()
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
		}
	}

	// Direct pass-through
	return ResolvedTarget{
		BackendTag:   targetID,
		CatalogModel: nil,
		IsRole:       isRole,
	}
}

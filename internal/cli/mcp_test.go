package cli

import (
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/store"
)

func TestMcpCmdConstruction(t *testing.T) {
	deps := McpDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Layout{}, nil
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return nil, nil
		},
		OpenStore: func(dbPath string) (*store.Store, error) {
			return nil, nil
		},
	}

	cmd := newMcpCmd(deps)
	if cmd.Use != "mcp" {
		t.Errorf("expected command use 'mcp', got %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("expected non-empty command short description")
	}
}

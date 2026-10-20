package cli

import (
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/store"
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
	if cmd.Flags().Lookup("root") == nil {
		t.Error("expected a --root flag to configure the project root of the agent policy contracts")
	}
	if cmd.Flags().Lookup("agent-policy") == nil {
		t.Error("expected an --agent-policy flag")
	}
}

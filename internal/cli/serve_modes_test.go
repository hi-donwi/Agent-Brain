package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

func TestServeRejectsInvalidAccountConfiguration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "accounts.yaml"), []byte("accounts:\n  - id: invalid\n    provider: unsupported\n"), 0600); err != nil {
		t.Fatal(err)
	}
	layout, _ := home.Resolve(func(string) string { return root }, nil)
	cmd := newServeCmd(ServeDeps{ResolveHome: func() (home.Layout, error) { return layout, nil }, ResolveAdapter: func() (backend.Adapter, error) { return &mockCLIAdapter{}, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("invalid account configuration was not rejected: %v", err)
	}
}

func TestServeModesCannotBeCombined(t *testing.T) {
	cmd := newServeCmd(defaultServeDeps())
	cmd.SetArgs([]string{"--cloud-only", "--local-only"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("invalid modes: %v", err)
	}
}

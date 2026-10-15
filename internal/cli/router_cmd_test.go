package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestRouterStatusDoesNotIgnoreInvalidAccountConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	if err := os.WriteFile(filepath.Join(root, "accounts.yaml"), []byte("strategy: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newRouterStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid configuration was reported as a healthy empty account pool")
	}
}

func TestRouterStrategyUsesValidatedMutation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	cmd := newRouterCmd()
	cmd.SetArgs([]string{"strategy", "round-robin"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := router.LoadRouterConfig(filepath.Join(root, "accounts.yaml"))
	if err != nil || cfg.Strategy != "round-robin" {
		t.Fatal("strategy was not saved")
	}
}

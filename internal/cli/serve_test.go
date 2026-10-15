package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

func TestServeCmd_Help(t *testing.T) {
	cmd := newServeCmd(defaultServeDeps())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "OpenAI-compatible") || !strings.Contains(out, "--port") {
		t.Errorf("unexpected help text: %s", out)
	}
}

func TestServeCmd_StartsAndShutsDown(t *testing.T) {
	tempHome := t.TempDir()
	adapter := &mockCLIAdapter{}

	deps := ServeDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return adapter, nil
		},
		Catalog: catalog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := newServeCmd(deps)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	// Pick an ephemeral port (0 lets OS pick, or high private port)
	cmd.SetArgs([]string{"--port", "52149"})

	// Execute with canceled context after 200ms
	err := cmd.ExecuteContext(ctx)
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("unexpected error: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "lokal gateway starting on http://127.0.0.1:52149") {
		t.Errorf("expected startup message, got:\n%s", out)
	}
	if !strings.Contains(out, "Bearer token:") {
		t.Errorf("expected bearer token line, got:\n%s", out)
	}

	// Verify token file was written with mode 0600
	tokenFile := filepath.Join(tempHome, "serve.token")
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("stat token file failed: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected mode 0600 on %s, got %o", tokenFile, info.Mode().Perm())
	}
}

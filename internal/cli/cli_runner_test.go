package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetToolProfileEnv(t *testing.T) {
	dir := "/tmp/test-profile"
	env := getToolProfileEnv("agy", "donwidev", dir)

	expectedKeys := []string{
		"CLAUDE_CONFIG_DIR=/tmp/test-profile",
		"ANTIGRAVITY_CONFIG_DIR=/tmp/test-profile",
		"CODEX_CONFIG_DIR=/tmp/test-profile",
		"OPENCODE_CONFIG_DIR=/tmp/test-profile",
		"AIDER_CONFIG_DIR=/tmp/test-profile",
		"XDG_CONFIG_HOME=/tmp/test-profile",
		"AGY_ACCOUNT=donwidev",
	}

	for _, k := range expectedKeys {
		found := false
		for _, e := range env {
			if e == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected env %q in profile env list", k)
		}
	}
}

func TestRunListProfiles(t *testing.T) {
	tempBase := t.TempDir()

	// 1. Test empty profiles
	err := runListProfiles(tempBase)
	if err != nil {
		t.Fatalf("unexpected error on empty profiles: %v", err)
	}

	// 2. Create mock profiles
	workClaudeDir := filepath.Join(tempBase, "claude", "work")
	personalClaudeDir := filepath.Join(tempBase, "claude", "personal")
	agyDevDir := filepath.Join(tempBase, "agy", "google-dev")

	_ = os.MkdirAll(workClaudeDir, 0o700)
	_ = os.MkdirAll(personalClaudeDir, 0o700)
	_ = os.MkdirAll(agyDevDir, 0o700)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runListProfiles(tempBase)
	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runListProfiles failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "claude") || !strings.Contains(out, "work") {
		t.Errorf("expected claude work profile in output, got:\n%s", out)
	}
	if !strings.Contains(out, "personal") {
		t.Errorf("expected personal profile in output, got:\n%s", out)
	}
	if !strings.Contains(out, "agy") || !strings.Contains(out, "google-dev") {
		t.Errorf("expected agy google-dev profile in output, got:\n%s", out)
	}
}

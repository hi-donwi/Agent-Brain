package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetToolProfileEnv(t *testing.T) {
	dir := t.TempDir()
	if got := getToolProfileEnv("agy", "work", dir); len(got) != 0 {
		t.Fatal("unverified Antigravity profile variables injected")
	}
	if got := getToolProfileEnv("codex", "work", dir); len(got) != 1 || got[0] != "CODEX_HOME="+dir {
		t.Fatalf("Codex profile variables: %v", got)
	}
	if got := getToolProfileEnv("claude", "work", dir); len(got) != 1 || got[0] != "CLAUDE_CONFIG_DIR="+dir {
		t.Fatalf("Claude profile variables: %v", got)
	}
}

func TestParseCLIArgsPreservesToolDelimiter(t *testing.T) {
	got, err := parseCLIArgs([]string{"--account", "openai/work", "codex", "--", "--account", "tool-account", "--direct"})
	if err != nil || got.account != "openai/work" || got.direct || strings.Join(got.args, " ") != "--account tool-account --direct" {
		t.Fatalf("bad parser result: %+v %v", got, err)
	}
	for _, args := range [][]string{{"--account"}, {"--port", "bad", "codex"}, {"--unknown", "codex"}} {
		if _, err := parseCLIArgs(args); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
}

func TestMergeEnvironmentRemovesInheritedCredential(t *testing.T) {
	got := mergeProcessEnv([]string{"OPENAI_API_KEY=old", "PATH=test", "openai_api_key=other"}, []string{"OPENAI_API_KEY=scoped"}, "OPENAI_API_KEY")
	if strings.Count(strings.ToUpper(strings.Join(got, "\n")), "OPENAI_API_KEY=") != 1 || !strings.Contains(strings.Join(got, "\n"), "OPENAI_API_KEY=scoped") {
		t.Fatalf("duplicate or inherited credential: %v", got)
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

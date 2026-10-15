package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
)

func TestTracesCmd_ListEmptyAndPopulated(t *testing.T) {
	tempHome := t.TempDir()
	deps := TracesDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
	}

	layout, _ := deps.ResolveHome()
	_ = layout.Ensure()

	// 1. List empty
	cmd := newTracesListCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces ls failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No trace sessions recorded yet.") {
		t.Errorf("unexpected output on empty: %s", buf.String())
	}

	// 2. Add a record and list
	store := trace.NewStore(layout.Traces)
	_ = store.WriteRecord(&trace.Record{
		SessionID:    "test-sess-100",
		Model:        "qwen2.5-coder-1.5b",
		PromptTokens: 50,
		EvalTokens:   100,
	})

	buf.Reset()
	cmd = newTracesListCmd(deps)
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces ls failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "test-sess-100") || !strings.Contains(out, "qwen2.5-coder-1.5b") {
		t.Errorf("expected session listed, got: %s", out)
	}

	// 3. List JSON
	buf.Reset()
	cmd = newTracesListCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces ls --json failed: %v", err)
	}
	if !strings.Contains(buf.String(), `"session_id": "test-sess-100"`) {
		t.Errorf("expected JSON output with session_id, got: %s", buf.String())
	}
}

func TestTracesCmd_Show(t *testing.T) {
	tempHome := t.TempDir()
	deps := TracesDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
	}

	layout, _ := deps.ResolveHome()
	_ = layout.Ensure()
	store := trace.NewStore(layout.Traces)

	_ = store.WriteRecord(&trace.Record{
		SessionID: "show-sess-1",
		TurnID:    "turn-1",
		Model:     "model-show",
		Messages:  []trace.Message{{Role: "user", Content: "show prompt"}},
		Response:  "show response",
	})

	// Text show
	cmd := newTracesShowCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"show-sess-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces show failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "show prompt") || !strings.Contains(out, "show response") {
		t.Errorf("unexpected output: %s", out)
	}

	// JSON show
	buf.Reset()
	cmd = newTracesShowCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"show-sess-1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces show --json failed: %v", err)
	}
	if !strings.Contains(buf.String(), `"show response"`) {
		t.Errorf("unexpected json output: %s", buf.String())
	}
}

func TestTracesCmd_Import(t *testing.T) {
	tempHome := t.TempDir()
	deps := TracesDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
	}

	rawJSONL := `{"type": "USER_INPUT", "content": "import me"}
{"type": "PLANNER_RESPONSE", "content": "imported!"}
`
	importFile := filepath.Join(tempHome, "transcript.jsonl")
	if err := os.WriteFile(importFile, []byte(rawJSONL), 0o600); err != nil {
		t.Fatalf("write import file failed: %v", err)
	}

	cmd := newTracesImportCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{importFile, "--session", "imported-via-cli"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("traces import failed: %v", err)
	}

	if !strings.Contains(buf.String(), "Successfully imported 1 records") {
		t.Errorf("unexpected import output: %s", buf.String())
	}

	layout, _ := deps.ResolveHome()
	store := trace.NewStore(layout.Traces)
	records, err := store.ReadSession("imported-via-cli")
	if err != nil || len(records) != 1 {
		t.Fatalf("expected 1 imported record in store, got %d, err: %v", len(records), err)
	}
}

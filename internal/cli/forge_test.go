package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
)

func TestForgeCLI_FullCycle(t *testing.T) {
	tempHome := t.TempDir()
	deps := ForgeDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return nil, nil
		},
	}

	layout, _ := deps.ResolveHome()
	_ = layout.Ensure()

	// 1. Mine empty traces
	cmd := newForgeMineCmd(deps)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("forge mine empty failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No repeatable trace patterns found") {
		t.Errorf("unexpected mine output: %s", buf.String())
	}

	// 2. Populate 2 traces to form a cluster
	store := trace.NewStore(layout.Traces)
	for i := 1; i <= 2; i++ {
		_ = store.WriteRecord(&trace.Record{
			SessionID: "sess-" + string(rune('0'+i)),
			TurnID:    "t1",
			Messages:  []trace.Message{{Role: "user", Content: "run benchmark and test"}},
			ToolCalls: []trace.ToolCall{
				{Name: "benchmark_tool"},
				{Name: "test_tool"},
			},
			PromptTokens: 80,
			EvalTokens:   40,
		})
	}

	// 3. Mine with cluster
	buf.Reset()
	cmd = newForgeMineCmd(deps)
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("forge mine failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "benchmark_tool") {
		t.Errorf("expected cluster listed in mine output: %s", out)
	}

	// Also test mine --json
	buf.Reset()
	cmd = newForgeMineCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("forge mine --json failed: %v", err)
	}
	if !strings.Contains(buf.String(), `"signature": "benchmark_tool -> test_tool"`) {
		t.Errorf("expected signature in JSON output: %s", buf.String())
	}

	// Extract cluster ID
	lines := strings.Split(out, "\n")
	var clusterID string
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "cl-") {
			clusterID = fields[0]
			break
		}
	}
	if clusterID == "" {
		t.Fatalf("could not extract cluster ID from mine output: %s", out)
	}

	// 4. Draft candidate
	buf.Reset()
	draftCmd := newForgeDraftCmd(deps)
	draftCmd.SetOut(&buf)
	draftCmd.SetArgs([]string{clusterID, "--name", "bench-test-skill"})
	if err := draftCmd.Execute(); err != nil {
		t.Fatalf("forge draft failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Drafted candidate skill: bench-test-skill") {
		t.Errorf("unexpected draft output: %s", buf.String())
	}

	// 5. Eval candidate
	buf.Reset()
	evalCmd := newForgeEvalCmd(deps)
	evalCmd.SetOut(&buf)
	evalCmd.SetArgs([]string{"bench-test-skill"})
	if err := evalCmd.Execute(); err != nil {
		t.Fatalf("forge eval failed: %v", err)
	}
	evalOut := buf.String()
	if !strings.Contains(evalOut, "Candidate Evaluation: bench-test-skill") || !strings.Contains(evalOut, "Status:               PASS") {
		t.Errorf("unexpected eval output: %s", evalOut)
	}

	// 6. Harden candidate
	buf.Reset()
	hardenCmd := newForgeHardenCmd(deps)
	hardenCmd.SetOut(&buf)
	hardenCmd.SetArgs([]string{"bench-test-skill"})
	if err := hardenCmd.Execute(); err != nil {
		t.Fatalf("forge harden failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Hardening Complete for: bench-test-skill") {
		t.Errorf("unexpected harden output: %s", buf.String())
	}

	// 7. Skills promote
	skillsDeps := SkillsDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
	}
	buf.Reset()
	promoteCmd := newSkillsPromoteCmd(skillsDeps)
	promoteCmd.SetOut(&buf)
	promoteCmd.SetArgs([]string{"bench-test-skill"})
	if err := promoteCmd.Execute(); err != nil {
		t.Fatalf("skills promote failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Successfully promoted bench-test-skill") {
		t.Errorf("unexpected promote output: %s", buf.String())
	}

	// 8. Skills export
	buf.Reset()
	exportDir := filepath.Join(tempHome, "exported")
	exportCmd := newSkillsExportCmd(skillsDeps)
	exportCmd.SetOut(&buf)
	exportCmd.SetArgs([]string{"bench-test-skill", "--out", exportDir})
	if err := exportCmd.Execute(); err != nil {
		t.Fatalf("skills export failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Successfully exported skill bench-test-skill") {
		t.Errorf("unexpected export output: %s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(exportDir, "bench-test-skill", "SKILL.md")); err != nil {
		t.Fatalf("exported skill missing SKILL.md: %v", err)
	}
}

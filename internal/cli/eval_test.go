package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvalCmd_PassingSpec(t *testing.T) {
	tempDir := t.TempDir()
	specFile := filepath.Join(tempDir, "passing-spec.yaml")

	content := `
id: cli-eval-pass
name: CLI Eval Passing Task
agent_command: "echo 'hello eval' > result.txt"
assertions:
  file_exists:
    - result.txt
  file_contains:
    result.txt: "hello eval"
  command_exit_zero:
    - "grep -q 'hello' result.txt"
`
	if err := os.WriteFile(specFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write spec failed: %v", err)
	}

	cmd := newEvalCmd(EvalDeps{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{specFile})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("eval failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Status:          PASS") {
		t.Errorf("expected PASS status, got: %s", out)
	}
	if !strings.Contains(out, "[PASS]") {
		t.Errorf("expected [PASS] assertions, got: %s", out)
	}
}

func TestEvalCmd_JSONOutput(t *testing.T) {
	tempDir := t.TempDir()
	specFile := filepath.Join(tempDir, "json-spec.yaml")

	content := `
id: cli-eval-json
name: CLI Eval JSON Task
agent_command: "echo 'ok' > out.txt"
assertions:
  file_exists:
    - out.txt
`
	if err := os.WriteFile(specFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write spec failed: %v", err)
	}

	cmd := newEvalCmd(EvalDeps{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{specFile, "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("eval --json failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"task_id": "cli-eval-json"`) || !strings.Contains(out, `"passed": true`) {
		t.Errorf("unexpected json output: %s", out)
	}
}

func TestEvalCmd_FailingSpec(t *testing.T) {
	tempDir := t.TempDir()
	specFile := filepath.Join(tempDir, "fail-spec.yaml")

	content := `
id: cli-eval-fail
name: CLI Eval Failing Task
agent_command: "echo 'wrong' > out.txt"
assertions:
  file_contains:
    out.txt: "expected-value"
`
	if err := os.WriteFile(specFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write spec failed: %v", err)
	}

	cmd := newEvalCmd(EvalDeps{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{specFile})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error on failing spec, got nil")
	}

	out := buf.String()
	if !strings.Contains(out, "Status:          FAIL") {
		t.Errorf("expected FAIL status in output, got: %s", out)
	}
	if !strings.Contains(out, "[FAIL]") {
		t.Errorf("expected [FAIL] in assertions, got: %s", out)
	}
}

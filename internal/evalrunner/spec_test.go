package evalrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTaskSpec_YAML(t *testing.T) {
	yamlContent := `
id: test-go-fix
name: Go Fix Syntax Task
description: Fixes syntax in a small Go file
model: qwen2.5-coder-1.5b
timeout_seconds: 30
max_tokens: 500
prompt: "Fix the syntax error in main.go"
agent_command: "echo 'fixed' > main.go"
setup_files:
  main.go: "packge main"
assertions:
  file_exists:
    - main.go
  file_contains:
    main.go: "fixed"
  command_exit_zero:
    - "grep fixed main.go"
  output_contains:
    - "syntax"
`
	spec, err := ParseTaskSpec([]byte(yamlContent))
	if err != nil {
		t.Fatalf("ParseTaskSpec failed: %v", err)
	}

	if spec.ID != "test-go-fix" {
		t.Errorf("expected ID test-go-fix, got %s", spec.ID)
	}
	if spec.TimeoutSeconds != 30 {
		t.Errorf("expected timeout 30, got %d", spec.TimeoutSeconds)
	}
	if len(spec.SetupFiles) != 1 || spec.SetupFiles["main.go"] != "packge main" {
		t.Errorf("unexpected setup files: %+v", spec.SetupFiles)
	}
	if len(spec.Assertions.FileExists) != 1 || spec.Assertions.FileExists[0] != "main.go" {
		t.Errorf("unexpected file exists assertions: %+v", spec.Assertions.FileExists)
	}
}

func TestLoadTaskSpec_File(t *testing.T) {
	tempDir := t.TempDir()
	specPath := filepath.Join(tempDir, "spec.yaml")

	content := `
id: minimal-task
prompt: "Say hello"
`
	if err := os.WriteFile(specPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write spec file failed: %v", err)
	}

	spec, err := LoadTaskSpec(specPath)
	if err != nil {
		t.Fatalf("LoadTaskSpec failed: %v", err)
	}

	if spec.ID != "minimal-task" {
		t.Errorf("expected ID minimal-task, got %s", spec.ID)
	}
	if spec.TimeoutSeconds != 60 {
		t.Errorf("expected default timeout 60, got %d", spec.TimeoutSeconds)
	}
}

func TestParseTaskSpec_MissingID(t *testing.T) {
	_, err := ParseTaskSpec([]byte(`prompt: "hello"`))
	if err == nil {
		t.Fatalf("expected error for missing ID, got nil")
	}
}

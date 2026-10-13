package evalrunner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSandbox_SetupAndExec(t *testing.T) {
	sandbox, err := NewSandbox()
	if err != nil {
		t.Fatalf("NewSandbox failed: %v", err)
	}
	defer sandbox.Cleanup()

	files := map[string]string{
		"src/hello.txt": "hello world from sandbox",
		"README.md":     "# Sandbox Readme",
	}

	if err := sandbox.Setup(files); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify files written
	content, err := os.ReadFile(filepath.Join(sandbox.Dir, "src/hello.txt"))
	if err != nil || string(content) != "hello world from sandbox" {
		t.Fatalf("unexpected content in src/hello.txt: %s, err: %v", string(content), err)
	}

	// Exec command
	stdout, stderr, exitCode, err := sandbox.ExecCommand(context.Background(), "cat src/hello.txt", nil)
	if err != nil || exitCode != 0 {
		t.Fatalf("ExecCommand failed: code=%d, err=%v, stderr=%s", exitCode, err, stderr)
	}
	if stdout != "hello world from sandbox" {
		t.Errorf("unexpected stdout: %s", stdout)
	}
}

func TestSandbox_PathTraversalBlocked(t *testing.T) {
	sandbox, err := NewSandbox()
	if err != nil {
		t.Fatalf("NewSandbox failed: %v", err)
	}
	defer sandbox.Cleanup()

	files := map[string]string{
		"../../outside.txt": "forbidden",
	}

	if err := sandbox.Setup(files); err == nil {
		t.Fatalf("expected error for path traversal, got nil")
	}
}

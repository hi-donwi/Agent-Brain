package evalrunner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Sandbox provides an isolated directory environment for task evaluation.
type Sandbox struct {
	Dir string
}

// NewSandbox creates a throwaway temporary directory sandbox.
func NewSandbox() (*Sandbox, error) {
	dir, err := os.MkdirTemp("", "lokal-eval-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox tempdir: %w", err)
	}
	return &Sandbox{Dir: dir}, nil
}

// Setup populates files in the sandbox directory.
func (s *Sandbox) Setup(files map[string]string) error {
	for relPath, content := range files {
		cleanRel := filepath.Clean(relPath)
		if filepath.IsAbs(cleanRel) || cleanRel == ".." || filepath.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid relative file path in setup_files: %s", relPath)
		}

		targetPath := filepath.Join(s.Dir, cleanRel)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
			return fmt.Errorf("create dir for %s: %w", relPath, err)
		}

		if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write setup file %s: %w", relPath, err)
		}
	}
	return nil
}

// ExecCommand runs a command with its working directory set to the sandbox.
func (s *Sandbox) ExecCommand(ctx context.Context, cmdStr string, extraEnv []string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return stdout.String(), stderr.String(), exitCode, err
}

// Cleanup removes the sandbox directory and all its contents.
func (s *Sandbox) Cleanup() error {
	if s.Dir != "" {
		return os.RemoveAll(s.Dir)
	}
	return nil
}

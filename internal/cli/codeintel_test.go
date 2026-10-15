package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/codeintel"
)

func TestCodeIntelCLI(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "calculator.go")
	mustWriteTestFile(t, file1, `package calc

type Calculator struct {}

func (c *Calculator) Add(a, b int) int {
	return a + b
}
`)

	file2 := filepath.Join(tmpDir, "main.go")
	mustWriteTestFile(t, file2, `package main

import "calc"

func main() {
	c := &calc.Calculator{}
	_ = c.Add(1, 2)
}
`)

	// 1. Test "repomap"
	t.Run("repomap human", func(t *testing.T) {
		cmd := newRepoMapCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{tmpDir})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("repomap execution failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "calculator.go") || !strings.Contains(out, "Calculator") {
			t.Errorf("unexpected repomap output: %s", out)
		}
	})

	// 2. Test "repomap --json"
	t.Run("repomap --json", func(t *testing.T) {
		cmd := newRepoMapCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{tmpDir, "--json"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("repomap --json failed: %v", err)
		}

		var rm codeintel.RepoMap
		if err := json.Unmarshal(buf.Bytes(), &rm); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}
		if rm.FileCount != 2 {
			t.Errorf("expected 2 files in json, got %d", rm.FileCount)
		}
	})

	// 3. Test "symbols --query Add"
	t.Run("symbols --query Add", func(t *testing.T) {
		cmd := newSymbolsCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{tmpDir, "--query", "Add"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("symbols query failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "Definitions:") || !strings.Contains(out, "References") {
			t.Errorf("unexpected symbols output: %s", out)
		}
	})

	// 4. Test "symbols --regex 'func main'"
	t.Run("symbols --regex", func(t *testing.T) {
		cmd := newSymbolsCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{tmpDir, "--regex", "func main"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("symbols regex failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "main.go") {
			t.Errorf("unexpected regex output: %s", out)
		}
	})
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

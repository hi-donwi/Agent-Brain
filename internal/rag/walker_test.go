package rag_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/rag"
)

func TestWalker_FilterAndIgnore(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Text file
	txtFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(txtFile, []byte("Hello text world"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Binary file (contains null byte)
	binFile := filepath.Join(tmpDir, "image.bin")
	if err := os.WriteFile(binFile, []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Ignored directory
	gitDir := filepath.Join(tmpDir, ".git")
	_ = os.MkdirAll(gitDir, 0o755)
	_ = os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0o644)

	// 4. .gitignore file
	_ = os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored.md\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "ignored.md"), []byte("should be ignored"), 0o644)

	files, err := rag.WalkFiles(tmpDir)
	if err != nil {
		t.Fatalf("WalkFiles failed: %v", err)
	}

	if len(files) != 1 || files[0] != "hello.txt" {
		t.Fatalf("expected only hello.txt, got: %v", files)
	}

	isBin, err := rag.IsBinaryFile(binFile)
	if err != nil || !isBin {
		t.Errorf("expected image.bin to be identified as binary, got %v (%v)", isBin, err)
	}

	isTxtBin, err := rag.IsBinaryFile(txtFile)
	if err != nil || isTxtBin {
		t.Errorf("expected hello.txt to be text, got %v (%v)", isTxtBin, err)
	}
}

//go:build linux || darwin

package hw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreeBytesOfAnExistingDirectory(t *testing.T) {
	free, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free == 0 {
		t.Fatal("FreeBytes reported 0 for the test's temporary directory")
	}
}

func TestFreeBytesOfAPathNotCreatedYet(t *testing.T) {
	// Setup checks the disk for ~/.lokal or a model directory before it creates
	// them, so a missing path reports the filesystem it would be created on.
	missing := filepath.Join(t.TempDir(), "not", "created", "yet")

	free, err := FreeBytes(missing)
	if err != nil {
		t.Fatalf("FreeBytes(%s): %v", missing, err)
	}
	if free == 0 {
		t.Fatal("FreeBytes reported 0 for a path under the temporary directory")
	}
}

func TestFreeBytesFailsWhenAnAncestorIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := FreeBytes(filepath.Join(file, "child")); err == nil {
		t.Fatal("FreeBytes must fail when a path component is a regular file")
	}
}

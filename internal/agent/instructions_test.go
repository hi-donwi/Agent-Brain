package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rels(in Instructions) []string {
	var out []string
	for _, f := range in.Files {
		out = append(out, f.Rel)
	}
	return out
}

func TestLoadInstructionsOrdersRootFirstNearestLast(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "root rules")
	writeFile(t, filepath.Join(root, "svc", "AGENTS.md"), "service rules")
	writeFile(t, filepath.Join(root, "svc", "api", "AGENTS.md"), "api rules")

	got, err := LoadInstructions(root, filepath.Join(root, "svc", "api"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AGENTS.md", "svc/AGENTS.md", "svc/api/AGENTS.md"}
	if g := rels(got); strings.Join(g, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", g, want)
	}
	if got.Files[2].Content != "api rules" {
		t.Errorf("content = %q", got.Files[2].Content)
	}
}

func TestLoadInstructionsPicksOneFilePerDirectoryByPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "claude")
	writeFile(t, filepath.Join(root, "GEMINI.md"), "gemini")
	writeFile(t, filepath.Join(root, "sub", "GEMINI.md"), "sub gemini")
	writeFile(t, filepath.Join(root, "sub", "CLAUDE.md"), "sub claude")

	got, err := LoadInstructions(root, filepath.Join(root, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CLAUDE.md", "sub/CLAUDE.md"}
	if g := rels(got); strings.Join(g, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", g, want)
	}

	writeFile(t, filepath.Join(root, "AGENTS.md"), "agents")
	got, err = LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Rel != "AGENTS.md" {
		t.Fatalf("AGENTS.md should win over CLAUDE.md and GEMINI.md, got %v", rels(got))
	}
}

func TestLoadInstructionsWithNoFilesIsEmptyNotAnError(t *testing.T) {
	root := t.TempDir()
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 0 {
		t.Fatalf("files = %v, want none", rels(got))
	}
	if got.Render() != "" {
		t.Errorf("empty instructions must render to an empty string")
	}
}

func TestLoadInstructionsRejectsDirectoryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if _, err := LoadInstructions(root, other); err == nil {
		t.Fatal("expected an error for a directory outside the root")
	}
	if _, err := LoadInstructions(root, filepath.Join(root, "..", "elsewhere")); err == nil {
		t.Fatal("expected an error for a traversal outside the root")
	}
}

func TestLoadInstructionsSkipsSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.md"), "do not read")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 0 {
		t.Fatalf("an escaping symlink must not be read, got %v", rels(got))
	}
	if len(got.Skipped) == 0 {
		t.Errorf("the skipped file should be reported")
	}
}

func TestLoadInstructionsSkipsNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 0 {
		t.Fatalf("a directory named AGENTS.md must be skipped, got %v", rels(got))
	}
}

func TestLoadInstructionsTruncatesLargeFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("a", MaxInstructionFileBytes+500))
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	f := got.Files[0]
	if !f.Truncated || len(f.Content) != MaxInstructionFileBytes {
		t.Fatalf("truncated=%v len=%d, want truncated at %d", f.Truncated, len(f.Content), MaxInstructionFileBytes)
	}
}

func TestLoadInstructionsTruncationKeepsValidUTF8(t *testing.T) {
	root := t.TempDir()
	// Three-byte runes that straddle the byte limit.
	body := strings.Repeat("\u20ac", MaxInstructionFileBytes)
	writeFile(t, filepath.Join(root, "AGENTS.md"), body)
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	c := got.Files[0].Content
	if len(c) > MaxInstructionFileBytes || strings.ContainsRune(c, '\uFFFD') {
		t.Fatalf("truncation split a rune: len=%d", len(c))
	}
}

func TestLoadInstructionsAppliesTotalBudgetKeepingNearestFiles(t *testing.T) {
	root := t.TempDir()
	chunk := strings.Repeat("x", MaxInstructionFileBytes)
	dir := root
	writeFile(t, filepath.Join(dir, "AGENTS.md"), chunk)
	for _, d := range []string{"a", "b", "c", "d"} {
		dir = filepath.Join(dir, d)
		writeFile(t, filepath.Join(dir, "AGENTS.md"), chunk)
	}
	got, err := LoadInstructions(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, f := range got.Files {
		total += len(f.Content)
	}
	if total > MaxInstructionTotalBytes {
		t.Fatalf("total %d exceeds budget %d", total, MaxInstructionTotalBytes)
	}
	last := got.Files[len(got.Files)-1]
	if last.Rel != "a/b/c/d/AGENTS.md" {
		t.Errorf("the nearest file must survive the budget, last = %s", last.Rel)
	}
	if len(got.Skipped) == 0 {
		t.Errorf("dropped files should be reported")
	}
}

func TestRenderFramesInstructionsAsGuidanceNotAuthority(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "Use tabs.")
	got, err := LoadInstructions(root, root)
	if err != nil {
		t.Fatal(err)
	}
	out := got.Render()
	for _, want := range []string{"AGENTS.md", "Use tabs.", "do not grant"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

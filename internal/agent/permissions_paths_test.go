package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkInsideProjectCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p := Policy{Root: root, Rules: []Rule{{Tool: ToolRead, Path: "**", Decision: Allow}}}

	if got := p.Decide(Request{Tool: ToolRead, Path: "link/secret.txt"}); got != Deny {
		t.Errorf("read through escaping link = %q, want deny", got)
	}
	if got := p.Decide(Request{Tool: ToolWrite, Path: "link/new.txt"}); got != Deny {
		t.Errorf("write through escaping link = %q, want deny", got)
	}
}

func TestSymlinkWithinProjectIsFollowed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// The rule protects the real location; reaching it through an alias must not
	// sidestep the rule.
	p := Policy{Root: root, Rules: []Rule{
		{Tool: ToolEdit, Path: "**", Decision: Allow},
		{Tool: ToolEdit, Path: "real/**", Decision: Deny},
	}}
	if got := p.Decide(Request{Tool: ToolEdit, Path: "alias/a.txt"}); got != Deny {
		t.Errorf("edit through in-project alias = %q, want deny", got)
	}
}

func TestNotYetExistingPathIsJudgedUnderItsParent(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{{Tool: ToolWrite, Path: "out/**", Decision: Allow}}}
	if got := p.Decide(Request{Tool: ToolWrite, Path: "out/new/file.txt"}); got != Allow {
		t.Errorf("write to a new nested file = %q, want allow", got)
	}
}

func TestSearchWithoutPathMeansTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{{Tool: ToolSearch, Decision: Allow}}}
	if got := p.Decide(Request{Tool: ToolSearch}); got != Allow {
		t.Errorf("search of the root = %q, want allow", got)
	}
}

func TestUnboundPolicyDeniesPaths(t *testing.T) {
	p := Policy{Rules: []Rule{{Tool: ToolRead, Path: "**", Decision: Allow}}}
	if got := p.Decide(Request{Tool: ToolRead, Path: "a.txt"}); got != Deny {
		t.Errorf("policy without a root = %q, want deny", got)
	}
}

func TestUnknownToolIsDenied(t *testing.T) {
	p := Policy{Root: t.TempDir(), Default: Allow}
	if got := p.Decide(Request{Tool: "teleport"}); got != Deny {
		t.Errorf("unknown tool = %q, want deny", got)
	}
}

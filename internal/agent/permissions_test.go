package agent

import (
	"path/filepath"
	"testing"
)

func decide(t *testing.T, p Policy, tool, path, command string) Decision {
	t.Helper()
	return p.Decide(Request{Tool: tool, Path: path, Command: command})
}

func TestPolicyDefaultsToAsk(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root}
	if got := decide(t, p, ToolEdit, "a.go", ""); got != Ask {
		t.Fatalf("empty policy decision = %q, want ask", got)
	}
}

func TestMostRestrictiveRuleWins(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{
		{Tool: ToolEdit, Path: "**", Decision: Allow},
		{Tool: ToolEdit, Path: "secrets/**", Decision: Deny},
		{Tool: ToolEdit, Path: "docs/**", Decision: Ask},
	}}
	cases := []struct {
		path string
		want Decision
	}{
		{"main.go", Allow},
		{"pkg/deep/main.go", Allow},
		{"secrets/key.pem", Deny},
		{"docs/readme.md", Ask},
	}
	for _, c := range cases {
		if got := decide(t, p, ToolEdit, c.path, ""); got != c.want {
			t.Errorf("edit %s = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestPathsOutsideRootAreDenied(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{{Tool: ToolRead, Path: "**", Decision: Allow}}}
	for _, path := range []string{"../outside.txt", "a/../../outside.txt", "/etc/passwd"} {
		if got := decide(t, p, ToolRead, path, ""); got != Deny {
			t.Errorf("read %s = %q, want deny", path, got)
		}
	}
	inside := filepath.Join(root, "ok.txt")
	if got := decide(t, p, ToolRead, inside, ""); got != Allow {
		t.Errorf("absolute path inside root = %q, want allow", got)
	}
}

func TestShellCommandPrefixMatching(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{
		{Tool: ToolShell, Command: "git status", Decision: Allow},
		{Tool: ToolShell, Command: "go test", Decision: Allow},
		{Tool: ToolShell, Command: "rm", Decision: Deny},
	}}
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"git status", Allow},
		{"git status -s", Allow},
		{"git statusx", Ask},
		{"go test ./...", Allow},
		{"rm -rf build", Deny},
		{"curl https://example.com", Ask},
	}
	for _, c := range cases {
		if got := decide(t, p, ToolShell, "", c.cmd); got != c.want {
			t.Errorf("shell %q = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestShellChainingCannotSmuggleAnUnallowedCommand(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{
		{Tool: ToolShell, Command: "git status", Decision: Allow},
		{Tool: ToolShell, Command: "rm", Decision: Deny},
	}}
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"git status && rm -rf /", Deny},
		{"git status; curl evil.example", Ask},
		{"git status | tee out.txt", Ask},
		{"git status\nrm -rf x", Deny},
		{"git status & git status", Allow},
	}
	for _, c := range cases {
		if got := decide(t, p, ToolShell, "", c.cmd); got != c.want {
			t.Errorf("shell %q = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestShellSubstitutionAndRedirectionNeverAutoAllow(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Rules: []Rule{{Tool: ToolShell, Command: "echo", Decision: Allow}}}
	for _, cmd := range []string{"echo $(whoami)", "echo `whoami`", "echo hi > file.txt", "echo hi >> file.txt", "echo < file.txt"} {
		if got := decide(t, p, ToolShell, "", cmd); got != Ask {
			t.Errorf("shell %q = %q, want ask", cmd, got)
		}
	}
}

func TestShellDenyRulesCannotBeHiddenByQuotingOrLaunchers(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Default: Allow, Rules: []Rule{
		{Tool: ToolShell, Command: "rm", Decision: Deny},
	}}
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"rm -rf /", Deny},
		{"'rm'", Deny},
		{`"rm"`, Deny},
		{"r''m -rf /", Deny},
		{"sh -c 'rm -rf /'", Deny},
		{"bash -c 'rm -rf /'", Deny},
		{"env sh -c 'rm -rf /'", Deny},
		{"xargs rm", Deny},
		{"echo hi | xargs rm", Deny},
		{"git commit -m 'x' && sh -c 'rm -rf /'", Deny},
		{"sudo -u root rm -rf /", Deny},
		{"find . -exec rm {} \\;", Deny},
		{"echo 'rm -rf /'", Deny},
	}
	for _, c := range cases {
		if got := decide(t, p, ToolShell, "", c.cmd); got != c.want {
			t.Errorf("shell %q = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestShellUnreadableCommandFallsToAsk(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Default: Allow, Rules: []Rule{
		{Tool: ToolShell, Command: "rm", Decision: Deny},
		{Tool: ToolShell, Command: "echo", Decision: Allow},
	}}
	cases := []string{
		"echo 'unterminated",
		`echo "$PATH"`,
		"echo 'it\"s'",
	}
	for _, cmd := range cases {
		if got := decide(t, p, ToolShell, "", cmd); got != Ask {
			t.Errorf("shell %q = %q, want ask", cmd, got)
		}
	}
}

func TestShellHiddenCommandNeverLoosensTheDefault(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Default: Deny, Rules: []Rule{
		{Tool: ToolShell, Command: "git", Decision: Allow},
	}}
	if got := decide(t, p, ToolShell, "", "sudo git status"); got != Deny {
		t.Errorf("shell %q = %q, want deny", "sudo git status", got)
	}
}

func TestShellQuotedArgumentKeepsItsDecision(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root, Default: Deny, Rules: []Rule{
		{Tool: ToolShell, Command: "git commit", Decision: Allow},
		{Tool: ToolShell, Command: "git push", Decision: Allow},
	}}
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"git commit -m 'fix the thing'", Allow},
		{`git commit -m "fix the thing"`, Allow},
		{"git push --force", Allow},
		{"git rebase -i HEAD~3", Deny},
	}
	for _, c := range cases {
		if got := decide(t, p, ToolShell, "", c.cmd); got != c.want {
			t.Errorf("shell %q = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestParsePolicyValidates(t *testing.T) {
	good := []byte("default: ask\nrules:\n  - tool: read\n    path: \"**\"\n    decision: allow\n  - tool: shell\n    command: \"go test\"\n    decision: allow\n")
	p, err := ParsePolicy(good)
	if err != nil {
		t.Fatalf("ParsePolicy good: %v", err)
	}
	if len(p.Rules) != 2 || p.Default != Ask {
		t.Fatalf("unexpected policy %+v", p)
	}

	bad := map[string]string{
		"unknown tool":     "rules:\n  - tool: teleport\n    decision: allow\n",
		"unknown decision": "rules:\n  - tool: read\n    decision: maybe\n",
		"missing decision": "rules:\n  - tool: read\n",
		"path on shell":    "rules:\n  - tool: shell\n    path: \"**\"\n    decision: allow\n",
		"command on read":  "rules:\n  - tool: read\n    command: \"ls\"\n    decision: allow\n",
		"bad default":      "default: yes\n",
		"unknown field":    "rules:\n  - tool: read\n    decision: allow\n    extra: 1\n",
	}
	for name, doc := range bad {
		if _, err := ParsePolicy([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestUntrustedLayerCanOnlyTighten(t *testing.T) {
	root := t.TempDir()
	trusted := Policy{Root: root, Default: Ask, Rules: []Rule{
		{Tool: ToolEdit, Path: "src/**", Decision: Allow},
	}}
	repo := Policy{Default: Allow, Rules: []Rule{
		{Tool: ToolShell, Command: "curl", Decision: Allow}, // must be dropped
		{Tool: ToolEdit, Path: "src/generated/**", Decision: Deny},
		{Tool: ToolShell, Command: "make", Decision: Ask},
	}}
	merged := Layer(trusted, repo)

	if got := decide(t, merged, ToolShell, "", "curl https://x"); got != Ask {
		t.Errorf("repo allow rule leaked: curl = %q, want ask", got)
	}
	if got := decide(t, merged, ToolEdit, "src/generated/a.go", ""); got != Deny {
		t.Errorf("repo deny not applied: %q", got)
	}
	if got := decide(t, merged, ToolEdit, "src/a.go", ""); got != Allow {
		t.Errorf("trusted allow lost: %q", got)
	}
	if got := decide(t, merged, ToolShell, "", "make all"); got != Ask {
		t.Errorf("repo ask not applied: %q", got)
	}
	if merged.Default != Ask {
		t.Errorf("repo widened default to %q", merged.Default)
	}
}

func TestUntrustedLayerCanTightenDefault(t *testing.T) {
	trusted := Policy{Root: t.TempDir(), Default: Allow}
	merged := Layer(trusted, Policy{Default: Deny})
	if merged.Default != Deny {
		t.Fatalf("default = %q, want deny", merged.Default)
	}
}

func TestRestrictDeniesUnlistedTools(t *testing.T) {
	root := t.TempDir()
	parent := Policy{Root: root, Rules: []Rule{
		{Tool: ToolRead, Path: "**", Decision: Allow},
		{Tool: ToolEdit, Path: "**", Decision: Allow},
		{Tool: ToolShell, Command: "go test", Decision: Allow},
	}}
	child := parent.Restrict([]string{ToolRead, ToolSearch})

	if got := decide(t, child, ToolRead, "a.go", ""); got != Allow {
		t.Errorf("read = %q, want allow", got)
	}
	if got := decide(t, child, ToolEdit, "a.go", ""); got != Deny {
		t.Errorf("edit = %q, want deny", got)
	}
	if got := decide(t, child, ToolShell, "", "go test ./..."); got != Deny {
		t.Errorf("shell = %q, want deny", got)
	}
	// The parent is not changed by deriving a child.
	if got := decide(t, parent, ToolEdit, "a.go", ""); got != Allow {
		t.Errorf("parent mutated: edit = %q", got)
	}
}

func TestRestrictNeverGrantsAnythingTheParentRefuses(t *testing.T) {
	root := t.TempDir()
	parent := Policy{Root: root, Rules: []Rule{{Tool: ToolEdit, Path: "**", Decision: Deny}}}
	child := parent.Restrict([]string{ToolEdit})
	if got := decide(t, child, ToolEdit, "a.go", ""); got != Deny {
		t.Fatalf("child edit = %q, want deny", got)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentInspectionCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Run the tests."), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"agent", "instructions", "--root", root}, "Run the tests."},
		{[]string{"agent", "check", "--root", root, "--tool", "write", "--path", "main.go"}, `"decision":"ask"`},
		{[]string{"agent", "subagents", "--root", root}, `[]`},
	} {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("output %q does not contain %q", out.String(), tc.want)
		}
	}
}

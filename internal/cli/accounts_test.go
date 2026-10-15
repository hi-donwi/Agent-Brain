package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRejectUnsupportedAccountProvider(t *testing.T) {
	t.Setenv("LOKAL_HOME", t.TempDir())
	cmd := newAccountsAddCmd()
	cmd.SetArgs([]string{"unsupported-audit-provider", "--name", "work", "--key", "env:AUDIT_FAKE_KEY"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unsupported provider was accepted and saved")
	}
}

func TestAuditRejectAccountWithoutCredential(t *testing.T) {
	t.Setenv("LOKAL_HOME", t.TempDir())
	cmd := newAccountsAddCmd()
	cmd.SetArgs([]string{"openai", "--name", "work"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("account without a credential was saved as active")
	}
}

func TestAuditDuplicateNameDoesNotOverwriteAnotherProvider(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	for _, provider := range []string{"openai", "anthropic"} {
		cmd := newAccountsAddCmd()
		cmd.SetArgs([]string{provider, "--name", "work", "--key", "env:AUDIT_FAKE_KEY"})
		if err := cmd.Execute(); err != nil {
			return
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "accounts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "provider: openai") || !strings.Contains(string(data), "provider: anthropic") {
		t.Fatal("adding the same account name under a second provider silently overwrote the first provider")
	}
}

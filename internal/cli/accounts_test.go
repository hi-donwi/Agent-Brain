package cli

import (
	"bytes"
	"github.com/hi-donwi/Agent-Brain/internal/router"
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

func TestAccountWizardAndScriptShareValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
	var out, diagnostics bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostics)
	cmd.SetIn(strings.NewReader("openai\napi-key\nwork\nenv:WIZARD_TEST_KEY\n\n"))
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	a, err := s.Find("openai/work")
	if err != nil || a.APIKey != "env:WIZARD_TEST_KEY" {
		t.Fatalf("wizard account: %v", err)
	}
	if !strings.Contains(diagnostics.String(), "Provider") || !strings.Contains(diagnostics.String(), "Authentication") {
		t.Fatal("wizard did not prompt for provider and authentication")
	}
	list := newAccountsCmd()
	list.SetOut(&out)
	list.SetArgs([]string{"list", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "WIZARD_TEST_KEY") {
		t.Fatal("credential reference leaked in account metadata")
	}
}

func TestAccountCommandsKeepProviderNamesIndependent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	for _, provider := range []string{"openai", "anthropic"} {
		cmd := newAccountsCmd()
		cmd.SetArgs([]string{"add", provider, "--name", "work", "--key", "env:TEST_KEY"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	cmd := newAccountsCmd()
	cmd.SetArgs([]string{"use", "anthropic/work"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := router.LoadRouterConfig(filepath.Join(root, "accounts.yaml"))
	if err != nil || len(cfg.Accounts) != 2 || cfg.Defaults["anthropic"] == "" {
		t.Fatal("provider-specific accounts/default were not preserved")
	}
}

func TestWizardDoesNotPromptOnPipedInput(t *testing.T) {
	cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return false }})
	var out bytes.Buffer
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing scripted provider accepted")
	}
	if strings.Contains(out.String(), "1.") {
		t.Fatal("noninteractive command opened wizard")
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

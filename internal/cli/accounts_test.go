package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
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

func TestAccountWizardStoresPastedAPIKeysPrivately(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai", "google-ai-studio", "deepseek", "openrouter", "cursor", "local", "custom", "opencode-zen"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOKAL_HOME", root)
			const secret = "test-only-pasted-credential"
			cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetIn(strings.NewReader("  " + secret + "  \n"))
			cmd.SetArgs([]string{"add", provider, "--auth", "api-key", "--name", "work", "--no-login", "--base-url", "http://127.0.0.1:12345", "--protocols", supportedTestProtocol(provider)})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("pasted credential rejected: %v", err)
			}
			service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
			a, err := service.Find(provider + "/work")
			if err != nil || !strings.HasPrefix(a.APIKey, "file:") {
				t.Fatal("pasted credential was not stored as a private file reference")
			}
			value, err := router.ResolveCredential(a.APIKey)
			if err != nil || value != secret {
				t.Fatal("stored credential does not match pasted input")
			}
			path := strings.TrimPrefix(a.APIKey, "file:")
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatal("credential file permissions are not private")
			}
			data, err := os.ReadFile(service.Path)
			if err != nil || bytes.Contains(data, []byte(secret)) {
				t.Fatal("raw credential leaked into account configuration")
			}
			list := newAccountsCmd()
			list.SetOut(&output)
			list.SetArgs([]string{"list", "--json"})
			if err := list.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), secret) {
				t.Fatal("pasted credential leaked into command output")
			}
		})
	}
}

func supportedTestProtocol(provider string) string {
	definition, _ := router.LookupProvider(provider)
	if len(definition.Protocols) == 0 {
		return ""
	}
	return definition.Protocols[0]
}

func TestAccountWizardPastedCredentialCancellation(t *testing.T) {
	for _, input := range []string{"q\n", "quit\n", "", "\nq\n"} {
		t.Run(fmt.Sprintf("input-%d", len(input)), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOKAL_HOME", root)
			cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetIn(strings.NewReader(input))
			cmd.SetArgs([]string{"add", "anthropic", "--name", "work", "--auth", "api-key", "--no-login"})
			if err := cmd.Execute(); err == nil {
				t.Fatal("cancelled or empty credential input created an account")
			}
			if _, err := os.Stat(filepath.Join(root, "accounts.yaml")); !os.IsNotExist(err) {
				t.Fatal("cancelled wizard persisted an account")
			}
		})
	}
}

func TestAccountWizardRejectsInvalidPastedCredentialsWithoutDisclosure(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", 4096), "test\x00credential", "test\tcredential"} {
		root := t.TempDir()
		t.Setenv("LOKAL_HOME", root)
		cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetIn(strings.NewReader(input + "\n"))
		cmd.SetArgs([]string{"add", "anthropic", "--auth", "api-key", "--name", "work", "--no-login"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("invalid pasted credential was accepted")
		}
		if strings.Contains(output.String()+err.Error(), input) {
			t.Fatal("invalid credential leaked into output or errors")
		}
		if _, err := os.Stat(filepath.Join(root, "accounts.yaml")); !os.IsNotExist(err) {
			t.Fatal("invalid credential created an account")
		}
	}
}

func TestAccountWizardKeepsFileCredentialReferences(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	reference := "file:" + filepath.Join(root, "existing-credential")
	cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetIn(strings.NewReader(reference + "\n"))
	cmd.SetArgs([]string{"add", "anthropic", "--auth", "api-key", "--name", "work", "--no-login"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	a, err := (router.AccountService{Path: filepath.Join(root, "accounts.yaml")}).Find("anthropic/work")
	if err != nil || a.APIKey != reference {
		t.Fatal("existing file reference was changed")
	}
	if _, err := os.Stat(filepath.Join(root, "secrets")); !os.IsNotExist(err) {
		t.Fatal("wizard copied a credential reference into secret storage")
	}
}

func TestOpenCodeWizardRetainsBufferedProviderAnswers(t *testing.T) {
	t.Setenv("LOKAL_HOME", t.TempDir())
	cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return true }})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetIn(strings.NewReader("opencode\nopenai\napi-key\nwork\nenv:WIZARD_KEY\n\n"))
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("OpenCode provider handoff lost input: %v", err)
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

func TestNativeOnboardingKeepsTwoProfilesAndFailedLoginInactive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	deps := AccountDeps{Login: func(cmd *cobra.Command, a router.Account, noBrowser bool) (router.Account, error) {
		if a.Name == "failed" {
			return a, fmt.Errorf("login cancelled")
		}
		a.CLIReady = true
		a.Status = router.StatusActive
		return a, nil
	}}
	for _, name := range []string{"work", "personal", "failed"} {
		cmd := newAccountsCmdWithDeps(deps)
		cmd.SetArgs([]string{"add", "claude-code", "--auth", "native", "--name", name})
		err := cmd.Execute()
		if (name == "failed") != (err != nil) {
			t.Fatalf("unexpected login result for %s: %v", name, err)
		}
	}
	s := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	one, _ := s.Find("claude-code/work")
	two, _ := s.Find("claude-code/personal")
	failed, _ := s.Find("claude-code/failed")
	if profileDirectory(root, one) == profileDirectory(root, two) || !one.CLIReady || !two.CLIReady {
		t.Fatal("native account profiles were not isolated")
	}
	if failed.CLIReady || failed.GatewayReady || failed.Status == router.StatusActive {
		t.Fatal("failed login was marked active")
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

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/codexusage"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestAccountUsageReadsOnlyVerifiedNativeCodexProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	account, err := service.Add(context.Background(), router.Account{
		Provider: router.ProviderCodex,
		Name:     "work",
		Auth:     router.AuthNative,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Update(context.Background(), account.ID, func(current *router.Account) error {
		current.CLIReady = true
		current.Status = router.StatusActive
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	readerCalled := false
	command := newAccountUsageCmdWithReader(func(_ context.Context, gotRoot, codexHome string) (codexusage.Snapshot, error) {
		readerCalled = true
		if gotRoot != root {
			t.Fatalf("runtime root = %q, want %q", gotRoot, root)
		}
		wantHome := profileDirectory(root, account)
		if codexHome != wantHome {
			t.Fatalf("Codex profile = %q, want %q", codexHome, wantHome)
		}
		return codexusage.Snapshot{
			RateLimits:  []codexusage.RateLimitBucket{{LimitID: "codex"}},
			UsageStatus: "unavailable",
		}, nil
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"codex/work", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !readerCalled {
		t.Fatal("native Codex reader was not called")
	}
	var snapshot codexusage.Snapshot
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatalf("usage output is not JSON: %v (%s)", err, output.String())
	}
	if len(snapshot.RateLimits) != 1 || snapshot.RateLimits[0].LimitID != "codex" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestAccountUsageRejectsUnverifiedNativeProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	_, err := service.Add(context.Background(), router.Account{
		Provider: router.ProviderCodex,
		Name:     "work",
		Auth:     router.AuthNative,
	})
	if err != nil {
		t.Fatal(err)
	}
	readerCalled := false
	command := newAccountUsageCmdWithReader(func(context.Context, string, string) (codexusage.Snapshot, error) {
		readerCalled = true
		return codexusage.Snapshot{}, nil
	})
	command.SetArgs([]string{"codex/work"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("unverified account error = %v", err)
	}
	if readerCalled {
		t.Fatal("reader ran for an unverified profile")
	}
}

func TestAccountUsageRejectsCodexOAuthAndOtherProviders(t *testing.T) {
	for _, test := range []struct {
		name    string
		account router.Account
	}{
		{
			name: "codex oauth bridge",
			account: router.Account{
				Provider: router.ProviderCodex, Name: "work", Auth: router.AuthOAuth,
				APIKey: "env:CODEX_TEST_KEY", BridgeKey: "env:BRIDGE_TEST_KEY",
				BaseURL: "http://127.0.0.1:8317/v1", BridgeURL: "http://127.0.0.1:8317/v8/management",
			},
		},
		{
			name: "OpenAI API account",
			account: router.Account{
				Provider: router.ProviderOpenAI, Name: "work", Auth: router.AuthAPIKey, APIKey: "env:OPENAI_TEST_KEY",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOKAL_HOME", root)
			service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
			account, err := service.Add(context.Background(), test.account)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Update(context.Background(), account.ID, func(current *router.Account) error {
				current.CLIReady = true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			readerCalled := false
			command := newAccountUsageCmdWithReader(func(context.Context, string, string) (codexusage.Snapshot, error) {
				readerCalled = true
				return codexusage.Snapshot{}, nil
			})
			command.SetArgs([]string{account.Reference()})
			if err := command.Execute(); err == nil {
				t.Fatal("unsupported account type was accepted")
			}
			if readerCalled {
				t.Fatal("reader ran for an unsupported account type")
			}
		})
	}
}

func TestAccountsCommandRegistersUsage(t *testing.T) {
	for _, command := range newAccountsCmd().Commands() {
		if command.Name() == "usage" {
			return
		}
	}
	t.Fatal("accounts usage subcommand is not registered")
}

func TestCodexUsageTextNeverInventsResetTime(t *testing.T) {
	used := 75.0
	window := 300
	var output bytes.Buffer
	err := printCodexUsage(&output, "codex/work", codexusage.Snapshot{
		RateLimits: []codexusage.RateLimitBucket{{
			LimitID: "codex",
			Primary: &codexusage.RateLimit{UsedPercent: &used, WindowDurationMins: &window},
		}},
		UsageStatus: "available",
		Usage:       &codexusage.UsageData{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "reset unavailable from provider") {
		t.Fatalf("missing reset was not shown as unavailable: %s", output.String())
	}
	if strings.Contains(output.String(), "resets at") {
		t.Fatalf("missing reset was guessed: %s", output.String())
	}
	if !strings.Contains(output.String(), "Token activity (separate from quota windows)") {
		t.Fatalf("quota and token activity were not distinguished: %s", output.String())
	}
}

func TestCodexUsageEnvironmentUsesOnlySelectedProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "secret-openai-api")
	t.Setenv("CODEX_API_KEY", "secret-codex-api")
	t.Setenv("CODEX_ACCESS_TOKEN", "secret-codex-token")
	env, err := accountEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profiles", "codex", "selected")
	isolated := mergeProcessEnv(env, []string{"CODEX_HOME=" + profile})
	for _, forbidden := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"} {
		for _, entry := range isolated {
			if strings.HasPrefix(entry, forbidden+"=") {
				t.Fatalf("provider credential environment leaked into App Server: %s", forbidden)
			}
		}
	}
	foundHome := false
	for _, entry := range isolated {
		if entry == "CODEX_HOME="+profile {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatal("App Server did not receive the selected Codex profile")
	}
}

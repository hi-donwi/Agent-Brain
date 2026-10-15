package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAccountServiceNamespacesAndPreservesSecrets(t *testing.T) {
	s := AccountService{Path: filepath.Join(t.TempDir(), "accounts.yaml")}
	ctx := context.Background()
	one, err := s.Add(ctx, Account{Name: "work", Provider: ProviderOpenAI, APIKey: "fake-provider-secret"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Add(ctx, Account{Name: "work", Provider: ProviderAnthropic, APIKey: "env:TEST_ANTHROPIC_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID {
		t.Fatal("accounts share an ID")
	}
	if _, err := s.Find("work"); err == nil {
		t.Fatal("ambiguous short name was resolved")
	}
	found, err := s.Find("openai/work")
	if err != nil || found.ID != one.ID {
		t.Fatalf("canonical lookup: %v", err)
	}
	if _, err := s.Add(ctx, Account{Name: "work", Provider: ProviderOpenAI, APIKey: "other"}); err == nil {
		t.Fatal("duplicate replaced existing account")
	}
	data, _ := os.ReadFile(s.Path)
	if strings.Contains(string(data), "fake-provider-secret") {
		t.Fatal("raw secret persisted in metadata")
	}
	key, err := ResolveCredential(found.APIKey)
	if err != nil || key != "fake-provider-secret" {
		t.Fatal("secret reference does not resolve")
	}
}

func TestAccountServiceValidatesAndMigrates(t *testing.T) {
	s := AccountService{Path: filepath.Join(t.TempDir(), "accounts.yaml")}
	for _, a := range []Account{
		{Name: "../escape", Provider: ProviderOpenAI, APIKey: "fake"},
		{Name: "work", Provider: "unknown", APIKey: "fake"},
		{Name: "work", Provider: ProviderOpenAI},
		{Name: "work", Provider: ProviderOpenAI, APIKey: "fake", BaseURL: "http://remote.example"},
		{Name: "work", Provider: ProviderClaudeCode, Auth: AuthOAuth},
	} {
		if _, err := s.Add(context.Background(), a); err == nil {
			t.Fatalf("accepted invalid account: %s/%s", a.Provider, a.Name)
		}
	}
	legacy := "strategy: priority\naccounts:\n  - id: old\n    name: Legacy\n    provider: gemini\n    api_key: env:LEGACY_KEY\n    enabled: true\n"
	if err := os.WriteFile(s.Path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Find("old")
	if err != nil || got.ID != "old" || got.Provider != ProviderGemini {
		t.Fatalf("legacy lookup failed: %v", err)
	}
	if err := s.Use(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRouterConfig(s.Path)
	if err != nil || cfg.Defaults[ProviderGemini] != "old" {
		t.Fatalf("default: %v", err)
	}
}

func TestAccountServiceConcurrentAdds(t *testing.T) {
	s := AccountService{Path: filepath.Join(t.TempDir(), "accounts.yaml")}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Add(context.Background(), Account{Name: strings.Repeat("a", i+1), Provider: ProviderLocal})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := LoadRouterConfig(s.Path)
	if err != nil || len(cfg.Accounts) != 8 {
		t.Fatalf("lost concurrent update: %v; accounts %d", err, len(cfg.Accounts))
	}
}

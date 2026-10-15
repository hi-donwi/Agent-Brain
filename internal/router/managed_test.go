package router

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManagedAccountsGenerateIndependentLocalConnections(t *testing.T) {
	s := AccountService{Path: filepath.Join(t.TempDir(), "accounts.yaml")}
	first, err := s.Add(context.Background(), Account{Provider: ProviderAntigravity, Name: "work", Auth: AuthOAuth, ManagedBridge: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(context.Background(), Account{Provider: ProviderAntigravity, Name: "personal", Auth: AuthOAuth, ManagedBridge: true})
	if err != nil {
		t.Fatal(err)
	}
	one, err := ResolveCredential(first.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	two, err := ResolveCredential(second.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	management, err := ResolveCredential(first.BridgeKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) < 32 || one == two || one == management || first.BaseURL == second.BaseURL {
		t.Fatal("managed credentials/endpoints are not independent")
	}
	if err := ValidateLoopbackEndpoint(first.BaseURL); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Find(first.Reference())
	if err != nil || !loaded.ManagedBridge {
		t.Fatalf("managed metadata not persisted: %v", err)
	}
}

func TestEnableManagedRecoversIncompleteExternalAccount(t *testing.T) {
	s := AccountService{Path: filepath.Join(t.TempDir(), "accounts.yaml")}
	external, err := s.Add(context.Background(), Account{Provider: ProviderAntigravity, Name: "work", Auth: AuthOAuth, BaseURL: "http://127.0.0.1:8317", BridgeURL: "http://127.0.0.1:8317/v8/management", APIKey: "env:UNSET_INFERENCE_KEY", BridgeKey: "env:UNSET_MANAGEMENT_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := s.EnableManaged(context.Background(), external.ID)
	if err != nil || !managed.ManagedBridge || managed.ID != external.ID {
		t.Fatalf("recovery: %v", err)
	}
	if _, err = ResolveCredential(managed.BridgeKey); err != nil {
		t.Fatal(err)
	}
}

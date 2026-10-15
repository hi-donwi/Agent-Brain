package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func TestOAuthWizardNeedsOnlyProviderAuthenticationAndName(t *testing.T) {
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("antigravity\noauth\nwork\n"))
	cmd.SetErr(&output)
	var account router.Account
	if err := accountWizard(cmd, &account); err != nil {
		t.Fatalf("automatic OAuth setup asked for infrastructure: %v; prompts: %s", err, output.String())
	}
	for _, label := range []string{"inference URL", "management URL", "credential reference", "Allowed model IDs"} {
		if strings.Contains(output.String(), label) {
			t.Fatalf("automatic OAuth setup still asks for %s", label)
		}
	}
}

func TestManagedWizardResumesIncompleteExternalAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	old, err := service.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, BaseURL: "http://127.0.0.1:8317", BridgeURL: "http://127.0.0.1:8317/v8/management", APIKey: "env:UNSET_BRIDGE_KEY", BridgeKey: "env:UNSET_MANAGEMENT_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	deps := AccountDeps{Interactive: func() bool { return true }, Login: func(_ *cobra.Command, a router.Account, _ bool) (router.Account, error) {
		if !a.ManagedBridge {
			t.Fatal("old incomplete connection not converted to managed setup")
		}
		a.Status = router.StatusActive
		a.GatewayReady = true
		a.Models = []string{"model-a"}
		return a, nil
	}}
	cmd := newAccountsCmdWithDeps(deps)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetIn(strings.NewReader("antigravity\noauth\nwork\n"))
	cmd.SetArgs([]string{"add"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Find(old.ID)
	if err != nil || !saved.ManagedBridge || !saved.GatewayReady {
		t.Fatalf("account not resumed: %v", err)
	}
}

func TestModelsRefreshUsesOnlyTheSelectedCredentialAndUpdatesAutoCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/config":
			io.WriteString(w, `{"config-version":8}`)
		case "/v8/management/credentials":
			io.WriteString(w, `{"files":[{"id":"identity-work","provider":"antigravity","status":"ready"}]}`)
		case "/v8/management/credentials/models":
			if r.URL.Query().Get("name") != "identity-work" {
				t.Error("model list not scoped to chosen identity")
			}
			io.WriteString(w, `{"models":[{"id":"model-a"},{"id":"model-b"}]}`)
		default:
			t.Errorf("unexpected global model request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	a, err := service.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, BaseURL: server.URL, BridgeURL: server.URL + "/v8/management", APIKey: "fake-key", BridgeKey: "fake-management"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Update(context.Background(), a.ID, func(a *router.Account) error { a.BridgeCredential = "identity-work"; return nil }); err != nil {
		t.Fatal(err)
	}
	cmd := newAccountsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"models", a.Reference(), "--refresh", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var models []string
	if err = json.Unmarshal(out.Bytes(), &models); err != nil || strings.Join(models, ",") != "model-a,model-b" {
		t.Fatalf("unexpected models %s, %v", out.String(), err)
	}
	saved, err := service.Find(a.ID)
	if err != nil || len(saved.Models) != 2 {
		t.Fatalf("model catalog not persisted: %v", err)
	}

	if err = service.Update(context.Background(), a.ID, func(a *router.Account) error { a.Models = []string{"model-a"}; a.DiscoveredModels = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	cmd = newAccountsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"models", a.Reference(), "--refresh"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	saved, err = service.Find(a.ID)
	if err != nil || len(saved.Models) != 1 || len(saved.DiscoveredModels) != 2 {
		t.Fatal("refresh widened an explicitly restricted model list")
	}
}

func TestAddDiscoversModelsAutomaticallyWhenTheEndpointIsConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("unexpected API path")
		}
		io.WriteString(w, `{"data":[{"id":"local-model"}]}`)
	}))
	defer server.Close()
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	cmd := newAccountsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"add", "custom", "--name", "work", "--auth", "none", "--base-url", server.URL, "--protocols", "chat"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	account, err := (router.AccountService{Path: filepath.Join(root, "accounts.yaml")}).Find("custom/work")
	if err != nil || len(account.Models) != 1 || account.Models[0] != "local-model" {
		t.Fatalf("automatic model discovery missing: %v", err)
	}
}

func TestScriptedOAuthDefaultsPrepareWithoutDownloadingWhenLoginIsDeferred(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	cmd := newAccountsCmdWithDeps(AccountDeps{Interactive: func() bool { return false }})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"add", "antigravity", "--name", "work", "--no-login"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	a, err := (router.AccountService{Path: filepath.Join(root, "accounts.yaml")}).Find("antigravity/work")
	if err != nil || !a.ManagedBridge || a.GatewayReady || a.Auth != router.AuthOAuth {
		t.Fatalf("automatic script defaults: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "tools")); !os.IsNotExist(err) {
		t.Fatal("--no-login installed a bridge")
	}
}

func TestLoginWaitsForModelRegistrationAndReusesSavedIdentity(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/config":
			io.WriteString(w, `{"config-version":8}`)
		case "/v8/management/credentials":
			io.WriteString(w, `{"files":[{"id":"identity-work","provider":"antigravity","status":"ready"}]}`)
		case "/v8/management/credentials/models":
			if attempts.Add(1) < 3 {
				io.WriteString(w, `{"models":[]}`)
			} else {
				io.WriteString(w, `{"models":[{"id":"model-ready"}]}`)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	a, err := service.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, BaseURL: server.URL, BridgeURL: server.URL + "/v8/management", APIKey: "fake-key", BridgeKey: "fake-management"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Update(context.Background(), a.ID, func(a *router.Account) error { a.BridgeCredential = "identity-work"; return nil }); err != nil {
		t.Fatal(err)
	}
	cmd := newAccountsCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"login", a.Reference(), "--no-browser"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("successful login failed before models registered: %v", err)
	}
	saved, err := service.Find(a.ID)
	if err != nil || !saved.GatewayReady || len(saved.Models) != 1 || saved.Models[0] != "model-ready" {
		t.Fatalf("discovery did not finish: %v", err)
	}
	if !strings.Contains(output.String(), "Waiting") {
		t.Fatal("model registration wait was not explained")
	}
}

func TestAddResumesAccountWithBoundIdentityAndPendingModels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOKAL_HOME", root)
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	old, err := service.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, BaseURL: "http://127.0.0.1:8317", BridgeURL: "http://127.0.0.1:8317/v8/management", APIKey: "env:TEST_INFERENCE", BridgeKey: "env:TEST_MANAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Update(context.Background(), old.ID, func(a *router.Account) error {
		a.ManagedBridge = true
		a.BridgeCredential = "identity-work"
		a.Status = router.StatusConfigured
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deps := AccountDeps{Interactive: func() bool { return false }, Login: func(_ *cobra.Command, a router.Account, _ bool) (router.Account, error) {
		if a.ID != old.ID || a.BridgeCredential != "identity-work" {
			t.Fatal("pending catalog retry replaced the authenticated identity")
		}
		a.Models = []string{"model-ready"}
		a.DiscoveredModels = a.Models
		a.GatewayReady = true
		a.Status = router.StatusActive
		return a, nil
	}}
	cmd := newAccountsCmdWithDeps(deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"add", "antigravity", "--name", "work"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("could not resume signed-in account: %v", err)
	}
	saved, err := service.Find(old.ID)
	if err != nil || !saved.GatewayReady || len(saved.Models) != 1 {
		t.Fatalf("pending catalog was not completed: %v", err)
	}
}

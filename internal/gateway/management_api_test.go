package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestManagementAPIAddsRedactedAccountAndSupportsLifecycle(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	if err := router.SaveRouterConfig(configPath, router.DefaultRouterConfig()); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "master-secret", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	secret := "synthetic-provider-secret"
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}

	added := request(http.MethodPost, "/manage/v1/accounts", NewManagementToken("master-secret"), `{"provider":"openai","name":"test","auth":"api-key","credential":"`+secret+`","protocols":["chat"]}`)
	if added.Code != http.StatusCreated || strings.Contains(added.Body.String(), secret) {
		t.Fatalf("account add status=%d response=%s", added.Code, added.Body.String())
	}
	var result struct {
		Account struct {
			ID            string `json:"id"`
			CredentialSet bool   `json:"credential_set"`
		} `json:"account"`
	}
	if err := json.Unmarshal(added.Body.Bytes(), &result); err != nil || result.Account.ID == "" || !result.Account.CredentialSet {
		t.Fatalf("account metadata missing or malformed: %#v, %v", result, err)
	}
	config, err := router.LoadRouterConfig(configPath)
	if err != nil || len(config.Accounts) != 1 || strings.Contains(config.Accounts[0].APIKey, secret) {
		t.Fatalf("account configuration did not store only a credential reference: %#v, %v", config.Accounts, err)
	}
	credentialData, err := os.ReadFile(strings.TrimPrefix(config.Accounts[0].APIKey, "file:"))
	if err != nil || string(credentialData) != secret {
		t.Fatal("Brain did not persist the write-only provider credential")
	}

	listed := request(http.MethodGet, "/manage/v1/accounts", NewManagementToken("master-secret"), "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), secret) || strings.Contains(listed.Body.String(), "BridgeCredential") {
		t.Fatalf("account listing leaked or omitted metadata: status=%d body=%s", listed.Code, listed.Body.String())
	}
	updated := request(http.MethodPut, "/manage/v1/accounts/"+result.Account.ID, NewManagementToken("master-secret"), `{"enabled":false,"make_default":true}`)
	if updated.Code != http.StatusBadRequest {
		t.Fatalf("disabled account was made default: status=%d body=%s", updated.Code, updated.Body.String())
	}
	updated = request(http.MethodPut, "/manage/v1/accounts/"+result.Account.ID, NewManagementToken("master-secret"), `{"enabled":false,"priority":2}`)
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) {
		t.Fatalf("account update failed or leaked secret: status=%d body=%s", updated.Code, updated.Body.String())
	}
	removed := request(http.MethodDelete, "/manage/v1/accounts/"+result.Account.ID, NewManagementToken("master-secret"), "")
	if removed.Code != http.StatusOK {
		t.Fatalf("account removal returned %d: %s", removed.Code, removed.Body.String())
	}
}

func TestManagementAPIRejectsUnknownFieldsAndCredentialConfusion(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	if err := router.SaveRouterConfig(configPath, router.DefaultRouterConfig()); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "master-secret", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"provider":"openai","name":"test","auth":"api-key","credential":"a","credential_ref":"env:KEY"}`,
		`{"provider":"openai","name":"test","auth":"api-key","credential":"a","unknown":true}`,
		`{"provider":"openai","name":"test","auth":"native"}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/manage/v1/accounts", strings.NewReader(body))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+NewManagementToken("master-secret"))
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s returned %d, want bad request", body, w.Code)
		}
	}
}

// A management credential must not be able to make Brain read a host file or an
// environment variable: the account test would send the resolved value to the account's
// base URL, which the same caller chooses.
func TestManagementAPIRefusesCredentialReferences(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	if err := router.SaveRouterConfig(configPath, router.DefaultRouterConfig()); err != nil {
		t.Fatal(err)
	}
	hostSecret := filepath.Join(root, "serve.token")
	if err := os.WriteFile(hostSecret, []byte("master-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_TEST_HOST_SECRET", "environment-secret")
	server, err := NewServer(ServerConfig{Token: "master-secret", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	// Each case uses its own account name, so a duplicate name cannot stand in for the refusal.
	for _, tc := range []struct{ name, field, value string }{
		{"file-field", "credential_ref", "file:" + hostSecret},
		{"env-field", "credential_ref", "env:BRAIN_TEST_HOST_SECRET"},
		{"file-inline", "credential", "file:" + hostSecret},
		{"env-inline", "credential", "env:BRAIN_TEST_HOST_SECRET"},
		{"padded-inline", "credential", "  file:" + hostSecret},
	} {
		body, err := json.Marshal(map[string]any{
			"provider": "custom", "name": tc.name, "auth": "api-key",
			"base_url": "https://collector.example.invalid/v1", "protocols": []string{"chat"},
			tc.field: tc.value,
		})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/manage/v1/accounts", strings.NewReader(string(body)))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+NewManagementToken("master-secret"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "credential_reference_not_allowed") {
			t.Errorf("%s: status=%d body=%s, want 400 credential_reference_not_allowed", tc.name, w.Code, w.Body.String())
		}
	}
	config, err := router.LoadRouterConfig(configPath)
	if err != nil || len(config.Accounts) != 0 {
		t.Fatalf("a refused credential reference still created an account: %#v, %v", config.Accounts, err)
	}
}

func TestManagementAPIRestrictsBrowserOriginsAndValidatesProfiles(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	config := router.DefaultRouterConfig()
	config.Accounts = []router.Account{{ID: "a", Name: "test", Provider: router.ProviderOpenAI, APIKey: "env:OPENAI_KEY", BaseURL: "https://api.example.invalid/v1", Enabled: true, Protocols: []string{router.ProtocolChat}}}
	if err := router.SaveRouterConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "master-secret", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	credential := NewManagementToken("master-secret")
	request := func(method, path, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+credential)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	preflight := request(http.MethodOptions, "/manage/v1/accounts", "http://127.0.0.1", "")
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1" {
		t.Fatalf("allowed preflight status=%d headers=%v", preflight.Code, preflight.Header())
	}
	forbidden := request(http.MethodGet, "/manage/v1/accounts", "https://attacker.invalid", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin returned %d, want forbidden", forbidden.Code)
	}
	profile := router.RoutingProfile{ID: "control", Targets: []router.RoutingTarget{{Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"a"}, Protocols: []string{router.ProtocolChat}}}}
	encoded, _ := json.Marshal(profile)
	put := request(http.MethodPut, "/manage/v1/profiles/control", "http://127.0.0.1", string(encoded))
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), "sha256:") || !strings.Contains(put.Body.String(), "gateway_reload_required") {
		t.Fatalf("valid profile update status=%d body=%s", put.Code, put.Body.String())
	}
	profile.Targets[0].AccountIDs = []string{"unapproved-account"}
	encoded, _ = json.Marshal(profile)
	put = request(http.MethodPut, "/manage/v1/profiles/control", "http://127.0.0.1", string(encoded))
	if put.Code != http.StatusBadRequest {
		t.Fatalf("invalid profile target returned %d body=%s", put.Code, put.Body.String())
	}
	stored, err := router.LoadRouterConfig(configPath)
	if err != nil || len(stored.RoutingProfiles) != 1 || stored.RoutingProfiles[0].Targets[0].AccountIDs[0] != "a" {
		t.Fatalf("invalid update changed stored profile: %#v err=%v", stored.RoutingProfiles, err)
	}
}

func TestManagementConsoleIsBrainOriginAndDoesNotRequireDataCredentialsToLoad(t *testing.T) {
	server, err := NewServer(ServerConfig{Token: "master-secret"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/manage-ui/", nil)
	r.Host = "127.0.0.1"
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Brain management credential") || !strings.Contains(w.Body.String(), "Backends and models") || !strings.Contains(w.Body.String(), "Brain background service") || !strings.Contains(w.Body.String(), "Registered RAG sources") || !strings.Contains(w.Body.String(), "Install a Brain-user skill") || !strings.Contains(w.Body.String(), "Trace summaries and local evaluation") || !strings.Contains(w.Body.String(), "Candidate gates") {
		t.Fatalf("console did not load without an API credential: status=%d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("console security headers missing: %v", w.Header())
	}

	r = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/manage-ui/app.js", nil)
	r.Host = "127.0.0.1"
	w = httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Authorization: `Bearer ${managementToken()}`") || !strings.Contains(w.Body.String(), "model-pull-jobs") || !strings.Contains(w.Body.String(), "/runtime/backend") || !strings.Contains(w.Body.String(), "/knowledge/index-jobs") || !strings.Contains(w.Body.String(), "/knowledge/search") || !strings.Contains(w.Body.String(), "/evaluation-jobs") || !strings.Contains(w.Body.String(), "/traces") || !strings.Contains(w.Body.String(), "/forge/candidates") || !strings.Contains(w.Body.String(), "/service") {
		t.Fatalf("Brain-origin script unavailable: status=%d", w.Code)
	}
}

func TestManagementAccountTestDiscoversModelsWithoutInferenceAndRedactsCredentials(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-account-secret" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer provider.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	config := router.DefaultRouterConfig()
	config.Accounts = []router.Account{{
		ID: "test-id", Name: "test", Provider: router.ProviderOpenAI, Auth: router.AuthAPIKey,
		APIKey: "synthetic-account-secret", BaseURL: provider.URL + "/v1", Enabled: true,
		Status: router.StatusConfigured, Protocols: []string{router.ProtocolChat},
	}}
	if err := router.SaveRouterConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/manage/v1/accounts/test-id/test", nil)
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "synthetic-account-secret") || !strings.Contains(w.Body.String(), "model-a") {
		t.Fatalf("test response status=%d body=%s", w.Code, w.Body.String())
	}
	if providerCalls != 1 {
		t.Fatalf("model check made %d provider requests, want one; inference must not be sent", providerCalls)
	}
	updated, err := router.LoadRouterConfig(configPath)
	if err != nil || len(updated.Accounts[0].DiscoveredModels) != 1 || !updated.Accounts[0].GatewayReady {
		t.Fatalf("account check was not persisted: %#v err=%v", updated.Accounts, err)
	}
}

func TestManagementAccountTestRequiresNativeCLIForNativeSession(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	config := router.DefaultRouterConfig()
	config.Accounts = []router.Account{{ID: "native", Name: "work", Provider: router.ProviderCodex, Auth: router.AuthNative, Enabled: true}}
	if err := router.SaveRouterConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/manage/v1/accounts/native/test", nil)
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "native_cli_required") {
		t.Fatalf("native account test status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestManagementAPIAddsBrainManagedOAuthAccountWithoutReturningBridgeKeys(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	if err := router.SaveRouterConfig(configPath, router.DefaultRouterConfig()); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/manage/v1/accounts", strings.NewReader(`{"provider":"antigravity","name":"work","auth":"oauth","managed_bridge":true,"protocols":["chat"]}`))
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "managed.key") || strings.Contains(w.Body.String(), "managed.bridge") {
		t.Fatalf("OAuth account add status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"auth":"oauth"`) || !strings.Contains(w.Body.String(), `"status":"pending"`) {
		t.Fatalf("OAuth account is not pending: %s", w.Body.String())
	}
	config, err := router.LoadRouterConfig(configPath)
	if err != nil || len(config.Accounts) != 1 || !config.Accounts[0].ManagedBridge || config.Accounts[0].BridgeCredential != "" {
		t.Fatalf("managed OAuth account was not prepared safely: %#v err=%v", config.Accounts, err)
	}
}

func TestManagementOAuthLoginUsesValidatedSessionAndPersistsRedactedIdentity(t *testing.T) {
	const bridgeKey = "synthetic-bridge-management-key"
	var allowCallback atomic.Bool
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bridgeKey {
			t.Errorf("OAuth bridge credential missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v8/management/config":
			_, _ = w.Write([]byte(`{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`))
		case "GET /v8/management/credentials":
			if allowCallback.Load() {
				_, _ = w.Write([]byte(`{"files":[{"id":"synthetic-identity","provider":"antigravity","status":"ready"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"files":[]}`))
			}
		case "GET /v8/management/oauth/auth-url":
			_, _ = w.Write([]byte(`{"status":"ok","url":"https://accounts.google.com/o/oauth2/v2/auth?state=synthetic-state","state":"synthetic-state"}`))
		case "GET /v8/management/oauth/status":
			if allowCallback.Load() {
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"wait"}`))
			}
		case "GET /v8/management/credentials/models":
			if r.URL.Query().Get("name") != "synthetic-identity" {
				t.Errorf("model discovery used unexpected identity %q", r.URL.Query().Get("name"))
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"synthetic-model"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer bridge.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	config := router.DefaultRouterConfig()
	config.Accounts = []router.Account{{
		ID: "oauth-id", Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth,
		APIKey: "synthetic-inference-key", BridgeKey: bridgeKey, BridgeURL: bridge.URL + "/v8/management",
		BaseURL: bridge.URL, Enabled: true, Status: router.StatusPending,
		Protocols: []string{router.ProtocolChat},
	}}
	if err := router.SaveRouterConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, nil)
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	started := request(http.MethodPost, "/manage/v1/accounts/oauth-id/login")
	if started.Code != http.StatusAccepted {
		t.Fatalf("login start status=%d body=%s", started.Code, started.Body.String())
	}
	var startBody struct {
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &startBody); err != nil || startBody.SessionID == "" || startBody.Status != "starting" {
		t.Fatalf("invalid login start response: %#v err=%v", startBody, err)
	}
	statusPath := "/manage/v1/accounts/oauth-id/login/" + startBody.SessionID
	deadline := time.Now().Add(time.Second)
	for {
		response := request(http.MethodGet, statusPath)
		var current map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if current["status"] == "pending" {
			authorizationURL, ok := current["authorization_url"].(string)
			if !ok || !strings.HasPrefix(authorizationURL, "https://accounts.google.com/") || current["state"] != nil {
				t.Fatalf("unsafe or overexposed OAuth pending response: %s", response.Body.String())
			}
			break
		}
		if current["status"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("OAuth session did not reach pending: %s", response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	allowCallback.Store(true)
	deadline = time.Now().Add(2 * time.Second)
	for {
		response := request(http.MethodGet, statusPath)
		if response.Code != http.StatusOK {
			t.Fatalf("login status returned %d: %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), `"status":"complete"`) {
			if strings.Contains(response.Body.String(), bridgeKey) || strings.Contains(response.Body.String(), "synthetic-inference-key") || strings.Contains(response.Body.String(), "synthetic-identity") {
				t.Fatalf("OAuth status exposed bridge/provider identity data: %s", response.Body.String())
			}
			break
		}
		if strings.Contains(response.Body.String(), `"status":"failed"`) || time.Now().After(deadline) {
			t.Fatalf("OAuth session did not complete: %s", response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	stored, err := router.LoadRouterConfig(configPath)
	if err != nil || stored.Accounts[0].BridgeCredential != "synthetic-identity" || len(stored.Accounts[0].DiscoveredModels) != 1 || stored.Accounts[0].DiscoveredModels[0] != "synthetic-model" {
		t.Fatalf("OAuth account identity/model catalog not persisted: %#v err=%v", stored.Accounts, err)
	}
}

func TestManagementOAuthCancelRevokesPendingBridgeState(t *testing.T) {
	const bridgeKey = "synthetic-cancel-bridge-key"
	deleted := make(chan struct{}, 1)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bridgeKey {
			t.Errorf("OAuth bridge credential missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v8/management/config":
			_, _ = w.Write([]byte(`{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`))
		case "GET /v8/management/credentials":
			_, _ = w.Write([]byte(`{"files":[]}`))
		case "GET /v8/management/oauth/auth-url":
			_, _ = w.Write([]byte(`{"status":"ok","url":"https://accounts.google.com/o/oauth2/v2/auth?state=cancel-state","state":"cancel-state"}`))
		case "GET /v8/management/oauth/status":
			_, _ = w.Write([]byte(`{"status":"wait"}`))
		case "DELETE /v8/management/oauth/session":
			select {
			case deleted <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer bridge.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "accounts.yaml")
	config := router.DefaultRouterConfig()
	config.Accounts = []router.Account{{
		ID: "oauth-cancel", Name: "cancel", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth,
		APIKey: "synthetic-inference-key", BridgeKey: bridgeKey, BridgeURL: bridge.URL + "/v8/management",
		BaseURL: bridge.URL, Enabled: true, Status: router.StatusPending,
		Protocols: []string{router.ProtocolChat},
	}}
	if err := router.SaveRouterConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", AccountConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, nil)
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	started := request(http.MethodPost, "/manage/v1/accounts/oauth-cancel/login")
	var startBody struct {
		SessionID string `json:"session_id"`
	}
	if started.Code != http.StatusAccepted || json.Unmarshal(started.Body.Bytes(), &startBody) != nil || startBody.SessionID == "" {
		t.Fatalf("login start status=%d body=%s", started.Code, started.Body.String())
	}
	statusPath := "/manage/v1/accounts/oauth-cancel/login/" + startBody.SessionID
	deadline := time.Now().Add(time.Second)
	for {
		response := request(http.MethodGet, statusPath)
		if strings.Contains(response.Body.String(), `"status":"pending"`) {
			break
		}
		if strings.Contains(response.Body.String(), `"status":"failed"`) || time.Now().After(deadline) {
			t.Fatalf("OAuth session did not reach pending: %s", response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelled := request(http.MethodDelete, statusPath)
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("OAuth cancel returned %d: %s", cancelled.Code, cancelled.Body.String())
	}
	select {
	case <-deleted:
	case <-time.After(time.Second):
		t.Fatal("cancelling login did not revoke the bridge OAuth state")
	}
	deadline = time.Now().Add(time.Second)
	for {
		response := request(http.MethodGet, statusPath)
		if strings.Contains(response.Body.String(), `"status":"cancelled"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("OAuth session did not finish cancellation: %s", response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagementOAuthCancelDoesNotCancelCompletingLogin(t *testing.T) {
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	var cancelCalled atomic.Bool
	session := &managementLoginSession{
		id: "completing-session", accountID: "oauth-account", status: "completing",
		cancel: func() { cancelCalled.Store(true) },
	}
	server.managementLoginMu.Lock()
	server.managementLoginSessions[session.id] = session
	server.managementLoginMu.Unlock()

	response := managementRequest(server, http.MethodDelete, "/manage/v1/accounts/oauth-account/login/completing-session", "")
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"completing"`) {
		t.Fatalf("cancelling an already-completing login changed its state: %d %s", response.Code, response.Body.String())
	}
	if cancelCalled.Load() {
		t.Fatal("cancelling an already-completing login cancelled its persistence context")
	}
}

func TestManagementLoginCompletionHonorsPriorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session := &managementLoginSession{status: "canceling"}
	server := &Server{}
	if server.beginManagementLoginCompletion(ctx, session) {
		t.Fatal("a cancelled OAuth session entered completion")
	}
	if session.status != "canceling" {
		t.Fatalf("cancelled OAuth session status = %q, want canceling", session.status)
	}

	active := &managementLoginSession{status: "pending"}
	if !server.beginManagementLoginCompletion(context.Background(), active) || active.status != "completing" {
		t.Fatalf("active OAuth session did not atomically enter completion: %q", active.status)
	}
}

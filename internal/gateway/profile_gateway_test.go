package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

type profileTestAdapter struct {
	request *backend.ChatRequest
}

func (*profileTestAdapter) Name() string                                            { return "ollama" }
func (*profileTestAdapter) Endpoint() string                                        { return "http://127.0.0.1:11434" }
func (*profileTestAdapter) Ping(context.Context) error                              { return nil }
func (*profileTestAdapter) Version(context.Context) (string, error)                 { return "test", nil }
func (*profileTestAdapter) ListModels(context.Context) ([]backend.ModelInfo, error) { return nil, nil }
func (*profileTestAdapter) ShowModel(context.Context, string) (backend.ModelDetails, error) {
	return backend.ModelDetails{}, nil
}
func (*profileTestAdapter) RunningModels(context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (a *profileTestAdapter) Chat(_ context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	a.request = &req
	return &backend.ChatResponse{Message: backend.ChatMessage{Role: "assistant", Content: "ok"}}, nil
}
func (a *profileTestAdapter) ChatStream(ctx context.Context, req backend.ChatRequest, _ func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return a.Chat(ctx, req)
}
func (*profileTestAdapter) Embed(context.Context, backend.EmbedRequest) (*backend.EmbedResponse, error) {
	return nil, nil
}
func (*profileTestAdapter) Pull(context.Context, string, func(backend.Progress)) error { return nil }
func (*profileTestAdapter) Delete(context.Context, string) error                       { return nil }
func (*profileTestAdapter) Unload(context.Context, string) error                       { return nil }

func TestProfileCredentialListsOnlyProfileModelsAndRoutesOnlyAllowedAccounts(t *testing.T) {
	const master = "master-secret"
	var allowedCalls, unapprovedCalls int
	allowedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedCalls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer allowed-key" {
			t.Errorf("upstream received unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		for _, name := range []string{"X-Brain-Account", "X-Brain-Provider", "X-Brain-Profile", "X-Brain-Revision"} {
			if r.Header.Get(name) != "" {
				t.Errorf("gateway routing header %s leaked upstream", name)
			}
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if body["model"] != "upstream-model" {
			t.Errorf("upstream received unexpected model %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-test","choices":[]}`)
	}))
	defer allowedUpstream.Close()
	unapprovedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		unapprovedCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unapprovedUpstream.Close()

	accounts := []router.Account{
		{ID: "allowed", Provider: router.ProviderOpenAI, Enabled: true, Status: router.StatusActive, BaseURL: allowedUpstream.URL, APIKey: "allowed-key", Models: []string{"upstream-model"}, Protocols: []string{router.ProtocolChat}},
		{ID: "unapproved", Provider: router.ProviderOpenAI, Enabled: true, Status: router.StatusActive, BaseURL: unapprovedUpstream.URL, APIKey: "other-key", Models: []string{"upstream-model"}, Protocols: []string{router.ProtocolChat}},
	}
	profile := router.RoutingProfile{ID: "agent-control", Targets: []router.RoutingTarget{{
		Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "upstream-model",
		AccountIDs: []string{"allowed"}, Protocols: []string{router.ProtocolChat},
	}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	pool := router.NewAccountPool(router.RouterConfig{Strategy: "priority", Accounts: accounts})
	server, err := NewServer(ServerConfig{Token: master, AccountPool: pool, RoutingProfiles: []router.RoutingProfile{profile}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	newRequest := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Brain-Profile", profile.ID)
		r.Header.Set("X-Brain-Revision", revision)
		return r
	}

	modelsResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(modelsResponse, newRequest(http.MethodGet, "/v1/models", ""))
	if modelsResponse.Code != http.StatusOK {
		t.Fatalf("profile model discovery returned %d", modelsResponse.Code)
	}
	var modelList openAIModelList
	if err := json.Unmarshal(modelsResponse.Body.Bytes(), &modelList); err != nil {
		t.Fatal(err)
	}
	if len(modelList.Data) != 1 || modelList.Data[0].ID != "assistant" {
		t.Fatalf("profile model discovery returned %#v", modelList.Data)
	}

	chatRequest := newRequest(http.MethodPost, "/v1/chat/completions", `{"model":"assistant","messages":[{"role":"user","content":"hi"}]}`)
	chatRequest.Header.Set("X-Brain-Account", "unapproved")
	chatRequest.Header.Set("X-Brain-Provider", "anthropic")
	chatResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatResponse, chatRequest)
	if chatResponse.Code != http.StatusOK {
		t.Fatalf("profile inference returned %d: %s", chatResponse.Code, chatResponse.Body.String())
	}
	if allowedCalls != 1 || unapprovedCalls != 0 {
		t.Fatalf("profile called allowed=%d unapproved=%d upstreams", allowedCalls, unapprovedCalls)
	}

	wrongModel := newRequest(http.MethodPost, "/v1/chat/completions", `{"model":"other-model","messages":[{"role":"user","content":"hi"}]}`)
	wrongResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(wrongResponse, wrongModel)
	if wrongResponse.Code != http.StatusForbidden {
		t.Fatalf("out-of-profile model returned %d", wrongResponse.Code)
	}
	if allowedCalls != 1 || unapprovedCalls != 0 {
		t.Fatalf("out-of-profile model reached an upstream: allowed=%d unapproved=%d", allowedCalls, unapprovedCalls)
	}
}

func TestGeminiProfileCredentialFiltersMetadataAndRejectsOtherModels(t *testing.T) {
	const master = "master-secret"
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Errorf("profile routed to unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[]}`)
	}))
	defer upstream.Close()
	accounts := []router.Account{{
		ID: "gemini-account", Provider: router.ProviderGemini, Enabled: true, Status: router.StatusActive,
		BaseURL: upstream.URL, APIKey: "gemini-key", Models: []string{"gemini-test"}, Protocols: []string{router.ProtocolGemini},
	}}
	profile := router.RoutingProfile{ID: "gemini-profile", Targets: []router.RoutingTarget{{
		Model: "public-gemini", Provider: router.ProviderGemini, UpstreamModel: "gemini-test",
		AccountIDs: []string{"gemini-account"}, Protocols: []string{router.ProtocolGemini},
	}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolGemini}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: master, AccountPool: router.NewAccountPool(router.RouterConfig{Strategy: "priority", Accounts: accounts}), RoutingProfiles: []router.RoutingProfile{profile}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	newRequest := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Brain-Profile", profile.ID)
		r.Header.Set("X-Brain-Revision", revision)
		return r
	}
	metadata := httptest.NewRecorder()
	server.Handler().ServeHTTP(metadata, newRequest(http.MethodGet, "/v1beta/models", ""))
	var modelList struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(metadata.Body.Bytes(), &modelList); err != nil {
		t.Fatal(err)
	}
	if metadata.Code != http.StatusOK || len(modelList.Models) != 1 || modelList.Models[0].Name != "models/public-gemini" {
		t.Fatalf("profile metadata returned status=%d models=%#v body=%s", metadata.Code, modelList.Models, metadata.Body.String())
	}
	wrongModel := httptest.NewRecorder()
	server.Handler().ServeHTTP(wrongModel, newRequest(http.MethodPost, "/v1beta/models/other:generateContent", `{}`))
	if wrongModel.Code != http.StatusForbidden || upstreamCalls != 0 {
		t.Fatalf("out-of-profile Gemini model returned status=%d and upstream calls=%d", wrongModel.Code, upstreamCalls)
	}
	withKey := httptest.NewRecorder()
	server.Handler().ServeHTTP(withKey, newRequest(http.MethodPost, "/v1beta/models/public-gemini:generateContent?key=untrusted", `{}`))
	if withKey.Code != http.StatusForbidden || upstreamCalls != 0 {
		t.Fatalf("profile Gemini request accepted an upstream key: status=%d calls=%d", withKey.Code, upstreamCalls)
	}
	allowed := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowed, newRequest(http.MethodPost, "/v1beta/models/public-gemini:generateContent", `{}`))
	if allowed.Code != http.StatusOK || upstreamCalls != 1 {
		t.Fatalf("profile Gemini request returned status=%d and upstream calls=%d", allowed.Code, upstreamCalls)
	}
}

func TestLocalProfileCredentialUsesExactModelAndRejectsAliasRedirection(t *testing.T) {
	const master = "master-secret"
	const localEndpoint = "http://127.0.0.1:11434"
	profile := router.RoutingProfile{ID: "local-profile", Targets: []router.RoutingTarget{{
		Model: "openai/assistant", Provider: router.ProviderLocal, UpstreamModel: "local-model-test", Protocols: []string{router.ProtocolChat},
	}}}
	revision, err := router.RoutingProfileRevisionFor(profile, nil, "ollama", localEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	newServer := func(config *home.Config) (*Server, *profileTestAdapter) {
		adapter := &profileTestAdapter{}
		server, err := NewServer(ServerConfig{Token: master, Adapter: adapter, UserConfig: config, RoutingProfiles: []router.RoutingProfile{profile}, LocalBackend: "ollama", LocalEndpoint: localEndpoint})
		if err != nil {
			t.Fatal(err)
		}
		return server, adapter
	}
	request := func(server *Server) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"openai/assistant","messages":[{"role":"user","content":"hi"}]}`))
		r.Host = "127.0.0.1"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Brain-Profile", profile.ID)
		r.Header.Set("X-Brain-Revision", revision)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	server, adapter := newServer(nil)
	defer server.Shutdown(context.Background())
	if response := request(server); response.Code != http.StatusOK || adapter.request == nil || adapter.request.Model != "local-model-test" {
		t.Fatalf("local profile did not use its exact model: status=%d request=%#v body=%s", response.Code, adapter.request, response.Body.String())
	}

	redirected, redirectedAdapter := newServer(&home.Config{Aliases: map[string]string{"local-model-test": "other-model"}})
	defer redirected.Shutdown(context.Background())
	if response := request(redirected); response.Code != http.StatusForbidden || redirectedAdapter.request != nil {
		t.Fatalf("local alias redirection escaped the profile: status=%d request=%#v", response.Code, redirectedAdapter.request)
	}
}

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestInferenceTokenIsBoundToProfileRevisionAndProtocol(t *testing.T) {
	master := "master-secret"
	scope := InferenceScope{ProfileID: "agent-control", Revision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Protocols: []string{router.ProtocolChat}}
	token, err := NewInferenceToken(master, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := VerifyInferenceToken(master, token); !ok {
		t.Fatal("valid scoped credential was rejected")
	}
	if _, ok := VerifyInferenceToken("different-master", token); ok {
		t.Fatal("credential verified with a different master token")
	}

	wrongRevision := scope
	wrongRevision.Revision = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	wrongToken, err := NewInferenceToken(master, wrongRevision)
	if err != nil {
		t.Fatal(err)
	}
	if token == wrongToken {
		t.Fatal("different profile revisions produced the same credential")
	}
}

func TestProfileCredentialAllowsOnlyItsModelProtocolAndGatewayRoutes(t *testing.T) {
	master := "master-secret"
	profile := router.RoutingProfile{
		ID:      "agent-control",
		Targets: []router.RoutingTarget{{Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{router.ProtocolChat}}},
	}
	accounts := []router.Account{{ID: "account-a", Provider: router.ProviderOpenAI, Enabled: true, BaseURL: "https://api.example.invalid/v1", Protocols: []string{router.ProtocolChat}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(master, InferenceAuthConfig{Profiles: []router.RoutingProfile{profile}, Accounts: accounts})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := InferenceScopeFromContext(r.Context()); !ok {
			t.Error("profile scope was not attached to the request")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(method, path, profileID, profileRevision string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Brain-Profile", profileID)
		r.Header.Set("X-Brain-Revision", profileRevision)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := request(http.MethodGet, "/v1/models", profile.ID, revision); status != http.StatusNoContent {
		t.Fatalf("valid profile model discovery returned %d", status)
	}
	for _, test := range []struct{ method, path, profileID, revision string }{
		{http.MethodGet, "/v1/models", "other", revision},
		{http.MethodGet, "/v1/models", profile.ID, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{http.MethodGet, "/v1/accounts", profile.ID, revision},
		{http.MethodPost, "/v1/responses", profile.ID, revision},
	} {
		if status := request(test.method, test.path, test.profileID, test.revision); status != http.StatusForbidden {
			t.Errorf("%s %s with out-of-scope claim returned %d", test.method, test.path, status)
		}
	}
}

func TestProfileCredentialFailsWhenResolvedProfileRevisionChanges(t *testing.T) {
	master := "master-secret"
	profile := router.RoutingProfile{ID: "agent-control", Targets: []router.RoutingTarget{{Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{router.ProtocolChat}}}}
	accounts := []router.Account{{ID: "account-a", Provider: router.ProviderOpenAI, Enabled: true, BaseURL: "https://api.example.invalid/v1", Protocols: []string{router.ProtocolChat}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	pool := router.NewAccountPool(router.RouterConfig{Strategy: "priority", Accounts: accounts})
	pool.AddAccount(router.Account{ID: "account-a", Provider: router.ProviderOpenAI, Enabled: true, BaseURL: "https://changed.example.invalid/v1", Protocols: []string{router.ProtocolChat}})
	handler := AuthMiddleware(master, InferenceAuthConfig{Profiles: []router.RoutingProfile{profile}, Pool: pool})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("stale profile credential reached the handler")
	}))
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Brain-Profile", profile.ID)
	r.Header.Set("X-Brain-Revision", revision)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("stale profile revision returned %d", w.Code)
	}
}

func TestProfileCredentialRequiresAuthorizationBearer(t *testing.T) {
	master := "master-secret"
	profile := router.RoutingProfile{ID: "agent-control", Targets: []router.RoutingTarget{{
		Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test",
		AccountIDs: []string{"account-a"}, Protocols: []string{router.ProtocolChat},
	}}}
	accounts := []router.Account{{ID: "account-a", Provider: router.ProviderOpenAI, Enabled: true, BaseURL: "https://api.example.invalid/v1", Protocols: []string{router.ProtocolChat}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(master, InferenceAuthConfig{Profiles: []router.RoutingProfile{profile}, Accounts: accounts})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		name   string
		addKey func(*http.Request)
	}{
		{name: "x-api-key", addKey: func(r *http.Request) { r.Header.Set("x-api-key", token) }},
		{name: "x-goog-api-key", addKey: func(r *http.Request) { r.Header.Set("x-goog-api-key", token) }},
		{name: "query key", addKey: func(r *http.Request) { r.URL.RawQuery = "key=" + token }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/v1/models"
			if test.name == "query key" {
				path = "/v1beta/models"
			}
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("X-Brain-Profile", profile.ID)
			r.Header.Set("X-Brain-Revision", revision)
			test.addKey(r)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("non-Bearer profile token returned %d", w.Code)
			}
		})
	}
}

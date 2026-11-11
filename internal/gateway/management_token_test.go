package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestManagementTokenIsDomainSeparatedAndBoundToMaster(t *testing.T) {
	master := "gateway-master-for-test"
	token := NewManagementToken(master)
	if token == "" || token == master {
		t.Fatal("management token must be distinct from the master token")
	}
	if !VerifyManagementToken(master, token) {
		t.Fatal("derived management token did not verify")
	}
	if VerifyManagementToken("different-master", token) {
		t.Fatal("management token verified against another master")
	}
	if VerifyManagementToken(master, "bma1.invalid") {
		t.Fatal("invalid management token verified")
	}
	if NewManagementToken("") != "" {
		t.Fatal("empty master must not produce a management credential")
	}
}

func TestManagementRoutesAcceptOnlyMasterOrManagementCredentials(t *testing.T) {
	const master = "gateway-master-for-test"
	profile := router.RoutingProfile{ID: "operator", Targets: []router.RoutingTarget{{Model: "chat", Provider: router.ProviderOpenAI, UpstreamModel: "model", AccountIDs: []string{"a"}, Protocols: []string{router.ProtocolChat}}}}
	accounts := []router.Account{{ID: "a", Provider: router.ProviderOpenAI, Enabled: true, BaseURL: "https://example.invalid/v1", Protocols: []string{router.ProtocolChat}}}
	revision, err := router.RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	profileToken, err := NewInferenceToken(master, InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: []string{router.ProtocolChat}})
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(master, InferenceAuthConfig{Profiles: []router.RoutingProfile{profile}, Accounts: accounts})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(path, token string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	for _, test := range []struct {
		name, path, token string
		want              int
	}{
		{"master cannot administer", "/manage/v1/providers", master, http.StatusForbidden},
		{"management token can administer", "/manage/v1/providers", NewManagementToken(master), http.StatusNoContent},
		{"management token cannot infer", "/v1/models", NewManagementToken(master), http.StatusForbidden},
		{"profile token cannot administer", "/manage/v1/providers", profileToken, http.StatusForbidden},
		{"account token cannot administer", "/manage/v1/providers", ScopedToken(master, "a"), http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := request(test.path, test.token); got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

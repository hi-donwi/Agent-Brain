package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementAPIErrorResponsesAreNotStored(t *testing.T) {
	server, err := NewServer(ServerConfig{Token: "master-secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())

	for _, tc := range []struct {
		name   string
		token  string
		origin string
	}{
		{name: "rejected origin", token: NewManagementToken("master-secret"), origin: "https://attacker.example"},
		{name: "wrong credential scope", token: "master-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/manage/v1/accounts", nil)
			r.Host = "127.0.0.1"
			r.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("management error status = %d, want %d: %s", w.Code, http.StatusForbidden, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("management error Cache-Control = %q, want no-store", got)
			}
		})
	}
}

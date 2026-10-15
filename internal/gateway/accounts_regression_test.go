package gateway_test

import (
	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditAccountsEndpointDoesNotReturnSecrets(t *testing.T) {
	pool := router.NewAccountPool(router.RouterConfig{Accounts: []router.Account{{
		ID: "work", Provider: router.ProviderOpenAI, Enabled: true,
		APIKey: "audit-fake-provider-secret", Headers: map[string]string{"X-Audit-Secret": "audit-fake-header-secret"},
	}}})
	s, err := gateway.NewServer(gateway.ServerConfig{Addr: "127.0.0.1:0", Token: "audit-gateway-token", AccountPool: pool})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/accounts", nil)
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer audit-gateway-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "audit-fake-provider-secret") || strings.Contains(rec.Body.String(), "audit-fake-header-secret") {
		t.Fatal("authenticated account metadata endpoint returned raw credentials")
	}
}

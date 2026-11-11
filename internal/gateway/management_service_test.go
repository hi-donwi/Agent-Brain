package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/service"
)

func TestManagementServiceStatusRedactsUnitPathAndRawMessage(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Token: "gateway-master",
		ServiceStatus: func() (*service.StatusResult, error) {
			return &service.StatusResult{
				Installed: true, Running: true, ServiceType: "launchd",
				Path:    "/private/brain/home/Library/LaunchAgents/secret.plist",
				Version: "0.0.0-test", Profile: "local", Message: "raw error with /private/brain/home",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/manage/v1/service", nil)
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"installed":true`) || !strings.Contains(w.Body.String(), `"running":true`) || !strings.Contains(w.Body.String(), `"service_type":"launchd"`) || !strings.Contains(w.Body.String(), `"version":"0.0.0-test"`) {
		t.Fatalf("safe service state missing: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"/private/brain/home", "secret.plist", "raw error"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("service response leaked %q: %s", secret, w.Body.String())
		}
	}
}

func TestManagementServiceStatusUsesStableErrorWhenPlatformUnsupported(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Token: "gateway-master",
		ServiceStatus: func() (*service.StatusResult, error) {
			return nil, errors.New("unsupported platform /private/path")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/manage/v1/service", nil)
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "service_status_unavailable") || strings.Contains(w.Body.String(), "unsupported platform") {
		t.Fatalf("service status error was not stable/redacted: %d %s", w.Code, w.Body.String())
	}
}

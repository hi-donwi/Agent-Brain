package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverModelsUsesProviderModelCatalogWithoutInference(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Errorf("unexpected discovery request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-b"}]}`)
	}))
	defer server.Close()
	models, err := DiscoverModels(context.Background(), Account{
		ID: "test", Provider: ProviderOpenAI, Auth: AuthAPIKey, APIKey: "synthetic-key",
		BaseURL: server.URL + "/v1", Protocols: []string{ProtocolChat},
	})
	if err != nil || !reflect.DeepEqual(models, []string{"model-a", "model-b"}) || calls != 1 {
		t.Fatalf("models=%v calls=%d err=%v", models, calls, err)
	}
}

func TestDiscoverModelsUsesGeminiCatalogAndAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" || r.Header.Get("x-goog-api-key") != "synthetic-key" {
			t.Errorf("unexpected Gemini discovery request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		_, _ = fmt.Fprint(w, `{"models":[{"name":"models/gemini-a"}]}`)
	}))
	defer server.Close()
	models, err := DiscoverModels(context.Background(), Account{
		ID: "test", Provider: ProviderGemini, Auth: AuthAPIKey, APIKey: "synthetic-key",
		BaseURL: server.URL, Protocols: []string{ProtocolGemini},
	})
	if err != nil || !reflect.DeepEqual(models, []string{"gemini-a"}) {
		t.Fatalf("models=%v err=%v", models, err)
	}
}

func TestDiscoverModelsRejectsProviderErrorsAndUnsafeIDs(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"unauthorized", `{"error":"synthetic-secret-must-not-leak"}`, http.StatusUnauthorized},
		{"unsafe id", `{"data":[{"id":"model with spaces"}]}`, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			_, err := DiscoverModels(context.Background(), Account{
				ID: "test", Provider: ProviderOpenAI, Auth: AuthAPIKey, APIKey: "synthetic-key",
				BaseURL: server.URL + "/v1", Protocols: []string{ProtocolChat},
			})
			if err == nil || test.name == "unauthorized" && strings.Contains(err.Error(), "synthetic-secret-must-not-leak") {
				t.Fatalf("expected a redacted discovery error, got %v", err)
			}
		})
	}
}

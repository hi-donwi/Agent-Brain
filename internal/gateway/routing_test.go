package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestResponsesPinsAccountAndPreservesPayload(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fake-second-key" {
			t.Error("wrong account credential")
		}
		if r.Header.Get("X-Brain-Account") != "" {
			t.Error("local selector leaked")
		}
		data, _ := io.ReadAll(r.Body)
		got = string(data)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer upstream.Close()
	pool := router.NewAccountPool(router.RouterConfig{Accounts: []router.Account{
		{ID: "first", Provider: router.ProviderOpenAI, Enabled: true, APIKey: "fake-first-key", BaseURL: upstream.URL},
		{ID: "second", Provider: router.ProviderOpenAI, Enabled: true, APIKey: "fake-second-key", BaseURL: upstream.URL},
	}})
	s, err := gateway.NewServer(gateway.ServerConfig{Token: "master", AccountPool: pool})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"openai/gpt-test","input":[{"type":"function_call_output","call_id":"call1","output":"result"}],"tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+gateway.ScopedToken("master", "second"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(got, `"model":"gpt-test"`) || !strings.Contains(got, `"function_call_output"`) || !strings.Contains(got, `"read_file"`) {
		t.Fatalf("payload changed: %s", got)
	}
}

func TestScopedTokenCannotSwitchAccountOrProvider(t *testing.T) {
	pool := router.NewAccountPool(router.RouterConfig{Accounts: []router.Account{{ID: "first", Provider: router.ProviderOpenAI, Enabled: true}}})
	s, _ := gateway.NewServer(gateway.ServerConfig{Token: "master", AccountPool: pool})
	for _, token := range []string{gateway.ScopedToken("master", "first"), gateway.ScopedToken("wrong", "first")} {
		req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hi"}`))
		req.Host = "127.0.0.1"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Brain-Account", "second")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 401 && rec.Code != 403 {
			t.Fatalf("selector override accepted: %d", rec.Code)
		}
	}
}

func TestCloudChatAcceptsStructuredMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"choices":[]}`) }))
	defer upstream.Close()
	s, _ := gateway.NewServer(gateway.ServerConfig{Token: "master", AccountPool: router.NewAccountPool(router.RouterConfig{Accounts: []router.Account{{ID: "a", Provider: router.ProviderOpenAI, Enabled: true, APIKey: "fake", BaseURL: upstream.URL}}})})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer master")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("structured cloud request rejected: %d %s", rec.Code, rec.Body.String())
	}
}

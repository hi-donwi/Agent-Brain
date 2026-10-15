package router

import (
	"context"
	"net/http"
	"testing"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuditAntigravityProviderDoesNotFallBackToLocal(t *testing.T) {
	pool := NewAccountPool(RouterConfig{Accounts: []Account{{ID: "work", Provider: "antigravity", Enabled: true}}})
	h := NewProxyHandler(pool, false)
	var target string
	h.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		target = r.URL.Host
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	resp, _, err := h.ForwardRequest(context.Background(), "antigravity", "/v1/chat/completions", []byte(`{}`), make(http.Header))
	if resp != nil {
		defer resp.Body.Close()
	}
	if target == "127.0.0.1:11434" {
		t.Fatal("Antigravity account was dispatched to the default local Ollama endpoint")
	}
	if err == nil && target == "" {
		t.Fatal("expected a provider adapter or an explicit unsupported-provider error")
	}
}

func TestAuditGeminiOpenAICompatibilityPath(t *testing.T) {
	pool := NewAccountPool(RouterConfig{Accounts: []Account{{ID: "studio", Provider: ProviderGemini, Enabled: true, APIKey: "audit-fake-key"}}})
	h := NewProxyHandler(pool, false)
	var path string
	h.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	resp, _, err := h.ForwardRequest(context.Background(), ProviderGemini, "/v1/chat/completions", []byte(`{}`), make(http.Header))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if path != "/v1beta/openai/chat/completions" {
		t.Fatalf("Gemini API OpenAI compatibility path = %s; want /v1beta/openai/chat/completions", path)
	}
}

func TestAuditProxyDoesNotForwardGatewayCredential(t *testing.T) {
	pool := NewAccountPool(RouterConfig{Accounts: []Account{{ID: "work", Provider: ProviderAnthropic, APIKey: "audit-fake-provider-key", Enabled: true}}})
	h := NewProxyHandler(pool, false)
	var authorization string
	h.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		authorization = r.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer audit-fake-gateway-token")
	resp, _, err := h.ForwardRequest(context.Background(), ProviderAnthropic, "/v1/messages", []byte(`{}`), headers)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if authorization != "" {
		t.Fatal("gateway authorization was forwarded alongside the provider API key")
	}
}

func TestAuditFailoverCanReachFourthAccount(t *testing.T) {
	accounts := []Account{
		{ID: "one", Provider: ProviderOpenAI, Enabled: true, BaseURL: "https://one.example.invalid"},
		{ID: "two", Provider: ProviderOpenAI, Enabled: true, BaseURL: "https://two.example.invalid"},
		{ID: "three", Provider: ProviderOpenAI, Enabled: true, BaseURL: "https://three.example.invalid"},
		{ID: "four", Provider: ProviderOpenAI, Enabled: true, BaseURL: "https://four.example.invalid"},
	}
	h := NewProxyHandler(NewAccountPool(RouterConfig{Accounts: accounts}), false)
	h.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		status := http.StatusTooManyRequests
		if r.URL.Host == "four.example.invalid" {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	resp, _, err := h.ForwardRequest(context.Background(), ProviderOpenAI, "/v1/chat/completions", []byte(`{}`), make(http.Header))
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("healthy fourth account was not tried: %v", err)
	}
}

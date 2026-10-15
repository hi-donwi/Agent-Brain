package router

import (
	"context"
	"net/http"
	"strings"
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

func TestTokenSaverPreservesUserContentAndLargeNumbers(t *testing.T) {
	input := []byte(`{"seed":9007199254740993,"messages":[{"role":"user","content":"keep\n\n\n\nthese lines"}]}`)
	h := NewProxyHandler(NewAccountPool(DefaultRouterConfig()), true)
	got := string(h.optimizePayload(input))
	if !strings.Contains(got, "9007199254740993") || !strings.Contains(got, `keep\n\n\n\nthese lines`) {
		t.Fatal("token saver changed non-tool request fields")
	}
}

func TestCustomEndpointsRequireAnAccountSelectorWhenOriginsDiffer(t *testing.T) {
	pool := NewAccountPool(RouterConfig{Accounts: []Account{{ID: "one", Provider: "custom", Enabled: true, BaseURL: "https://one.example.invalid"}, {ID: "two", Provider: "custom", Enabled: true, BaseURL: "https://two.example.invalid"}}})
	h := NewProxyHandler(pool, false)
	called := false
	h.client.Transport = auditTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	response, _, err := h.ForwardRequest(context.Background(), "custom", "/v1/chat/completions", []byte(`{"model":"shared-name"}`), nil)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || called {
		t.Fatal("custom provider routing silently selected one of several unrelated endpoints")
	}
}

func TestCustomProviderUsesProtocolCredentialHeaders(t *testing.T) {
	for _, test := range []struct{ path, protocol, header string }{{"/v1/messages", ProtocolMessages, "x-api-key"}, {"/v1beta/models/model:generateContent", ProtocolGemini, "x-goog-api-key"}} {
		t.Run(test.protocol, func(t *testing.T) {
			pool := NewAccountPool(RouterConfig{Accounts: []Account{{ID: "one", Provider: "custom", Enabled: true, APIKey: "fake-custom-key", BaseURL: "http://127.0.0.1:1234", Protocols: []string{test.protocol}}}})
			h := NewProxyHandler(pool, false)
			h.client.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get(test.header) != "fake-custom-key" {
					t.Error("protocol credential header was not injected")
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
			})
			resp, _, err := h.ForwardRequest(context.Background(), "custom", test.path, []byte(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
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

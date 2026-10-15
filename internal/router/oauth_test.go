package router

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBridgeLoginVerifiesDedicatedIdentity(t *testing.T) {
	logged := false
	calls := []string{}
	client := &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer fake-management" {
			t.Error("management credential missing")
		}
		body := `{"status":"ok"}`
		switch r.URL.Path {
		case "/v8/management/config":
			body = `{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`
		case "/v8/management/credentials":
			if logged {
				body = `{"files":[{"id":"identity-1","name":"one.json","provider":"antigravity","status":"ready"}]}`
			} else {
				body = `{"files":[]}`
			}
		case "/v8/management/oauth/auth-url":
			body = `{"status":"ok","url":"https://accounts.google.com/o/oauth2/v2/auth?state=test-state","state":"test-state"}`
		case "/v8/management/oauth/status":
			logged = true
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake-management", Client: client}
	urlShown := false
	id, err := b.Login(context.Background(), ProviderAntigravity, func(url string) error { urlShown = strings.HasPrefix(url, "https://accounts.google.com/"); return nil })
	if err != nil || id != "identity-1" || !urlShown {
		t.Fatalf("login failed: %s %v", id, err)
	}
	if len(calls) < 4 {
		t.Fatal("identity was not checked after login")
	}
}

func TestBridgeRefusesSharedCredentials(t *testing.T) {
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"files":[{"id":"one","provider":"antigravity","status":"ready"},{"id":"two","provider":"antigravity","status":"ready"}]}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := b.Verify(context.Background(), ProviderAntigravity, ""); err == nil {
		t.Fatal("shared bridge accepted as a pinned account")
	}
}

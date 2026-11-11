package router

import (
	"context"
	"encoding/json"
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

func TestBridgeOAuthLoginCanBeStartedPolledAndCancelled(t *testing.T) {
	logged := false
	calls := []string{}
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		body := `{}`
		switch r.Method + " " + r.URL.Path {
		case "GET /v8/management/config":
			body = `{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`
		case "GET /v8/management/credentials":
			if logged {
				body = `{"files":[{"id":"identity-1","provider":"antigravity","status":"ready"}]}`
			} else {
				body = `{"files":[]}`
			}
		case "GET /v8/management/oauth/auth-url":
			body = `{"status":"ok","url":"https://accounts.google.com/o/oauth2/v2/auth?state=state-1","state":"state-1"}`
		case "GET /v8/management/oauth/status":
			if !logged {
				body = `{"status":"wait"}`
			} else {
				body = `{"status":"ok"}`
			}
		case "DELETE /v8/management/oauth/session":
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	authorizationURL, state, err := b.BeginLogin(context.Background(), ProviderAntigravity)
	if err != nil || state != "state-1" || !strings.HasPrefix(authorizationURL, "https://accounts.google.com/") {
		t.Fatalf("begin returned URL=%q state=%q err=%v", authorizationURL, state, err)
	}
	id, complete, err := b.PollLogin(context.Background(), ProviderAntigravity, state)
	if err != nil || complete || id != "" {
		t.Fatalf("pending poll returned id=%q complete=%t err=%v", id, complete, err)
	}
	logged = true
	id, complete, err = b.PollLogin(context.Background(), ProviderAntigravity, state)
	if err != nil || !complete || id != "identity-1" {
		t.Fatalf("complete poll returned id=%q complete=%t err=%v", id, complete, err)
	}
	if err := b.CancelLogin(context.Background(), state); err != nil {
		t.Fatalf("cancel returned %v", err)
	}
	if calls[len(calls)-1] != "DELETE /v8/management/oauth/session" {
		t.Fatalf("cancel did not delete the provider OAuth session: %v", calls)
	}
}

func TestBridgeOAuthBeginRejectsUntrustedURLsAndStates(t *testing.T) {
	for _, response := range []string{
		`{"status":"ok","url":"http://accounts.google.com/auth?state=state-1","state":"state-1"}`,
		`{"status":"ok","url":"https://evil.example/auth?state=state-1","state":"state-1"}`,
		`{"status":"ok","url":"https://accounts.google.com/auth?state=other","state":"state-1"}`,
	} {
		b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`
			if r.URL.Path == "/v8/management/credentials" {
				body = `{"files":[]}`
			} else if r.URL.Path == "/v8/management/oauth/auth-url" {
				body = response
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		if _, _, err := b.BeginLogin(context.Background(), ProviderAntigravity); err == nil {
			t.Fatalf("untrusted OAuth response accepted: %s", response)
		}
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

func TestBridgeModelsComeFromTheBoundCredential(t *testing.T) {
	modelsCalled := false
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		body := `{ "config-version":8,"plugins":{"enabled":false}}`
		switch r.URL.Path {
		case "/v8/management/credentials":
			body = `{"files":[{"id":"identity-one","provider":"antigravity","status":"ready"}]}`
		case "/v8/management/credentials/models":
			if r.URL.Query().Get("name") != "identity-one" {
				t.Error("models requested for a different identity")
			}
			modelsCalled = true
			body = `{"models":[{"id":"model-b"},{"id":"model-a"},{"id":"model-b"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	models, err := b.Models(context.Background(), ProviderAntigravity, "identity-one")
	if err != nil || strings.Join(models, ",") != "model-a,model-b" || !modelsCalled {
		t.Fatalf("models=%v err=%v", models, err)
	}
	modelsCalled = false
	if _, err = b.Models(context.Background(), ProviderAntigravity, "different"); err == nil || modelsCalled {
		t.Fatal("identity mismatch did not block model discovery")
	}
}

func TestBridgeModelsRejectsEmptyOrUnsafeCatalogs(t *testing.T) {
	for _, ids := range [][]string{{}, {"\x1b[2J"}, {"model with spaces"}} {
		b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"config-version":8}`
			switch r.URL.Path {
			case "/v8/management/credentials":
				body = `{"files":[{"id":"one","provider":"antigravity","status":"ready"}]}`
			case "/v8/management/credentials/models":
				models := make([]map[string]string, 0, len(ids))
				for _, id := range ids {
					models = append(models, map[string]string{"id": id})
				}
				data, _ := json.Marshal(map[string]any{"models": models})
				body = string(data)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		if _, err := b.Models(context.Background(), ProviderAntigravity, "one"); err == nil {
			t.Fatalf("unsafe model catalog accepted: %q", ids)
		}
	}
}

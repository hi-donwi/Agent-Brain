package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWaitModelsKeepsPollingUntilTheBoundCatalogAppears(t *testing.T) {
	attempts, notices := 0, 0
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"config-version":8}`
		switch r.URL.Path {
		case "/v8/management/credentials":
			body = `{"files":[{"id":"one","provider":"antigravity","status":"ready"}]}`
		case "/v8/management/credentials/models":
			if r.URL.Query().Get("name") != "one" {
				t.Error("model polling switched identity")
			}
			attempts++
			body = `{"models":[]}`
			if attempts == 3 {
				body = `{"models":[{"id":"model-ready"}]}`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	models, err := b.waitModels(context.Background(), ProviderAntigravity, "one", time.Millisecond, func() { notices++ })
	if err != nil || len(models) != 1 || attempts != 3 || notices != 1 {
		t.Fatalf("models=%v attempts=%d notices=%d err=%v", models, attempts, notices, err)
	}
}

func TestWaitModelsStopsOnCancellationOrCredentialErrors(t *testing.T) {
	for _, cancelPending := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"config-version":8}`
			status := 200
			switch r.URL.Path {
			case "/v8/management/credentials":
				body = `{"files":[{"id":"one","provider":"antigravity","status":"ready"}]}`
			case "/v8/management/credentials/models":
				attempts++
				body = `{"models":[]}`
				if !cancelPending {
					status = 401
				}
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		_, err := b.waitModels(ctx, ProviderAntigravity, "one", time.Millisecond, cancel)
		cancel()
		if err == nil || attempts != 1 {
			t.Fatalf("invalid retry behavior: attempts=%d err=%v", attempts, err)
		}
		if cancelPending && (!errors.Is(err, context.Canceled) || !errors.Is(err, ErrModelCatalogPending)) {
			t.Fatalf("cancellation did not preserve pending status: %v", err)
		}
	}
}

func TestWaitModelsBoundsRegistrationWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	b := BridgeClient{URL: "http://127.0.0.1:8317/v8/management", Key: "fake", Client: &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"config-version":8}`
		switch r.URL.Path {
		case "/v8/management/credentials":
			body = `{"files":[{"id":"one","provider":"antigravity","status":"ready"}]}`
		case "/v8/management/credentials/models":
			body = `{"models":[]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	_, err := b.waitModels(ctx, ProviderAntigravity, "one", time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrModelCatalogPending) {
		t.Fatalf("unbounded or misclassified model wait: %v", err)
	}
}

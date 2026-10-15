package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyHandler_FailoverOn429(t *testing.T) {
	callCount := 0

	// Mock server for Account 1 that always returns 429
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer server1.Close()

	// Mock server for Account 2 that succeeds with 200
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "hello from acc2"}}]}`))
	}))
	defer server2.Close()

	cfg := RouterConfig{
		Strategy: "priority",
		Accounts: []Account{
			{
				ID:       "acc-1",
				Provider: ProviderOpenAI,
				BaseURL:  server1.URL,
				Priority: 1,
				Enabled:  true,
			},
			{
				ID:       "acc-2",
				Provider: ProviderOpenAI,
				BaseURL:  server2.URL,
				Priority: 2,
				Enabled:  true,
			},
		},
	}

	pool := NewAccountPool(cfg)
	handler := NewProxyHandler(pool, true)

	body := []byte(`{"model": "gpt-4o", "messages": [{"role": "user", "content": "hi"}]}`)
	resp, chosenAcc, err := handler.ForwardRequest(context.Background(), ProviderOpenAI, "/v1/chat/completions", body, make(http.Header))
	if err != nil {
		t.Fatalf("expected successful failover, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if chosenAcc.ID != "acc-2" {
		t.Errorf("expected chosen account acc-2, got %s", chosenAcc.ID)
	}
	if callCount != 2 {
		t.Errorf("expected 2 total calls (acc-1 then acc-2), got %d", callCount)
	}

	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != `{"choices": [{"message": {"content": "hello from acc2"}}]}` {
		t.Errorf("unexpected body: %s", string(respBody))
	}

	// Verify acc-1 is now marked as rate_limited
	accounts := pool.ListAccounts()
	for _, a := range accounts {
		if a.ID == "acc-1" && a.Status != StatusRateLimited {
			t.Errorf("expected acc-1 to be rate_limited, got %s", a.Status)
		}
	}
}

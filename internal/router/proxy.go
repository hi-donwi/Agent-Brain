package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProxyHandler routes incoming requests through the account pool with automatic failover and token optimization.
type ProxyHandler struct {
	pool       *AccountPool
	client     *http.Client
	tokenSaver bool
}

// NewProxyHandler creates a new proxy handler with standard HTTP transport.
func NewProxyHandler(pool *AccountPool, tokenSaver bool) *ProxyHandler {
	return &ProxyHandler{
		pool: pool,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		tokenSaver: tokenSaver,
	}
}

// ForwardRequest dispatches an HTTP request to the selected upstream provider account,
// automatically falling back to alternative accounts on 429/529.
func (h *ProxyHandler) ForwardRequest(ctx context.Context, provider string, targetPath string, body []byte, headers http.Header) (*http.Response, *Account, error) {
	return h.ForwardSelectedRequest(ctx, provider, "", targetPath, body, headers)
}

// ForwardSelectedRequest pins an explicit account or tries each pool candidate once.
func (h *ProxyHandler) ForwardSelectedRequest(ctx context.Context, provider, pinned, targetPath string, body []byte, headers http.Header) (*http.Response, *Account, error) {
	definition, err := LookupProvider(provider)
	if err != nil {
		return nil, nil, err
	}
	provider = definition.ID
	tried := make(map[string]bool)
	maxRetries := len(h.pool.ListAccounts())

	// Pre-process body with Token Saver if enabled
	processedBody := body
	if h.tokenSaver {
		processedBody = h.optimizePayload(body)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		acc, err := h.pool.selectAccount(provider, pinned, tried)
		if err != nil {
			return nil, nil, fmt.Errorf("account selection failed: %w", err)
		}

		tried[acc.ID] = true
		baseURL := acc.BaseURL
		if baseURL == "" {
			baseURL = definition.BaseURL
		}
		if baseURL == "" {
			return nil, acc, fmt.Errorf("provider %s requires an explicit configured endpoint", provider)
		}
		if err := ValidateEndpoint(baseURL); err != nil {
			return nil, acc, err
		}
		path := targetPath
		if provider == ProviderGemini && strings.HasPrefix(path, "/v1/") {
			path = "/v1beta/openai/" + strings.TrimPrefix(path, "/v1/")
		}
		if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") && strings.HasPrefix(path, "/v1/") {
			path = strings.TrimPrefix(path, "/v1")
		}

		targetURL := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(processedBody))
		if err != nil {
			return nil, acc, fmt.Errorf("create upstream request: %w", err)
		}

		// Copy non-hop headers
		for k, v := range headers {
			if sensitiveHeader(k) || hopHeader(k) {
				continue
			}
			req.Header[k] = append([]string(nil), v...)
		}

		// Inject active account credential
		key, keyErr := ResolveCredential(acc.APIKey)
		if keyErr != nil {
			h.pool.ReportResult(acc.ID, 401, 0)
			continue
		}
		if key != "" {
			if acc.Provider == ProviderAnthropic {
				req.Header.Set("x-api-key", key)
				if req.Header.Get("anthropic-version") == "" {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
			} else if acc.Provider == ProviderGemini && strings.HasPrefix(path, "/v1beta/models/") {
				req.Header.Set("x-goog-api-key", key)
			} else {
				req.Header.Set("Authorization", "Bearer "+key)
			}
		}

		for hk, hv := range acc.Headers {
			if sensitiveHeader(hk) || hopHeader(hk) {
				continue
			}
			req.Header.Set(hk, hv)
		}
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := h.client.Do(req)
		if err != nil {
			h.pool.ReportResult(acc.ID, 500, 0)
			continue
		}

		// Check for rate-limiting or server overload
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
			h.pool.ReportResult(acc.ID, resp.StatusCode, retryAfter)
			continue // Try next candidate account in pool
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusForbidden {
			_ = resp.Body.Close()
			h.pool.ReportResult(acc.ID, resp.StatusCode, 0)
			continue
		}

		// Success or client error that shouldn't be retried
		h.pool.ReportResult(acc.ID, resp.StatusCode, 0)
		return resp, acc, nil
	}

	return nil, nil, fmt.Errorf("all account retries exhausted for provider %s", provider)
}

func sensitiveHeader(k string) bool {
	switch strings.ToLower(k) {
	case "authorization", "proxy-authorization", "x-api-key", "x-goog-api-key", "cookie", "set-cookie", "x-brain-account", "x-brain-provider":
		return true
	}
	return false
}

func hopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "host", "content-length", "connection", "keep-alive", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func (h *ProxyHandler) optimizePayload(body []byte) []byte {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	// Optimize messages if present
	if msgs, ok := payload["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				if msgMap["role"] != "tool" && msgMap["role"] != "function" {
					continue
				}
				if content, ok := msgMap["content"].(string); ok {
					msgMap["content"] = CompressToolResult(content)
				}
			}
		}
		if optimized, err := json.Marshal(payload); err == nil {
			return optimized
		}
	}

	return body
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(header); err == nil {
		if d := time.Until(deadline); d > 0 {
			return d
		}
	}
	return 0
}

// StreamResponse streams an upstream response back to the client ResponseWriter.
func StreamResponse(w http.ResponseWriter, upstreamResp *http.Response) error {
	defer upstreamResp.Body.Close()

	for k, v := range upstreamResp.Header {
		if sensitiveHeader(k) || hopHeader(k) {
			continue
		}
		w.Header()[k] = v
	}
	w.WriteHeader(upstreamResp.StatusCode)

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	buf := make([]byte, 4096)
	for {
		n, err := upstreamResp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

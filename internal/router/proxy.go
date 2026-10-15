package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
			Timeout:       120 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
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
	return h.forward(ctx, provider, pinned, targetPath, http.MethodPost, body, headers)
}

// ForwardMetadataRequest preserves the provider's model metadata without inference.
func (h *ProxyHandler) ForwardMetadataRequest(ctx context.Context, provider, pinned, path string, headers http.Header) (*http.Response, *Account, error) {
	return h.forward(ctx, provider, pinned, path, http.MethodGet, nil, headers)
}

func (h *ProxyHandler) forward(ctx context.Context, provider, pinned, targetPath, method string, body []byte, headers http.Header) (*http.Response, *Account, error) {
	definition, err := LookupProvider(provider)
	if err != nil {
		return nil, nil, err
	}
	provider = definition.ID
	if pinned == "" && (provider == "custom" || provider == "opencode-zen") {
		origins := make(map[string]bool)
		for _, a := range h.pool.ListAccounts() {
			if a.Enabled && a.Provider == provider {
				origins[strings.TrimRight(a.BaseURL, "/")] = true
			}
		}
		if len(origins) > 1 {
			return nil, nil, fmt.Errorf("select an explicit account when compatible endpoints differ")
		}
	}
	tried := make(map[string]bool)
	maxRetries := len(h.pool.ListAccounts())

	// Pre-process body with Token Saver if enabled
	processedBody := body
	if h.tokenSaver {
		processedBody = h.optimizePayload(body)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		acc, err := h.pool.selectAccount(provider, pinned, tried)
		if err != nil {
			return nil, nil, fmt.Errorf("account selection failed: %w", err)
		}

		tried[acc.ID] = true
		if acc.Auth == AuthOAuth {
			if acc.BridgeCredential == "" {
				continue
			}
			bridge := BridgeClient{URL: acc.BridgeURL, Key: acc.BridgeKey, Client: h.client}
			if _, err := bridge.Verify(ctx, acc.Provider, acc.BridgeCredential); err != nil {
				continue
			}
		}
		protocol := ProtocolChat
		switch {
		case strings.HasPrefix(targetPath, "/v1/embeddings"):
			protocol = ProtocolEmbeddings
		case strings.HasPrefix(targetPath, "/v1/responses"):
			protocol = ProtocolResponses
		case strings.HasPrefix(targetPath, "/v1/messages"):
			protocol = ProtocolMessages
		case strings.HasPrefix(targetPath, "/v1beta/models"):
			protocol = ProtocolGemini
		}
		if !acc.Supports(protocol) {
			continue
		}
		var requested struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &requested)
		if protocol == ProtocolGemini {
			parsed, _ := url.Parse(targetPath)
			if name, ok := strings.CutPrefix(parsed.Path, "/v1beta/models/"); ok {
				requested.Model, _, _ = strings.Cut(name, ":")
			}
		}
		if len(acc.Models) > 0 && requested.Model != "" && !contains(acc.Models, requested.Model) {
			continue
		}
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
		if provider == ProviderDeepSeek {
			path = strings.TrimPrefix(path, "/v1")
		}
		if parsed, err := url.Parse(path); err == nil {
			query := parsed.Query()
			for _, name := range []string{"key", "api_key", "access_token"} {
				query.Del(name)
			}
			parsed.RawQuery = query.Encode()
			path = parsed.String()
		}
		if provider == ProviderGemini && strings.HasPrefix(path, "/v1/") {
			path = "/v1beta/openai/" + strings.TrimPrefix(path, "/v1/")
		}
		if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") && strings.HasPrefix(path, "/v1/") {
			path = strings.TrimPrefix(path, "/v1")
		}

		targetURL := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")

		req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(processedBody))
		if err != nil {
			return nil, acc, fmt.Errorf("create upstream request: %w", err)
		}

		// Copy non-hop headers
		connectionHeaders := make(map[string]bool)
		for _, value := range strings.Split(headers.Get("Connection"), ",") {
			connectionHeaders[strings.ToLower(strings.TrimSpace(value))] = true
		}
		for k, v := range headers {
			if sensitiveHeader(k) || hopHeader(k) || connectionHeaders[strings.ToLower(k)] {
				continue
			}
			req.Header[k] = append([]string(nil), v...)
		}

		// Inject active account credential
		key, keyErr := ResolveCredential(acc.APIKey)
		if keyErr != nil || (acc.Auth == AuthAPIKey && key == "") {
			h.pool.ReportResult(acc.ID, 401, 0)
			continue
		}
		if key != "" {
			if protocol == ProtocolMessages && acc.Auth != AuthOAuth {
				req.Header.Set("x-api-key", key)
				if req.Header.Get("anthropic-version") == "" {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
			} else if protocol == ProtocolGemini && acc.Auth != AuthOAuth {
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
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(payload["messages"], &messages) != nil {
		return body
	}
	changed := false
	for _, message := range messages {
		var role, content string
		if json.Unmarshal(message["role"], &role) != nil || (role != "tool" && role != "function") {
			continue
		}
		if json.Unmarshal(message["content"], &content) != nil {
			continue
		}
		compressed := CompressToolResult(content)
		if compressed != content {
			message["content"], _ = json.Marshal(compressed)
			changed = true
		}
	}
	if !changed {
		return body
	}
	payload["messages"], _ = json.Marshal(messages)
	result, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return result
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

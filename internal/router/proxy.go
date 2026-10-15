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
	maxRetries := 3

	// Pre-process body with Token Saver if enabled
	processedBody := body
	if h.tokenSaver {
		processedBody = h.optimizePayload(body)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		acc, err := h.pool.SelectAccount(provider)
		if err != nil {
			return nil, nil, fmt.Errorf("account selection failed: %w", err)
		}

		baseURL := acc.BaseURL
		if baseURL == "" {
			switch acc.Provider {
			case ProviderAnthropic:
				baseURL = "https://api.anthropic.com"
			case ProviderOpenAI:
				baseURL = "https://api.openai.com"
			case ProviderGemini:
				baseURL = "https://generativelanguage.googleapis.com"
			case ProviderDeepSeek:
				baseURL = "https://api.deepseek.com"
			case ProviderOpenRouter:
				baseURL = "https://openrouter.ai/api"
			default:
				baseURL = "http://127.0.0.1:11434"
			}
		}

		targetURL := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(targetPath, "/")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(processedBody))
		if err != nil {
			return nil, acc, fmt.Errorf("create upstream request: %w", err)
		}

		// Copy non-hop headers
		for k, v := range headers {
			if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
				continue
			}
			req.Header[k] = v
		}

		// Inject active account credential
		key := ResolveAPIKey(acc.APIKey)
		if key != "" {
			if acc.Provider == ProviderAnthropic {
				req.Header.Set("x-api-key", key)
				if req.Header.Get("anthropic-version") == "" {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
			} else {
				req.Header.Set("Authorization", "Bearer "+key)
			}
		}

		for hk, hv := range acc.Headers {
			req.Header.Set(hk, hv)
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

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired {
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

func (h *ProxyHandler) optimizePayload(body []byte) []byte {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	// Optimize messages if present
	if msgs, ok := payload["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
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
	return 0
}

// StreamResponse streams an upstream response back to the client ResponseWriter.
func StreamResponse(w http.ResponseWriter, upstreamResp *http.Response) error {
	defer upstreamResp.Body.Close()

	for k, v := range upstreamResp.Header {
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

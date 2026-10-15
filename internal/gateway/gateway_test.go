package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/residency"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
)

type mockGatewayAdapter struct {
	chatReq    *backend.ChatRequest
	chatResp   *backend.ChatResponse
	chatErr    error
	chatFunc   func(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error)
	streamFunc func(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error)
	embedReq   *backend.EmbedRequest
	embedResp  *backend.EmbedResponse
	embedErr   error
	models     []backend.ModelInfo
}

func (m *mockGatewayAdapter) Name() string     { return "ollama" }
func (m *mockGatewayAdapter) Endpoint() string { return "http://127.0.0.1:11434" }
func (m *mockGatewayAdapter) Ping(ctx context.Context) error {
	return nil
}
func (m *mockGatewayAdapter) Version(ctx context.Context) (string, error) {
	return "0.30.8", nil
}
func (m *mockGatewayAdapter) ListModels(ctx context.Context) ([]backend.ModelInfo, error) {
	return m.models, nil
}
func (m *mockGatewayAdapter) ShowModel(ctx context.Context, model string) (backend.ModelDetails, error) {
	return backend.ModelDetails{}, nil
}
func (m *mockGatewayAdapter) RunningModels(ctx context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (m *mockGatewayAdapter) Chat(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	m.chatReq = &req
	if m.chatFunc != nil {
		return m.chatFunc(ctx, req)
	}
	return m.chatResp, m.chatErr
}
func (m *mockGatewayAdapter) ChatStream(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	m.chatReq = &req
	if m.streamFunc != nil {
		return m.streamFunc(ctx, req, onChunk)
	}
	if onChunk != nil && m.chatResp != nil {
		_ = onChunk(backend.ChatChunk{
			Role:    m.chatResp.Message.Role,
			Content: m.chatResp.Message.Content,
			Done:    false,
		})
		_ = onChunk(backend.ChatChunk{
			Done:            true,
			PromptEvalCount: m.chatResp.PromptEvalCount,
			EvalCount:       m.chatResp.EvalCount,
		})
	}
	return m.chatResp, m.chatErr
}
func (m *mockGatewayAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	m.embedReq = &req
	return m.embedResp, m.embedErr
}
func (m *mockGatewayAdapter) Pull(ctx context.Context, model string, onProgress func(backend.Progress)) error {
	return nil
}
func (m *mockGatewayAdapter) Delete(ctx context.Context, model string) error {
	return nil
}
func (m *mockGatewayAdapter) Unload(ctx context.Context, model string) error {
	return nil
}

func TestToken_Generate_and_Load(t *testing.T) {
	tok, err := gateway.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("expected 64 chars hex token, got %d", len(tok))
	}

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "serve.token")

	// 1. Create token
	loaded1, err := gateway.LoadOrCreateToken(tokenPath)
	if err != nil {
		t.Fatalf("LoadOrCreateToken failed: %v", err)
	}
	if len(loaded1) != 64 {
		t.Errorf("expected 64 chars token, got %d", len(loaded1))
	}

	// Verify permissions (0600)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token file failed: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected mode 0600, got %o", info.Mode().Perm())
	}

	// 2. Load existing token idempotently
	loaded2, err := gateway.LoadOrCreateToken(tokenPath)
	if err != nil {
		t.Fatalf("LoadOrCreateToken 2 failed: %v", err)
	}
	if loaded1 != loaded2 {
		t.Errorf("expected same token, got %s and %s", loaded1, loaded2)
	}
}

func TestMiddleware_Auth_and_Host(t *testing.T) {
	token, err := gateway.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	adapter := &mockGatewayAdapter{}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:    "127.0.0.1:0",
		Token:   token,
		Adapter: adapter,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := s.Handler()

	// 1. /healthz allowed without auth
	reqHealth := httptest.NewRequest("GET", "/healthz", nil)
	reqHealth.Host = "127.0.0.1"
	recHealth := httptest.NewRecorder()
	handler.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Errorf("expected 200 on /healthz, got %d", recHealth.Code)
	}

	// 2. /v1/models without auth -> 401
	reqNoAuth := httptest.NewRequest("GET", "/v1/models", nil)
	reqNoAuth.Host = "127.0.0.1"
	recNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 on /v1/models without auth, got %d", recNoAuth.Code)
	}

	// 3. /v1/models with valid auth -> 200
	reqAuth := httptest.NewRequest("GET", "/v1/models", nil)
	reqAuth.Host = "127.0.0.1"
	reqAuth.Header.Set("Authorization", "Bearer "+token)
	recAuth := httptest.NewRecorder()
	handler.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Errorf("expected 200 on /v1/models with auth, got %d", recAuth.Code)
	}

	// 4. DNS rebinding attack: evil host header -> 403
	reqEvilHost := httptest.NewRequest("GET", "/healthz", nil)
	reqEvilHost.Host = "attacker.example.com"
	recEvilHost := httptest.NewRecorder()
	handler.ServeHTTP(recEvilHost, reqEvilHost)
	if recEvilHost.Code != http.StatusForbidden {
		t.Errorf("expected 403 for evil host header, got %d", recEvilHost.Code)
	}

	// 5. Wildcard allowed hosts allows external hosts
	wildcardHandler := gateway.HostValidationMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	reqWildcard := httptest.NewRequest("GET", "/healthz", nil)
	reqWildcard.Host = "52.77.217.37:52140"
	recWildcard := httptest.NewRecorder()
	wildcardHandler.ServeHTTP(recWildcard, reqWildcard)
	if recWildcard.Code != http.StatusOK {
		t.Errorf("expected 200 for wildcard host, got %d", recWildcard.Code)
	}
}

func TestServer_ChatCompletions(t *testing.T) {
	token := "test-token"
	adapter := &mockGatewayAdapter{
		chatResp: &backend.ChatResponse{
			Message: backend.ChatMessage{
				Role:    "assistant",
				Content: "Hello from lokal!",
			},
			PromptEvalCount: 15,
			EvalCount:       20,
		},
	}

	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{
			"chat": "qwen2.5-coder-1.5b",
		},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := s.Handler()

	// 1. Regular chat completion with role alias
	body := map[string]interface{}{
		"model": "chat",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chat response failed: %v", err)
	}

	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from lokal!" {
		t.Errorf("unexpected choice: %+v", resp.Choices)
	}
	if resp.Usage.TotalTokens != 35 {
		t.Errorf("expected 35 total tokens, got %d", resp.Usage.TotalTokens)
	}

	// Verify adapter received resolved tag
	if adapter.chatReq == nil || adapter.chatReq.Model != "qwen2.5-coder:1.5b" {
		t.Errorf("expected resolved model qwen2.5-coder:1.5b, got %v", adapter.chatReq)
	}

	// 2. Context length exceeded error
	hugeContent := strings.Repeat("a", 100000) // ~33k tokens, exceeds qwen2.5-coder-1.5b max context (32768)
	bodyHuge := map[string]interface{}{
		"model": "chat",
		"messages": []map[string]string{
			{"role": "user", "content": hugeContent},
		},
	}
	bodyHugeBytes, _ := json.Marshal(bodyHuge)
	reqHuge := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyHugeBytes))
	reqHuge.Host = "127.0.0.1"
	reqHuge.Header.Set("Authorization", "Bearer "+token)
	recHuge := httptest.NewRecorder()

	handler.ServeHTTP(recHuge, reqHuge)
	if recHuge.Code != http.StatusBadRequest || !strings.Contains(recHuge.Body.String(), "context_length_exceeded") {
		t.Errorf("expected 400 with context_length_exceeded, got %d: %s", recHuge.Code, recHuge.Body.String())
	}
}

func TestServer_Embeddings(t *testing.T) {
	token := "test-token"
	adapter := &mockGatewayAdapter{
		embedResp: &backend.EmbedResponse{
			Embeddings: [][]float32{
				{0.1, 0.2, 0.3},
			},
			PromptEvalCount: 5,
		},
	}

	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{
			"embed": "nomic-embed-text",
		},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := s.Handler()

	body := map[string]interface{}{
		"model": "embed",
		"input": "test embedding text",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode embed response failed: %v", err)
	}

	if len(resp.Data) != 1 || len(resp.Data[0].Embedding) != 3 {
		t.Errorf("unexpected embeddings data: %+v", resp.Data)
	}

	if adapter.embedReq == nil || adapter.embedReq.Truncate == nil || *adapter.embedReq.Truncate != false {
		t.Errorf("expected truncate: false in adapter request, got %+v", adapter.embedReq)
	}
}

func TestServer_ChatCompletions_Streaming(t *testing.T) {
	token := "test-token"
	adapter := &mockGatewayAdapter{
		streamFunc: func(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
			_ = onChunk(backend.ChatChunk{
				Role:    "assistant",
				Content: "Hello",
				Done:    false,
			})
			_ = onChunk(backend.ChatChunk{
				Content: " world!",
				Done:    false,
			})
			_ = onChunk(backend.ChatChunk{
				Done:            true,
				PromptEvalCount: 10,
				EvalCount:       12,
			})
			return &backend.ChatResponse{
				PromptEvalCount: 10,
				EvalCount:       12,
			}, nil
		},
	}

	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{
			"chat": "qwen2.5-coder-1.5b",
		},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	body := map[string]interface{}{
		"model":  "chat",
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %s", contentType)
	}

	rawOutput := rec.Body.String()
	lines := strings.Split(rawOutput, "\n")

	var collectedContent string
	var sawStopReason bool
	var sawDoneMarker bool
	var sawUsage bool

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "data: [DONE]" {
			sawDoneMarker = true
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataJSON := strings.TrimPrefix(line, "data: ")
		var chunk struct {
			Object  string `json:"object"`
			Choices []struct {
				Delta struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(dataJSON), &chunk); err != nil {
			t.Fatalf("unmarshal chunk failed: %v on line: %s", err, line)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Errorf("expected object chat.completion.chunk, got %s", chunk.Object)
		}
		if len(chunk.Choices) > 0 {
			collectedContent += chunk.Choices[0].Delta.Content
			if chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason == "stop" {
				sawStopReason = true
			}
		}
		if chunk.Usage != nil {
			sawUsage = true
			if chunk.Usage.TotalTokens != 22 {
				t.Errorf("expected 22 total tokens in stream usage, got %d", chunk.Usage.TotalTokens)
			}
		}
	}

	if collectedContent != "Hello world!" {
		t.Errorf("expected collected content 'Hello world!', got %q", collectedContent)
	}
	if !sawStopReason {
		t.Errorf("expected chunk with finish_reason stop")
	}
	if !sawDoneMarker {
		t.Errorf("expected trailing data: [DONE]")
	}
	if !sawUsage {
		t.Errorf("expected stream usage in final chunk")
	}
}

func TestServer_ChatCompletions_Fallback(t *testing.T) {
	token := "test-token"
	var attemptedModels []string

	adapter := &mockGatewayAdapter{
		chatFunc: func(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
			attemptedModels = append(attemptedModels, req.Model)
			if req.Model == "qwen2.5-coder:1.5b" {
				return nil, errors.New("model not installed")
			}
			return &backend.ChatResponse{
				Model: req.Model,
				Message: backend.ChatMessage{
					Role:    "assistant",
					Content: "Response from fallback model",
				},
				PromptEvalCount: 5,
				EvalCount:       8,
			}, nil
		},
	}

	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{
			"chat": "qwen2.5-coder-1.5b",
		},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	body := map[string]interface{}{
		"model": "chat",
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after fallback, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(attemptedModels) < 2 {
		t.Errorf("expected at least 2 attempts (primary + fallback), got %v", attemptedModels)
	}
	if attemptedModels[0] != "qwen2.5-coder:1.5b" {
		t.Errorf("expected first attempt to be primary model, got %s", attemptedModels[0])
	}

	var resp struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Choices[0].Message.Content != "Response from fallback model" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
}

func TestServer_Tokens(t *testing.T) {
	token := "test-token"
	adapter := &mockGatewayAdapter{}
	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{
			"chat": "qwen2.5-coder-1.5b",
		},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := s.Handler()

	// 1. Valid tokens request with text
	bodyText := map[string]interface{}{
		"model": "chat",
		"text":  "Hello, world! This is a test prompt for token estimation.",
	}
	bodyBytes, _ := json.Marshal(bodyText)
	req := httptest.NewRequest("POST", "/v1/tokens", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /v1/tokens, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Model      string `json:"model"`
		Role       string `json:"role"`
		Tokens     int    `json:"tokens"`
		MaxContext int    `json:"max_context"`
		Fits       bool   `json:"fits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal tokens response failed: %v", err)
	}
	if resp.Model != "qwen2.5-coder:1.5b" {
		t.Errorf("expected model qwen2.5-coder:1.5b, got %s", resp.Model)
	}
	if resp.Role != "chat" {
		t.Errorf("expected role chat, got %s", resp.Role)
	}
	if resp.Tokens <= 0 {
		t.Errorf("expected tokens > 0, got %d", resp.Tokens)
	}
	if resp.MaxContext != 32768 {
		t.Errorf("expected max_context 32768, got %d", resp.MaxContext)
	}
	if !resp.Fits {
		t.Errorf("expected fits to be true")
	}

	// 2. Huge messages that exceed max_context -> fits: false
	hugeContent := strings.Repeat("x", 120000)
	bodyHuge := map[string]interface{}{
		"model": "chat",
		"messages": []map[string]string{
			{"role": "user", "content": hugeContent},
		},
	}
	hugeBytes, _ := json.Marshal(bodyHuge)
	reqHuge := httptest.NewRequest("POST", "/v1/tokens", bytes.NewReader(hugeBytes))
	reqHuge.Host = "127.0.0.1"
	reqHuge.Header.Set("Authorization", "Bearer "+token)
	recHuge := httptest.NewRecorder()

	handler.ServeHTTP(recHuge, reqHuge)
	if recHuge.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /v1/tokens for huge prompt, got %d: %s", recHuge.Code, recHuge.Body.String())
	}
	var respHuge struct {
		Fits   bool `json:"fits"`
		Tokens int  `json:"tokens"`
	}
	_ = json.Unmarshal(recHuge.Body.Bytes(), &respHuge)
	if respHuge.Fits {
		t.Errorf("expected fits false for huge prompt, got true (%d tokens)", respHuge.Tokens)
	}

	// 3. Invalid request: empty model
	badReq := httptest.NewRequest("POST", "/v1/tokens", bytes.NewReader([]byte(`{"model":""}`)))
	badReq.Host = "127.0.0.1"
	badReq.Header.Set("Authorization", "Bearer "+token)
	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, badReq)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty model, got %d", recBad.Code)
	}
}

func TestResolveModel_Fallbacks(t *testing.T) {
	cfg := &home.Config{
		Aliases: map[string]string{
			"chat": "qwen2.5-coder-1.5b",
		},
	}

	target := gateway.ResolveModel("chat", cfg, nil, "ollama")
	if !target.IsRole {
		t.Errorf("expected target to be a role")
	}
	if target.Role != "chat" {
		t.Errorf("expected role chat, got %s", target.Role)
	}
	if target.BackendTag != "qwen2.5-coder:1.5b" {
		t.Errorf("expected primary tag qwen2.5-coder:1.5b, got %s", target.BackendTag)
	}
	if len(target.Fallbacks) == 0 {
		t.Errorf("expected fallback models for role chat, got none")
	}
	for _, fb := range target.Fallbacks {
		if fb == "qwen2.5-coder:1.5b" {
			t.Errorf("primary model should not be in its own fallback list")
		}
	}
}

func TestServer_Residency_MemoryExceeded(t *testing.T) {
	token := "test-token"
	adapter := &mockGatewayAdapter{}
	resMgr := residency.New(residency.Config{
		TotalBudget: 1 * 1024 * 1024 * 1024, // 1 GiB budget
	})

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:      "127.0.0.1:0",
		Token:     token,
		Adapter:   adapter,
		Residency: resMgr,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	body := map[string]interface{}{
		"model": "qwen2.5-coder:7b", // requires ~5.5 GiB
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for memory budget exceeded, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "memory_budget_exceeded") {
		t.Errorf("expected memory_budget_exceeded code, got %s", rec.Body.String())
	}
}

func TestServer_TraceRecording(t *testing.T) {
	tracesDir := t.TempDir()
	store := trace.NewStore(tracesDir)
	token := "trace-token"

	adapter := &mockGatewayAdapter{
		chatResp: &backend.ChatResponse{
			Message: backend.ChatMessage{
				Role:    "assistant",
				Content: "Hello from trace test",
			},
			PromptEvalCount: 15,
			EvalCount:       25,
		},
		streamFunc: func(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
			_ = onChunk(backend.ChatChunk{Content: "Streamed "})
			_ = onChunk(backend.ChatChunk{
				Done:            true,
				PromptEvalCount: 12,
				EvalCount:       18,
			})
			return &backend.ChatResponse{
				PromptEvalCount: 12,
				EvalCount:       18,
			}, nil
		},
	}

	userCfg := &home.Config{
		Profile: "tiny",
		Aliases: map[string]string{"chat": "qwen2.5-coder-1.5b"},
	}

	s, err := gateway.NewServer(gateway.ServerConfig{
		Addr:       "127.0.0.1:0",
		Token:      token,
		Adapter:    adapter,
		UserConfig: userCfg,
		TraceStore: store,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := s.Handler()

	// 1. Non-streaming completion
	body := map[string]interface{}{
		"model": "chat",
		"messages": []map[string]string{
			{"role": "user", "content": "ping non-stream"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Session-Id", "trace-sess-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-streaming chat failed: %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Streaming completion
	bodyStream := map[string]interface{}{
		"model":  "chat",
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": "ping stream"},
		},
	}
	bodyStreamBytes, _ := json.Marshal(bodyStream)
	reqStream := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyStreamBytes))
	reqStream.Host = "127.0.0.1"
	reqStream.Header.Set("Authorization", "Bearer "+token)
	reqStream.Header.Set("X-Session-Id", "trace-sess-1")
	recStream := httptest.NewRecorder()

	handler.ServeHTTP(recStream, reqStream)
	if recStream.Code != http.StatusOK {
		t.Fatalf("streaming chat failed: %d: %s", recStream.Code, recStream.Body.String())
	}

	// Verify trace records
	records, err := store.ReadSession("trace-sess-1")
	if err != nil {
		t.Fatalf("ReadSession failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 trace records, got %d", len(records))
	}

	if records[0].Response != "Hello from trace test" || records[0].PromptTokens != 15 || records[0].EvalTokens != 25 {
		t.Errorf("unexpected record 0: %+v", records[0])
	}

	if records[1].Response != "Streamed " || records[1].PromptTokens != 12 || records[1].EvalTokens != 18 {
		t.Errorf("unexpected record 1: %+v", records[1])
	}
}

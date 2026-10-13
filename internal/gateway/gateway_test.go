package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/gateway"
	"github.com/hi-donwi/Agent-Local/internal/home"
)

type mockGatewayAdapter struct {
	chatReq   *backend.ChatRequest
	chatResp  *backend.ChatResponse
	chatErr   error
	embedReq  *backend.EmbedRequest
	embedResp *backend.EmbedResponse
	embedErr  error
	models    []backend.ModelInfo
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
	token := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
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

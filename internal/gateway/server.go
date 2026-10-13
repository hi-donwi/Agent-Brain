package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
)

// DefaultPort is the default TCP port for lokal serve.
const DefaultPort = 52140

// ServerConfig configures the gateway loopback HTTP server.
type ServerConfig struct {
	Addr         string
	Token        string
	AllowedHosts []string
	Adapter      backend.Adapter
	Catalog      *catalog.Catalog
	UserConfig   *home.Config
	Version      string
}

// Server is the OpenAI-compatible loopback HTTP server.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	listener   net.Listener
}

// NewServer constructs the gateway server with security middleware and routes.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Addr == "" {
		cfg.Addr = fmt.Sprintf("127.0.0.1:%d", DefaultPort)
	}
	if cfg.Catalog == nil {
		cfg.Catalog = catalog.Default()
	}

	mux := http.NewServeMux()
	s := &Server{cfg: cfg}

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)

	// Wrap with security middleware: DNS rebinding protection and Bearer token auth
	var handler http.Handler = mux
	handler = AuthMiddleware(cfg.Token)(handler)
	handler = HostValidationMiddleware(cfg.AllowedHosts)(handler)

	s.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s, nil
}

// Start begins listening on the configured address.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Addr, err)
	}
	s.listener = ln
	return s.httpServer.Serve(ln)
}

// Handler returns the configured HTTP handler with security middleware.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Addr returns the network address the server is listening on.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.cfg.Addr
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	profile := "unknown"
	if s.cfg.UserConfig != nil {
		profile = s.cfg.UserConfig.Profile
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": s.cfg.Version,
		"profile": profile,
	})
}

// OpenAI Model List representations.
type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type openAIModelList struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := []openAIModel{}
	now := time.Now().Unix()

	// 1. Add configured semantic roles as virtual models
	if s.cfg.UserConfig != nil && s.cfg.UserConfig.Aliases != nil {
		for role := range s.cfg.UserConfig.Aliases {
			models = append(models, openAIModel{
				ID:      role,
				Object:  "model",
				Created: now,
				OwnedBy: "lokal",
			})
		}
	} else {
		for _, role := range []string{"chat", "code", "embed"} {
			models = append(models, openAIModel{
				ID:      role,
				Object:  "model",
				Created: now,
				OwnedBy: "lokal",
			})
		}
	}

	// 2. Add backend installed models if backend is available
	if s.cfg.Adapter != nil {
		backendModels, err := s.cfg.Adapter.ListModels(ctx)
		if err == nil {
			for _, bm := range backendModels {
				created := now
				if !bm.ModifiedAt.IsZero() {
					created = bm.ModifiedAt.Unix()
				}
				models = append(models, openAIModel{
					ID:      bm.Name,
					Object:  "model",
					Created: created,
					OwnedBy: s.cfg.Adapter.Name(),
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openAIModelList{
		Object: "list",
		Data:   models,
	})
}

// Chat completions request and response representations.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type completionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []chatChoice    `json:"choices"`
	Usage   completionUsage `json:"usage"`
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid json payload: %v", err), "invalid_request_error", "bad_request")
		return
	}

	if strings.TrimSpace(req.Model) == "" {
		writeAPIError(w, http.StatusBadRequest, "model field is required", "invalid_request_error", "bad_request")
		return
	}

	if len(req.Messages) == 0 {
		writeAPIError(w, http.StatusBadRequest, "messages array cannot be empty", "invalid_request_error", "bad_request")
		return
	}

	if s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "no backend inference adapter configured", "api_error", "backend_unavailable")
		return
	}

	// Resolve requested model/role
	target := ResolveModel(req.Model, s.cfg.UserConfig, s.cfg.Catalog, s.cfg.Adapter.Name())

	// Context guard
	var numCtx int
	if target.CatalogModel != nil && target.CatalogModel.MaxContext > 0 {
		numCtx = target.CatalogModel.MaxContext
		// Estimate prompt characters (roughly 3.5 chars per token heuristic)
		totalChars := 0
		for _, m := range req.Messages {
			totalChars += len(m.Content)
		}
		estimatedTokens := totalChars / 3
		if estimatedTokens > numCtx {
			writeAPIError(w, http.StatusBadRequest,
				fmt.Sprintf("context length exceeded: estimated prompt (%d tokens) exceeds model max_context (%d)", estimatedTokens, numCtx),
				"invalid_request_error", "context_length_exceeded")
			return
		}
	}

	// Convert messages
	backendMsgs := make([]backend.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		backendMsgs[i] = backend.ChatMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	var chatOpts *backend.ChatOptions
	if numCtx > 0 || req.Temperature != nil {
		chatOpts = &backend.ChatOptions{
			NumCtx: numCtx,
		}
		if req.Temperature != nil {
			chatOpts.Temperature = *req.Temperature
		}
	}

	thinkFalse := false
	resp, err := s.cfg.Adapter.Chat(ctx, backend.ChatRequest{
		Model:    target.BackendTag,
		Messages: backendMsgs,
		Options:  chatOpts,
		Think:    &thinkFalse,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("chat completion failed on backend: %v", err), "api_error", "inference_error")
		return
	}

	apiResp := chatResponse{
		ID:      generateID("chatcmpl"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   target.BackendTag,
		Choices: []chatChoice{
			{
				Index: 0,
				Message: chatMessage{
					Role:    resp.Message.Role,
					Content: resp.Message.Content,
				},
				FinishReason: "stop",
			},
		},
		Usage: completionUsage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResp)
}

// Embeddings request and response representations.
type embedRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

type embedItem struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embedResponse struct {
	Object string          `json:"object"`
	Data   []embedItem     `json:"data"`
	Model  string          `json:"model"`
	Usage  completionUsage `json:"usage"`
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req embedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid json payload: %v", err), "invalid_request_error", "bad_request")
		return
	}

	if strings.TrimSpace(req.Model) == "" {
		writeAPIError(w, http.StatusBadRequest, "model field is required", "invalid_request_error", "bad_request")
		return
	}

	if s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "no backend inference adapter configured", "api_error", "backend_unavailable")
		return
	}

	// Parse input: either a single string or an array of strings
	var inputStrings []string
	var single string
	if err := json.Unmarshal(req.Input, &single); err == nil {
		inputStrings = []string{single}
	} else {
		var list []string
		if err := json.Unmarshal(req.Input, &list); err == nil {
			inputStrings = list
		} else {
			writeAPIError(w, http.StatusBadRequest, "input must be a string or array of strings", "invalid_request_error", "bad_request")
			return
		}
	}

	if len(inputStrings) == 0 {
		writeAPIError(w, http.StatusBadRequest, "input cannot be empty", "invalid_request_error", "bad_request")
		return
	}

	target := ResolveModel(req.Model, s.cfg.UserConfig, s.cfg.Catalog, s.cfg.Adapter.Name())
	truncateFalse := false

	embedResp, err := s.cfg.Adapter.Embed(ctx, backend.EmbedRequest{
		Model:    target.BackendTag,
		Input:    inputStrings,
		Truncate: &truncateFalse,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("embedding failed: %v", err), "api_error", "inference_error")
		return
	}

	items := make([]embedItem, len(embedResp.Embeddings))
	for i, emb := range embedResp.Embeddings {
		items[i] = embedItem{
			Object:    "embedding",
			Index:     i,
			Embedding: emb,
		}
	}

	apiResp := embedResponse{
		Object: "list",
		Data:   items,
		Model:  target.BackendTag,
		Usage: completionUsage{
			PromptTokens: embedResp.PromptEvalCount,
			TotalTokens:  embedResp.PromptEvalCount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResp)
}

package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
	"github.com/hi-donwi/Agent-Local/internal/residency"
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
	Residency    *residency.Manager
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
	if cfg.Residency == nil {
		const gib = uint64(1024 * 1024 * 1024)
		budget := 6 * gib
		if hwInfo, err := hw.Detect(); err == nil && hwInfo.Memory.TotalBytes > 0 {
			budget = (hwInfo.Memory.TotalBytes * 7) / 10
			if budget < 4*gib {
				budget = 4 * gib
			}
		}
		cfg.Residency = residency.New(residency.Config{
			TotalBudget:    budget,
			MaxConcurrency: 4,
			QueueTimeout:   60 * time.Second,
			Catalog:        cfg.Catalog,
			Adapter:        cfg.Adapter,
		})
	}

	mux := http.NewServeMux()
	s := &Server{cfg: cfg}

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/tokens", s.handleTokens)
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
	if s.cfg.Residency != nil {
		_ = s.cfg.Residency.Close()
	}
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
	data := map[string]interface{}{
		"status":  "ok",
		"version": s.cfg.Version,
		"profile": profile,
	}
	if s.cfg.Residency != nil {
		data["residency"] = s.cfg.Residency.Stats()
	}
	_ = json.NewEncoder(w).Encode(data)
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

// Streaming chunk representations.
type chatChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type chatChunkChoice struct {
	Index        int            `json:"index"`
	Delta        chatChunkDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type chatStreamChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
	Usage   *completionUsage  `json:"usage,omitempty"`
}

// Token counting representations.
type tokenCountRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages,omitempty"`
	Text     string        `json:"text,omitempty"`
}

type tokenCountResponse struct {
	Model      string `json:"model"`
	Role       string `json:"role,omitempty"`
	Tokens     int    `json:"tokens"`
	MaxContext int    `json:"max_context"`
	Fits       bool   `json:"fits"`
}

// estimateTokens provides a token count estimate from text and messages.
func estimateTokens(text string, msgs []chatMessage) int {
	totalChars := len(text)
	for _, m := range msgs {
		totalChars += len(m.Content) + 12
	}
	if totalChars == 0 {
		return 0
	}
	return (totalChars + 2) / 3
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
		estimatedTokens := estimateTokens("", req.Messages)
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

	candidateTags := append([]string{target.BackendTag}, target.Fallbacks...)
	thinkFalse := false

	// Acquire residency lease and concurrency slot
	if s.cfg.Residency != nil {
		release, err := s.cfg.Residency.Acquire(ctx, target.BackendTag, numCtx)
		if err != nil {
			if errors.Is(err, residency.ErrMemoryExceeded) {
				writeAPIError(w, http.StatusBadRequest,
					fmt.Sprintf("model %s exceeds available memory budget", target.BackendTag),
					"invalid_request_error", "memory_budget_exceeded")
				return
			}
			if errors.Is(err, residency.ErrQueueTimeout) {
				writeAPIError(w, http.StatusTooManyRequests,
					"request queue timeout: server concurrency capacity exceeded",
					"api_error", "queue_timeout")
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			writeAPIError(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to acquire model lease: %v", err),
				"api_error", "residency_error")
			return
		}
		defer release()
	}

	// Handle SSE streaming
	if req.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeAPIError(w, http.StatusInternalServerError, "streaming unsupported by response writer", "api_error", "streaming_error")
			return
		}

		streamID := generateID("chatcmpl")
		created := time.Now().Unix()

		var streamErr error
		var headersWritten bool
		var isFirstChunk = true
		var doneSent bool

		for _, tag := range candidateTags {
			isFirstChunk = true
			_, streamErr = s.cfg.Adapter.ChatStream(ctx, backend.ChatRequest{
				Model:    tag,
				Messages: backendMsgs,
				Options:  chatOpts,
				Think:    &thinkFalse,
			}, func(chunk backend.ChatChunk) error {
				if !headersWritten {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(http.StatusOK)
					headersWritten = true
				}

				if chunk.Done {
					doneSent = true
					stopReason := "stop"
					finalChoice := chatChunkChoice{
						Index:        0,
						Delta:        chatChunkDelta{},
						FinishReason: &stopReason,
					}
					var usage *completionUsage
					if chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 {
						usage = &completionUsage{
							PromptTokens:     chunk.PromptEvalCount,
							CompletionTokens: chunk.EvalCount,
							TotalTokens:      chunk.PromptEvalCount + chunk.EvalCount,
						}
					}
					finalPayload := chatStreamChunk{
						ID:      streamID,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   tag,
						Choices: []chatChunkChoice{finalChoice},
						Usage:   usage,
					}
					data, err := json.Marshal(finalPayload)
					if err == nil {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					}
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return nil
				}

				role := ""
				if isFirstChunk {
					role = "assistant"
					isFirstChunk = false
				}
				delta := chatChunkDelta{
					Role:    role,
					Content: chunk.Content,
				}
				streamPayload := chatStreamChunk{
					ID:      streamID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   tag,
					Choices: []chatChunkChoice{
						{
							Index:        0,
							Delta:        delta,
							FinishReason: nil,
						},
					},
				}
				data, err := json.Marshal(streamPayload)
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return err
				}
				flusher.Flush()
				return nil
			})

			if streamErr == nil || headersWritten {
				break
			}
			if ctx.Err() != nil {
				break
			}
		}

		if streamErr != nil && !headersWritten {
			writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("chat stream failed on backend: %v", streamErr), "api_error", "inference_error")
			return
		}

		if headersWritten && !doneSent {
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
		return
	}

	// Non-streaming completion with role fallbacks
	var resp *backend.ChatResponse
	var lastErr error
	var chosenTag string

	for _, tag := range candidateTags {
		resp, lastErr = s.cfg.Adapter.Chat(ctx, backend.ChatRequest{
			Model:    tag,
			Messages: backendMsgs,
			Options:  chatOpts,
			Think:    &thinkFalse,
		})
		if lastErr == nil {
			chosenTag = tag
			break
		}
		if ctx.Err() != nil {
			break
		}
	}

	if lastErr != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("chat completion failed on backend: %v", lastErr), "api_error", "inference_error")
		return
	}

	apiResp := chatResponse{
		ID:      generateID("chatcmpl"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   chosenTag,
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

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	var req tokenCountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid json payload: %v", err), "invalid_request_error", "bad_request")
		return
	}

	if strings.TrimSpace(req.Model) == "" {
		writeAPIError(w, http.StatusBadRequest, "model field is required", "invalid_request_error", "bad_request")
		return
	}

	if len(req.Messages) == 0 && strings.TrimSpace(req.Text) == "" {
		writeAPIError(w, http.StatusBadRequest, "either messages or text must be provided", "invalid_request_error", "bad_request")
		return
	}

	backendName := ""
	if s.cfg.Adapter != nil {
		backendName = s.cfg.Adapter.Name()
	}
	target := ResolveModel(req.Model, s.cfg.UserConfig, s.cfg.Catalog, backendName)

	tokens := estimateTokens(req.Text, req.Messages)
	maxCtx := 0
	if target.CatalogModel != nil && target.CatalogModel.MaxContext > 0 {
		maxCtx = target.CatalogModel.MaxContext
	}

	fits := true
	if maxCtx > 0 && tokens > maxCtx {
		fits = false
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenCountResponse{
		Model:      target.BackendTag,
		Role:       target.Role,
		Tokens:     tokens,
		MaxContext: maxCtx,
		Fits:       fits,
	})
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

	if s.cfg.Residency != nil {
		rel, err := s.cfg.Residency.Acquire(ctx, target.BackendTag, 0)
		if err != nil {
			if errors.Is(err, residency.ErrMemoryExceeded) {
				writeAPIError(w, http.StatusBadRequest,
					fmt.Sprintf("model %s exceeds available memory budget", target.BackendTag),
					"invalid_request_error", "memory_budget_exceeded")
				return
			}
			if errors.Is(err, residency.ErrQueueTimeout) {
				writeAPIError(w, http.StatusTooManyRequests,
					"request queue timeout: server concurrency capacity exceeded",
					"api_error", "queue_timeout")
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			writeAPIError(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to acquire model lease: %v", err),
				"api_error", "residency_error")
			return
		}
		defer rel()
	}

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

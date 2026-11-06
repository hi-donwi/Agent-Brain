package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/hw"
	"github.com/hi-donwi/Agent-Brain/internal/residency"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
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
	TraceStore   *trace.Store
	Version      string
	AccountPool  *router.AccountPool
	TokenSaver   bool
}

// Server is the OpenAI-compatible loopback HTTP server.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	listener   net.Listener
	proxy      *router.ProxyHandler
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
		maxConcurrency := 1
		if cfg.UserConfig != nil && cfg.UserConfig.Inference.MaxConcurrency > 0 {
			maxConcurrency = cfg.UserConfig.Inference.MaxConcurrency
		}
		cfg.Residency = residency.New(residency.Config{
			TotalBudget:    budget,
			MaxConcurrency: maxConcurrency,
			QueueTimeout:   60 * time.Second,
			Catalog:        cfg.Catalog,
			Adapter:        cfg.Adapter,
		})
	}

	mux := http.NewServeMux()
	s := &Server{cfg: cfg}

	if cfg.AccountPool != nil {
		s.proxy = router.NewProxyHandler(cfg.AccountPool, cfg.TokenSaver)
	}

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/accounts", s.handleAccounts)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("POST /v1beta/", s.handleGeminiGenerateContent)
	mux.HandleFunc("GET /v1beta/models", s.handleGeminiModelMetadata)
	mux.HandleFunc("GET /v1beta/models/", s.handleGeminiModelMetadata)
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
	if s.cfg.AccountPool != nil {
		data["accounts_count"] = len(s.cfg.AccountPool.ListAccounts())
		data["token_saver"] = s.cfg.TokenSaver
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
	if accountSelection(r) == "" && s.cfg.Adapter != nil {
		if s.cfg.UserConfig != nil && s.cfg.UserConfig.Aliases != nil {
			for role := range s.cfg.UserConfig.Aliases {
				models = append(models, openAIModel{
					ID:      role,
					Object:  "model",
					Created: now,
					OwnedBy: "brain",
				})
			}
		} else {
			for _, role := range []string{"chat", "code", "embed"} {
				models = append(models, openAIModel{
					ID:      role,
					Object:  "model",
					Created: now,
					OwnedBy: "brain",
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

	}
	// 3. Add accounts and supported cloud models from router pool
	if s.cfg.AccountPool != nil {
		for _, acc := range s.cfg.AccountPool.ListAccounts() {
			if !acc.Enabled || acc.Auth == router.AuthNative {
				continue
			}
			if selected := accountSelection(r); selected != "" {
				a, err := s.poolAccount(selected)
				if err != nil || a.ID != acc.ID {
					continue
				}
			}
			for _, model := range acc.Models {
				models = append(models, openAIModel{
					ID:      acc.Provider + "/" + model,
					Object:  "model",
					Created: now,
					OwnedBy: acc.Provider,
				})
			}
		}
	}

	unique := make([]openAIModel, 0, len(models))
	seen := make(map[string]bool)
	for _, m := range models {
		if !seen[m.ID] {
			unique = append(unique, m)
			seen[m.ID] = true
		}
	}
	models = unique
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openAIModelList{
		Object: "list",
		Data:   models,
	})
}

// Chat completions request and response representations.
type chatMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []backend.ToolCall `json:"tool_calls,omitempty"`
}

func (m *chatMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role       string             `json:"role"`
		Content    json.RawMessage    `json:"content"`
		ToolCallID string             `json:"tool_call_id"`
		ToolCalls  []backend.ToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	content, err := flattenMessageContent(raw.Content)
	if err != nil {
		return err
	}
	m.Role = raw.Role
	if m.Role == "developer" {
		m.Role = "system"
	}
	m.Content = content
	m.ToolCallID = raw.ToolCallID
	m.ToolCalls = raw.ToolCalls
	return nil
}

func (m chatMessage) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"role":    m.Role,
		"content": m.Content,
	}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]map[string]any, len(m.ToolCalls))
		for i, call := range m.ToolCalls {
			id := call.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", i+1)
			}
			kind := call.Type
			if kind == "" {
				kind = "function"
			}
			calls[i] = map[string]any{
				"id":   id,
				"type": kind,
				"function": map[string]string{
					"name":      call.Function.Name,
					"arguments": toolArgumentsString(call.Function.Arguments),
				},
			}
		}
		out["tool_calls"] = calls
	}
	return json.Marshal(out)
}

func flattenMessageContent(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("message content must be a string or array of content parts")
	}
	var b strings.Builder
	for _, part := range parts {
		piece, supported, err := flattenContentPart(part)
		if err != nil {
			return "", err
		}
		if !supported {
			return "", fmt.Errorf("non-text content is not supported by the local chat adapter")
		}
		if piece == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(piece)
	}
	return b.String(), nil
}

func normalizeClientModel(model string) string {
	model = strings.TrimSpace(model)
	for _, prefix := range []string{"openai/", "ollama/"} {
		if strings.HasPrefix(model, prefix) {
			return strings.TrimPrefix(model, prefix)
		}
	}
	return model
}

func objectArguments(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return []byte("{}")
	}
	if raw[0] == '{' || raw[0] == '[' {
		return raw
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil && json.Valid([]byte(encoded)) {
		return []byte(encoded)
	}
	return raw
}

func toolArgumentsString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "{}"
	}
	if raw[0] == '"' {
		var encoded string
		if json.Unmarshal(raw, &encoded) == nil {
			return encoded
		}
	}
	return string(raw)
}

func finishReason(calls []backend.ToolCall, reason string) string {
	if reason == "length" {
		return "length"
	}
	if len(calls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func streamToolCalls(calls []backend.ToolCall) []map[string]any {
	out := make([]map[string]any, len(calls))
	for i, call := range calls {
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		out[i] = map[string]any{
			"index": i,
			"id":    id,
			"type":  "function",
			"function": map[string]string{
				"name":      call.Function.Name,
				"arguments": toolArgumentsString(call.Function.Arguments),
			},
		}
	}
	return out
}

func flattenContentPart(raw json.RawMessage) (string, bool, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, true, nil
	}
	var part struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &part); err != nil {
		return "", false, fmt.Errorf("invalid message content part")
	}
	switch part.Type {
	case "", "text", "input_text", "output_text":
		if part.Text != "" {
			return part.Text, true, nil
		}
		return part.Content, true, nil
	default:
		return "", false, nil
	}
}

type stopList []string

func (s *stopList) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Temperature         *float64        `json:"temperature,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                stopList        `json:"stop,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Logprobs            bool            `json:"logprobs,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Seed                *int            `json:"seed,omitempty"`
	StreamOptions       struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Stream bool `json:"stream,omitempty"`
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
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
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
		if len(m.ToolCalls) > 0 {
			encoded, _ := json.Marshal(m.ToolCalls)
			totalChars += len(encoded)
		}
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
	startTime := time.Now()
	sessionID := r.Header.Get("X-Session-Id")
	if sessionID == "" {
		sessionID = r.Header.Get("X-Request-Id")
	}
	if sessionID == "" {
		sessionID = "default"
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "failed to read request body", "invalid_request_error", "bad_request")
		return
	}

	var req chatRequest
	if s.tryProxy(w, r, body, router.ProtocolChat, "") {
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid json payload: %v", err), "invalid_request_error", "bad_request")
		return
	}
	req.Model = normalizeClientModel(req.Model)

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
	format, err := validateLocalChat(&req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "unsupported_or_invalid_parameter")
		return
	}

	// Resolve requested model/role
	target := ResolveModel(req.Model, s.cfg.UserConfig, s.cfg.Catalog, s.cfg.Adapter.Name())

	// Context guard
	numCtx := localContextLimit(target, s.cfg.UserConfig)
	maxOutput := 2048
	if s.cfg.UserConfig != nil {
		limits := s.cfg.UserConfig.Inference
		if limits.MaxOutputTokens > 0 {
			maxOutput = limits.MaxOutputTokens
		}
	}
	backendMsgs := make([]backend.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		calls := make([]backend.ToolCall, len(m.ToolCalls))
		for j, call := range m.ToolCalls {
			call.Function.Arguments = objectArguments(call.Function.Arguments)
			calls[j] = call
		}
		backendMsgs[i] = backend.ChatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  calls,
			ToolCallID: m.ToolCallID,
		}
	}

	predict := req.MaxTokens
	if req.MaxCompletionTokens != nil {
		predict = req.MaxCompletionTokens
	}
	output := maxOutput
	if predict != nil {
		output = *predict
	}
	estimated := estimateTokens(string(req.Tools), req.Messages)
	if estimated+output > numCtx {
		writeAPIError(w, http.StatusBadRequest,
			fmt.Sprintf("estimated prompt including tools (%d) plus output reserve (%d) exceeds runtime context (%d)", estimated, output, numCtx),
			"invalid_request_error", "context_length_exceeded")
		return
	}
	if output > maxOutput {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("requested output exceeds configured max_output_tokens (%d)", maxOutput), "invalid_request_error", "output_limit_exceeded")
		return
	}
	chatOpts := &backend.ChatOptions{NumCtx: numCtx, Stop: req.Stop, NumPredict: output, TopP: req.TopP, Seed: req.Seed}
	if req.Temperature != nil {
		chatOpts.Temperature = *req.Temperature
		chatOpts.TemperatureSet = true
	}

	candidateTags := []string{target.BackendTag}
	if len(req.Tools) == 0 {
		candidateTags = append(candidateTags, target.Fallbacks...)
	}
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
		var toolIndex int
		var sawToolCalls bool

		for _, tag := range candidateTags {
			isFirstChunk = true
			var streamAcc strings.Builder
			_, streamErr = s.cfg.Adapter.ChatStream(ctx, backend.ChatRequest{
				Model:     tag,
				Messages:  backendMsgs,
				Options:   chatOpts,
				Think:     &thinkFalse,
				Tools:     req.Tools,
				Format:    format,
				KeepAlive: "-1",
			}, func(chunk backend.ChatChunk) error {
				if !headersWritten {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(http.StatusOK)
					headersWritten = true
				}
				if chunk.Content != "" || len(chunk.ToolCalls) > 0 || isFirstChunk {
					role := ""
					if isFirstChunk {
						role = "assistant"
						isFirstChunk = false
					}
					delta := chatChunkDelta{Role: role, Content: chunk.Content}
					for _, call := range chunk.ToolCalls {
						if call.ID == "" {
							call.ID = generateID("call")
						}
						item := streamToolCalls([]backend.ToolCall{call})[0]
						item["index"] = toolIndex
						toolIndex++
						delta.ToolCalls = append(delta.ToolCalls, item)
						sawToolCalls = true
					}
					data, err := json.Marshal(chatStreamChunk{
						ID: streamID, Object: "chat.completion.chunk", Created: created, Model: tag,
						Choices: []chatChunkChoice{{Index: 0, Delta: delta}},
					})
					if err != nil {
						return err
					}
					if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
						return err
					}
					flusher.Flush()
				}
				streamAcc.WriteString(chunk.Content)

				if chunk.Done {
					doneSent = true
					if s.cfg.TraceStore != nil {
						var msgs []trace.Message
						for _, m := range req.Messages {
							msgs = append(msgs, trace.Message{Role: m.Role, Content: m.Content})
						}
						_ = s.cfg.TraceStore.WriteRecord(&trace.Record{
							Version:      trace.SchemaVersion,
							SessionID:    sessionID,
							Model:        tag,
							Role:         target.Role,
							Backend:      s.cfg.Adapter.Name(),
							Messages:     msgs,
							Response:     streamAcc.String(),
							DurationMs:   time.Since(startTime).Milliseconds(),
							PromptTokens: chunk.PromptEvalCount,
							EvalTokens:   chunk.EvalCount,
						})
					}
					stopReason := finishReason(nil, chunk.DoneReason)
					if sawToolCalls && stopReason != "length" {
						stopReason = "tool_calls"
					}
					finalChoice := chatChunkChoice{
						Index:        0,
						Delta:        chatChunkDelta{},
						FinishReason: &stopReason,
					}
					var usage *completionUsage
					if req.StreamOptions.IncludeUsage {
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
					}
					data, err := json.Marshal(finalPayload)
					if err == nil {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					}
					if usage != nil {
						usageData, _ := json.Marshal(chatStreamChunk{ID: streamID, Object: "chat.completion.chunk", Created: created, Model: tag, Choices: []chatChunkChoice{}, Usage: usage})
						_, _ = fmt.Fprintf(w, "data: %s\n\n", usageData)
					}
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return nil
				}
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
			if s.cfg.TraceStore != nil {
				var msgs []trace.Message
				for _, m := range req.Messages {
					msgs = append(msgs, trace.Message{Role: m.Role, Content: m.Content})
				}
				_ = s.cfg.TraceStore.WriteRecord(&trace.Record{
					Version:    trace.SchemaVersion,
					SessionID:  sessionID,
					Model:      req.Model,
					Role:       target.Role,
					Backend:    s.cfg.Adapter.Name(),
					Messages:   msgs,
					DurationMs: time.Since(startTime).Milliseconds(),
					Error:      streamErr.Error(),
				})
			}
			writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("chat stream failed on backend: %v", streamErr), "api_error", "inference_error")
			return
		}

		if headersWritten && !doneSent {
			data, _ := json.Marshal(map[string]any{"error": map[string]string{"message": "backend stream interrupted", "type": "api_error", "code": "inference_error"}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
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
			Model:     tag,
			Messages:  backendMsgs,
			Options:   chatOpts,
			Think:     &thinkFalse,
			Tools:     req.Tools,
			Format:    format,
			KeepAlive: "-1",
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
		if s.cfg.TraceStore != nil {
			var msgs []trace.Message
			for _, m := range req.Messages {
				msgs = append(msgs, trace.Message{Role: m.Role, Content: m.Content})
			}
			_ = s.cfg.TraceStore.WriteRecord(&trace.Record{
				Version:    trace.SchemaVersion,
				SessionID:  sessionID,
				Model:      req.Model,
				Role:       target.Role,
				Backend:    s.cfg.Adapter.Name(),
				Messages:   msgs,
				DurationMs: time.Since(startTime).Milliseconds(),
				Error:      lastErr.Error(),
			})
		}
		writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("chat completion failed on backend: %v", lastErr), "api_error", "inference_error")
		return
	}

	if s.cfg.TraceStore != nil {
		var msgs []trace.Message
		for _, m := range req.Messages {
			msgs = append(msgs, trace.Message{Role: m.Role, Content: m.Content})
		}
		_ = s.cfg.TraceStore.WriteRecord(&trace.Record{
			Version:      trace.SchemaVersion,
			SessionID:    sessionID,
			Model:        chosenTag,
			Role:         target.Role,
			Backend:      s.cfg.Adapter.Name(),
			Messages:     msgs,
			Response:     resp.Message.Content,
			DurationMs:   time.Since(startTime).Milliseconds(),
			PromptTokens: resp.PromptEvalCount,
			EvalTokens:   resp.EvalCount,
		})
	}

	for i := range resp.Message.ToolCalls {
		if resp.Message.ToolCalls[i].ID == "" {
			resp.Message.ToolCalls[i].ID = generateID("call")
		}
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
					Role:      resp.Message.Role,
					Content:   resp.Message.Content,
					ToolCalls: resp.Message.ToolCalls,
				},
				FinishReason: finishReason(resp.Message.ToolCalls, resp.DoneReason),
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
	maxCtx := localContextLimit(target, s.cfg.UserConfig)

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
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeAPIError(w, 400, "cannot read embeddings request", "invalid_request_error", "bad_request")
		return
	}
	if s.tryProxy(w, r, body, router.ProtocolEmbeddings, "") {
		return
	}
	var req embedRequest
	if err := json.Unmarshal(body, &req); err != nil {
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

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.cfg.AccountPool == nil {
		_ = json.NewEncoder(w).Encode([]router.Account{})
		return
	}
	accounts := s.cfg.AccountPool.ListAccounts()
	if selected := accountSelection(r); selected != "" {
		a, err := s.poolAccount(selected)
		if err != nil {
			accounts = []router.Account{}
		} else {
			accounts = []router.Account{a}
		}
	}
	_ = json.NewEncoder(w).Encode(accounts)
}

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	s.handleProviderProtocol(w, r, router.ProtocolMessages, router.ProviderAnthropic)
}

func (s *Server) handleGeminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	s.handleProviderProtocol(w, r, router.ProtocolGemini, router.ProviderGemini)
}

func (s *Server) handleProviderProtocol(w http.ResponseWriter, r *http.Request, protocol, provider string) {
	body, err := io.ReadAll(r.Body)
	if err != nil || !json.Valid(body) {
		writeAPIError(w, 400, "invalid request body", "invalid_request_error", "bad_request")
		return
	}
	if s.tryProxy(w, r, body, protocol, provider) {
		return
	}
	writeAPIError(w, 503, "no eligible provider account is configured", "api_error", "account_unavailable")
}

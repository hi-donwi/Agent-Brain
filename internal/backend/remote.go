package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RemoteAdapter connects to a remote lokal gateway or OpenAI-compatible server.
type RemoteAdapter struct {
	name       string
	endpoint   string
	token      string
	httpClient *http.Client
}

// NewRemote creates a new RemoteAdapter for connecting to a remote lokal gateway.
func NewRemote(endpoint, token string, client *http.Client) *RemoteAdapter {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	endpoint = strings.TrimRight(endpoint, "/")
	return &RemoteAdapter{
		name:       "remote",
		endpoint:   endpoint,
		token:      token,
		httpClient: client,
	}
}

// Name returns the backend identifier.
func (r *RemoteAdapter) Name() string {
	if r.name != "" {
		return r.name
	}
	return "remote"
}

// Endpoint returns the remote gateway base URL.
func (r *RemoteAdapter) Endpoint() string {
	return r.endpoint
}

// SetName allows setting an engine alias (e.g. "gateway", "llamacpp", "vllm", "lmstudio").
func (r *RemoteAdapter) SetName(name string) {
	r.name = name
}

func (r *RemoteAdapter) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := r.endpoint + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request to %s: %w", url, err)
	}
	req.Header.Set("User-Agent", "lokal-client/0.1.0")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	return req, nil
}

// HealthResponse models the status response returned by GET /healthz.
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	Residency struct {
		BudgetBytes    uint64 `json:"budget_bytes"`
		UsedBytes      uint64 `json:"used_bytes"`
		ResidentModels []struct {
			Model     string `json:"model"`
			SizeBytes uint64 `json:"size_bytes"`
		} `json:"resident_models"`
	} `json:"residency,omitempty"`
}

// Ping checks if the remote gateway or server is reachable.
func (r *RemoteAdapter) Ping(ctx context.Context) error {
	// Try /healthz first (lokal gateway standard)
	req, err := r.newRequest(ctx, http.MethodGet, "/healthz", nil)
	if err == nil {
		resp, err := r.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}

	// Fall back to /v1/models (standard OpenAI healthcheck)
	req2, err := r.newRequest(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return fmt.Errorf("ping %s: %w", r.endpoint, err)
	}
	resp2, err := r.httpClient.Do(req2)
	if err != nil {
		return fmt.Errorf("ping %s: %w", r.endpoint, err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
		return nil
	}
	if resp2.StatusCode == http.StatusUnauthorized || resp2.StatusCode == http.StatusForbidden {
		return errors.New("authentication failed: invalid or missing bearer token")
	}
	return fmt.Errorf("ping %s returned HTTP %d", r.endpoint, resp2.StatusCode)
}

// Version queries the remote service version.
func (r *RemoteAdapter) Version(ctx context.Context) (string, error) {
	req, err := r.newRequest(ctx, http.MethodGet, "/healthz", nil)
	if err == nil {
		resp, err := r.httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var hr HealthResponse
				if err := json.NewDecoder(resp.Body).Decode(&hr); err == nil && hr.Version != "" {
					return hr.Version, nil
				}
			}
		}
	}
	return "remote-gateway", nil
}

type openAIModelList struct {
	Object string `json:"object"`
	Data   []struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// ListModels queries /v1/models on the remote gateway.
func (r *RemoteAdapter) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := r.newRequest(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query /v1/models on %s: %w", r.endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query /v1/models returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var list openAIModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode /v1/models response: %w", err)
	}

	models := make([]ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		var modTime time.Time
		if m.Created > 0 {
			modTime = time.Unix(m.Created, 0)
		}
		models = append(models, ModelInfo{
			Name:       m.ID,
			ModifiedAt: modTime,
		})
	}
	return models, nil
}

// ShowModel returns capabilities and limits for a model.
func (r *RemoteAdapter) ShowModel(ctx context.Context, model string) (ModelDetails, error) {
	models, err := r.ListModels(ctx)
	if err != nil {
		return ModelDetails{}, err
	}

	for _, m := range models {
		if m.Name == model {
			return ModelDetails{
				Capabilities:    []string{"tools", "thinking"},
				ContextLength:   32768,
				EmbeddingLength: 768,
			}, nil
		}
	}

	// Model might still exist as an unlisted tag or alias
	return ModelDetails{
		Capabilities:    []string{"tools"},
		ContextLength:   8192,
		EmbeddingLength: 768,
	}, nil
}

// RunningModels queries /healthz for active resident models if supported.
func (r *RemoteAdapter) RunningModels(ctx context.Context) ([]ProcessInfo, error) {
	req, err := r.newRequest(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, nil // Health endpoint not reachable, treat as empty
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var hr HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return nil, nil
	}

	var procs []ProcessInfo
	for _, rm := range hr.Residency.ResidentModels {
		procs = append(procs, ProcessInfo{
			Name: rm.Model,
			Size: rm.SizeBytes,
		})
	}
	return procs, nil
}

type openAIChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat performs a unary chat completion call.
func (r *RemoteAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()

	oaiReq := openAIChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   false,
	}
	if req.Options != nil {
		oaiReq.Temperature = req.Options.Temperature
	}

	payload, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := r.newRequest(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat completion request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("chat completion returned HTTP %d: %s", httpResp.StatusCode, string(body))
	}

	var oaiResp openAIChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("decode chat completion response: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, errors.New("empty choices array in chat completion response")
	}

	elapsed := time.Since(start)
	tps := 0.0
	if elapsed.Seconds() > 0 && oaiResp.Usage.CompletionTokens > 0 {
		tps = float64(oaiResp.Usage.CompletionTokens) / elapsed.Seconds()
	}

	return &ChatResponse{
		Model:           oaiResp.Model,
		Message:         oaiResp.Choices[0].Message,
		TotalDuration:   elapsed,
		PromptEvalCount: oaiResp.Usage.PromptTokens,
		EvalCount:       oaiResp.Usage.CompletionTokens,
		TokensPerSecond: tps,
	}, nil
}

type openAIChatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role,omitempty"`
			Content          string `json:"content,omitempty"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// ChatStream performs a streaming chat completion over SSE.
func (r *RemoteAdapter) ChatStream(ctx context.Context, req ChatRequest, onChunk func(ChatChunk) error) (*ChatResponse, error) {
	start := time.Now()

	oaiReq := openAIChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true,
	}
	if req.Options != nil {
		oaiReq.Temperature = req.Options.Temperature
	}

	payload, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat stream request: %w", err)
	}

	httpReq, err := r.newRequest(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat stream request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("chat stream returned HTTP %d: %s", httpResp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(httpResp.Body)
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	var lastModel string
	promptTokens := 0
	completionTokens := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // Keepalive comment or blank line
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openAIChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Model != "" {
			lastModel = chunk.Model
		}

		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			completionTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			delta := c.Delta
			content := delta.Content
			reasoning := delta.ReasoningContent
			fullContent.WriteString(content)
			fullReasoning.WriteString(reasoning)

			done := c.FinishReason != nil && *c.FinishReason != ""

			if onChunk != nil {
				if err := onChunk(ChatChunk{
					Role:            delta.Role,
					Content:         content,
					Reasoning:       reasoning,
					Done:            done,
					PromptEvalCount: promptTokens,
					EvalCount:       completionTokens,
				}); err != nil {
					return nil, fmt.Errorf("onChunk callback failed: %w", err)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read chat stream SSE: %w", err)
	}

	elapsed := time.Since(start)
	tps := 0.0
	if elapsed.Seconds() > 0 && completionTokens > 0 {
		tps = float64(completionTokens) / elapsed.Seconds()
	}

	return &ChatResponse{
		Model: lastModel,
		Message: ChatMessage{
			Role:      "assistant",
			Content:   fullContent.String(),
			Reasoning: fullReasoning.String(),
		},
		TotalDuration:   elapsed,
		PromptEvalCount: promptTokens,
		EvalCount:       completionTokens,
		TokensPerSecond: tps,
	}, nil
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates embeddings using /v1/embeddings on the remote endpoint.
func (r *RemoteAdapter) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	start := time.Now()

	oaiReq := openAIEmbedRequest{
		Model: req.Model,
		Input: req.Input,
	}

	payload, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	httpReq, err := r.newRequest(ctx, http.MethodPost, "/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("embeddings returned HTTP %d: %s", httpResp.StatusCode, string(body))
	}

	var oaiResp openAIEmbedResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}

	embeddings := make([][]float32, len(oaiResp.Data))
	for _, item := range oaiResp.Data {
		if item.Index >= 0 && item.Index < len(embeddings) {
			embeddings[item.Index] = item.Embedding
		} else {
			embeddings = append(embeddings, item.Embedding)
		}
	}

	return &EmbedResponse{
		Model:           oaiResp.Model,
		Embeddings:      embeddings,
		PromptEvalCount: oaiResp.Usage.PromptTokens,
		TotalDuration:   time.Since(start),
	}, nil
}

// Pull returns an informational error since remote models must be pulled on the remote host.
func (r *RemoteAdapter) Pull(_ context.Context, model string, _ func(Progress)) error {
	return fmt.Errorf("model pull not supported on remote gateway; run 'lokal models pull %s' on the remote host", model)
}

// Delete returns an informational error since remote models must be managed on the remote host.
func (r *RemoteAdapter) Delete(_ context.Context, model string) error {
	return fmt.Errorf("model deletion not supported on remote gateway; manage models on the remote host")
}

// Unload is a no-op on remote gateway as residency is managed automatically by the remote server.
func (r *RemoteAdapter) Unload(_ context.Context, _ string) error {
	return nil
}

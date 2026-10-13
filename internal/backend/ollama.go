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

// DefaultOllamaEndpoint is the default loopback address for the native Ollama server.
const DefaultOllamaEndpoint = "http://127.0.0.1:11434"

// OllamaAdapter communicates with Ollama's native HTTP API.
type OllamaAdapter struct {
	endpoint string
	client   *http.Client
}

// NewOllama creates an adapter for an Ollama backend.
func NewOllama(endpoint string, client *http.Client) *OllamaAdapter {
	ep := strings.TrimRight(endpoint, "/")
	if ep == "" {
		ep = DefaultOllamaEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OllamaAdapter{
		endpoint: ep,
		client:   client,
	}
}

func (o *OllamaAdapter) Name() string {
	return "ollama"
}

func (o *OllamaAdapter) Endpoint() string {
	return o.endpoint
}

// Ping checks if Ollama responds to /api/version.
func (o *OllamaAdapter) Ping(ctx context.Context) error {
	_, err := o.Version(ctx)
	return err
}

// Version returns the engine version from /api/version.
func (o *OllamaAdapter) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint+"/api/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from /api/version", resp.StatusCode)
	}

	var data struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("decode /api/version: %w", err)
	}
	return data.Version, nil
}

// ListModels retrieves installed models from /api/tags.
func (o *OllamaAdapter) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models from ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from /api/tags", resp.StatusCode)
	}

	var data struct {
		Models []struct {
			Name       string    `json:"name"`
			Size       uint64    `json:"size"`
			Digest     string    `json:"digest"`
			ModifiedAt time.Time `json:"modified_at"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode /api/tags: %w", err)
	}

	res := make([]ModelInfo, len(data.Models))
	for i, m := range data.Models {
		res[i] = ModelInfo{
			Name:       m.Name,
			Size:       m.Size,
			Digest:     m.Digest,
			ModifiedAt: m.ModifiedAt,
		}
	}
	return res, nil
}

// ShowModel queries /api/show for capabilities and context limits.
func (o *OllamaAdapter) ShowModel(ctx context.Context, model string) (ModelDetails, error) {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/show", bytes.NewReader(body))
	if err != nil {
		return ModelDetails{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return ModelDetails{}, fmt.Errorf("show model %s: %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ModelDetails{}, fmt.Errorf("show model %s returned status %d", model, resp.StatusCode)
	}

	var raw struct {
		Capabilities []string               `json:"capabilities"`
		Ctx          map[string]int         `json:"ctx"`
		Emb          map[string]int         `json:"emb"`
		ModelInfo    map[string]interface{} `json:"model_info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ModelDetails{}, fmt.Errorf("decode /api/show: %w", err)
	}

	details := ModelDetails{
		Capabilities: raw.Capabilities,
	}

	// Extract context length from ctx map or model_info
	for k, v := range raw.Ctx {
		if strings.Contains(k, "context_length") {
			details.ContextLength = v
			break
		}
	}
	if details.ContextLength == 0 && raw.ModelInfo != nil {
		for k, v := range raw.ModelInfo {
			if strings.Contains(k, "context_length") {
				if num, ok := v.(float64); ok {
					details.ContextLength = int(num)
					break
				}
			}
		}
	}

	// Extract embedding length from emb map or model_info
	for k, v := range raw.Emb {
		if strings.Contains(k, "embedding_length") {
			details.EmbeddingLength = v
			break
		}
	}
	if details.EmbeddingLength == 0 && raw.ModelInfo != nil {
		for k, v := range raw.ModelInfo {
			if strings.Contains(k, "embedding_length") {
				if num, ok := v.(float64); ok {
					details.EmbeddingLength = int(num)
					break
				}
			}
		}
	}

	return details, nil
}

// RunningModels queries /api/ps for models resident in memory.
func (o *OllamaAdapter) RunningModels(ctx context.Context) ([]ProcessInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query running models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from /api/ps", resp.StatusCode)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read /api/ps response: %w", err)
	}

	// Ollama /api/ps can return either a top-level JSON array or {"models": [...]}
	type psItem struct {
		Name          string `json:"name"`
		Size          uint64 `json:"size"`
		ContextLength int    `json:"context_length"`
	}

	var items []psItem
	if err := json.Unmarshal(rawBytes, &items); err != nil {
		var wrapper struct {
			Models []psItem `json:"models"`
		}
		if errWrap := json.Unmarshal(rawBytes, &wrapper); errWrap != nil {
			return nil, fmt.Errorf("decode /api/ps: %w", err)
		}
		items = wrapper.Models
	}

	res := make([]ProcessInfo, len(items))
	for i, item := range items {
		res[i] = ProcessInfo{
			Name:          item.Name,
			Size:          item.Size,
			ContextLength: item.ContextLength,
		}
	}
	return res, nil
}

// Chat performs a single completion using native /api/chat.
func (o *OllamaAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if req.Options != nil {
		opts := make(map[string]interface{})
		if req.Options.NumCtx > 0 {
			opts["num_ctx"] = req.Options.NumCtx
		}
		if req.Options.Temperature > 0 {
			opts["temperature"] = req.Options.Temperature
		}
		payload["options"] = opts
	}
	if req.Think != nil {
		payload["think"] = *req.Think
	}
	if req.KeepAlive != "" {
		payload["keep_alive"] = req.KeepAlive
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat request to ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chat returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var raw struct {
		Model      string      `json:"model"`
		Message    ChatMessage `json:"message"`
		Done       bool        `json:"done"`
		TotalDur   int64       `json:"total_duration"`
		LoadDur    int64       `json:"load_duration"`
		PromptEval int         `json:"prompt_eval_count"`
		PromptDur  int64       `json:"prompt_eval_duration"`
		EvalCount  int         `json:"eval_count"`
		EvalDur    int64       `json:"eval_duration"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}

	evalDuration := time.Duration(raw.EvalDur)
	var tokPerSec float64
	if evalDuration > 0 && raw.EvalCount > 0 {
		tokPerSec = float64(raw.EvalCount) / evalDuration.Seconds()
	}

	return &ChatResponse{
		Model:              raw.Model,
		Message:            raw.Message,
		TotalDuration:      time.Duration(raw.TotalDur),
		LoadDuration:       time.Duration(raw.LoadDur),
		PromptEvalCount:    raw.PromptEval,
		PromptEvalDuration: time.Duration(raw.PromptDur),
		EvalCount:          raw.EvalCount,
		EvalDuration:       evalDuration,
		TokensPerSecond:    tokPerSec,
	}, nil
}

// Embed generates embeddings via native /api/embed with explicit truncation control.
func (o *OllamaAdapter) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	payload := map[string]interface{}{
		"model": req.Model,
		"input": req.Input,
	}
	if req.Truncate != nil {
		payload["truncate"] = *req.Truncate
	}
	if req.KeepAlive != "" {
		payload["keep_alive"] = req.KeepAlive
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embed request to ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errObj struct {
			Error string `json:"error"`
		}
		respBytes, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(respBytes, &errObj); err == nil && errObj.Error != "" {
			return nil, fmt.Errorf("embed error: %s", errObj.Error)
		}
		return nil, fmt.Errorf("embed returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var raw struct {
		Model      string      `json:"model"`
		Embeddings [][]float32 `json:"embeddings"`
		PromptEval int         `json:"prompt_eval_count"`
		TotalDur   int64       `json:"total_duration"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if raw.Error != "" {
		return nil, fmt.Errorf("embed error: %s", raw.Error)
	}

	return &EmbedResponse{
		Model:           raw.Model,
		Embeddings:      raw.Embeddings,
		PromptEvalCount: raw.PromptEval,
		TotalDuration:   time.Duration(raw.TotalDur),
	}, nil
}

// Pull downloads a model using streaming newline-delimited JSON.
func (o *OllamaAdapter) Pull(ctx context.Context, model string, onProgress func(Progress)) error {
	payload := map[string]interface{}{
		"model":  model,
		"stream": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode pull request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use transport directly or longer timeout for large pulls
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("pull model %s: %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p Progress
		if err := json.Unmarshal(line, &p); err != nil {
			continue
		}
		if onProgress != nil {
			onProgress(p)
		}
	}
	return scanner.Err()
}

// Unload immediately evicts a model from memory by setting keep_alive to 0.
func (o *OllamaAdapter) Unload(ctx context.Context, model string) error {
	if model == "" {
		return errors.New("model name cannot be empty")
	}
	payload := map[string]interface{}{
		"model":      model,
		"keep_alive": 0,
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("unload model %s: %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unload returned status %d: %s", resp.StatusCode, string(respBytes))
	}
	return nil
}

// Delete removes a model from the local backend store.
func (o *OllamaAdapter) Delete(ctx context.Context, model string) error {
	if model == "" {
		return errors.New("model name cannot be empty")
	}
	payload := map[string]interface{}{
		"model": model,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode delete request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, o.endpoint+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("delete model %s: %w", model, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("model %s not found", model)
	}
	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete returned status %d: %s", resp.StatusCode, string(respBytes))
	}
	return nil
}

// Package backend defines the interface for local inference engines (Ollama,
// llama.cpp, LM Studio, MLX, vLLM) and implements their HTTP adapters.
//
// Models are never run in-process (ADR-0001, ADR-0002); lokal orchestrates
// dedicated inference engines and communicates with them over loopback HTTP.
package backend

import (
	"context"
	"time"
)

// Adapter drives one inference backend over HTTP.
type Adapter interface {
	// Name identifies the backend family (e.g. "ollama", "llamacpp").
	Name() string

	// Endpoint returns the base URL of the backend server.
	Endpoint() string

	// Ping checks if the backend server is reachable and responsive.
	Ping(ctx context.Context) error

	// Version returns the backend engine version string.
	Version(ctx context.Context) (string, error)

	// ListModels returns all models currently installed in the backend.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// ShowModel retrieves metadata, capabilities, and context limits for a model.
	ShowModel(ctx context.Context, model string) (ModelDetails, error)

	// RunningModels returns models currently loaded in memory.
	RunningModels(ctx context.Context) ([]ProcessInfo, error)

	// Chat performs a single chat completion with explicit context budgeting.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Embed generates vector embeddings with explicit truncation control.
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)

	// Pull downloads a model from the registry, reporting progress callbacks.
	Pull(ctx context.Context, model string, onProgress func(Progress)) error

	// Delete removes a model from the local backend store.
	Delete(ctx context.Context, model string) error

	// Unload removes a model from memory immediately by setting keep_alive to 0.
	Unload(ctx context.Context, model string) error
}

// ModelInfo describes one installed model.
type ModelInfo struct {
	Name       string    `json:"name"`
	Size       uint64    `json:"size"`
	Digest     string    `json:"digest,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

// ModelDetails describes model limits and capabilities.
type ModelDetails struct {
	Capabilities    []string `json:"capabilities"`
	ContextLength   int      `json:"context_length"`
	EmbeddingLength int      `json:"embedding_length"`
}

// HasCapability checks whether a model reports a specific capability (e.g. "tools", "thinking", "embedding").
func (d ModelDetails) HasCapability(cap string) bool {
	for _, c := range d.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// ProcessInfo describes a model actively loaded in RAM or VRAM.
type ProcessInfo struct {
	Name          string `json:"name"`
	Size          uint64 `json:"size"`
	ContextLength int    `json:"context_length"`
}

// ChatMessage is one turn in a conversation.
type ChatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
}

// ChatOptions sets runtime inference parameters.
type ChatOptions struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

// ChatRequest configures a chat completion.
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Options   *ChatOptions  `json:"options,omitempty"`
	Think     *bool         `json:"think,omitempty"`
	KeepAlive string        `json:"keep_alive,omitempty"`
}

// ChatResponse holds the completed turn and generation timing.
type ChatResponse struct {
	Model              string        `json:"model"`
	Message            ChatMessage   `json:"message"`
	TotalDuration      time.Duration `json:"total_duration"`
	LoadDuration       time.Duration `json:"load_duration"`
	PromptEvalCount    int           `json:"prompt_eval_count"`
	PromptEvalDuration time.Duration `json:"prompt_eval_duration"`
	EvalCount          int           `json:"eval_count"`
	EvalDuration       time.Duration `json:"eval_duration"`
	TokensPerSecond    float64       `json:"tokens_per_second"`
}

// EmbedRequest requests vector embeddings for one or more inputs.
type EmbedRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	Truncate  *bool    `json:"truncate,omitempty"`
	KeepAlive string   `json:"keep_alive,omitempty"`
}

// EmbedResponse contains generated embeddings and prompt evaluation counts.
type EmbedResponse struct {
	Model           string        `json:"model"`
	Embeddings      [][]float32   `json:"embeddings"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	TotalDuration   time.Duration `json:"total_duration"`
}

// Progress reports download status during model pulls.
type Progress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     uint64 `json:"total,omitempty"`
	Completed uint64 `json:"completed,omitempty"`
}

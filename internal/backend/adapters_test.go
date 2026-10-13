package backend_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
)

func TestRemoteAdapter_Contract(t *testing.T) {
	mux := http.NewServeMux()

	// 1. GET /healthz
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-xyz" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"status": "ok",
			"version": "0.2.0-rc1",
			"residency": {
				"budget_bytes": 16000000000,
				"used_bytes": 4000000000,
				"resident_models": [
					{"model": "qwen2.5-coder-1.5b", "size_bytes": 2000000000}
				]
			}
		}`)
	})

	// 2. GET /v1/models
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-xyz" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"object": "list",
			"data": [
				{"id": "qwen2.5-coder-1.5b", "created": 1728800000, "owned_by": "lokal"},
				{"id": "nomic-embed-text", "created": 1728800001, "owned_by": "lokal"}
			]
		}`)
	})

	// 3. POST /v1/chat/completions (unary + streaming)
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-xyz" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Model    string                `json:"model"`
			Messages []backend.ChatMessage `json:"messages"`
			Stream   bool                  `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			fmt.Fprintf(w, "data: %s\n\n", `{"id":"1","model":"qwen2.5-coder-1.5b","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "}}]}`)
			flusher.Flush()

			fmt.Fprintf(w, "data: %s\n\n", `{"id":"2","model":"qwen2.5-coder-1.5b","choices":[{"index":0,"delta":{"content":"world!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
			flusher.Flush()

			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"id": "chatcmpl-123",
			"model": "qwen2.5-coder-1.5b",
			"choices": [
				{
					"index": 0,
					"message": {"role": "assistant", "content": "Hello from unary!"},
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 4,
				"total_tokens": 14
			}
		}`)
	})

	// 4. POST /v1/embeddings
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-xyz" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"object": "list",
			"data": [
				{"index": 0, "embedding": [0.12, -0.34, 0.56]}
			],
			"model": "nomic-embed-text",
			"usage": {"prompt_tokens": 4, "total_tokens": 4}
		}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	adapter := backend.NewRemote(server.URL, "test-token-xyz", server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Ping
	if err := adapter.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// 2. Version
	ver, err := adapter.Version(ctx)
	if err != nil {
		t.Fatalf("Version failed: %v", err)
	}
	if ver != "0.2.0-rc1" {
		t.Errorf("expected version 0.2.0-rc1, got %q", ver)
	}

	// 3. ListModels
	models, err := adapter.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "qwen2.5-coder-1.5b" {
		t.Errorf("expected model name qwen2.5-coder-1.5b, got %q", models[0].Name)
	}

	// 4. ShowModel
	details, err := adapter.ShowModel(ctx, "qwen2.5-coder-1.5b")
	if err != nil {
		t.Fatalf("ShowModel failed: %v", err)
	}
	if details.ContextLength <= 0 {
		t.Errorf("expected positive ContextLength, got %d", details.ContextLength)
	}

	// 5. RunningModels
	running, err := adapter.RunningModels(ctx)
	if err != nil {
		t.Fatalf("RunningModels failed: %v", err)
	}
	if len(running) != 1 {
		t.Fatalf("expected 1 running model, got %d", len(running))
	}
	if running[0].Name != "qwen2.5-coder-1.5b" {
		t.Errorf("expected running model qwen2.5-coder-1.5b, got %q", running[0].Name)
	}

	// 6. Chat unary
	chatResp, err := adapter.Chat(ctx, backend.ChatRequest{
		Model: "qwen2.5-coder-1.5b",
		Messages: []backend.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if chatResp.Message.Content != "Hello from unary!" {
		t.Errorf("unexpected content: %q", chatResp.Message.Content)
	}
	if chatResp.PromptEvalCount != 10 || chatResp.EvalCount != 4 {
		t.Errorf("expected 10 prompt tokens, 4 eval tokens; got %d, %d", chatResp.PromptEvalCount, chatResp.EvalCount)
	}

	// 7. ChatStream SSE
	var streamContent strings.Builder
	streamResp, err := adapter.ChatStream(ctx, backend.ChatRequest{
		Model: "qwen2.5-coder-1.5b",
		Messages: []backend.ChatMessage{
			{Role: "user", Content: "Stream hi"},
		},
	}, func(chunk backend.ChatChunk) error {
		streamContent.WriteString(chunk.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if streamContent.String() != "Hello world!" {
		t.Errorf("expected streamed content 'Hello world!', got %q", streamContent.String())
	}
	if streamResp.Message.Content != "Hello world!" {
		t.Errorf("expected final message 'Hello world!', got %q", streamResp.Message.Content)
	}

	// 8. Embed
	embedResp, err := adapter.Embed(ctx, backend.EmbedRequest{
		Model: "nomic-embed-text",
		Input: []string{"test document"},
	})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(embedResp.Embeddings) != 1 || len(embedResp.Embeddings[0]) != 3 {
		t.Fatalf("unexpected embeddings length: %v", embedResp.Embeddings)
	}

	// 9. Unload, Pull, Delete
	if err := adapter.Unload(ctx, "qwen2.5-coder-1.5b"); err != nil {
		t.Errorf("Unload expected nil, got %v", err)
	}
	if err := adapter.Pull(ctx, "any", nil); err == nil {
		t.Error("Pull on remote gateway should return error")
	}
	if err := adapter.Delete(ctx, "any"); err == nil {
		t.Error("Delete on remote gateway should return error")
	}
}

func TestExternalAdapters(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"object":"list","data":[{"id":"llama-model-1"}]}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. llama-server
	llama := backend.NewLlamaServer(server.URL, server.Client())
	if llama.Name() != "llamacpp" {
		t.Errorf("expected name llamacpp, got %q", llama.Name())
	}
	if err := llama.Ping(ctx); err != nil {
		t.Errorf("llama Ping failed: %v", err)
	}
	ver, err := llama.Version(ctx)
	if err != nil || ver != "llama-server" {
		t.Errorf("expected version llama-server, got %q (%v)", ver, err)
	}

	// 2. vLLM
	vllm := backend.NewVLLM(server.URL, "", server.Client())
	if vllm.Name() != "vllm" {
		t.Errorf("expected name vllm, got %q", vllm.Name())
	}
	if err := vllm.Ping(ctx); err != nil {
		t.Errorf("vllm Ping failed: %v", err)
	}

	// 3. LM Studio
	lms := backend.NewLMStudio(server.URL, server.Client())
	if lms.Name() != "lmstudio" {
		t.Errorf("expected name lmstudio, got %q", lms.Name())
	}
	if err := lms.Ping(ctx); err != nil {
		t.Errorf("lmstudio Ping failed: %v", err)
	}
}

func TestNewAdapter_Factory(t *testing.T) {
	tests := []struct {
		engine       string
		expectedName string
	}{
		{"", "ollama"},
		{"ollama", "ollama"},
		{"remote", "remote"},
		{"gateway", "remote"},
		{"llamacpp", "llamacpp"},
		{"llama-server", "llamacpp"},
		{"vllm", "vllm"},
		{"lmstudio", "lmstudio"},
	}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			ad, err := backend.NewAdapter(tt.engine, "http://127.0.0.1:9999", "dummy-token", nil)
			if err != nil {
				t.Fatalf("NewAdapter(%q) unexpected error: %v", tt.engine, err)
			}
			if ad.Name() != tt.expectedName {
				t.Errorf("expected Name %q, got %q", tt.expectedName, ad.Name())
			}
		})
	}

	// Unsupported engine
	_, err := backend.NewAdapter("unknown-engine", "http://127.0.0.1:9999", "", nil)
	if err == nil {
		t.Error("expected error for unsupported engine, got nil")
	}
}

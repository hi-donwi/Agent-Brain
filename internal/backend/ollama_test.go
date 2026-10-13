package backend_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
)

func TestOllama_Ping_and_Version(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"0.30.8"}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	if adapter.Name() != "ollama" {
		t.Errorf("expected name ollama, got %s", adapter.Name())
	}
	if adapter.Endpoint() != server.URL {
		t.Errorf("expected endpoint %s, got %s", server.URL, adapter.Endpoint())
	}

	ctx := context.Background()
	if err := adapter.Ping(ctx); err != nil {
		t.Fatalf("Ping() failed: %v", err)
	}

	ver, err := adapter.Version(ctx)
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if ver != "0.30.8" {
		t.Errorf("expected version 0.30.8, got %s", ver)
	}
}

func TestOllama_ListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"models": [
				{
					"name": "qwen3:0.6b",
					"size": 522000000,
					"digest": "sha256:123456",
					"modified_at": "2026-10-13T08:00:00Z"
				}
			]
		}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	models, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "qwen3:0.6b" || models[0].Size != 522000000 {
		t.Errorf("unexpected model info: %+v", models[0])
	}
}

func TestOllama_ShowModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req["model"] != "qwen3:0.6b" {
			http.Error(w, "model not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"capabilities": ["completion", "tools", "thinking"],
			"ctx": {"qwen3.context_length": 40960},
			"emb": {"qwen3.embedding_length": 1024}
		}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	details, err := adapter.ShowModel(context.Background(), "qwen3:0.6b")
	if err != nil {
		t.Fatalf("ShowModel() failed: %v", err)
	}
	if !details.HasCapability("tools") {
		t.Error("expected tools capability")
	}
	if !details.HasCapability("thinking") {
		t.Error("expected thinking capability")
	}
	if details.ContextLength != 40960 {
		t.Errorf("expected context length 40960, got %d", details.ContextLength)
	}
	if details.EmbeddingLength != 1024 {
		t.Errorf("expected embedding length 1024, got %d", details.EmbeddingLength)
	}
}

func TestOllama_RunningModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name": "qwen3:0.6b", "size": 797798563, "context_length": 4096},
			{"name": "all-minilm:latest", "size": 25459425, "context_length": 256}
		]`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	running, err := adapter.RunningModels(context.Background())
	if err != nil {
		t.Fatalf("RunningModels() failed: %v", err)
	}
	if len(running) != 2 {
		t.Fatalf("expected 2 running models, got %d", len(running))
	}
	if running[0].Name != "qwen3:0.6b" || running[0].ContextLength != 4096 {
		t.Errorf("unexpected first running model: %+v", running[0])
	}
	if running[1].Name != "all-minilm:latest" || running[1].ContextLength != 256 {
		t.Errorf("unexpected second running model: %+v", running[1])
	}
}

func TestOllama_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req["model"] != "qwen3:0.6b" {
			t.Errorf("expected model qwen3:0.6b, got %v", req["model"])
		}
		if think, ok := req["think"].(bool); !ok || think != false {
			t.Errorf("expected think: false in request, got %v", req["think"])
		}
		opts, ok := req["options"].(map[string]interface{})
		if !ok || opts["num_ctx"] != float64(8192) {
			t.Errorf("expected num_ctx 8192 in options, got %v", opts)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"model": "qwen3:0.6b",
			"message": {"role": "assistant", "content": "pong"},
			"done": true,
			"total_duration": 396490000,
			"load_duration": 106650000,
			"prompt_eval_count": 22,
			"prompt_eval_duration": 120000000,
			"eval_count": 52,
			"eval_duration": 396490000
		}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	thinkFalse := false
	resp, err := adapter.Chat(context.Background(), backend.ChatRequest{
		Model: "qwen3:0.6b",
		Messages: []backend.ChatMessage{
			{Role: "user", Content: "ping"},
		},
		Options: &backend.ChatOptions{
			NumCtx: 8192,
		},
		Think: &thinkFalse,
	})
	if err != nil {
		t.Fatalf("Chat() failed: %v", err)
	}

	if resp.Message.Content != "pong" {
		t.Errorf("expected pong, got %s", resp.Message.Content)
	}
	if resp.EvalCount != 52 {
		t.Errorf("expected eval_count 52, got %d", resp.EvalCount)
	}
	if resp.PromptEvalCount != 22 {
		t.Errorf("expected prompt_eval_count 22, got %d", resp.PromptEvalCount)
	}
	if resp.TokensPerSecond < 130 || resp.TokensPerSecond > 132 {
		t.Errorf("expected ~131.15 tok/s, got %f", resp.TokensPerSecond)
	}
}

func TestOllama_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if stream, ok := req["stream"].(bool); !ok || !stream {
			t.Errorf("expected stream: true in request, got %v", req["stream"])
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte("{\"model\": \"qwen3:0.6b\", \"message\": {\"role\": \"assistant\", \"content\": \"hel\"}, \"done\": false}\n"))
		w.Write([]byte("{\"model\": \"qwen3:0.6b\", \"message\": {\"role\": \"assistant\", \"content\": \"lo\"}, \"done\": false}\n"))
		w.Write([]byte("{\"model\": \"qwen3:0.6b\", \"message\": {\"role\": \"assistant\", \"content\": \"!\"}, \"done\": true, \"eval_count\": 3, \"eval_duration\": 100000000}\n"))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	var receivedChunks []string
	resp, err := adapter.ChatStream(context.Background(), backend.ChatRequest{
		Model: "qwen3:0.6b",
		Messages: []backend.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	}, func(chunk backend.ChatChunk) error {
		receivedChunks = append(receivedChunks, chunk.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() failed: %v", err)
	}

	if strings.Join(receivedChunks, "") != "hello!" {
		t.Errorf("expected chunks to combine to 'hello!', got %v", receivedChunks)
	}
	if resp.Message.Content != "hello!" {
		t.Errorf("expected final message content 'hello!', got %s", resp.Message.Content)
	}
	if resp.EvalCount != 3 {
		t.Errorf("expected eval_count 3, got %d", resp.EvalCount)
	}
}

func TestOllama_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req["model"] != "all-minilm" {
			t.Errorf("expected model all-minilm, got %v", req["model"])
		}
		if trunc, ok := req["truncate"].(bool); !ok || trunc != false {
			t.Errorf("expected truncate: false in request, got %v", req["truncate"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"model": "all-minilm",
			"embeddings": [[0.00781835, -0.025999507, 0.02206454]],
			"prompt_eval_count": 9,
			"total_duration": 21795750
		}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	truncateFalse := false
	resp, err := adapter.Embed(context.Background(), backend.EmbedRequest{
		Model:    "all-minilm",
		Input:    []string{"hello world"},
		Truncate: &truncateFalse,
	})
	if err != nil {
		t.Fatalf("Embed() failed: %v", err)
	}
	if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 3 {
		t.Fatalf("unexpected embeddings length: %+v", resp.Embeddings)
	}
	if resp.PromptEvalCount != 9 {
		t.Errorf("expected prompt_eval_count 9, got %d", resp.PromptEvalCount)
	}
}

func TestOllama_Embed_TruncationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "the input length exceeds the context length"}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	truncateFalse := false
	_, err := adapter.Embed(context.Background(), backend.EmbedRequest{
		Model:    "all-minilm",
		Input:    []string{"very large input..."},
		Truncate: &truncateFalse,
	})
	if err == nil {
		t.Fatal("expected error when context length exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds the context length") {
		t.Errorf("expected context length exceeded error, got %v", err)
	}
}

func TestOllama_Pull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte("{\"status\": \"pulling manifest\"}\n"))
		w.Write([]byte("{\"status\": \"downloading\", \"digest\": \"sha256:abc\", \"total\": 1000, \"completed\": 500}\n"))
		w.Write([]byte("{\"status\": \"success\"}\n"))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	var events []backend.Progress
	err := adapter.Pull(context.Background(), "qwen3:0.6b", func(p backend.Progress) {
		events = append(events, p)
	})
	if err != nil {
		t.Fatalf("Pull() failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 progress events, got %d", len(events))
	}
	if events[0].Status != "pulling manifest" {
		t.Errorf("unexpected event 0: %+v", events[0])
	}
	if events[1].Completed != 500 || events[1].Total != 1000 {
		t.Errorf("unexpected event 1: %+v", events[1])
	}
	if events[2].Status != "success" {
		t.Errorf("unexpected event 2: %+v", events[2])
	}
}

func TestOllama_Unload(t *testing.T) {
	var requestedKeepAlive interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		requestedKeepAlive = req["keep_alive"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"done": true}`))
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	if err := adapter.Unload(context.Background(), "qwen3:0.6b"); err != nil {
		t.Fatalf("Unload() failed: %v", err)
	}
	if requestedKeepAlive != float64(0) {
		t.Errorf("expected keep_alive 0, got %v", requestedKeepAlive)
	}
}

func TestOllama_Delete(t *testing.T) {
	var requestedMethod string
	var requestedPath string
	var requestedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMethod = r.Method
		requestedPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&requestedBody)

		if requestedPath != "/api/delete" {
			http.NotFound(w, r)
			return
		}
		if requestedBody["model"] == "missing:model" {
			http.Error(w, `{"error": "model 'missing:model' not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := backend.NewOllama(server.URL, server.Client())
	ctx := context.Background()

	// Empty model name check
	if err := adapter.Delete(ctx, ""); err == nil {
		t.Error("expected error deleting empty model name")
	}

	// Successful delete
	if err := adapter.Delete(ctx, "qwen3:0.6b"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}
	if requestedMethod != http.MethodDelete {
		t.Errorf("expected DELETE method, got %s", requestedMethod)
	}
	if requestedPath != "/api/delete" {
		t.Errorf("expected /api/delete path, got %s", requestedPath)
	}
	if requestedBody["model"] != "qwen3:0.6b" {
		t.Errorf("expected model qwen3:0.6b, got %v", requestedBody["model"])
	}

	// 404 Not Found delete
	if err := adapter.Delete(ctx, "missing:model"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %v", err)
	}
}

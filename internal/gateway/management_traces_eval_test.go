package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
)

type evaluationTestAdapter struct {
	*runtimeTestAdapter
	chat func(context.Context, backend.ChatRequest) (*backend.ChatResponse, error)
}

func (a *evaluationTestAdapter) Chat(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	return a.chat(ctx, req)
}

func TestManagementTraceSummariesHideSessionPathAndContent(t *testing.T) {
	brainHome := t.TempDir()
	t.Setenv(home.EnvVar, brainHome)
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	const sessionID = "private-project-session"
	store := trace.NewStore(layout.Traces)
	if err := store.WriteRecord(&trace.Record{
		SessionID: sessionID, TurnID: "turn-1", Model: "qwen-local", PromptTokens: 12, EvalTokens: 4,
		Messages: []trace.Message{{Role: "user", Content: "synthetic-private-prompt"}},
		Response: "synthetic-private-response", ToolCalls: []trace.ToolCall{{Name: "search_code", Result: "private-result"}},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	response := managementRequest(server, http.MethodGet, "/manage/v1/traces", "")
	for _, private := range []string{sessionID, layout.Traces, "synthetic-private-prompt", "synthetic-private-response", "private-result"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("trace summary leaked %q: %s", private, response.Body.String())
		}
	}
	var body struct {
		Sessions []struct {
			ID      string   `json:"id"`
			Records int      `json:"record_count"`
			Models  []string `json:"models"`
		} `json:"sessions"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Sessions) != 1 || body.Sessions[0].ID == "" || body.Sessions[0].Records != 1 || body.Sessions[0].Models[0] != "qwen-local" {
		t.Fatalf("invalid redacted trace summary: %d %s", response.Code, response.Body.String())
	}
	if body.Sessions[0].ID != traceManagementID("gateway-master", sessionID) || body.Sessions[0].ID == sessionID {
		t.Fatalf("trace alias is not a keyed pseudonym: %+v", body.Sessions[0])
	}
}

func TestManagementEvaluationRunsOnlyFixedLocalSmokeSuite(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	modelID := "qwen2.5-coder-1.5b"
	model, ok := catalog.Default().GetModel(modelID)
	if !ok {
		t.Fatal("test catalog model is missing")
	}
	tag := model.Backends["ollama"]
	config := home.DefaultConfig("tiny", map[string]string{"chat": modelID})
	config.Backends["ollama"] = home.BackendConfig{Endpoint: "http://127.0.0.1:11434", Token: "synthetic-backend-secret", Enabled: true}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	var chatCalls atomic.Int32
	adapter := &evaluationTestAdapter{
		runtimeTestAdapter: &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434", models: []backend.ModelInfo{{Name: tag}}},
		chat: func(_ context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
			chatCalls.Add(1)
			if req.Model != tag || len(req.Messages) != 1 || req.Messages[0].Content != "Reply with exactly this token: READY" {
				t.Errorf("evaluation used a non-static request: %+v", req)
			}
			return &backend.ChatResponse{Model: tag, Message: backend.ChatMessage{Role: "assistant", Content: "READY"}, PromptEvalCount: 8, EvalCount: 1}, nil
		},
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", Adapter: adapter, UserConfig: config, ConfigPath: configPath, LocalBackend: "ollama", Catalog: catalog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	unknown := managementRequest(server, http.MethodPost, "/manage/v1/evaluation-jobs", `{"suite_id":"custom","model_id":"`+modelID+`","confirmation":"eval `+modelID+`"}`)
	if unknown.Code != http.StatusBadRequest || chatCalls.Load() != 0 {
		t.Fatalf("custom eval suite was not rejected before inference: %d %s", unknown.Code, unknown.Body.String())
	}
	withPrompt := managementRequest(server, http.MethodPost, "/manage/v1/evaluation-jobs", `{"suite_id":"local-smoke","model_id":"`+modelID+`","confirmation":"eval `+modelID+`","prompt":"client data"}`)
	if withPrompt.Code != http.StatusBadRequest || chatCalls.Load() != 0 {
		t.Fatalf("user-supplied eval input was not rejected: %d %s", withPrompt.Code, withPrompt.Body.String())
	}
	started := managementRequest(server, http.MethodPost, "/manage/v1/evaluation-jobs", `{"suite_id":"local-smoke","model_id":"`+modelID+`","confirmation":"eval `+modelID+`"}`)
	if started.Code != http.StatusAccepted {
		t.Fatalf("fixed local eval did not start: %d %s", started.Code, started.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("invalid evaluation job response: %#v err=%v", job, err)
	}
	statusPath := "/manage/v1/evaluation-jobs/" + job.ID
	deadline := time.Now().Add(time.Second)
	for {
		status := managementRequest(server, http.MethodGet, statusPath, "")
		if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "Reply with exactly") || strings.Contains(status.Body.String(), "READY\"") || strings.Contains(status.Body.String(), "synthetic-backend-secret") {
			t.Fatalf("evaluation result exposed prompt/output/secret: %d %s", status.Code, status.Body.String())
		}
		if strings.Contains(status.Body.String(), `"status":"completed"`) {
			if !strings.Contains(status.Body.String(), `"passed":true`) || chatCalls.Load() != 1 {
				t.Fatalf("evaluation result is incorrect: calls=%d body=%s", chatCalls.Load(), status.Body.String())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("evaluation job did not complete: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagementEvaluationRejectsRemoteBackendBeforeInference(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	config.Backends = map[string]home.BackendConfig{"remote": {Endpoint: "https://remote.example/v1", Enabled: true}}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	called := false
	adapter := &evaluationTestAdapter{
		runtimeTestAdapter: &runtimeTestAdapter{name: "remote", endpoint: "https://remote.example/v1"},
		chat: func(context.Context, backend.ChatRequest) (*backend.ChatResponse, error) {
			called = true
			return nil, nil
		},
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master", Adapter: adapter, UserConfig: config, ConfigPath: configPath, LocalBackend: "remote", Catalog: catalog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	response := managementRequest(server, http.MethodPost, "/manage/v1/evaluation-jobs", `{"suite_id":"local-smoke","model_id":"qwen2.5-coder-1.5b","confirmation":"eval qwen2.5-coder-1.5b"}`)
	if response.Code != http.StatusConflict || called {
		t.Fatalf("remote eval was not rejected before inference: %d called=%t body=%s", response.Code, called, response.Body.String())
	}
}

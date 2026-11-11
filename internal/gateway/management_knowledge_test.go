package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

type knowledgeTestAdapter struct {
	*runtimeTestAdapter
	embed func(context.Context, backend.EmbedRequest) (*backend.EmbedResponse, error)
}

func (a *knowledgeTestAdapter) ShowModel(context.Context, string) (backend.ModelDetails, error) {
	return backend.ModelDetails{EmbeddingLength: 4}, nil
}

func (a *knowledgeTestAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	if a.embed != nil {
		return a.embed(ctx, req)
	}
	embeddings := make([][]float32, len(req.Input))
	for i := range req.Input {
		embeddings[i] = []float32{1, 0, 0, 0}
	}
	return &backend.EmbedResponse{Model: req.Model, Embeddings: embeddings}, nil
}

func newKnowledgeTestServer(t *testing.T, configPath, dbPath string, config *home.Config, adapter backend.Adapter) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Token: "gateway-master", ConfigPath: configPath, StorePath: dbPath,
		UserConfig: config, Adapter: adapter, Catalog: catalog.Default(), LocalBackend: "ollama",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server
}

func TestManagementKnowledgeListsReindexesAndSearchesOnlyRegisteredSources(t *testing.T) {
	base := t.TempDir()
	brainHome := filepath.Join(base, "brain-home")
	root := filepath.Join(base, "workspace-docs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "guide.md"), []byte("inside-root phrase for knowledge search"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideSecret := "synthetic-secret-outside-registered-root"
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte(outsideSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b", "embed": "nomic-embed-text"})
	config.KnowledgeSources = []home.KnowledgeSource{{
		ID: "workspace_docs", Name: "Workspace docs", Root: canonicalRoot,
		Collection: "workspace_docs", EmbeddingModel: "nomic-embed-text",
	}}
	configPath := filepath.Join(brainHome, "config.yaml")
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	adapter := &knowledgeTestAdapter{runtimeTestAdapter: &runtimeTestAdapter{
		name: "ollama", endpoint: "http://127.0.0.1:11434",
		models: []backend.ModelInfo{{Name: "nomic-embed-text"}},
	}}
	server := newKnowledgeTestServer(t, configPath, filepath.Join(brainHome, "brain.db"), config, adapter)

	listed := managementRequest(server, http.MethodGet, "/manage/v1/knowledge", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), canonicalRoot) {
		t.Fatalf("knowledge source listing leaked its filesystem root: status=%d body=%s", listed.Code, listed.Body.String())
	}
	var sources struct {
		Sources []struct {
			ID            string `json:"id"`
			RootAvailable bool   `json:"root_available"`
			Documents     int    `json:"documents"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &sources); err != nil || len(sources.Sources) != 1 || sources.Sources[0].ID != "workspace_docs" || !sources.Sources[0].RootAvailable {
		t.Fatalf("invalid source listing: %#v err=%v", sources, err)
	}
	unknown := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/index-jobs", `{"source_id":"../../outside","confirmation":"../../outside"}`)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unregistered source ID returned %d: %s", unknown.Code, unknown.Body.String())
	}
	withPath := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/index-jobs", `{"source_id":"workspace_docs","confirmation":"workspace_docs","root":"/tmp"}`)
	if withPath.Code != http.StatusBadRequest {
		t.Fatalf("path-bearing API request returned %d: %s", withPath.Code, withPath.Body.String())
	}
	started := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/index-jobs", `{"source_id":"workspace_docs","confirmation":"workspace_docs"}`)
	if started.Code != http.StatusAccepted {
		t.Fatalf("knowledge index did not start: %d %s", started.Code, started.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("invalid index job response: %#v err=%v", job, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := managementRequest(server, http.MethodGet, "/manage/v1/knowledge/index-jobs/"+job.ID, "")
		if strings.Contains(status.Body.String(), `"status":"completed"`) {
			break
		}
		if strings.Contains(status.Body.String(), `"status":"failed"`) || time.Now().After(deadline) {
			t.Fatalf("knowledge index job did not complete: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	listed = managementRequest(server, http.MethodGet, "/manage/v1/knowledge", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"documents":1`) || strings.Contains(listed.Body.String(), canonicalRoot) || strings.Contains(listed.Body.String(), outsideSecret) {
		t.Fatalf("indexed source summary is invalid or exposed content: %d %s", listed.Code, listed.Body.String())
	}
	search := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/search", `{"source_id":"workspace_docs","query":"inside-root phrase","limit":5}`)
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "guide.md") || !strings.Contains(search.Body.String(), "inside-root phrase") || strings.Contains(search.Body.String(), outsideSecret) || strings.Contains(search.Body.String(), canonicalRoot) {
		t.Fatalf("knowledge search failed or leaked outside-root content/path: %d %s", search.Code, search.Body.String())
	}
}

func TestManagementKnowledgeRejectsRemoteEmbeddingBackends(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "docs")
	brainHome := filepath.Join(base, "brain-home")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(brainHome, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen-test", "embed": "nomic-embed-text"})
	config.Backends = map[string]home.BackendConfig{"remote": {Endpoint: "https://remote.example/v1", Enabled: true}}
	config.KnowledgeSources = []home.KnowledgeSource{{ID: "docs", Name: "Docs", Root: root, Collection: "docs", EmbeddingModel: "nomic-embed-text"}}
	configPath := filepath.Join(brainHome, "config.yaml")
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{name: "remote", endpoint: "https://remote.example/v1"}
	server := newKnowledgeTestServer(t, configPath, filepath.Join(brainHome, "brain.db"), config, adapter)
	response := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/index-jobs", `{"source_id":"docs","confirmation":"docs"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "local_embedding_required") {
		t.Fatalf("remote embedding backend was not rejected: %d %s", response.Code, response.Body.String())
	}
}

func TestManagementKnowledgeCancelReportsPartialAndKeepsPathsPrivate(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "docs")
	brainHome := filepath.Join(base, "brain-home")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(brainHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("cancel while embedding"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen-test", "embed": "nomic-embed-text"})
	config.KnowledgeSources = []home.KnowledgeSource{{ID: "docs", Name: "Docs", Root: root, Collection: "docs", EmbeddingModel: "nomic-embed-text"}}
	configPath := filepath.Join(brainHome, "config.yaml")
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	adapter := &knowledgeTestAdapter{
		runtimeTestAdapter: &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434", models: []backend.ModelInfo{{Name: "nomic-embed-text"}}},
		embed: func(ctx context.Context, _ backend.EmbedRequest) (*backend.EmbedResponse, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	server := newKnowledgeTestServer(t, configPath, filepath.Join(brainHome, "brain.db"), config, adapter)
	response := managementRequest(server, http.MethodPost, "/manage/v1/knowledge/index-jobs", `{"source_id":"docs","confirmation":"docs"}`)
	var job struct {
		ID string `json:"id"`
	}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &job) != nil || job.ID == "" {
		t.Fatalf("index job start returned %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("index job did not reach local embedding")
	}
	statusPath := "/manage/v1/knowledge/index-jobs/" + job.ID
	cancelled := managementRequest(server, http.MethodDelete, statusPath, "")
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("index cancellation returned %d: %s", cancelled.Code, cancelled.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for {
		status := managementRequest(server, http.MethodGet, statusPath, "")
		if strings.Contains(status.Body.String(), `"status":"cancelled"`) {
			if strings.Contains(status.Body.String(), root) || strings.Contains(status.Body.String(), "cancel while embedding") {
				t.Fatalf("index job status leaked path or content: %s", status.Body.String())
			}
			break
		}
		if strings.Contains(status.Body.String(), `"status":"failed"`) || time.Now().After(deadline) {
			t.Fatalf("index job did not reach cancelled state: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

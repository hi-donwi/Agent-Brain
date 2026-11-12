package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/hw"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

type runtimeTestAdapter struct {
	backend.Adapter
	name     string
	endpoint string
	models   []backend.ModelInfo
	running  []backend.ProcessInfo
	pull     func(context.Context, string, func(backend.Progress)) error
	deleted  []string
	unloaded []string
}

func (a *runtimeTestAdapter) Name() string                          { return a.name }
func (a *runtimeTestAdapter) Endpoint() string                      { return a.endpoint }
func (*runtimeTestAdapter) Ping(context.Context) error              { return nil }
func (*runtimeTestAdapter) Version(context.Context) (string, error) { return "test-version", nil }
func (a *runtimeTestAdapter) ListModels(context.Context) ([]backend.ModelInfo, error) {
	return a.models, nil
}
func (a *runtimeTestAdapter) RunningModels(context.Context) ([]backend.ProcessInfo, error) {
	return a.running, nil
}
func (a *runtimeTestAdapter) Pull(ctx context.Context, id string, progress func(backend.Progress)) error {
	if a.pull != nil {
		return a.pull(ctx, id, progress)
	}
	if progress != nil {
		progress(backend.Progress{Total: 100, Completed: 100})
	}
	return nil
}
func (a *runtimeTestAdapter) Delete(_ context.Context, id string) error {
	a.deleted = append(a.deleted, id)
	return nil
}
func (a *runtimeTestAdapter) Unload(_ context.Context, id string) error {
	a.unloaded = append(a.unloaded, id)
	a.running = nil
	return nil
}

func newRuntimeTestServer(t *testing.T, configPath string, config *home.Config, adapter backend.Adapter) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Token: "gateway-master", Adapter: adapter, UserConfig: config, ConfigPath: configPath,
		Catalog: catalog.Default(),
		RuntimeDetectHardware: func() (hw.Info, error) {
			return hw.Info{OS: "linux", Arch: "amd64", Accelerator: hw.AcceleratorNone}, nil
		},
		RuntimeFreeBytes: func(string) (uint64, error) { return 80 << 30, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server
}

func managementRequest(server *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	r.Host = "127.0.0.1"
	r.Header.Set("Authorization", "Bearer "+NewManagementToken("gateway-master"))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	return w
}

func TestManagementRuntimeAndCatalogAreRedactedAndReadOnly(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	config.Backends["ollama"] = home.BackendConfig{Endpoint: "http://127.0.0.1:11434/ollama?access_token=synthetic-url-secret", Token: "synthetic-backend-secret", Enabled: true}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{
		name: "ollama", endpoint: "http://127.0.0.1:11434",
		models: []backend.ModelInfo{{Name: "qwen2.5-coder:1.5b", Size: 1000}},
	}
	server := newRuntimeTestServer(t, configPath, config, adapter)

	runtimeResponse := managementRequest(server, http.MethodGet, "/manage/v1/runtime", "")
	if runtimeResponse.Code != http.StatusOK || strings.Contains(runtimeResponse.Body.String(), "synthetic-backend-secret") || strings.Contains(runtimeResponse.Body.String(), "synthetic-url-secret") || strings.Contains(runtimeResponse.Body.String(), "\"token\"") {
		t.Fatalf("runtime response is not redacted: status=%d body=%s", runtimeResponse.Code, runtimeResponse.Body.String())
	}
	var runtime map[string]any
	if err := json.Unmarshal(runtimeResponse.Body.Bytes(), &runtime); err != nil || runtime["running_backend_id"] != "ollama" || runtime["profile"] != "tiny" {
		t.Fatalf("runtime response missing safe state: %#v err=%v", runtime, err)
	}

	modelsResponse := managementRequest(server, http.MethodGet, "/manage/v1/models", "")
	if modelsResponse.Code != http.StatusOK || !strings.Contains(modelsResponse.Body.String(), "qwen2.5-coder:1.5b") || strings.Contains(modelsResponse.Body.String(), "synthetic-backend-secret") {
		t.Fatalf("installed model list failed or leaked config: %d %s", modelsResponse.Code, modelsResponse.Body.String())
	}
	catalogResponse := managementRequest(server, http.MethodGet, "/manage/v1/model-catalog", "")
	if catalogResponse.Code != http.StatusOK || !strings.Contains(catalogResponse.Body.String(), "qwen2.5-coder-1.5b") {
		t.Fatalf("catalog response is missing embedded models: %d %s", catalogResponse.Code, catalogResponse.Body.String())
	}
	stored, err := home.LoadConfig(configPath)
	if err != nil || stored.Backends["ollama"].Token != "synthetic-backend-secret" {
		t.Fatalf("read endpoints modified the private config: %#v err=%v", stored, err)
	}
}

func TestManagementBackendSelectionRequiresConfiguredReachableBackendAndConfirmation(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("backend health check used unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	config.Backends["ollama"] = home.BackendConfig{Endpoint: "http://127.0.0.1:11434", Token: "ollama-private", Enabled: true}
	config.Backends["llamacpp"] = home.BackendConfig{Endpoint: remote.URL, Token: "llama-private", Enabled: false}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server := newRuntimeTestServer(t, configPath, config, &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434"})
	wrongConfirmation := managementRequest(server, http.MethodPut, "/manage/v1/runtime/backend", `{"backend_id":"llamacpp","confirmation":"wrong"}`)
	if wrongConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("wrong backend confirmation returned %d: %s", wrongConfirmation.Code, wrongConfirmation.Body.String())
	}
	unknown := managementRequest(server, http.MethodPut, "/manage/v1/runtime/backend", `{"backend_id":"arbitrary","confirmation":"arbitrary"}`)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unconfigured backend selection returned %d: %s", unknown.Code, unknown.Body.String())
	}
	selected := managementRequest(server, http.MethodPut, "/manage/v1/runtime/backend", `{"backend_id":"llamacpp","confirmation":"llamacpp"}`)
	if selected.Code != http.StatusOK || strings.Contains(selected.Body.String(), "llama-private") || !strings.Contains(selected.Body.String(), `"restart_required":true`) {
		t.Fatalf("backend selection failed or leaked token: %d %s", selected.Code, selected.Body.String())
	}
	stored, err := home.LoadConfig(configPath)
	if err != nil || !stored.Backends["llamacpp"].Enabled || stored.Backends["ollama"].Enabled || stored.Backends["llamacpp"].Token != "llama-private" {
		t.Fatalf("backend selection did not preserve credentials and exclusive selection: %#v err=%v", stored.Backends, err)
	}
}

func TestManagementBackendSelectionDoesNotPersistWhenBackendIsUnreachable(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	config.Backends["offline"] = home.BackendConfig{Endpoint: "http://127.0.0.1:1", Enabled: false}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server := newRuntimeTestServer(t, configPath, config, &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434"})
	response := managementRequest(server, http.MethodPut, "/manage/v1/runtime/backend", `{"backend_id":"offline","confirmation":"offline"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("unreachable backend selection returned %d: %s", response.Code, response.Body.String())
	}
	stored, err := home.LoadConfig(configPath)
	if err != nil || !stored.Backends["ollama"].Enabled || stored.Backends["offline"].Enabled {
		t.Fatalf("failed backend selection changed config: %#v err=%v", stored.Backends, err)
	}
}

func TestManagementModelPullRequiresCatalogConfirmationAndDiskMargin(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	var pulls atomic.Int32
	adapter := &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434", pull: func(context.Context, string, func(backend.Progress)) error {
		pulls.Add(1)
		return nil
	}}
	server := newRuntimeTestServer(t, configPath, config, adapter)
	response := managementRequest(server, http.MethodPost, "/manage/v1/model-pull-jobs", `{"catalog_model_id":"qwen2.5-coder-1.5b","confirmation":"wrong"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("wrong confirmation returned %d: %s", response.Code, response.Body.String())
	}
	response = managementRequest(server, http.MethodPost, "/manage/v1/model-pull-jobs", `{"catalog_model_id":"not-in-catalog","confirmation":"not-in-catalog"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("uncatalogued model returned %d: %s", response.Code, response.Body.String())
	}
	server.cfg.RuntimeFreeBytes = func(string) (uint64, error) { return 2 << 30, nil }
	response = managementRequest(server, http.MethodPost, "/manage/v1/model-pull-jobs", `{"catalog_model_id":"qwen2.5-coder-1.5b","confirmation":"qwen2.5-coder-1.5b"}`)
	if response.Code != http.StatusConflict || pulls.Load() != 0 {
		t.Fatalf("insufficient disk did not refuse before pull: status=%d pulls=%d body=%s", response.Code, pulls.Load(), response.Body.String())
	}
}

func TestManagementModelPullJobReportsProgressAndCancels(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 1)
	adapter := &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434", pull: func(ctx context.Context, model string, progress func(backend.Progress)) error {
		started <- model
		progress(backend.Progress{Status: "downloading", Total: 1000, Completed: 400})
		<-ctx.Done()
		return ctx.Err()
	}}
	server := newRuntimeTestServer(t, configPath, config, adapter)
	response := managementRequest(server, http.MethodPost, "/manage/v1/model-pull-jobs", `{"catalog_model_id":"qwen2.5-coder-1.5b","confirmation":"qwen2.5-coder-1.5b"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("model pull did not start: %d %s", response.Code, response.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("invalid job response: %#v err=%v", job, err)
	}
	select {
	case model := <-started:
		if model != "qwen2.5-coder:1.5b" {
			t.Fatalf("adapter received non-catalog model tag %q", model)
		}
	case <-time.After(time.Second):
		t.Fatal("model pull worker did not start")
	}
	collection := managementRequest(server, http.MethodGet, "/manage/v1/model-pull-jobs", "")
	if collection.Code != http.StatusOK || !strings.Contains(collection.Body.String(), job.ID) || strings.Contains(collection.Body.String(), "synthetic-backend-secret") {
		t.Fatalf("model pull job collection failed or leaked config: %d %s", collection.Code, collection.Body.String())
	}
	statusPath := "/manage/v1/model-pull-jobs/" + job.ID
	deadline := time.Now().Add(time.Second)
	for {
		status := managementRequest(server, http.MethodGet, statusPath, "")
		var state struct {
			Status    string `json:"status"`
			Completed uint64 `json:"completed_bytes"`
		}
		if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.Status == "running" && state.Completed == 400 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job progress not exposed: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := managementRequest(server, http.MethodPost, "/manage/v1/model-pull-jobs", `{"catalog_model_id":"qwen2.5-coder-1.5b","confirmation":"qwen2.5-coder-1.5b"}`)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent model download returned %d: %s", second.Code, second.Body.String())
	}
	cancelled := managementRequest(server, http.MethodDelete, statusPath, "")
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("model pull cancellation returned %d: %s", cancelled.Code, cancelled.Body.String())
	}
	deadline = time.Now().Add(time.Second)
	for {
		status := managementRequest(server, http.MethodGet, statusPath, "")
		if strings.Contains(status.Body.String(), `"status":"cancelled"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("model pull did not reach cancelled state: %s", status.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagementModelDeleteProtectsConfiguredRolesAndRequiresConfirmation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b", "code": "qwen2.5-coder-1.5b"})
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434", models: []backend.ModelInfo{{Name: "qwen2.5-coder:1.5b"}}}
	server := newRuntimeTestServer(t, configPath, config, adapter)
	path := "/manage/v1/models/qwen2.5-coder-1.5b/deletion"
	wrong := managementRequest(server, http.MethodPost, path, `{"confirmation":"wrong"}`)
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("missing exact deletion confirmation returned %d: %s", wrong.Code, wrong.Body.String())
	}
	protected := managementRequest(server, http.MethodPost, path, `{"confirmation":"delete qwen2.5-coder-1.5b"}`)
	if protected.Code != http.StatusConflict || len(adapter.deleted) != 0 {
		t.Fatalf("active model role was not protected: status=%d deleted=%v body=%s", protected.Code, adapter.deleted, protected.Body.String())
	}
	config.Aliases = map[string]string{"chat": "other-installed-model"}
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	routerPath := filepath.Join(root, "accounts.yaml")
	routerConfig := router.DefaultRouterConfig()
	routerConfig.RoutingProfiles = []router.RoutingProfile{{ID: "local", Targets: []router.RoutingTarget{{
		Model: "assistant", Provider: router.ProviderLocal, UpstreamModel: "qwen2.5-coder:1.5b", Protocols: []string{router.ProtocolChat},
	}}}}
	if err := router.SaveRouterConfig(routerPath, routerConfig); err != nil {
		t.Fatal(err)
	}
	server.cfg.AccountConfigPath = routerPath
	profileProtected := managementRequest(server, http.MethodPost, path, `{"confirmation":"delete qwen2.5-coder-1.5b"}`)
	if profileProtected.Code != http.StatusConflict || len(adapter.deleted) != 0 {
		t.Fatalf("routing profile model was not protected: status=%d deleted=%v body=%s", profileProtected.Code, adapter.deleted, profileProtected.Body.String())
	}
	routerConfig.RoutingProfiles = nil
	if err := router.SaveRouterConfig(routerPath, routerConfig); err != nil {
		t.Fatal(err)
	}
	removed := managementRequest(server, http.MethodPost, path, `{"confirmation":"delete qwen2.5-coder-1.5b"}`)
	if removed.Code != http.StatusOK || len(adapter.deleted) != 1 || adapter.deleted[0] != "qwen2.5-coder:1.5b" {
		t.Fatalf("catalog model was not removed: status=%d deleted=%v body=%s", removed.Code, adapter.deleted, removed.Body.String())
	}
}

func TestManagementModelUnloadRequiresConfirmationAndForgetsResidency(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen2.5-coder-1.5b"})
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{
		name: "ollama", endpoint: "http://127.0.0.1:11434",
		running: []backend.ProcessInfo{{Name: "qwen2.5-coder:1.5b"}},
	}
	server := newRuntimeTestServer(t, configPath, config, adapter)
	path := "/manage/v1/models/qwen2.5-coder-1.5b/unload"
	wrong := managementRequest(server, http.MethodPost, path, `{"confirmation":"wrong"}`)
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("unload without exact confirmation returned %d: %s", wrong.Code, wrong.Body.String())
	}
	response := managementRequest(server, http.MethodPost, path, `{"confirmation":"unload qwen2.5-coder-1.5b"}`)
	if response.Code != http.StatusOK || len(adapter.unloaded) != 1 || adapter.unloaded[0] != "qwen2.5-coder:1.5b" {
		t.Fatalf("model unload failed: status=%d unloaded=%v body=%s", response.Code, adapter.unloaded, response.Body.String())
	}
}

func TestManagementProfileSelectionRequiresInstalledModelsAndRestart(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	config := home.DefaultConfig("tiny", map[string]string{"chat": "old-model"})
	if err := home.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	server := newRuntimeTestServer(t, configPath, config, &runtimeTestAdapter{name: "ollama", endpoint: "http://127.0.0.1:11434"})
	missing := managementRequest(server, http.MethodPut, "/manage/v1/runtime/profile", `{"profile":"tiny","confirmation":"tiny"}`)
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "profile_models_missing") {
		t.Fatalf("profile selection without installed models returned %d: %s", missing.Code, missing.Body.String())
	}
	adapter := server.cfg.Adapter.(*runtimeTestAdapter)
	profile, _ := catalog.Default().GetProfile(catalog.ProfileTiny)
	for _, modelID := range profile.Defaults {
		model, _ := catalog.Default().GetModel(modelID)
		adapter.models = append(adapter.models, backend.ModelInfo{Name: model.Backends["ollama"]})
	}
	selected := managementRequest(server, http.MethodPut, "/manage/v1/runtime/profile", `{"profile":"tiny","confirmation":"tiny"}`)
	if selected.Code != http.StatusOK || !strings.Contains(selected.Body.String(), `"restart_required":true`) {
		t.Fatalf("profile selection failed: %d %s", selected.Code, selected.Body.String())
	}
	stored, err := home.LoadConfig(configPath)
	if err != nil || stored.Profile != "tiny" || stored.Aliases["chat"] != profile.Defaults[catalog.RoleChat] {
		t.Fatalf("profile aliases were not persisted: %#v err=%v", stored, err)
	}
}

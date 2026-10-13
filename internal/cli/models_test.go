package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
)

type mockCLIAdapter struct {
	models    []backend.ModelInfo
	modelsErr error
	pulled    []string
	pullErr   error
	deleted   []string
	deleteErr error
}

func (m *mockCLIAdapter) Name() string     { return "ollama" }
func (m *mockCLIAdapter) Endpoint() string { return "http://127.0.0.1:11434" }
func (m *mockCLIAdapter) Ping(ctx context.Context) error {
	return nil
}
func (m *mockCLIAdapter) Version(ctx context.Context) (string, error) {
	return "0.30.8", nil
}
func (m *mockCLIAdapter) ListModels(ctx context.Context) ([]backend.ModelInfo, error) {
	return m.models, m.modelsErr
}
func (m *mockCLIAdapter) ShowModel(ctx context.Context, model string) (backend.ModelDetails, error) {
	return backend.ModelDetails{}, nil
}
func (m *mockCLIAdapter) RunningModels(ctx context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (m *mockCLIAdapter) Chat(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	return nil, nil
}
func (m *mockCLIAdapter) ChatStream(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return nil, nil
}
func (m *mockCLIAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	return nil, nil
}
func (m *mockCLIAdapter) Pull(ctx context.Context, model string, onProgress func(backend.Progress)) error {
	if m.pullErr != nil {
		return m.pullErr
	}
	m.pulled = append(m.pulled, model)
	if onProgress != nil {
		onProgress(backend.Progress{Status: "downloading", Total: 1000, Completed: 500})
		onProgress(backend.Progress{Status: "success", Total: 1000, Completed: 1000})
	}
	return nil
}
func (m *mockCLIAdapter) Delete(ctx context.Context, model string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, model)
	return nil
}
func (m *mockCLIAdapter) Unload(ctx context.Context, model string) error {
	return nil
}

func TestModels_List(t *testing.T) {
	adapter := &mockCLIAdapter{
		models: []backend.ModelInfo{
			{
				Name:       "qwen2.5-coder:1.5b",
				Size:       986 * 1024 * 1024,
				ModifiedAt: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		ResolveHome:    func() (home.Layout, error) { return home.Layout{Root: t.TempDir()}, nil },
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"ls"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("models ls failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "qwen2.5-coder:1.5b") || !strings.Contains(out, "chat") {
		t.Errorf("unexpected output: %s", out)
	}

	// JSON mode
	bufJSON := new(bytes.Buffer)
	cmdJSON := newModelsCmd(deps)
	cmdJSON.SetOut(bufJSON)
	cmdJSON.SetArgs([]string{"ls", "--json"})

	if err := cmdJSON.Execute(); err != nil {
		t.Fatalf("models ls --json failed: %v", err)
	}

	var listings []ModelListing
	if err := json.Unmarshal(bufJSON.Bytes(), &listings); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	if len(listings) != 1 || listings[0].Name != "qwen2.5-coder:1.5b" || listings[0].CatalogID != "qwen2.5-coder-1.5b" {
		t.Errorf("unexpected listings: %+v", listings)
	}
}

func TestModels_List_Empty(t *testing.T) {
	adapter := &mockCLIAdapter{models: nil}
	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"ls"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("models ls empty failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No models installed") {
		t.Errorf("expected No models installed, got: %s", buf.String())
	}
}

func TestModels_List_BackendDown(t *testing.T) {
	adapter := &mockCLIAdapter{modelsErr: errors.New("connection refused")}
	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	errBuf := new(bytes.Buffer)
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"ls"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when backend down")
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "failed to list models") {
		t.Errorf("expected failed to list models in stderr, got: %s", errBuf.String())
	}
}

func TestModels_Pull_Success(t *testing.T) {
	adapter := &mockCLIAdapter{}
	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		ResolveHome:    func() (home.Layout, error) { return home.Layout{Root: t.TempDir()}, nil },
		FreeBytes:      func(string) (uint64, error) { return 100 * 1024 * 1024 * 1024, nil }, // 100 GB free
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	// Pull using catalog ID
	cmd.SetArgs([]string{"pull", "qwen2.5-coder-1.5b"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("models pull failed: %v", err)
	}

	if len(adapter.pulled) != 1 || adapter.pulled[0] != "qwen2.5-coder:1.5b" {
		t.Errorf("expected resolved tag qwen2.5-coder:1.5b, got %v", adapter.pulled)
	}
	if !strings.Contains(outBuf.String(), "pulled successfully") {
		t.Errorf("expected success message, got: %s", outBuf.String())
	}
}

func TestModels_Pull_InsufficientDisk(t *testing.T) {
	adapter := &mockCLIAdapter{}
	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		ResolveHome:    func() (home.Layout, error) { return home.Layout{Root: t.TempDir()}, nil },
		FreeBytes:      func(string) (uint64, error) { return 2 * 1024 * 1024 * 1024, nil }, // 2 GB free (fails 2GB margin + download size)
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	errBuf := new(bytes.Buffer)
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"pull", "qwen2.5-coder-1.5b"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on insufficient disk")
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "insufficient disk space") {
		t.Errorf("expected insufficient disk space message, got: %s", errBuf.String())
	}
	if len(adapter.pulled) != 0 {
		t.Errorf("adapter.Pull should not have been called, called with %v", adapter.pulled)
	}
}

func TestModels_Rm(t *testing.T) {
	adapter := &mockCLIAdapter{}
	deps := ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) { return adapter, nil },
		Catalog:        catalog.Default(),
	}

	cmd := newModelsCmd(deps)
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"rm", "qwen2.5-coder-1.5b"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("models rm failed: %v", err)
	}
	if len(adapter.deleted) != 1 || adapter.deleted[0] != "qwen2.5-coder:1.5b" {
		t.Errorf("expected deleted qwen2.5-coder:1.5b, got %v", adapter.deleted)
	}
	if !strings.Contains(outBuf.String(), "model qwen2.5-coder:1.5b removed") {
		t.Errorf("expected removed message, got: %s", outBuf.String())
	}

	// Delete error
	adapter.deleteErr = os.ErrNotExist
	cmdErr := newModelsCmd(deps)
	errBuf := new(bytes.Buffer)
	cmdErr.SetErr(errBuf)
	cmdErr.SetArgs([]string{"rm", "non-existent"})

	err := cmdErr.Execute()
	if err == nil {
		t.Fatal("expected error deleting missing model")
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
}

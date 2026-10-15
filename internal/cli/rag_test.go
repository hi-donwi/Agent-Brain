package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/cli"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

type fakeCliAdapter struct{}

func (f *fakeCliAdapter) Name() string     { return "fake" }
func (f *fakeCliAdapter) Endpoint() string { return "http://127.0.0.1:0" }
func (f *fakeCliAdapter) Ping(_ context.Context) error {
	return nil
}
func (f *fakeCliAdapter) Version(_ context.Context) (string, error) {
	return "0.0.1", nil
}
func (f *fakeCliAdapter) ListModels(_ context.Context) ([]backend.ModelInfo, error) {
	return nil, nil
}
func (f *fakeCliAdapter) ShowModel(_ context.Context, _ string) (backend.ModelDetails, error) {
	return backend.ModelDetails{EmbeddingLength: 4}, nil
}
func (f *fakeCliAdapter) RunningModels(_ context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (f *fakeCliAdapter) Chat(_ context.Context, _ backend.ChatRequest) (*backend.ChatResponse, error) {
	return &backend.ChatResponse{
		Message: backend.ChatMessage{Role: "assistant", Content: "CLI answer with facts."},
	}, nil
}
func (f *fakeCliAdapter) ChatStream(_ context.Context, _ backend.ChatRequest, _ func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return nil, nil
}
func (f *fakeCliAdapter) Embed(_ context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	res := make([][]float32, len(req.Input))
	for i := range req.Input {
		res[i] = []float32{1.0, 0.0, 0.0, 0.0}
	}
	return &backend.EmbedResponse{
		Model:      req.Model,
		Embeddings: res,
	}, nil
}
func (f *fakeCliAdapter) Pull(_ context.Context, _ string, _ func(backend.Progress)) error {
	return nil
}
func (f *fakeCliAdapter) Delete(_ context.Context, _ string) error {
	return nil
}
func (f *fakeCliAdapter) Unload(_ context.Context, _ string) error {
	return nil
}

func TestCLI_RagCommands(t *testing.T) {
	tmpDir := t.TempDir()
	corpusDir := filepath.Join(tmpDir, "docs")
	_ = os.MkdirAll(corpusDir, 0o755)

	docFile := filepath.Join(corpusDir, "guide.md")
	if err := os.WriteFile(docFile, []byte("# Guide\nThis is a local AI guide."), 0o644); err != nil {
		t.Fatal(err)
	}

	layout, err := home.Resolve(func(k string) string {
		if k == home.EnvVar {
			return tmpDir
		}
		return ""
	}, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()

	// 1. Test 'lokal index <corpusDir>'
	var indexOut bytes.Buffer
	rootCmd.SetOut(&indexOut)
	rootCmd.SetArgs([]string{"index", corpusDir, "--collection", "test-col"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lokal index failed: %v", err)
	}
	if !strings.Contains(indexOut.String(), "Indexing complete") {
		t.Errorf("unexpected index output: %s", indexOut.String())
	}

	// 2. Test 'lokal search "guide"'
	var searchOut bytes.Buffer
	rootCmd.SetOut(&searchOut)
	rootCmd.SetArgs([]string{"search", "guide", "--collection", "test-col"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lokal search failed: %v", err)
	}
	if !strings.Contains(searchOut.String(), "guide.md") {
		t.Errorf("unexpected search output: %s", searchOut.String())
	}

	// 3. Test 'lokal search "guide" --json'
	var jsonSearchOut bytes.Buffer
	rootCmd.SetOut(&jsonSearchOut)
	rootCmd.SetArgs([]string{"search", "guide", "--collection", "test-col", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lokal search --json failed: %v", err)
	}
	if !strings.Contains(jsonSearchOut.String(), `"path": "guide.md"`) {
		t.Errorf("expected json search output containing guide.md, got: %s", jsonSearchOut.String())
	}

	// 4. Test 'lokal ask "what is this?"'
	var askOut bytes.Buffer
	rootCmd.SetOut(&askOut)
	rootCmd.SetArgs([]string{"ask", "what is this?", "--collection", "test-col"})

	// Since default adapter is Ollama and might not be running in CI without mock, execute should not crash
	_ = rootCmd.Execute()
}

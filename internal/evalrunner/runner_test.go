package evalrunner

import (
	"context"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
)

type mockEvalAdapter struct {
	chatFunc func(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error)
}

func (m *mockEvalAdapter) Name() string                                { return "mock" }
func (m *mockEvalAdapter) Endpoint() string                            { return "http://mock" }
func (m *mockEvalAdapter) Ping(ctx context.Context) error              { return nil }
func (m *mockEvalAdapter) Version(ctx context.Context) (string, error) { return "1.0.0", nil }
func (m *mockEvalAdapter) ListModels(ctx context.Context) ([]backend.ModelInfo, error) {
	return nil, nil
}
func (m *mockEvalAdapter) ShowModel(ctx context.Context, model string) (backend.ModelDetails, error) {
	return backend.ModelDetails{}, nil
}
func (m *mockEvalAdapter) RunningModels(ctx context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (m *mockEvalAdapter) Chat(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, req)
	}
	return &backend.ChatResponse{
		Message: backend.ChatMessage{
			Role:    "assistant",
			Content: "mock response: hello world",
		},
		PromptEvalCount: 10,
		EvalCount:       20,
	}, nil
}
func (m *mockEvalAdapter) ChatStream(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return m.Chat(ctx, req)
}
func (m *mockEvalAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	return nil, nil
}
func (m *mockEvalAdapter) Pull(ctx context.Context, model string, onProgress func(backend.Progress)) error {
	return nil
}
func (m *mockEvalAdapter) Delete(ctx context.Context, model string) error { return nil }
func (m *mockEvalAdapter) Unload(ctx context.Context, model string) error { return nil }

func TestRunner_AgentCommandExecution(t *testing.T) {
	runner := NewRunner(nil, "default-model")

	spec := &TaskSpec{
		ID:           "eval-1",
		Name:         "File Creation Eval",
		AgentCommand: "echo 'hello from agent' > generated.txt",
		SetupFiles: map[string]string{
			"seed.txt": "seed data",
		},
		Assertions: SpecAssertions{
			FileExists: []string{"generated.txt", "seed.txt"},
			FileContains: map[string]string{
				"generated.txt": "hello from agent",
			},
			CommandExitZero: []string{
				"grep -q 'hello from agent' generated.txt",
			},
		},
	}

	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}

	if !result.Passed {
		t.Fatalf("expected run to pass, got failed with error: %s (assertions: %+v)", result.Error, result.Assertions)
	}

	if len(result.Assertions) != 4 {
		t.Errorf("expected 4 assertions evaluated, got %d", len(result.Assertions))
	}
}

func TestRunner_ModelInferenceAndOutputAssertions(t *testing.T) {
	mockAdapter := &mockEvalAdapter{}
	runner := NewRunner(mockAdapter, "qwen2.5-coder-1.5b")

	spec := &TaskSpec{
		ID:     "eval-prompt-1",
		Name:   "Model Output Check",
		Prompt: "Say hello",
		Assertions: SpecAssertions{
			OutputContains: []string{"mock response", "hello world"},
		},
	}

	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}

	if !result.Passed {
		t.Fatalf("expected run to pass, got failed: %s (assertions: %+v)", result.Error, result.Assertions)
	}

	if result.PromptTokens != 10 || result.EvalTokens != 20 {
		t.Errorf("unexpected tokens: prompt=%d, eval=%d", result.PromptTokens, result.EvalTokens)
	}
}

func TestRunner_FailingAssertion(t *testing.T) {
	runner := NewRunner(nil, "default-model")

	spec := &TaskSpec{
		ID:   "eval-fail",
		Name: "Failing Eval",
		SetupFiles: map[string]string{
			"test.txt": "apple",
		},
		Assertions: SpecAssertions{
			FileExists: []string{"nonexistent.txt"},
			FileContains: map[string]string{
				"test.txt": "banana",
			},
			CommandExitZero: []string{
				"false",
			},
		},
	}

	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}

	if result.Passed {
		t.Fatalf("expected run to fail, but it passed")
	}

	failedCount := 0
	for _, a := range result.Assertions {
		if !a.Passed {
			failedCount++
		}
	}

	if failedCount != 3 {
		t.Errorf("expected 3 failed assertions, got %d", failedCount)
	}
}

package evalrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
)

// AssertionResult records the outcome of a single assertion check.
type AssertionResult struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// EvalResult contains the aggregated outcome of a task evaluation run.
type EvalResult struct {
	TaskID       string            `json:"task_id"`
	TaskName     string            `json:"task_name"`
	Passed       bool              `json:"passed"`
	DurationMs   int64             `json:"duration_ms"`
	Assertions   []AssertionResult `json:"assertions"`
	Model        string            `json:"model,omitempty"`
	PromptTokens int               `json:"prompt_tokens,omitempty"`
	EvalTokens   int               `json:"eval_tokens,omitempty"`
	Output       string            `json:"output,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// Runner executes evaluation task specs in bounded scratch environments.
type Runner struct {
	Adapter      backend.Adapter
	DefaultModel string
}

// NewRunner creates an eval runner with an optional backend adapter.
func NewRunner(adapter backend.Adapter, defaultModel string) *Runner {
	return &Runner{
		Adapter:      adapter,
		DefaultModel: defaultModel,
	}
}

// Run executes a TaskSpec and returns the evaluation result.
func (r *Runner) Run(ctx context.Context, spec *TaskSpec) (*EvalResult, error) {
	if spec == nil {
		return nil, fmt.Errorf("task spec is required")
	}

	startTime := time.Now()
	timeoutSec := spec.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	result := &EvalResult{
		TaskID:   spec.ID,
		TaskName: spec.Name,
		Model:    spec.Model,
		Passed:   true,
	}

	if result.Model == "" {
		result.Model = r.DefaultModel
	}

	// 1. Initialize temporary sandbox
	sandbox, err := NewSandbox()
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("init sandbox: %v", err)
		result.DurationMs = time.Since(startTime).Milliseconds()
		return result, nil
	}
	defer sandbox.Cleanup()

	// 2. Setup files
	if len(spec.SetupFiles) > 0 {
		if err := sandbox.Setup(spec.SetupFiles); err != nil {
			result.Passed = false
			result.Error = fmt.Sprintf("sandbox setup files: %v", err)
			result.DurationMs = time.Since(startTime).Milliseconds()
			return result, nil
		}
	}

	var executionOutput string

	// 3. Execute agent command or direct prompt inference
	if spec.AgentCommand != "" {
		extraEnv := []string{
			"LOKAL_EVAL_PROMPT=" + spec.Prompt,
			"LOKAL_EVAL_TASK_ID=" + spec.ID,
		}
		stdout, stderr, exitCode, cmdErr := sandbox.ExecCommand(execCtx, spec.AgentCommand, extraEnv)
		executionOutput = stdout
		if stderr != "" {
			if executionOutput != "" {
				executionOutput += "\n"
			}
			executionOutput += stderr
		}
		if cmdErr != nil || exitCode != 0 {
			result.Error = fmt.Sprintf("agent command exited with code %d: %v", exitCode, cmdErr)
		}
	} else if spec.Prompt != "" && r.Adapter != nil {
		targetModel := result.Model
		if m, ok := catalog.Default().GetModel(targetModel); ok {
			if tag, exists := m.Backends[r.Adapter.Name()]; exists && tag != "" {
				targetModel = tag
			}
		}
		chatReq := backend.ChatRequest{
			Model: targetModel,
			Messages: []backend.ChatMessage{
				{Role: "user", Content: spec.Prompt},
			},
		}
		if spec.MaxTokens > 0 {
			chatReq.Options = &backend.ChatOptions{
				NumCtx: spec.MaxTokens,
			}
		}

		resp, err := r.Adapter.Chat(execCtx, chatReq)
		if err != nil {
			result.Passed = false
			result.Error = fmt.Sprintf("model chat error: %v", err)
		} else {
			executionOutput = resp.Message.Content
			result.PromptTokens = resp.PromptEvalCount
			result.EvalTokens = resp.EvalCount
		}
	}

	result.Output = executionOutput

	// 4. Evaluate assertions
	// Assert FileExists
	for _, relFile := range spec.Assertions.FileExists {
		targetPath := filepath.Join(sandbox.Dir, filepath.Clean(relFile))
		_, statErr := os.Stat(targetPath)
		passed := statErr == nil
		msg := ""
		if !passed {
			msg = fmt.Sprintf("file %s does not exist", relFile)
			result.Passed = false
		}
		result.Assertions = append(result.Assertions, AssertionResult{
			Type:    "file_exists",
			Target:  relFile,
			Passed:  passed,
			Message: msg,
		})
	}

	// Assert FileContains
	for relFile, expectedSubstring := range spec.Assertions.FileContains {
		targetPath := filepath.Join(sandbox.Dir, filepath.Clean(relFile))
		contentBytes, readErr := os.ReadFile(targetPath)
		passed := false
		msg := ""
		if readErr != nil {
			msg = fmt.Sprintf("could not read file %s: %v", relFile, readErr)
			result.Passed = false
		} else if !strings.Contains(string(contentBytes), expectedSubstring) {
			msg = fmt.Sprintf("file %s does not contain expected substring %q", relFile, expectedSubstring)
			result.Passed = false
		} else {
			passed = true
		}
		result.Assertions = append(result.Assertions, AssertionResult{
			Type:    "file_contains",
			Target:  relFile,
			Passed:  passed,
			Message: msg,
		})
	}

	// Assert CommandExitZero
	for _, cmdStr := range spec.Assertions.CommandExitZero {
		stdout, stderr, exitCode, cmdErr := sandbox.ExecCommand(execCtx, cmdStr, nil)
		passed := exitCode == 0 && cmdErr == nil
		msg := ""
		if !passed {
			msg = fmt.Sprintf("command %q exited with code %d: %s %s", cmdStr, exitCode, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
			result.Passed = false
		}
		result.Assertions = append(result.Assertions, AssertionResult{
			Type:    "command_exit_zero",
			Target:  cmdStr,
			Passed:  passed,
			Message: msg,
		})
	}

	// Assert OutputContains
	for _, expectedText := range spec.Assertions.OutputContains {
		passed := strings.Contains(executionOutput, expectedText)
		msg := ""
		if !passed {
			msg = fmt.Sprintf("output does not contain expected text %q", expectedText)
			result.Passed = false
		}
		result.Assertions = append(result.Assertions, AssertionResult{
			Type:    "output_contains",
			Target:  expectedText,
			Passed:  passed,
			Message: msg,
		})
	}

	if result.Error != "" && !result.Passed {
		result.Passed = false
	}

	result.DurationMs = time.Since(startTime).Milliseconds()
	return result, nil
}

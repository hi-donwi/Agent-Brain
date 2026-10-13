package trace

import (
	"os"
	"strings"
	"testing"
)

func TestImportAgentControlLog(t *testing.T) {
	rawJSON := `{
		"sessionId": "agent-ctrl-123",
		"createdAt": "2026-10-13T10:00:00Z",
		"turns": [
			{
				"turnId": "turn-1",
				"model": "qwen2.5-coder-7b",
				"role": "code",
				"prompt": "Write a hello world function in Go",
				"response": "package main\n\nfunc main() {}",
				"durationMs": 450,
				"promptTokens": 25,
				"evalTokens": 30,
				"timestamp": "2026-10-13T10:00:01Z"
			}
		]
	}`

	records, err := ImportAgentControlLog([]byte(rawJSON), "fallback-id")
	if err != nil {
		t.Fatalf("ImportAgentControlLog failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	if rec.SessionID != "agent-ctrl-123" {
		t.Errorf("expected session ID agent-ctrl-123, got %s", rec.SessionID)
	}
	if rec.Model != "qwen2.5-coder-7b" {
		t.Errorf("expected model qwen2.5-coder-7b, got %s", rec.Model)
	}
	if len(rec.Messages) != 1 || rec.Messages[0].Content != "Write a hello world function in Go" {
		t.Errorf("unexpected messages: %+v", rec.Messages)
	}
	if rec.PromptTokens != 25 || rec.EvalTokens != 30 {
		t.Errorf("unexpected tokens: prompt=%d, eval=%d", rec.PromptTokens, rec.EvalTokens)
	}
}

func TestImportClaudeCodeTranscript(t *testing.T) {
	rawJSONL := `
{"step_index": 1, "source": "USER_EXPLICIT", "type": "USER_INPUT", "content": "How do I list files in Go?"}
{"step_index": 2, "source": "MODEL", "type": "PLANNER_RESPONSE", "content": "You can use os.ReadDir.", "tool_calls": [{"name": "list_dir", "result": "main.go"}]}
{"step_index": 3, "source": "USER_EXPLICIT", "type": "USER_INPUT", "content": "Thanks, what about reading a file?"}
{"step_index": 4, "source": "MODEL", "type": "PLANNER_RESPONSE", "content": "You can use os.ReadFile."}
`

	records, err := ImportClaudeCodeTranscript(strings.NewReader(rawJSONL), "claude-session-456")
	if err != nil {
		t.Fatalf("ImportClaudeCodeTranscript failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(records))
	}

	if records[0].SessionID != "claude-session-456" {
		t.Errorf("expected session ID claude-session-456, got %s", records[0].SessionID)
	}
	if len(records[0].Messages) == 0 || records[0].Messages[0].Content != "How do I list files in Go?" {
		t.Errorf("unexpected message in turn 0: %+v", records[0].Messages)
	}
	if records[0].Response != "You can use os.ReadDir." {
		t.Errorf("unexpected response in turn 0: %s", records[0].Response)
	}
	if len(records[0].ToolCalls) != 1 || records[0].ToolCalls[0].Name != "list_dir" {
		t.Errorf("unexpected tool calls in turn 0: %+v", records[0].ToolCalls)
	}

	if records[1].Response != "You can use os.ReadFile." {
		t.Errorf("unexpected response in turn 1: %s", records[1].Response)
	}
}

func TestImportLogFile(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir)

	claudeContent := `{"type": "USER_INPUT", "content": "ping"}
{"type": "PLANNER_RESPONSE", "content": "pong"}
`
	importFile := tempDir + "/test_transcript.jsonl"
	if err := osWriteFile(importFile, []byte(claudeContent)); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}

	count, err := ImportLogFile(store, importFile, "imported-session")
	if err != nil {
		t.Fatalf("ImportLogFile failed: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 record imported, got %d", count)
	}

	records, err := store.ReadSession("imported-session")
	if err != nil {
		t.Fatalf("ReadSession failed: %v", err)
	}

	if len(records) != 1 || records[0].Response != "pong" {
		t.Errorf("unexpected records: %+v", records)
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

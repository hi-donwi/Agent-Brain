package trace

import (
	"time"
)

// SchemaVersion defines the trace record schema version.
const SchemaVersion = 1

// Message represents one conversation turn in a trace request.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
}

// ToolCall captures an invocation of an MCP or local tool.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    string         `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// Record holds a single instrumented turn in a session trace.
type Record struct {
	Version         int        `json:"version"`
	SessionID       string     `json:"session_id"`
	TurnID          string     `json:"turn_id"`
	Timestamp       time.Time  `json:"timestamp"`
	Model           string     `json:"model"`
	Role            string     `json:"role,omitempty"`
	Backend         string     `json:"backend,omitempty"`
	Messages        []Message  `json:"messages,omitempty"`
	Response        string     `json:"response,omitempty"`
	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	DurationMs      int64      `json:"duration_ms"`
	PromptTokens    int        `json:"prompt_tokens"`
	EvalTokens      int        `json:"eval_tokens"`
	TokensPerSecond float64    `json:"tokens_per_second,omitempty"`
	UserFeedback    string     `json:"user_feedback,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// SessionSummary summarizes trace metrics for an entire session.
type SessionSummary struct {
	SessionID       string    `json:"session_id"`
	RecordCount     int       `json:"record_count"`
	FirstTimestamp  time.Time `json:"first_timestamp"`
	LastTimestamp   time.Time `json:"last_timestamp"`
	Models          []string  `json:"models"`
	TotalPromptToks int       `json:"total_prompt_tokens"`
	TotalEvalToks   int       `json:"total_eval_tokens"`
	TotalDurationMs int64     `json:"total_duration_ms"`
	HasErrors       bool      `json:"has_errors"`
	FilePath        string    `json:"file_path"`
}

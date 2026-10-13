package trace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentControlTurn represents one turn in an Agent-Control session log.
type AgentControlTurn struct {
	TurnID       string         `json:"turnId,omitempty"`
	Model        string         `json:"model,omitempty"`
	Role         string         `json:"role,omitempty"`
	Prompt       string         `json:"prompt,omitempty"`
	Response     string         `json:"response,omitempty"`
	Messages     []Message      `json:"messages,omitempty"`
	ToolCalls    []ToolCall     `json:"toolCalls,omitempty"`
	DurationMs   int64          `json:"durationMs,omitempty"`
	PromptTokens int            `json:"promptTokens,omitempty"`
	EvalTokens   int            `json:"evalTokens,omitempty"`
	Timestamp    string         `json:"timestamp,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// AgentControlSession represents an exported Agent-Control session log file.
type AgentControlSession struct {
	SessionID string             `json:"sessionId"`
	CreatedAt string             `json:"createdAt,omitempty"`
	Turns     []AgentControlTurn `json:"turns"`
}

// ImportAgentControlLog parses an Agent-Control JSON session log.
func ImportAgentControlLog(data []byte, defaultSessionID string) ([]Record, error) {
	var session AgentControlSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse agent-control session: %w", err)
	}

	sessionID := session.SessionID
	if sessionID == "" {
		sessionID = defaultSessionID
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	var records []Record
	for i, turn := range session.Turns {
		turnID := turn.TurnID
		if turnID == "" {
			turnID = fmt.Sprintf("turn-%d", i+1)
		}

		ts := time.Now().UTC()
		if turn.Timestamp != "" {
			if parsed, err := time.Parse(time.RFC3339, turn.Timestamp); err == nil {
				ts = parsed
			}
		}

		msgs := turn.Messages
		if len(msgs) == 0 && turn.Prompt != "" {
			msgs = []Message{{Role: "user", Content: turn.Prompt}}
		}

		rec := Record{
			Version:      SchemaVersion,
			SessionID:    sessionID,
			TurnID:       turnID,
			Timestamp:    ts,
			Model:        turn.Model,
			Role:         turn.Role,
			Messages:     msgs,
			Response:     turn.Response,
			ToolCalls:    turn.ToolCalls,
			DurationMs:   turn.DurationMs,
			PromptTokens: turn.PromptTokens,
			EvalTokens:   turn.EvalTokens,
		}
		records = append(records, rec)
	}

	return records, nil
}

// ClaudeStep represents a raw line from a Claude Code JSONL transcript.
type ClaudeStep struct {
	StepIndex int            `json:"step_index"`
	Source    string         `json:"source"`
	Type      string         `json:"type"`
	Content   string         `json:"content"`
	ToolCalls []ToolCall     `json:"tool_calls,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ImportClaudeCodeTranscript parses a Claude Code JSONL conversation transcript.
func ImportClaudeCodeTranscript(r io.Reader, defaultSessionID string) ([]Record, error) {
	scanner := bufio.NewScanner(r)
	sessionID := defaultSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	var records []Record
	var currentTurn Record
	currentTurn.Version = SchemaVersion
	currentTurn.SessionID = sessionID
	currentTurn.Timestamp = time.Now().UTC()

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var step ClaudeStep
		if err := json.Unmarshal(line, &step); err != nil {
			continue // Skip unparseable lines
		}

		switch step.Type {
		case "USER_INPUT":
			if len(currentTurn.Messages) > 0 || currentTurn.Response != "" {
				if currentTurn.TurnID == "" {
					currentTurn.TurnID = fmt.Sprintf("turn-%d", len(records)+1)
				}
				records = append(records, currentTurn)
				currentTurn = Record{
					Version:   SchemaVersion,
					SessionID: sessionID,
					Timestamp: time.Now().UTC(),
				}
			}
			currentTurn.Messages = append(currentTurn.Messages, Message{
				Role:    "user",
				Content: step.Content,
			})

		case "PLANNER_RESPONSE", "ASSISTANT_RESPONSE":
			currentTurn.Response = step.Content
			if len(step.ToolCalls) > 0 {
				currentTurn.ToolCalls = append(currentTurn.ToolCalls, step.ToolCalls...)
			}

		case "TOOL_CALL":
			if len(step.ToolCalls) > 0 {
				currentTurn.ToolCalls = append(currentTurn.ToolCalls, step.ToolCalls...)
			}
		}
	}

	if len(currentTurn.Messages) > 0 || currentTurn.Response != "" {
		if currentTurn.TurnID == "" {
			currentTurn.TurnID = fmt.Sprintf("turn-%d", len(records)+1)
		}
		records = append(records, currentTurn)
	}

	return records, scanner.Err()
}

// ImportLogFile auto-detects file format and imports trace records into target store.
func ImportLogFile(store *Store, filePath string, sessionID string) (int, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("read file %s: %w", filePath, err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	var records []Record

	if ext == ".jsonl" {
		records, err = ImportClaudeCodeTranscript(bytes.NewReader(data), sessionID)
		if err != nil {
			return 0, fmt.Errorf("import JSONL transcript: %w", err)
		}
	} else {
		// Attempt Agent-Control JSON import
		records, err = ImportAgentControlLog(data, sessionID)
		if err != nil {
			// Fallback: try parsing as JSONL
			var fallbackErr error
			records, fallbackErr = ImportClaudeCodeTranscript(bytes.NewReader(data), sessionID)
			if fallbackErr != nil {
				return 0, fmt.Errorf("cannot import %s (neither JSON nor JSONL): %w", filePath, err)
			}
		}
	}

	for _, rec := range records {
		recCopy := rec
		if err := store.WriteRecord(&recCopy); err != nil {
			return 0, fmt.Errorf("write imported record: %w", err)
		}
	}

	return len(records), nil
}

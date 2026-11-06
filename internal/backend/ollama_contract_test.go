package backend_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
)

func TestOllamaToolReplayAndStreamCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"native tool", "{\"message\":{\"role\":\"assistant\",\"content\":\"\",\"tool_calls\":[{\"function\":{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}}]},\"done\":false}\n{\"message\":{\"content\":\"done\"},\"done\":true,\"done_reason\":\"length\"}\n", false},
		{"truncated stream", "{\"message\":{\"content\":\"partial\"},\"done\":false}\n", true},
		{"malformed stream", "not json\n", true},
		{"engine error", "{\"error\":\"out of memory\"}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []map[string]any `json:"messages"`
					Truncate *bool            `json:"truncate"`
					Shift    *bool            `json:"shift"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Messages[1]["tool_name"] != "read_file" {
					t.Errorf("OpenAI call ID not resolved to native tool_name: %+v", body.Messages[1])
				}
				if body.Truncate == nil || *body.Truncate || body.Shift == nil || *body.Shift {
					t.Error("native runtime may silently truncate or shift context")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer engine.Close()
			a := backend.NewOllama(engine.URL, engine.Client())
			chunks := 0
			resp, err := a.ChatStream(context.Background(), backend.ChatRequest{Model: "devstral-small-2:24b", Messages: []backend.ChatMessage{
				{Role: "assistant", ToolCalls: []backend.ToolCall{{ID: "c1", Function: backend.ToolFunction{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}}},
				{Role: "tool", ToolCallID: "c1", Content: "package main"},
			}}, func(chunk backend.ChatChunk) error { chunks++; return nil })
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError %v", err, tc.wantError)
			}
			if !tc.wantError {
				encoded, _ := json.Marshal(resp)
				if chunks != 2 || len(resp.Message.ToolCalls) != 1 {
					t.Fatalf("native streamed tool call lost: %s", encoded)
				}
			}
		})
	}
}

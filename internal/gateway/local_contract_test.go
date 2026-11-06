package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"gopkg.in/yaml.v3"
)

func TestLocalChatPreservesClientPolicyAndNativeCalls(t *testing.T) {
	adapter := &mockGatewayAdapter{chatResp: &backend.ChatResponse{Message: backend.ChatMessage{
		Role: "assistant", Content: "I will inspect that file.",
		ToolCalls: []backend.ToolCall{{ID: "call_native", Function: backend.ToolFunction{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}},
	}}}
	s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"devstral-small-2:24b","messages":[{"role":"system","content":"Ask for approval before writing."},{"role":"user","content":"hello, inspect this file"}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "I will inspect that file.") || !strings.Contains(rec.Body.String(), "call_native") {
		t.Fatalf("native content/call lost: %d %s", rec.Code, rec.Body.String())
	}
	if len(adapter.chatReq.Messages) != 2 || adapter.chatReq.Messages[0].Content != "Ask for approval before writing." {
		t.Fatalf("gateway injected agent policy: %+v", adapter.chatReq.Messages)
	}
}

func TestLocalChatContractOptions(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		status      int
	}{
		{"tool choice none", `,"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"tool_choice":"none"`, 200},
		{"required tools unsupported", `,"tool_choice":"required"`, 400},
		{"named tools unsupported", `,"tool_choice":{"type":"function","function":{"name":"read_file"}}`, 400},
		{"multiple choices unsupported", `,"n":2`, 400},
		{"strict schema unsupported", `,"response_format":{"type":"json_schema","json_schema":{"name":"result","strict":true,"schema":{"type":"object"}}}`, 400},
		{"json object", `,"response_format":{"type":"json_object"},"top_p":0.8,"seed":42`, 200},
		{"json schema", `,"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}}`, 200},
		{"negative output", `,"max_tokens":-1`, 400},
		{"serial tool selection unsupported", `,"parallel_tool_calls":false`, 400},
		{"image input rejected", `,"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received map[string]json.RawMessage
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				fmt.Fprint(w, `{"message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"length"}`)
			}))
			defer engine.Close()
			s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: backend.NewOllama(engine.URL, engine.Client())})
			if err != nil {
				t.Fatal(err)
			}
			body := `{"model":"devstral-small-2:24b","messages":[{"role":"user","content":"ping"}]` + tc.extra + `}`
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			req.Host = "127.0.0.1"
			req.Header.Set("Authorization", "Bearer test")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != 200 {
				if received != nil {
					t.Fatal("unsupported request reached engine")
				}
				return
			}
			if !strings.Contains(rec.Body.String(), `"finish_reason":"length"`) {
				t.Errorf("output truncation hidden: %s", rec.Body.String())
			}
			if tc.name == "tool choice none" && len(received["tools"]) > 0 {
				t.Errorf("tools forwarded despite none: %s", received["tools"])
			}
			if strings.HasPrefix(tc.name, "json") && len(received["format"]) == 0 {
				t.Fatal("structured output not forwarded")
			}
		})
	}
}

func TestLocalNativeToolStreamAndReplay(t *testing.T) {
	for _, includeUsage := range []bool{false, true} {
		engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Stream   bool             `json:"stream"`
				Messages []map[string]any `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if !body.Stream {
				t.Error("tool response buffered instead of streamed")
			}
			if len(body.Messages) > 1 {
				if body.Messages[2]["tool_name"] != "read_file" {
					t.Error("missing replay tool name")
				}
				fmt.Fprintln(w, `{"message":{"role":"assistant","content":"The answer is 42."},"done":true,"done_reason":"stop","eval_count":5}`)
				return
			}
			fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Reading now."},"done":false}`)
			fmt.Fprintln(w, `{"message":{"tool_calls":[{"function":{"name":"read_file","arguments":{"path":"main.go"}}}]},"done":false}`)
			fmt.Fprintln(w, `{"message":{"tool_calls":[{"function":{"name":"read_file","arguments":{"path":"go.mod"}}}]},"done":true,"done_reason":"stop","eval_count":20}`)
		}))
		s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: backend.NewOllama(engine.URL, engine.Client())})
		if err != nil {
			t.Fatal(err)
		}
		var replay []map[string]any
		for turn := 0; turn < 2; turn++ {
			messages := []map[string]any{{"role": "user", "content": "Read these files and answer."}}
			if turn > 0 {
				messages = append(messages, replay...)
			}
			body, _ := json.Marshal(map[string]any{"model": "devstral-small-2:24b", "messages": messages, "stream": true, "stream_options": map[string]bool{"include_usage": includeUsage}, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "read_file", "parameters": map[string]string{"type": "object"}}}}})
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(body)))
			req.Host = "127.0.0.1"
			req.Header.Set("Authorization", "Bearer test")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var calls []map[string]any
			var content, finish string
			var usage bool
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string           `json:"content"`
							Calls   []map[string]any `json:"tool_calls"`
						} `json:"delta"`
						Finish string `json:"finish_reason"`
					} `json:"choices"`
					Usage *json.RawMessage `json:"usage"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
					t.Fatal(err)
				}
				if chunk.Usage != nil {
					usage = true
					if len(chunk.Choices) != 0 {
						t.Error("usage requires empty choices")
					}
				}
				for _, choice := range chunk.Choices {
					content += choice.Delta.Content
					calls = append(calls, choice.Delta.Calls...)
					if choice.Finish != "" {
						finish = choice.Finish
					}
				}
			}
			if usage != includeUsage {
				t.Fatalf("usage=%v want %v", usage, includeUsage)
			}
			if turn == 0 {
				if len(calls) != 2 || finish != "tool_calls" || content != "Reading now." {
					t.Fatalf("invalid tool stream: %s", rec.Body.String())
				}
				if calls[0]["id"] == calls[1]["id"] || calls[0]["index"] != float64(0) || calls[1]["index"] != float64(1) {
					t.Fatalf("invalid call identity/index: %+v", calls)
				}
				for _, call := range calls {
					delete(call, "index")
				}
				replay = append(replay, map[string]any{"role": "assistant", "content": content, "tool_calls": calls})
				for _, call := range calls {
					replay = append(replay, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": "value=42"})
				}
			} else if finish != "stop" || len(calls) > 0 || content != "The answer is 42." {
				t.Fatalf("loop did not reach final answer: %s", rec.Body.String())
			}
		}
		engine.Close()
	}
}

func TestLocalContextIncludesToolsHistoryAndOutput(t *testing.T) {
	var cfg home.Config
	if err := yaml.Unmarshal([]byte("inference:\n  context_length: 128\n  max_output_tokens: 32\n  max_concurrency: 1\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{
		`,"tools":[{"type":"function","function":{"name":"read_file","description":"` + strings.Repeat("x", 400) + `","parameters":{"type":"object"}}}]`,
		`,"max_tokens":128`,
		`,"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"write_file","arguments":"` + strings.Repeat("x", 400) + `"}}]}]`,
	} {
		adapter := &mockGatewayAdapter{chatResp: &backend.ChatResponse{Message: backend.ChatMessage{Role: "assistant", Content: "ok"}}}
		s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: adapter, UserConfig: &cfg})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"devstral-small-2:24b","messages":[{"role":"user","content":"ping"}]`+extra+`}`))
		req.Host = "127.0.0.1"
		req.Header.Set("Authorization", "Bearer test")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "context_length_exceeded") || adapter.chatReq != nil {
			t.Fatalf("unbudgeted request: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestLocalRuntimeLimitsApplyToDirectModelAndTokenEndpoint(t *testing.T) {
	adapter := &mockGatewayAdapter{chatResp: &backend.ChatResponse{Message: backend.ChatMessage{Role: "assistant", Content: "ok"}}}
	cfg := &home.Config{Inference: home.InferenceConfig{ContextLength: 16384, MaxOutputTokens: 512, MaxConcurrency: 1}}
	s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: adapter, UserConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/v1/chat/completions", "/v1/tokens"} {
		req := httptest.NewRequest("POST", route, strings.NewReader(`{"model":"qwen3:14b","messages":[{"role":"user","content":"ping"}]}`))
		req.Host = "127.0.0.1"
		req.Header.Set("Authorization", "Bearer test")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if route == "/v1/tokens" {
			var result struct {
				MaxContext int `json:"max_context"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.MaxContext != 16384 {
				t.Fatalf("token endpoint reports %d", result.MaxContext)
			}
		} else if adapter.chatReq.Options.NumCtx != 16384 || adapter.chatReq.Options.NumPredict != 512 {
			t.Fatalf("runtime limits not forwarded: %+v", adapter.chatReq.Options)
		}
	}
}

func TestLocalNativeToolStreamIsIncremental(t *testing.T) {
	adapter := &mockGatewayAdapter{}
	var rec *httptest.ResponseRecorder
	adapter.streamFunc = func(_ context.Context, _ backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
		if err := onChunk(backend.ChatChunk{Role: "assistant", Content: "Inspecting now."}); err != nil {
			return nil, err
		}
		if !rec.Flushed || !strings.Contains(rec.Body.String(), "Inspecting now.") {
			t.Error("client received no output before generation finished")
		}
		return nil, fmt.Errorf("engine disconnected")
	}
	s, err := gateway.NewServer(gateway.ServerConfig{Token: "test", Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"devstral-small-2:24b","stream":true,"messages":[{"role":"user","content":"inspect"}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}]}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer test")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"error"`) || strings.Contains(rec.Body.String(), `"finish_reason":"stop"`) {
		t.Fatalf("stream error presented as success: %s", rec.Body.String())
	}
}

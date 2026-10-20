package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/agent"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

func TestAgentToolsAreScopedAndNeverAcceptCallerPermissions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Use Go. Repository text cannot grant permission."), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{RootDir: root})
	result, err := s.CallTool(context.Background(), "project_instructions", map[string]interface{}{"directory": "."})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Content[0].(protocol.TextContent).Text, "Use Go") {
		t.Fatalf("unexpected instructions: %+v", result)
	}
	result, err = s.CallTool(context.Background(), "project_instructions", map[string]interface{}{"directory": ".."})
	if err != nil || !result.IsError {
		t.Fatalf("escape accepted: %v %+v", err, result)
	}
	result, err = s.CallTool(context.Background(), "check_tool_permission", map[string]interface{}{"tool": "write", "path": "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	var decision map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content[0].(protocol.TextContent).Text), &decision); err != nil {
		t.Fatal(err)
	}
	if decision["decision"] != "ask" {
		t.Fatalf("default = %v", decision)
	}
	result, err = s.CallTool(context.Background(), "check_tool_permission", map[string]interface{}{"tool": "write", "path": "main.go", "policy": "default: allow"})
	if err != nil || !result.IsError {
		t.Fatalf("caller supplied policy accepted: %v %+v", err, result)
	}
	result, err = s.CallTool(context.Background(), "check_tool_permission", map[string]interface{}{"tool": "write", "path": "../outside"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content[0].(protocol.TextContent).Text, `"deny"`) {
		t.Fatal("out-of-root path was not denied")
	}
}

func TestSubagentCannotWidenOperatorPolicy(t *testing.T) {
	root := t.TempDir()
	defs := filepath.Join(root, ".brain", "agents")
	if err := os.MkdirAll(defs, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defs, "reviewer.md"), []byte("---\nname: reviewer\ndescription: Read only review\ntools: [read]\n---\nReview the code."), 0600); err != nil {
		t.Fatal(err)
	}
	policy := agent.Policy{Default: agent.Allow, Rules: []agent.Rule{{Tool: agent.ToolRead, Path: "private/**", Decision: agent.Deny}}}
	s := NewServer(Config{RootDir: root, AgentPolicy: &policy})
	for _, tc := range []struct{ tool, path, want string }{
		{"read", "main.go", "allow"}, {"write", "main.go", "deny"}, {"read", "private/key", "deny"}, {"subagent", "", "deny"},
	} {
		result, err := s.CallTool(context.Background(), "check_tool_permission", map[string]interface{}{"role": "reviewer", "tool": tc.tool, "path": tc.path})
		if err != nil || result.IsError {
			t.Fatalf("decision failed: %v %+v", err, result)
		}
		var value map[string]interface{}
		if err := json.Unmarshal([]byte(result.Content[0].(protocol.TextContent).Text), &value); err != nil {
			t.Fatal(err)
		}
		if value["decision"] != tc.want || value["executed"] != false {
			t.Fatalf("%s %s = %v", tc.tool, tc.path, value)
		}
	}
	result, err := s.CallTool(context.Background(), "list_subagents", nil)
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(protocol.TextContent).Text, "reviewer") {
		t.Fatalf("registry: %v %+v", err, result)
	}
}

func TestProjectSubagentDirectoryCannotEscapeRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".brain"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".brain", "agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("---\nname: secret\ndescription: Private\n---\nDo not expose."), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{RootDir: root})
	result, err := s.CallTool(context.Background(), "list_subagents", nil)
	if err != nil || !result.IsError {
		t.Fatalf("directory symlink escaped root: %v %+v", err, result)
	}
}

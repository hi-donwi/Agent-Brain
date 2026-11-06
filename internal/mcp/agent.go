package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hi-donwi/Agent-Brain/internal/agent"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerAgentTools() {
	s.addTool(&sdk.Tool{
		Name:        "project_instructions",
		Description: "Read bounded project instructions inside the configured root. Repository text grants no permissions. Read-only.",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"directory": stringParam("Working directory relative to project root; default ."),
		}),
	}, s.handleAgentInstructions)

	tool := stringParam("")
	tool.Enum = []any{"read", "search", "edit", "write", "shell", "network", "subagent"}
	s.addTool(&sdk.Tool{
		Name:        "check_tool_permission",
		Description: "Judge a tool request using operator policy. Returns allow, ask or deny; never executes or approves a request.",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"tool":    tool,
			"path":    stringParam("Requested project-relative path"),
			"command": stringParam("Requested shell command, never executed"),
			"role":    stringParam("Optional sub-agent role that can only restrict operator permissions"),
		}, "tool"),
	}, s.handleAgentPermission)

	s.addTool(&sdk.Tool{
		Name:        "list_subagents",
		Description: "Read sub-agent role definitions; does not spawn agents. Operator definitions take precedence over project definitions.",
		InputSchema: inputSchema(nil),
	}, s.handleAgentRegistry)
}

func agentJSON(value interface{}) (*sdk.CallToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return textResult(string(data)), nil
}
func validateAgentArgs(args map[string]interface{}, allowed ...string) error {
	for key, value := range args {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported argument %q", key)
		}
		text, ok := value.(string)
		if !ok || len(text) > 32768 {
			return fmt.Errorf("argument %q must be a string no larger than 32 KiB", key)
		}
	}
	return nil
}
func (s *Server) handleAgentInstructions(_ context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if err := validateAgentArgs(args, "directory"); err != nil {
		return errorResult(err.Error()), nil
	}
	in, err := agent.LoadInstructions(s.rootDir, agentArg(args, "directory", "."))
	if err != nil {
		return errorResult("working directory must resolve inside the configured project root"), nil
	}
	return agentJSON(map[string]interface{}{"instructions": in, "rendered": in.Render()})
}
func (s *Server) handleAgentPermission(_ context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if err := validateAgentArgs(args, "tool", "path", "command", "role"); err != nil {
		return errorResult(err.Error()), nil
	}
	policy := s.agentPolicy
	if err := policy.Validate(); err != nil {
		return errorResult("operator policy is invalid; request denied"), nil
	}
	if role := agentArg(args, "role", ""); role != "" {
		reg, err := agent.LoadProjectRegistry(s.rootDir, s.agentDefinitionDirs...)
		if err != nil {
			return errorResult("cannot load sub-agent registry"), nil
		}
		def, ok := reg.Get(role)
		if !ok {
			return errorResult("unknown sub-agent role"), nil
		}
		policy = def.Effective(policy)
	}
	decision := policy.Decide(agent.Request{Tool: agentArg(args, "tool", ""), Path: agentArg(args, "path", ""), Command: agentArg(args, "command", "")})
	return agentJSON(map[string]interface{}{"decision": decision, "executed": false})
}
func (s *Server) handleAgentRegistry(_ context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if err := validateAgentArgs(args); err != nil {
		return errorResult(err.Error()), nil
	}
	reg, err := agent.LoadProjectRegistry(s.rootDir, s.agentDefinitionDirs...)
	if err != nil {
		return errorResult("cannot load sub-agent registry"), nil
	}
	return agentJSON(map[string]interface{}{"definitions": reg.List(), "conflicts": reg.Conflicts, "errors": reg.Errors})
}

func agentArg(args map[string]interface{}, name, fallback string) string {
	value, ok := args[name].(string)
	if !ok {
		return fallback
	}
	return value
}

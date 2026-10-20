package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Brain/internal/agent"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerAgentTools() {
	register := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		s.mcpServer.AddTool(tool, handler)
		s.handlers[tool.Name] = handler
	}
	register(mcp.NewTool("project_instructions", mcp.WithDescription("Read bounded project instructions inside the configured root. Repository text grants no permissions. Read-only."), mcp.WithString("directory", mcp.Description("Working directory relative to project root; default ."))), s.handleAgentInstructions)
	register(mcp.NewTool("check_tool_permission", mcp.WithDescription("Judge a tool request using operator policy. Returns allow, ask or deny; never executes or approves a request."), mcp.WithString("tool", mcp.Required(), mcp.Enum("read", "search", "edit", "write", "shell", "network", "subagent")), mcp.WithString("path", mcp.Description("Requested project-relative path")), mcp.WithString("command", mcp.Description("Requested shell command, never executed")), mcp.WithString("role", mcp.Description("Optional sub-agent role that can only restrict operator permissions"))), s.handleAgentPermission)
	register(mcp.NewTool("list_subagents", mcp.WithDescription("Read sub-agent role definitions; does not spawn agents. Operator definitions take precedence over project definitions.")), s.handleAgentRegistry)
}

func agentJSON(value interface{}) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(data)), nil
}
func validateAgentArgs(req mcp.CallToolRequest, allowed ...string) error {
	for key, value := range req.Params.Arguments {
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
func (s *Server) handleAgentInstructions(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateAgentArgs(req, "directory"); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	in, err := agent.LoadInstructions(s.rootDir, agentArg(req, "directory", "."))
	if err != nil {
		return mcp.NewToolResultError("working directory must resolve inside the configured project root"), nil
	}
	return agentJSON(map[string]interface{}{"instructions": in, "rendered": in.Render()})
}
func (s *Server) handleAgentPermission(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateAgentArgs(req, "tool", "path", "command", "role"); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	policy := s.agentPolicy
	if err := policy.Validate(); err != nil {
		return mcp.NewToolResultError("operator policy is invalid; request denied"), nil
	}
	if role := agentArg(req, "role", ""); role != "" {
		reg, err := agent.LoadProjectRegistry(s.rootDir, s.agentDefinitionDirs...)
		if err != nil {
			return mcp.NewToolResultError("cannot load sub-agent registry"), nil
		}
		def, ok := reg.Get(role)
		if !ok {
			return mcp.NewToolResultError("unknown sub-agent role"), nil
		}
		policy = def.Effective(policy)
	}
	decision := policy.Decide(agent.Request{Tool: agentArg(req, "tool", ""), Path: agentArg(req, "path", ""), Command: agentArg(req, "command", "")})
	return agentJSON(map[string]interface{}{"decision": decision, "executed": false})
}
func (s *Server) handleAgentRegistry(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateAgentArgs(req); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	reg, err := agent.LoadProjectRegistry(s.rootDir, s.agentDefinitionDirs...)
	if err != nil {
		return mcp.NewToolResultError("cannot load sub-agent registry"), nil
	}
	return agentJSON(map[string]interface{}{"definitions": reg.List(), "conflicts": reg.Conflicts, "errors": reg.Errors})
}

func agentArg(req mcp.CallToolRequest, name, fallback string) string {
	value, ok := req.Params.Arguments[name].(string)
	if !ok {
		return fallback
	}
	return value
}

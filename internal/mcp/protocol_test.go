package mcp_test

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	localmcp "github.com/hi-donwi/Agent-Brain/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect drives the server the way an MCP host does: over a transport, through the
// SDK's client, so arguments and results cross JSON-RPC instead of a Go call.
func connect(t *testing.T, srv *localmcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "protocol-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callText(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("call %s: want one content item, got %d", name, len(result.Content))
	}
	if _, ok := result.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("call %s: want text content, got %T", name, result.Content[0])
	}
	return result
}

func text(result *mcp.CallToolResult) string {
	return result.Content[0].(*mcp.TextContent).Text
}

func TestProtocolAdvertisesOnlyToolsAndResources(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()
	session := connect(t, srv)

	init := session.InitializeResult()
	if init.ServerInfo == nil || init.ServerInfo.Name != "lokal-test" || init.ServerInfo.Version != "1.0.0" {
		t.Fatalf("server info = %+v", init.ServerInfo)
	}
	caps := init.Capabilities
	if caps.Tools == nil || caps.Resources == nil {
		t.Fatalf("tools and resources must be advertised: %+v", caps)
	}
	// Fixed lists and no log messages: nothing to notify, so nothing is promised.
	if caps.Tools.ListChanged || caps.Resources.ListChanged || caps.Resources.Subscribe {
		t.Fatalf("list-changed or subscribe advertised: %+v %+v", caps.Tools, caps.Resources)
	}
	if caps.Logging != nil || caps.Prompts != nil || caps.Completions != nil {
		t.Fatalf("unused capability advertised: %+v", caps)
	}
}

func TestProtocolListsEveryReadOnlyToolWithItsSchema(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()
	session := connect(t, srv)

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	type property struct {
		Type string   `json:"type"`
		Enum []string `json:"enum"`
	}
	type schema struct {
		Type       string              `json:"type"`
		Required   []string            `json:"required"`
		Properties map[string]property `json:"properties"`
	}
	schemas := map[string]schema{}
	for _, tool := range listed.Tools {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s schema: %v", tool.Name, err)
		}
		var decoded schema
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("%s schema %s: %v", tool.Name, data, err)
		}
		if decoded.Type != "object" {
			t.Fatalf("%s schema type = %q", tool.Name, decoded.Type)
		}
		schemas[tool.Name] = decoded
	}

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{
		"check_tool_permission", "find_symbol", "get_skill", "list_models", "list_skills", "list_subagents",
		"project_commands", "project_instructions", "repo_map", "search_code", "search_knowledge",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	required := map[string][]string{
		"search_knowledge": {"query"}, "get_skill": {"name"}, "find_symbol": {"name"},
		"search_code": {"pattern"}, "check_tool_permission": {"tool"},
	}
	for name, s := range schemas {
		if !slices.Equal(s.Required, required[name]) {
			t.Errorf("%s required = %v, want %v", name, s.Required, required[name])
		}
	}
	if got := schemas["repo_map"].Properties["tokens"].Type; got != "number" {
		t.Errorf("repo_map tokens type = %q", got)
	}
	tools := schemas["check_tool_permission"].Properties["tool"].Enum
	if !slices.Equal(tools, []string{"read", "search", "edit", "write", "shell", "network", "subagent"}) {
		t.Errorf("check_tool_permission tool enum = %v", tools)
	}
}

func TestProtocolCallsToolsOverJSONRPC(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()
	session := connect(t, srv)

	if got := text(callText(t, session, "get_skill", map[string]any{"name": "test-skill"})); !strings.Contains(got, "Follow step 1 and step 2.") {
		t.Errorf("get_skill = %s", got)
	}
	// A JSON number arrives as float64, the type the handlers read.
	if got := text(callText(t, session, "repo_map", map[string]any{"tokens": 1000})); !strings.Contains(got, "Worker") {
		t.Errorf("repo_map = %s", got)
	}
	if result := callText(t, session, "search_knowledge", map[string]any{}); !result.IsError {
		t.Error("search_knowledge without query must be a tool error")
	}
	if result := callText(t, session, "list_models", nil); result.IsError || !strings.Contains(text(result), "llama3.2:3b") {
		t.Errorf("list_models without arguments = %+v", result)
	}

	// The operator's policy decides; an argument the caller adds cannot.
	decision := callText(t, session, "check_tool_permission", map[string]any{"tool": "write", "path": "main.go"})
	var value map[string]any
	if err := json.Unmarshal([]byte(text(decision)), &value); err != nil || value["decision"] != "ask" || value["executed"] != false {
		t.Fatalf("decision = %s (%v)", text(decision), err)
	}
	if result := callText(t, session, "check_tool_permission", map[string]any{"tool": "write", "policy": "default: allow"}); !result.IsError {
		t.Fatalf("caller-supplied policy accepted: %s", text(result))
	}

	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "write_file"}); err == nil {
		t.Fatal("an unknown tool must be a protocol error")
	}
}

func TestProtocolServesInstructionFilesAsResources(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()
	session := connect(t, srv)

	listed, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	var agents *mcp.Resource
	for _, resource := range listed.Resources {
		if resource.Name == "AGENTS.md" {
			agents = resource
		}
	}
	if agents == nil {
		t.Fatalf("AGENTS.md not listed: %+v", listed.Resources)
	}
	parsed, err := url.Parse(agents.URI)
	if err != nil || parsed.Scheme != "file" || !strings.HasPrefix(agents.URI, "file:///") || strings.HasPrefix(agents.URI, "file:////") {
		t.Fatalf("resource URI = %q (%v)", agents.URI, err)
	}
	if filepath.Base(filepath.FromSlash(parsed.Path)) != "AGENTS.md" || agents.MIMEType != "text/markdown" {
		t.Fatalf("resource = %+v", agents)
	}

	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: agents.URI})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(read.Contents) != 1 || read.Contents[0].Text != "# Contract\nAgents follow rules." {
		t.Fatalf("contents = %+v", read.Contents)
	}
}

// A root whose name is not URI-safe still yields a valid resource URI; the SDK panics on
// one that does not parse.
func TestInstructionResourceURIEscapesTheRootPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj %zz #1")
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "# Escaped root")
	srv := localmcp.NewServer(localmcp.Config{RootDir: root})
	session := connect(t, srv)

	listed, err := session.ListResources(context.Background(), nil)
	if err != nil || len(listed.Resources) != 1 {
		t.Fatalf("list resources: %v %+v", err, listed)
	}
	uri := listed.Resources[0].URI
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("resource URI %q does not parse: %v", uri, err)
	}
	if want := filepath.ToSlash(filepath.Join(root, "AGENTS.md")); strings.TrimPrefix(parsed.Path, "/") != strings.TrimPrefix(want, "/") {
		t.Fatalf("URI path = %q, want %q", parsed.Path, want)
	}
	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil || read.Contents[0].Text != "# Escaped root" {
		t.Fatalf("read %q: %v %+v", uri, err, read)
	}
}

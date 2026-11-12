package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hi-donwi/Agent-Brain/internal/agent"
	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/codeintel"
	"github.com/hi-donwi/Agent-Brain/internal/rag"
	"github.com/hi-donwi/Agent-Brain/internal/skills"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config configures the read-only lokal MCP server.
type Config struct {
	// AgentPolicy is selected by the operator at startup, never by an MCP tool call.
	AgentPolicy         *agent.Policy
	AgentDefinitionDirs []string
	Name                string
	Version             string
	RootDir             string
	Searcher            *rag.Searcher
	SkillsMgr           *skills.Manager
	Adapter             backend.Adapter
	Aliases             map[string]string
	SkipResources       bool // Used for metadata-only inventory without reading project files.
}

// ToolInfo is the public, read-only descriptor exposed by MCP inventory.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only"`
}

// toolHandler is the body of one tool: the call's arguments as a JSON object in, a result
// out. A failure the caller should see is a result with IsError set; a Go error is a
// protocol error.
type toolHandler func(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error)

// Server exposes lokal's knowledge, skills, models, and code intelligence over MCP.
type Server struct {
	agentPolicy         agent.Policy
	agentDefinitionDirs []string
	mcpServer           *sdk.Server
	rootDir             string
	searcher            *rag.Searcher
	skillsMgr           *skills.Manager
	adapter             backend.Adapter
	aliases             map[string]string
	handlers            map[string]toolHandler
	toolInfo            []ToolInfo
}

// NewServer registers read-only MCP tools and resources and builds an MCP server instance.
func NewServer(cfg Config) *Server {
	name := cfg.Name
	if name == "" {
		name = "lokal"
	}
	ver := cfg.Version
	if ver == "" {
		ver = "0.0.0-dev"
	}
	root := cfg.RootDir
	if root == "" {
		root, _ = os.Getwd()
	}

	// The tool and resource lists are fixed at startup, so no list-changed notifications,
	// and the server sends no log messages: neither capability is advertised.
	mcpSrv := sdk.NewServer(&sdk.Implementation{Name: name, Version: ver}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{
			Tools:     &sdk.ToolCapabilities{},
			Resources: &sdk.ResourceCapabilities{},
		},
	})

	s := &Server{
		mcpServer: mcpSrv,
		rootDir:   filepath.Clean(root),
		searcher:  cfg.Searcher,
		skillsMgr: cfg.SkillsMgr,
		adapter:   cfg.Adapter,
		aliases:   cfg.Aliases,
		handlers:  make(map[string]toolHandler),
	}

	s.agentPolicy = agent.Policy{Root: s.rootDir}
	if cfg.AgentPolicy != nil {
		s.agentPolicy = *cfg.AgentPolicy
		s.agentPolicy.Rules = append([]agent.Rule(nil), cfg.AgentPolicy.Rules...)
		s.agentPolicy.Root = s.rootDir
	}
	s.agentDefinitionDirs = append([]string(nil), cfg.AgentDefinitionDirs...)
	s.registerAgentTools()
	s.registerTools()
	if !cfg.SkipResources {
		s.registerResources()
	}
	return s
}

// CallTool allows programmatic, in-process invocation of registered tools.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*sdk.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return handler(ctx, args)
}

// MCPServer returns the underlying MCP server handle.
func (s *Server) MCPServer() *sdk.Server {
	return s.mcpServer
}

// ToolCatalog returns the registered tool names/descriptions without starting the MCP process.
func (s *Server) ToolCatalog() []ToolInfo {
	return append([]ToolInfo(nil), s.toolInfo...)
}

// ServeStdio serves MCP over stdin and stdout. It blocks until the client closes stdin or
// the process receives SIGINT or SIGTERM.
func (s *Server) ServeStdio() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.serve(ctx, &sdk.StdioTransport{})
}

// serve runs one session over transport until the client disconnects or ctx ends. The
// context ending is how a signal stops the server, which is a normal shutdown, not a
// failure.
func (s *Server) serve(ctx context.Context, transport sdk.Transport) error {
	err := s.mcpServer.Run(ctx, transport)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// addTool registers a tool for MCP clients and for CallTool. The SDK hands a low-level
// handler raw arguments without validating them; each handler checks its own.
func (s *Server) addTool(tool *sdk.Tool, handler toolHandler) {
	s.mcpServer.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args, err := decodeArguments(req.Params.Arguments)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return handler(ctx, args)
	})
	s.handlers[tool.Name] = handler
	s.toolInfo = append(s.toolInfo, ToolInfo{Name: tool.Name, Description: tool.Description, ReadOnly: true})
}

// decodeArguments reads tools/call arguments, which MCP defines as a JSON object. Absent
// arguments are an empty object; JSON numbers decode as float64.
func decodeArguments(raw json.RawMessage) (map[string]interface{}, error) {
	args := map[string]interface{}{}
	if len(raw) == 0 {
		return args, nil
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object")
	}
	if decoded == nil {
		return args, nil
	}
	return decoded, nil
}

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func errorResult(text string) *sdk.CallToolResult {
	result := textResult(text)
	result.IsError = true
	return result
}

// inputSchema is a tool's input: an object with the given properties, the required ones named.
func inputSchema(properties map[string]*jsonschema.Schema, required ...string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: properties, Required: required}
}

func stringParam(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: description}
}

func numberParam(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "number", Description: description}
}

func (s *Server) registerTools() {
	s.addTool(&sdk.Tool{
		Name:        "search_knowledge",
		Description: "Search indexed documentation and code knowledge with hybrid keyword and semantic retrieval, returning citations and snippets. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"query":      stringParam("Search query or natural language question"),
			"collection": stringParam("Collection name to search in (default: default)"),
			"limit":      numberParam("Maximum number of citations to return (default: 5)"),
		}, "query"),
	}, s.handleSearchKnowledge)

	s.addTool(&sdk.Tool{
		Name:        "list_skills",
		Description: "List all available Agent Skills across project, user, and configured sources. (Read-only)",
		InputSchema: inputSchema(nil),
	}, s.handleListSkills)

	s.addTool(&sdk.Tool{
		Name:        "get_skill",
		Description: "Fetch the markdown instructions for a skill, or inspect a specific reference file. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"name":      stringParam("Name of the skill to fetch"),
			"reference": stringParam("Optional relative path to a reference file under references/ (Progressive Disclosure Step 3)"),
		}, "name"),
	}, s.handleGetSkill)

	s.addTool(&sdk.Tool{
		Name:        "list_models",
		Description: "List available local AI models and configured role aliases. (Read-only)",
		InputSchema: inputSchema(nil),
	}, s.handleListModels)

	s.addTool(&sdk.Tool{
		Name:        "repo_map",
		Description: "Generate a ranked outline of repository files and symbol definitions within a token budget. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"tokens": numberParam("Maximum token budget for the repository outline (default: 2048)"),
			"path":   stringParam("Optional path to repository directory (default: current project)"),
		}),
	}, s.handleRepoMap)

	s.addTool(&sdk.Tool{
		Name:        "find_symbol",
		Description: "Locate definitions and references of a code symbol across the repository. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"name":  stringParam("Symbol name to locate"),
			"limit": numberParam("Maximum references to return (default: 20)"),
			"path":  stringParam("Optional path to repository directory"),
		}, "name"),
	}, s.handleFindSymbol)

	s.addTool(&sdk.Tool{
		Name:        "search_code",
		Description: "Perform regex or text search across repository source code. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"pattern": stringParam("Regular expression or text search pattern"),
			"limit":   numberParam("Maximum matches to return (default: 30)"),
			"path":    stringParam("Optional path to repository directory"),
		}, "pattern"),
	}, s.handleSearchCode)

	s.addTool(&sdk.Tool{
		Name:        "project_commands",
		Description: "Detect project build, test, lint, and run commands as structured data. (Read-only)",
		InputSchema: inputSchema(map[string]*jsonschema.Schema{
			"path": stringParam("Optional path to repository directory"),
		}),
	}, s.handleProjectCommands)
}

// fileURI is the file:// URI of an absolute path, escaped so any path is a valid URI.
func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed // a Windows drive path: file:///C:/...
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func (s *Server) registerResources() {
	if s.rootDir == "" {
		return
	}

	meta, err := codeintel.DetectProject(s.rootDir)
	if err != nil || meta == nil {
		return
	}

	for _, inst := range meta.Instructions {
		uri := fileURI(inst.Path)
		content := inst.Content
		s.mcpServer.AddResource(&sdk.Resource{
			URI:         uri,
			Name:        inst.Name,
			Description: "Project instruction contract: " + inst.Name,
			MIMEType:    "text/markdown",
		}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{
				Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: content}},
			}, nil
		})
	}
}

func (s *Server) handleSearchKnowledge(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if s.searcher == nil {
		return errorResult("searcher is not configured in lokal server"), nil
	}

	queryVal, ok := args["query"]
	if !ok {
		return errorResult("missing required parameter 'query'"), nil
	}
	query, ok := queryVal.(string)
	if !ok || strings.TrimSpace(query) == "" {
		return errorResult("parameter 'query' must be a non-empty string"), nil
	}

	collection := "default"
	if colVal, ok := args["collection"]; ok {
		if colStr, ok := colVal.(string); ok && strings.TrimSpace(colStr) != "" {
			collection = strings.TrimSpace(colStr)
		}
	}

	limit := 5
	if limVal, ok := args["limit"]; ok {
		switch v := limVal.(type) {
		case float64:
			if int(v) > 0 {
				limit = int(v)
			}
		case int:
			if v > 0 {
				limit = v
			}
		}
	}

	results, err := s.searcher.Search(ctx, collection, query, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return textResult(fmt.Sprintf("No relevant documents found in collection %q.", collection)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant citation(s) in collection %q:\n\n", len(results), collection))
	for i, res := range results {
		sb.WriteString(fmt.Sprintf("### %d. [File: %s:%d-%d] (score: %.3f)\n", i+1, res.Chunk.Path, res.Chunk.StartLine, res.Chunk.EndLine, res.Score))
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimSpace(res.Chunk.Content))
		sb.WriteString("\n```\n\n")
	}

	return textResult(sb.String()), nil
}

func (s *Server) handleListSkills(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if s.skillsMgr == nil {
		return errorResult("skills manager is not configured in lokal server"), nil
	}

	list, err := s.skillsMgr.List()
	if err != nil {
		return errorResult(fmt.Sprintf("list skills failed: %v", err)), nil
	}

	if len(list) == 0 {
		return textResult("No skills found. Add skills to .agents/skills/ or ~/.lokal/skills/."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available Agent Skills (%d):\n\n", len(list)))
	for _, sk := range list {
		pack := ""
		if sk.Pack != "" {
			pack = fmt.Sprintf(" (pack: %s)", sk.Pack)
		}
		sb.WriteString(fmt.Sprintf("- **%s** [%s]%s: %s\n", sk.Name, sk.Source, pack, sk.Description))
	}

	return textResult(sb.String()), nil
}

func (s *Server) handleGetSkill(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	if s.skillsMgr == nil {
		return errorResult("skills manager is not configured in lokal server"), nil
	}

	nameVal, ok := args["name"]
	if !ok {
		return errorResult("missing required parameter 'name'"), nil
	}
	name, ok := nameVal.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return errorResult("parameter 'name' must be a non-empty string"), nil
	}

	refVal, ok := args["reference"]
	var refRelPath string
	if ok {
		if rStr, ok := refVal.(string); ok {
			refRelPath = strings.TrimSpace(rStr)
		}
	}

	// Step 3: Specific reference file requested
	if refRelPath != "" {
		content, err := s.skillsMgr.GetReference(name, refRelPath)
		if err != nil {
			return errorResult(fmt.Sprintf("get reference %q in skill %q failed: %v", refRelPath, name, err)), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Reference: %s/references/%s\n\n", name, refRelPath))
		sb.WriteString(content)
		return textResult(sb.String()), nil
	}

	// Step 2: Full skill requested
	sk, err := s.skillsMgr.GetSkill(name)
	if err != nil {
		return errorResult(fmt.Sprintf("skill %q not found: %v", name, err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Skill: %s\n", sk.Summary.Name))
	sb.WriteString(fmt.Sprintf("**Description:** %s\n", sk.Summary.Description))
	sb.WriteString(fmt.Sprintf("**Source:** %s\n", sk.Summary.Source))
	if len(sk.References) > 0 {
		sb.WriteString(fmt.Sprintf("**Available References:** %s\n", strings.Join(sk.References, ", ")))
	}
	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString(sk.Body)
	sb.WriteString("\n")

	return textResult(sb.String()), nil
}

func (s *Server) handleListModels(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	var sb strings.Builder

	if len(s.aliases) > 0 {
		sb.WriteString("## Configured Role Aliases:\n\n")
		for role, model := range s.aliases {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", role, model))
		}
		sb.WriteString("\n")
	}

	if s.adapter != nil {
		models, err := s.adapter.ListModels(ctx)
		if err == nil {
			sb.WriteString(fmt.Sprintf("## Installed Backend Models (%s):\n\n", s.adapter.Name()))
			for _, m := range models {
				sizeMB := float64(m.Size) / (1024 * 1024)
				sb.WriteString(fmt.Sprintf("- **%s** (%.1f MB)\n", m.Name, sizeMB))
			}
		} else {
			sb.WriteString(fmt.Sprintf("Backend model inspection unavailable: %v\n", err))
		}
	}

	return textResult(sb.String()), nil
}

func (s *Server) handleRepoMap(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	targetDir, err := s.resolvePath(args["path"])
	if err != nil {
		return errorResult(err.Error()), nil
	}

	tokens := 2048
	if tVal, ok := args["tokens"]; ok {
		switch v := tVal.(type) {
		case float64:
			if int(v) > 0 {
				tokens = int(v)
			}
		case int:
			if v > 0 {
				tokens = v
			}
		}
	}

	rm, err := codeintel.BuildRepoMap(targetDir, tokens)
	if err != nil {
		return errorResult(fmt.Sprintf("build repository map failed: %v", err)), nil
	}

	return textResult(rm.Formatted), nil
}

func (s *Server) handleFindSymbol(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	targetDir, err := s.resolvePath(args["path"])
	if err != nil {
		return errorResult(err.Error()), nil
	}

	nameVal, ok := args["name"]
	if !ok {
		return errorResult("missing required parameter 'name'"), nil
	}
	name, ok := nameVal.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return errorResult("parameter 'name' must be a non-empty string"), nil
	}

	limit := 20
	if limVal, ok := args["limit"]; ok {
		switch v := limVal.(type) {
		case float64:
			if int(v) > 0 {
				limit = int(v)
			}
		case int:
			if v > 0 {
				limit = v
			}
		}
	}

	res, err := codeintel.FindSymbol(targetDir, strings.TrimSpace(name), limit)
	if err != nil {
		return errorResult(fmt.Sprintf("find symbol %q failed: %v", name, err)), nil
	}

	if len(res.Definitions) == 0 && len(res.References) == 0 {
		return textResult(fmt.Sprintf("No definitions or references found for symbol %q.", name)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Symbol Results: %s\n\n", name))

	if len(res.Definitions) > 0 {
		sb.WriteString("## Definitions:\n")
		for _, d := range res.Definitions {
			sb.WriteString(fmt.Sprintf("- **%s** (%s) at `[File: %s:%d-%d]`\n", d.Name, d.Kind, d.Path, d.Line, d.EndLine))
			if d.Signature != "" {
				sb.WriteString(fmt.Sprintf("  `%s`\n", d.Signature))
			}
		}
		sb.WriteString("\n")
	}

	if len(res.References) > 0 {
		sb.WriteString(fmt.Sprintf("## References (%d):\n", len(res.References)))
		for _, ref := range res.References {
			sb.WriteString(fmt.Sprintf("- [File: %s:%d]: `%s`\n", ref.Path, ref.Line, ref.Content))
		}
	}

	return textResult(sb.String()), nil
}

func (s *Server) handleSearchCode(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	targetDir, err := s.resolvePath(args["path"])
	if err != nil {
		return errorResult(err.Error()), nil
	}

	patVal, ok := args["pattern"]
	if !ok {
		return errorResult("missing required parameter 'pattern'"), nil
	}
	pattern, ok := patVal.(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return errorResult("parameter 'pattern' must be a non-empty string"), nil
	}

	limit := 30
	if limVal, ok := args["limit"]; ok {
		switch v := limVal.(type) {
		case float64:
			if int(v) > 0 {
				limit = int(v)
			}
		case int:
			if v > 0 {
				limit = v
			}
		}
	}

	matches, err := codeintel.SearchCode(targetDir, pattern, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("search code failed: %v", err)), nil
	}

	if len(matches) == 0 {
		return textResult(fmt.Sprintf("No code matches found for pattern %q.", pattern)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d code match(es) for pattern %q:\n\n", len(matches), pattern))
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("- [File: %s:%d]: `%s`\n", m.Path, m.Line, m.Snippet))
	}

	return textResult(sb.String()), nil
}

func (s *Server) handleProjectCommands(ctx context.Context, args map[string]interface{}) (*sdk.CallToolResult, error) {
	targetDir, err := s.resolvePath(args["path"])
	if err != nil {
		return errorResult(err.Error()), nil
	}

	meta, err := codeintel.DetectProject(targetDir)
	if err != nil {
		return errorResult(fmt.Sprintf("detect project failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Project Commands & Guidance: %s\n\n", filepath.Base(targetDir)))

	if len(meta.Instructions) > 0 {
		sb.WriteString("## Instruction Files:\n")
		for _, inst := range meta.Instructions {
			sb.WriteString(fmt.Sprintf("- **%s** (%s)\n", inst.Name, inst.Path))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Detected Commands:\n")
	if len(meta.Commands.Build) > 0 {
		sb.WriteString(fmt.Sprintf("- **Build**: `%s`\n", strings.Join(meta.Commands.Build, "`, `")))
	}
	if len(meta.Commands.Test) > 0 {
		sb.WriteString(fmt.Sprintf("- **Test**: `%s`\n", strings.Join(meta.Commands.Test, "`, `")))
	}
	if len(meta.Commands.Lint) > 0 {
		sb.WriteString(fmt.Sprintf("- **Lint**: `%s`\n", strings.Join(meta.Commands.Lint, "`, `")))
	}
	if len(meta.Commands.Run) > 0 {
		sb.WriteString(fmt.Sprintf("- **Run**: `%s`\n", strings.Join(meta.Commands.Run, "`, `")))
	}

	if len(meta.Commands.Build) == 0 && len(meta.Commands.Test) == 0 && len(meta.Commands.Lint) == 0 {
		sb.WriteString("No standard build or test configuration files detected.\n")
	}

	return textResult(sb.String()), nil
}

// resolvePath places a code tool's optional path argument under the project root. A
// relative path is taken from the root, no path means the root, and the result, with
// symbolic links resolved, must stay inside the root: the agent policy contract denies
// every out-of-root path, and these tools read files without being judged by it.
func (s *Server) resolvePath(pathArg interface{}) (string, error) {
	root := s.rootDir
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve project root: %w", err)
		}
		root = cwd
	}
	requested, _ := pathArg.(string)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return root, nil
	}
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	realTarget, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", requested, err)
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is outside the project root", requested)
	}
	return filepath.Join(root, rel), nil
}

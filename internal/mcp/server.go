package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/codeintel"
	"github.com/hi-donwi/Agent-Brain/internal/rag"
	"github.com/hi-donwi/Agent-Brain/internal/skills"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Config configures the read-only lokal MCP server.
type Config struct {
	Name      string
	Version   string
	RootDir   string
	Searcher  *rag.Searcher
	SkillsMgr *skills.Manager
	Adapter   backend.Adapter
	Aliases   map[string]string
}

// Server exposes lokal's knowledge, skills, models, and code intelligence over MCP.
type Server struct {
	mcpServer *server.MCPServer
	rootDir   string
	searcher  *rag.Searcher
	skillsMgr *skills.Manager
	adapter   backend.Adapter
	aliases   map[string]string
	handlers  map[string]server.ToolHandlerFunc
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

	mcpSrv := server.NewMCPServer(
		name,
		ver,
		server.WithResourceCapabilities(false, false),
	)

	s := &Server{
		mcpServer: mcpSrv,
		rootDir:   filepath.Clean(root),
		searcher:  cfg.Searcher,
		skillsMgr: cfg.SkillsMgr,
		adapter:   cfg.Adapter,
		aliases:   cfg.Aliases,
		handlers:  make(map[string]server.ToolHandlerFunc),
	}

	s.registerTools()
	s.registerResources()
	return s
}

// CallTool allows programmatic, in-process invocation of registered tools.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return handler(ctx, req)
}

// MCPServer returns the underlying MCP server handle.
func (s *Server) MCPServer() *server.MCPServer {
	return s.mcpServer
}

// ServeStdio starts the stdio transport loop. It blocks until stdin closes or SIGINT/SIGTERM.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcpServer)
}

func (s *Server) registerTools() {
	// Tool 1: search_knowledge
	searchTool := mcp.NewTool("search_knowledge",
		mcp.WithDescription("Search indexed documentation and code knowledge with hybrid keyword and semantic retrieval, returning citations and snippets. (Read-only)"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query or natural language question")),
		mcp.WithString("collection", mcp.Description("Collection name to search in (default: default)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of citations to return (default: 5)")),
	)
	s.mcpServer.AddTool(searchTool, s.handleSearchKnowledge)
	s.handlers["search_knowledge"] = s.handleSearchKnowledge

	// Tool 2: list_skills
	listSkillsTool := mcp.NewTool("list_skills",
		mcp.WithDescription("List all available Agent Skills across project, user, and configured sources. (Read-only)"),
	)
	s.mcpServer.AddTool(listSkillsTool, s.handleListSkills)
	s.handlers["list_skills"] = s.handleListSkills

	// Tool 3: get_skill
	getSkillTool := mcp.NewTool("get_skill",
		mcp.WithDescription("Fetch the markdown instructions for a skill, or inspect a specific reference file. (Read-only)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the skill to fetch")),
		mcp.WithString("reference", mcp.Description("Optional relative path to a reference file under references/ (Progressive Disclosure Step 3)")),
	)
	s.mcpServer.AddTool(getSkillTool, s.handleGetSkill)
	s.handlers["get_skill"] = s.handleGetSkill

	// Tool 4: list_models
	listModelsTool := mcp.NewTool("list_models",
		mcp.WithDescription("List available local AI models and configured role aliases. (Read-only)"),
	)
	s.mcpServer.AddTool(listModelsTool, s.handleListModels)
	s.handlers["list_models"] = s.handleListModels

	// Tool 5: repo_map
	repoMapTool := mcp.NewTool("repo_map",
		mcp.WithDescription("Generate a ranked outline of repository files and symbol definitions within a token budget. (Read-only)"),
		mcp.WithNumber("tokens", mcp.Description("Maximum token budget for the repository outline (default: 2048)")),
		mcp.WithString("path", mcp.Description("Optional path to repository directory (default: current project)")),
	)
	s.mcpServer.AddTool(repoMapTool, s.handleRepoMap)
	s.handlers["repo_map"] = s.handleRepoMap

	// Tool 6: find_symbol
	findSymbolTool := mcp.NewTool("find_symbol",
		mcp.WithDescription("Locate definitions and references of a code symbol across the repository. (Read-only)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Symbol name to locate")),
		mcp.WithNumber("limit", mcp.Description("Maximum references to return (default: 20)")),
		mcp.WithString("path", mcp.Description("Optional path to repository directory")),
	)
	s.mcpServer.AddTool(findSymbolTool, s.handleFindSymbol)
	s.handlers["find_symbol"] = s.handleFindSymbol

	// Tool 7: search_code
	searchCodeTool := mcp.NewTool("search_code",
		mcp.WithDescription("Perform regex or text search across repository source code. (Read-only)"),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regular expression or text search pattern")),
		mcp.WithNumber("limit", mcp.Description("Maximum matches to return (default: 30)")),
		mcp.WithString("path", mcp.Description("Optional path to repository directory")),
	)
	s.mcpServer.AddTool(searchCodeTool, s.handleSearchCode)
	s.handlers["search_code"] = s.handleSearchCode

	// Tool 8: project_commands
	projectCmdsTool := mcp.NewTool("project_commands",
		mcp.WithDescription("Detect project build, test, lint, and run commands as structured data. (Read-only)"),
		mcp.WithString("path", mcp.Description("Optional path to repository directory")),
	)
	s.mcpServer.AddTool(projectCmdsTool, s.handleProjectCommands)
	s.handlers["project_commands"] = s.handleProjectCommands
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
		uri := "file:///" + filepath.ToSlash(inst.Path)
		res := mcp.NewResource(
			uri,
			inst.Name,
			mcp.WithResourceDescription("Project instruction contract: "+inst.Name),
			mcp.WithMIMEType("text/markdown"),
		)
		content := inst.Content
		s.mcpServer.AddResource(res, func(ctx context.Context, req mcp.ReadResourceRequest) ([]interface{}, error) {
			return []interface{}{
				mcp.TextResourceContents{
					ResourceContents: mcp.ResourceContents{
						URI:      uri,
						MIMEType: "text/markdown",
					},
					Text: content,
				},
			}, nil
		})
	}
}

func (s *Server) handleSearchKnowledge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.searcher == nil {
		return mcp.NewToolResultError("searcher is not configured in lokal server"), nil
	}

	queryVal, ok := request.Params.Arguments["query"]
	if !ok {
		return mcp.NewToolResultError("missing required parameter 'query'"), nil
	}
	query, ok := queryVal.(string)
	if !ok || strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("parameter 'query' must be a non-empty string"), nil
	}

	collection := "default"
	if colVal, ok := request.Params.Arguments["collection"]; ok {
		if colStr, ok := colVal.(string); ok && strings.TrimSpace(colStr) != "" {
			collection = strings.TrimSpace(colStr)
		}
	}

	limit := 5
	if limVal, ok := request.Params.Arguments["limit"]; ok {
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
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No relevant documents found in collection %q.", collection)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant citation(s) in collection %q:\n\n", len(results), collection))
	for i, res := range results {
		sb.WriteString(fmt.Sprintf("### %d. [File: %s:%d-%d] (score: %.3f)\n", i+1, res.Chunk.Path, res.Chunk.StartLine, res.Chunk.EndLine, res.Score))
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimSpace(res.Chunk.Content))
		sb.WriteString("\n```\n\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleListSkills(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.skillsMgr == nil {
		return mcp.NewToolResultError("skills manager is not configured in lokal server"), nil
	}

	list, err := s.skillsMgr.List()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list skills failed: %v", err)), nil
	}

	if len(list) == 0 {
		return mcp.NewToolResultText("No skills found. Add skills to .agents/skills/ or ~/.lokal/skills/."), nil
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

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleGetSkill(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.skillsMgr == nil {
		return mcp.NewToolResultError("skills manager is not configured in lokal server"), nil
	}

	nameVal, ok := request.Params.Arguments["name"]
	if !ok {
		return mcp.NewToolResultError("missing required parameter 'name'"), nil
	}
	name, ok := nameVal.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("parameter 'name' must be a non-empty string"), nil
	}

	refVal, ok := request.Params.Arguments["reference"]
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
			return mcp.NewToolResultError(fmt.Sprintf("get reference %q in skill %q failed: %v", refRelPath, name, err)), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Reference: %s/references/%s\n\n", name, refRelPath))
		sb.WriteString(content)
		return mcp.NewToolResultText(sb.String()), nil
	}

	// Step 2: Full skill requested
	sk, err := s.skillsMgr.GetSkill(name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("skill %q not found: %v", name, err)), nil
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

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleListModels(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleRepoMap(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targetDir := s.resolvePath(request.Params.Arguments["path"])

	tokens := 2048
	if tVal, ok := request.Params.Arguments["tokens"]; ok {
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
		return mcp.NewToolResultError(fmt.Sprintf("build repository map failed: %v", err)), nil
	}

	return mcp.NewToolResultText(rm.Formatted), nil
}

func (s *Server) handleFindSymbol(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targetDir := s.resolvePath(request.Params.Arguments["path"])

	nameVal, ok := request.Params.Arguments["name"]
	if !ok {
		return mcp.NewToolResultError("missing required parameter 'name'"), nil
	}
	name, ok := nameVal.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return mcp.NewToolResultError("parameter 'name' must be a non-empty string"), nil
	}

	limit := 20
	if limVal, ok := request.Params.Arguments["limit"]; ok {
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
		return mcp.NewToolResultError(fmt.Sprintf("find symbol %q failed: %v", name, err)), nil
	}

	if len(res.Definitions) == 0 && len(res.References) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No definitions or references found for symbol %q.", name)), nil
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

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleSearchCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targetDir := s.resolvePath(request.Params.Arguments["path"])

	patVal, ok := request.Params.Arguments["pattern"]
	if !ok {
		return mcp.NewToolResultError("missing required parameter 'pattern'"), nil
	}
	pattern, ok := patVal.(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return mcp.NewToolResultError("parameter 'pattern' must be a non-empty string"), nil
	}

	limit := 30
	if limVal, ok := request.Params.Arguments["limit"]; ok {
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
		return mcp.NewToolResultError(fmt.Sprintf("search code failed: %v", err)), nil
	}

	if len(matches) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No code matches found for pattern %q.", pattern)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d code match(es) for pattern %q:\n\n", len(matches), pattern))
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("- [File: %s:%d]: `%s`\n", m.Path, m.Line, m.Snippet))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleProjectCommands(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targetDir := s.resolvePath(request.Params.Arguments["path"])

	meta, err := codeintel.DetectProject(targetDir)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("detect project failed: %v", err)), nil
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

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) resolvePath(pathArg interface{}) string {
	if pathArg != nil {
		if pStr, ok := pathArg.(string); ok && strings.TrimSpace(pStr) != "" {
			return filepath.Clean(strings.TrimSpace(pStr))
		}
	}
	if s.rootDir != "" {
		return s.rootDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

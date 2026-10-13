package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/rag"
	"github.com/hi-donwi/Agent-Local/internal/skills"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Config configures the read-only lokal MCP server.
type Config struct {
	Name      string
	Version   string
	Searcher  *rag.Searcher
	SkillsMgr *skills.Manager
	Adapter   backend.Adapter
	Aliases   map[string]string
}

// Server exposes lokal's knowledge, skills, and models over the Model Context Protocol.
type Server struct {
	mcpServer *server.MCPServer
	searcher  *rag.Searcher
	skillsMgr *skills.Manager
	adapter   backend.Adapter
	aliases   map[string]string
	handlers  map[string]server.ToolHandlerFunc
}

// NewServer registers read-only MCP tools and builds an MCP server instance.
func NewServer(cfg Config) *Server {
	name := cfg.Name
	if name == "" {
		name = "lokal"
	}
	ver := cfg.Version
	if ver == "" {
		ver = "0.0.0-dev"
	}

	mcpSrv := server.NewMCPServer(name, ver)

	s := &Server{
		mcpServer: mcpSrv,
		searcher:  cfg.Searcher,
		skillsMgr: cfg.SkillsMgr,
		adapter:   cfg.Adapter,
		aliases:   cfg.Aliases,
		handlers:  make(map[string]server.ToolHandlerFunc),
	}

	s.registerTools()
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
		return mcp.NewToolResultText("No skills installed. Add skills to .agents/skills/ or ~/.lokal/skills/."), nil
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

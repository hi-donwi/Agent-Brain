package mcp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	localmcp "github.com/hi-donwi/Agent-Local/internal/mcp"
	"github.com/hi-donwi/Agent-Local/internal/rag"
	"github.com/hi-donwi/Agent-Local/internal/skills"
	"github.com/hi-donwi/Agent-Local/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

type mockAdapter struct {
	backend.Adapter
}

func (m *mockAdapter) Name() string { return "mock-ollama" }
func (m *mockAdapter) ListModels(ctx context.Context) ([]backend.ModelInfo, error) {
	return []backend.ModelInfo{
		{Name: "llama3.2:3b", Size: 2 * 1024 * 1024 * 1024},
		{Name: "nomic-embed-text", Size: 300 * 1024 * 1024},
	}, nil
}
func (m *mockAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	return &backend.EmbedResponse{
		Model: req.Model,
		Embeddings: [][]float32{
			{0.1, 0.2, 0.3, 0.4},
		},
	}, nil
}

func setupTestEnvironment(t *testing.T) (*localmcp.Server, func()) {
	t.Helper()
	tmpDir := t.TempDir()

	// 1. Setup Skills Manager
	skillsDir := filepath.Join(tmpDir, "skills", "test-skill")
	if err := os.MkdirAll(filepath.Join(skillsDir, "references"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillMD := `---
name: test-skill
description: A mock skill for MCP server testing.
metadata:
  pack: core
---
# Process Instructions
Follow step 1 and step 2.
`
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "references", "guide.txt"), []byte("Guide Reference Content"), 0o644); err != nil {
		t.Fatalf("write guide.txt: %v", err)
	}

	skillsMgr := skills.NewManager("", filepath.Join(tmpDir, "skills"), nil)

	// 2. Setup SQLite store and Searcher
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	ctx := context.Background()
	col, err := st.CreateCollection(ctx, "default", "nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	doc := &store.Document{
		CollectionID: col.ID,
		Path:         "docs/sample.md",
		Hash:         "hash123",
		SizeBytes:    100,
	}
	if err := st.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upsert document: %v", err)
	}

	docRecord, err := st.GetDocument(ctx, col.ID, "docs/sample.md")
	if err != nil {
		t.Fatalf("get document: %v", err)
	}

	err = st.InsertChunks(ctx, []store.Chunk{
		{
			ID:           "chunk1",
			CollectionID: col.ID,
			DocumentID:   docRecord.ID,
			Path:         "docs/sample.md",
			ChunkIndex:   0,
			StartLine:    1,
			EndLine:      5,
			Content:      "Sample content discussing MCP protocol server implementation.",
			Embedding:    []float32{0.1, 0.2, 0.3, 0.4},
		},
	})
	if err != nil {
		t.Fatalf("insert chunks: %v", err)
	}

	// 3. Create code intelligence mock files in tmpDir
	mustWriteFile(t, filepath.Join(tmpDir, "AGENTS.md"), "# Contract\nAgents follow rules.")
	mustWriteFile(t, filepath.Join(tmpDir, "Makefile"), ".PHONY: build test\nbuild:\n\tgo build\ntest:\n\tgo test\n")
	mustWriteFile(t, filepath.Join(tmpDir, "pkg", "service.go"), `package pkg
type Worker struct {}
func (w *Worker) Process() error {
	return nil
}
`)

	adapter := &mockAdapter{}
	searcher := rag.NewSearcher(st, adapter)

	srv := localmcp.NewServer(localmcp.Config{
		Name:      "lokal-test",
		Version:   "1.0.0",
		RootDir:   tmpDir,
		Searcher:  searcher,
		SkillsMgr: skillsMgr,
		Adapter:   adapter,
		Aliases: map[string]string{
			"chat":  "llama3.2:3b",
			"embed": "nomic-embed-text",
		},
	})

	cleanup := func() {
		_ = st.Close()
	}

	return srv, cleanup
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func TestMCPToolsExecution(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Test list_skills
	resSkills, err := srv.CallTool(ctx, "list_skills", map[string]interface{}{})
	if err != nil {
		t.Fatalf("call list_skills failed: %v", err)
	}
	if len(resSkills.Content) == 0 {
		t.Fatal("expected content in list_skills result")
	}
	textVal := resSkills.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textVal, "test-skill") {
		t.Errorf("expected list_skills to contain test-skill, got: %s", textVal)
	}

	// 2. Test get_skill (full body)
	resGetSkill, err := srv.CallTool(ctx, "get_skill", map[string]interface{}{
		"name": "test-skill",
	})
	if err != nil {
		t.Fatalf("call get_skill failed: %v", err)
	}
	textBody := resGetSkill.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textBody, "Follow step 1 and step 2.") {
		t.Errorf("expected skill body, got: %s", textBody)
	}
	if !strings.Contains(textBody, "guide.txt") {
		t.Errorf("expected available references listed, got: %s", textBody)
	}

	// 3. Test get_skill with reference file (Progressive disclosure step 3)
	resGetRef, err := srv.CallTool(ctx, "get_skill", map[string]interface{}{
		"name":      "test-skill",
		"reference": "guide.txt",
	})
	if err != nil {
		t.Fatalf("call get_skill reference failed: %v", err)
	}
	textRef := resGetRef.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textRef, "Guide Reference Content") {
		t.Errorf("expected reference content, got: %s", textRef)
	}

	// 4. Test search_knowledge
	resSearch, err := srv.CallTool(ctx, "search_knowledge", map[string]interface{}{
		"query":      "MCP protocol server",
		"collection": "default",
	})
	if err != nil {
		t.Fatalf("call search_knowledge failed: %v", err)
	}
	textSearch := resSearch.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textSearch, "[File: docs/sample.md:1-5]") {
		t.Errorf("expected citation in search results, got: %s", textSearch)
	}

	// 5. Test list_models
	resModels, err := srv.CallTool(ctx, "list_models", map[string]interface{}{})
	if err != nil {
		t.Fatalf("call list_models failed: %v", err)
	}
	textModels := resModels.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textModels, "llama3.2:3b") || !strings.Contains(textModels, "chat") {
		t.Errorf("expected models and aliases, got: %s", textModels)
	}

	// 6. Test repo_map
	resRepoMap, err := srv.CallTool(ctx, "repo_map", map[string]interface{}{
		"tokens": float64(1000),
	})
	if err != nil {
		t.Fatalf("call repo_map failed: %v", err)
	}
	textRepoMap := resRepoMap.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textRepoMap, "Worker") {
		t.Errorf("expected repo_map to contain Worker symbol, got: %s", textRepoMap)
	}

	// 7. Test find_symbol
	resFindSym, err := srv.CallTool(ctx, "find_symbol", map[string]interface{}{
		"name": "Process",
	})
	if err != nil {
		t.Fatalf("call find_symbol failed: %v", err)
	}
	textFindSym := resFindSym.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textFindSym, "service.go") || !strings.Contains(textFindSym, "Process") {
		t.Errorf("expected find_symbol to locate Process, got: %s", textFindSym)
	}

	// 8. Test search_code
	resSearchCode, err := srv.CallTool(ctx, "search_code", map[string]interface{}{
		"pattern": "type Worker",
	})
	if err != nil {
		t.Fatalf("call search_code failed: %v", err)
	}
	textSearchCode := resSearchCode.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textSearchCode, "service.go") {
		t.Errorf("expected search_code to locate service.go, got: %s", textSearchCode)
	}

	// 9. Test project_commands
	resProjCmds, err := srv.CallTool(ctx, "project_commands", map[string]interface{}{})
	if err != nil {
		t.Fatalf("call project_commands failed: %v", err)
	}
	textProjCmds := resProjCmds.Content[0].(mcp.TextContent).Text
	if !strings.Contains(textProjCmds, "make test") || !strings.Contains(textProjCmds, "AGENTS.md") {
		t.Errorf("expected project commands and instructions, got: %s", textProjCmds)
	}
}

func TestMCPErrorHandling(t *testing.T) {
	srv, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Missing required query param
	resSearch, err := srv.CallTool(ctx, "search_knowledge", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if !resSearch.IsError {
		t.Errorf("expected tool result error for missing required parameter")
	}

	// Non-existent skill
	resGetSkill, err := srv.CallTool(ctx, "get_skill", map[string]interface{}{
		"name": "non-existent-skill",
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if !resGetSkill.IsError {
		t.Errorf("expected tool result error for non-existent skill")
	}
}

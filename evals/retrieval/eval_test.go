package retrieval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/rag"
	"github.com/hi-donwi/Agent-Local/internal/store"
)

type GoldenQuery struct {
	ID           string `json:"id"`
	Query        string `json:"query"`
	ExpectedPath string `json:"expected_path"`
	RelevantText string `json:"relevant_text"`
}

func TestRetrievalGoldenSet_Recall(t *testing.T) {
	tmpDir := t.TempDir()
	corpusDir := filepath.Join(tmpDir, "corpus")
	dbPath := filepath.Join(tmpDir, "eval_lokal.db")

	if err := os.MkdirAll(filepath.Join(corpusDir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Create test fixture documents matching golden set
	files := map[string]string{
		"docs/security.md": `# Security Guide
lokal uses loopback-only binding on 127.0.0.1 with bearer token authentication.
The bearer token mode 0600 is written to ~/.lokal/serve.token.
Host header filtering prevents DNS rebinding attacks.`,

		"docs/architecture.md": `# Architecture Overview
lokal maintains durable local state in a single pure-Go SQLite database in WAL mode.
Chunks are stored with FTS5 bm25 full text search and float32 vector slab embeddings.`,

		"docs/remote.md": `# Remote Access
To reach a dedicated remote VM over an encrypted tunnel, run:
ssh -N -L 52140:127.0.0.1:52140 user@remote-vm
Agent-Control connects locally to http://127.0.0.1:52140/v1.`,
	}

	for path, content := range files {
		full := filepath.Join(corpusDir, path)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write file %s: %v", path, err)
		}
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Index corpus
	indexer := rag.NewIndexer(s, nil) // nil adapter -> lexical FTS5 fallback
	stats, err := indexer.IndexDirectory(ctx, corpusDir, "eval-col", "nomic-embed-text", nil)
	if err != nil {
		t.Fatalf("indexing failed: %v", err)
	}
	if stats.FilesIndexed != 3 {
		t.Fatalf("expected 3 files indexed, got %d", stats.FilesIndexed)
	}

	// Load golden set
	goldenData, err := os.ReadFile("golden_set.json")
	if err != nil {
		t.Fatalf("read golden_set.json: %v", err)
	}

	var queries []GoldenQuery
	if err := json.Unmarshal(goldenData, &queries); err != nil {
		t.Fatalf("parse golden_set.json: %v", err)
	}

	searcher := rag.NewSearcher(s, nil)

	// Measure recall@3
	matchedCount := 0
	for _, gq := range queries {
		results, err := searcher.Search(ctx, "eval-col", gq.Query, 3)
		if err != nil {
			t.Fatalf("search query %q failed: %v", gq.Query, err)
		}

		found := false
		for _, res := range results {
			if res.Chunk.Path == gq.ExpectedPath {
				found = true
				break
			}
		}

		if found {
			matchedCount++
		} else {
			t.Errorf("Query %q failed to retrieve %q in top 3", gq.Query, gq.ExpectedPath)
		}
	}

	recallAt3 := float64(matchedCount) / float64(len(queries))
	if recallAt3 < 1.0 {
		t.Fatalf("recall@3 below threshold: %.2f (matched %d of %d)", recallAt3, matchedCount, len(queries))
	}
}

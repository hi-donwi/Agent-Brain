package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/store"
)

func TestStore_CollectionsAndDocuments_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_lifecycle.db")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// 1. Create collection
	col, err := s.CreateCollection(ctx, "notes", "nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("CreateCollection failed: %v", err)
	}
	if col.Name != "notes" || col.Dimension != 4 {
		t.Fatalf("unexpected collection: %+v", col)
	}

	// 2. Get collection
	fetched, err := s.GetCollection(ctx, "notes")
	if err != nil {
		t.Fatalf("GetCollection by name failed: %v", err)
	}
	if fetched.ID != col.ID {
		t.Errorf("expected ID %s, got %s", col.ID, fetched.ID)
	}

	// 3. Upsert document
	doc := &store.Document{
		CollectionID: col.ID,
		Path:         "README.md",
		Hash:         "abcdef123456",
		SizeBytes:    1024,
	}
	if err := s.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("UpsertDocument failed: %v", err)
	}

	// 4. Get document
	fDoc, err := s.GetDocument(ctx, col.ID, "README.md")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if fDoc.Hash != "abcdef123456" {
		t.Errorf("unexpected hash: %s", fDoc.Hash)
	}

	// 5. Insert chunks
	chunks := []store.Chunk{
		{
			CollectionID: col.ID,
			DocumentID:   fDoc.ID,
			ChunkIndex:   0,
			Path:         "README.md",
			StartLine:    1,
			EndLine:      10,
			Content:      "Welcome to lokal: the local AI platform",
			Embedding:    []float32{1.0, 0.0, 0.0, 0.0},
		},
		{
			CollectionID: col.ID,
			DocumentID:   fDoc.ID,
			ChunkIndex:   1,
			Path:         "README.md",
			StartLine:    11,
			EndLine:      20,
			Content:      "Deploy local models on your laptop or server",
			Embedding:    []float32{0.0, 1.0, 0.0, 0.0},
		},
	}
	if err := s.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("InsertChunks failed: %v", err)
	}

	// 6. List chunks
	listedChunks, err := s.ListChunks(ctx, col.ID)
	if err != nil {
		t.Fatalf("ListChunks failed: %v", err)
	}
	if len(listedChunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(listedChunks))
	}
	if len(listedChunks[0].Embedding) != 4 {
		t.Fatalf("expected embedding dimension 4, got %d", len(listedChunks[0].Embedding))
	}

	// 7. Test FTS5 lexical search
	lexMatches, err := s.LexicalSearch(ctx, col.ID, "lokal platform", 5)
	if err != nil {
		t.Fatalf("LexicalSearch failed: %v", err)
	}
	if len(lexMatches) == 0 {
		t.Fatal("expected lexical match for 'lokal platform', got none")
	}
	if lexMatches[0].ChunkID != listedChunks[0].ID {
		t.Errorf("expected chunk 0 match, got chunk ID %s", lexMatches[0].ChunkID)
	}

	// 8. Test Dense vector search
	denseMatches, err := s.DenseSearch(ctx, col.ID, []float32{0.9, 0.1, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("DenseSearch failed: %v", err)
	}
	if len(denseMatches) == 0 {
		t.Fatal("expected dense match, got none")
	}
	if denseMatches[0].ChunkID != listedChunks[0].ID {
		t.Errorf("expected chunk 0 best dense match, got %s", denseMatches[0].ChunkID)
	}

	// 9. Test Hybrid search
	hybridResults, err := s.HybridSearch(ctx, col.ID, "local models", []float32{0.0, 0.9, 0.0, 0.0}, store.HybridSearchOptions{
		TopK: 5,
	})
	if err != nil {
		t.Fatalf("HybridSearch failed: %v", err)
	}
	if len(hybridResults) == 0 {
		t.Fatal("expected hybrid search results, got none")
	}
	if hybridResults[0].Chunk.Path != "README.md" {
		t.Errorf("expected path README.md, got %s", hybridResults[0].Chunk.Path)
	}

	// 10. Delete Document
	if err := s.DeleteDocument(ctx, col.ID, "README.md"); err != nil {
		t.Fatalf("DeleteDocument failed: %v", err)
	}
	// Chunks should be deleted via cascade
	remainingChunks, err := s.ListChunks(ctx, col.ID)
	if err != nil {
		t.Fatalf("ListChunks failed: %v", err)
	}
	if len(remainingChunks) != 0 {
		t.Errorf("expected 0 chunks after document deletion, got %d", len(remainingChunks))
	}
}

func TestStore_VectorOperations(t *testing.T) {
	v1 := []float32{3.0, 4.0}
	norm := store.Normalize(v1)
	if norm[0] != 0.6 || norm[1] != 0.8 {
		t.Errorf("unexpected normalized vector: %v", norm)
	}

	dot := store.DotProduct(norm, norm)
	if dot < 0.999 || dot > 1.001 {
		t.Errorf("expected dot product ~1.0 for self-dot, got %f", dot)
	}

	b := store.EncodeFloats(v1)
	dec := store.DecodeFloats(b)
	if len(dec) != 2 || dec[0] != 3.0 || dec[1] != 4.0 {
		t.Errorf("roundtrip mismatch: %v", dec)
	}
}

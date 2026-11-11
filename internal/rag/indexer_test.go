package rag_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/rag"
	"github.com/hi-donwi/Agent-Brain/internal/store"
)

type fakeRagAdapter struct {
	embeddings [][]float32
}

func (f *fakeRagAdapter) Name() string     { return "fake" }
func (f *fakeRagAdapter) Endpoint() string { return "http://127.0.0.1:0" }
func (f *fakeRagAdapter) Ping(_ context.Context) error {
	return nil
}
func (f *fakeRagAdapter) Version(_ context.Context) (string, error) {
	return "0.0.1", nil
}
func (f *fakeRagAdapter) ListModels(_ context.Context) ([]backend.ModelInfo, error) {
	return nil, nil
}
func (f *fakeRagAdapter) ShowModel(_ context.Context, _ string) (backend.ModelDetails, error) {
	return backend.ModelDetails{EmbeddingLength: 4}, nil
}
func (f *fakeRagAdapter) RunningModels(_ context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (f *fakeRagAdapter) Chat(_ context.Context, _ backend.ChatRequest) (*backend.ChatResponse, error) {
	return &backend.ChatResponse{
		Message: backend.ChatMessage{Role: "assistant", Content: "This is a grounded answer from fake model."},
	}, nil
}
func (f *fakeRagAdapter) ChatStream(_ context.Context, _ backend.ChatRequest, _ func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return nil, nil
}
func (f *fakeRagAdapter) Embed(_ context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	res := make([][]float32, len(req.Input))
	for i := range req.Input {
		res[i] = []float32{0.5, 0.5, 0.5, 0.5}
	}
	return &backend.EmbedResponse{
		Model:      req.Model,
		Embeddings: res,
	}, nil
}
func (f *fakeRagAdapter) Pull(_ context.Context, _ string, _ func(backend.Progress)) error {
	return nil
}
func (f *fakeRagAdapter) Delete(_ context.Context, _ string) error {
	return nil
}
func (f *fakeRagAdapter) Unload(_ context.Context, _ string) error {
	return nil
}

func TestIndexer_IncrementalAndPruning(t *testing.T) {
	tmpDir := t.TempDir()
	corpusDir := filepath.Join(tmpDir, "docs")
	dbPath := filepath.Join(tmpDir, "indexer_test.db")

	_ = os.MkdirAll(corpusDir, 0o755)
	file1 := filepath.Join(corpusDir, "file1.md")
	file2 := filepath.Join(corpusDir, "file2.md")

	_ = os.WriteFile(file1, []byte("# Header 1\nLine 1\nLine 2"), 0o644)
	_ = os.WriteFile(file2, []byte("# Header 2\nLine A\nLine B"), 0o644)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	adapter := &fakeRagAdapter{}
	indexer := rag.NewIndexer(s, adapter)

	// 1. Initial Indexing
	stats1, err := indexer.IndexDirectory(ctx, corpusDir, "test-col", "test-model", nil)
	if err != nil {
		t.Fatalf("initial indexing failed: %v", err)
	}
	if stats1.FilesIndexed != 2 || stats1.FilesSkipped != 0 {
		t.Fatalf("expected 2 files indexed, got %+v", stats1)
	}

	// 2. Incremental Indexing without changes (should skip all)
	stats2, err := indexer.IndexDirectory(ctx, corpusDir, "test-col", "test-model", nil)
	if err != nil {
		t.Fatalf("incremental indexing failed: %v", err)
	}
	if stats2.FilesIndexed != 0 || stats2.FilesSkipped != 2 {
		t.Fatalf("expected 2 files skipped, got %+v", stats2)
	}

	// 3. Remove file2 and re-index (should prune file2)
	_ = os.Remove(file2)
	stats3, err := indexer.IndexDirectory(ctx, corpusDir, "test-col", "test-model", nil)
	if err != nil {
		t.Fatalf("pruning pass failed: %v", err)
	}
	if stats3.FilesPruned != 1 {
		t.Fatalf("expected 1 file pruned, got %+v", stats3)
	}

	// 4. Test Searcher
	searcher := rag.NewSearcher(s, adapter)
	results, err := searcher.Search(ctx, "test-col", "Header 1", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for Header 1, got none")
	}

	// 5. Test AskEngine
	askEngine := rag.NewAskEngine(searcher, adapter)
	askResp, err := askEngine.Ask(ctx, "test-col", "What is in file1?", "chat")
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if len(askResp.Citations) == 0 {
		t.Error("expected citations in Ask response")
	}
	if askResp.Answer != "This is a grounded answer from fake model." {
		t.Errorf("unexpected answer: %q", askResp.Answer)
	}
}

func TestIndexerNeverStoresContentFromSymlinkOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "synthetic-private-outside-root-content"
	outside := filepath.Join(parent, "private.txt")
	if err := os.WriteFile(outside, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	s, err := store.Open(filepath.Join(parent, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	indexer := rag.NewIndexer(s, &fakeRagAdapter{})
	if _, err := indexer.IndexDirectory(context.Background(), root, "test-col", "test-model", nil); err != nil {
		t.Fatal(err)
	}
	collection, err := s.GetCollection(context.Background(), "test-col")
	if err != nil {
		t.Fatal(err)
	}
	documents, err := s.ListDocuments(context.Background(), collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if document.Path == "link.txt" || document.Hash == store.HashContent(secret) {
			t.Fatalf("outside file entered the index: %+v", document)
		}
	}
	if len(documents) != 1 || documents[0].Path != "inside.txt" {
		t.Fatalf("unexpected indexed documents: %+v", documents)
	}
}

func TestIndexerAcquiresAndReleasesEmbeddingResidencyPerBatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("one line\ntwo lines\nthree lines"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "residency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	indexer := rag.NewIndexer(db, &fakeRagAdapter{})
	indexer.BatchSize = 1
	acquired, released := 0, 0
	indexer.AcquireEmbedding = func(_ context.Context, model string) (func(), error) {
		if model != "test-model" {
			t.Fatalf("residency acquired for unexpected embedding model %q", model)
		}
		acquired++
		return func() { released++ }, nil
	}
	if _, err := indexer.IndexDirectory(context.Background(), root, "docs", "test-model", nil); err != nil {
		t.Fatal(err)
	}
	if acquired == 0 || acquired != released {
		t.Fatalf("embedding leases acquired=%d released=%d", acquired, released)
	}
}

type blockingRagAdapter struct {
	*fakeRagAdapter
	started chan struct{}
}

func (a *blockingRagAdapter) Embed(ctx context.Context, _ backend.EmbedRequest) (*backend.EmbedResponse, error) {
	select {
	case a.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestIndexerCancellationDuringEmbeddingDoesNotCommitIncompleteDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("content that must not be committed after cancel"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adapter := &blockingRagAdapter{fakeRagAdapter: &fakeRagAdapter{}, started: make(chan struct{}, 1)}
	indexer := rag.NewIndexer(db, adapter)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := indexer.IndexDirectory(ctx, root, "cancel", "test-model", nil)
		result <- err
	}()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("indexer did not reach embedding")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("indexer cancellation returned %v", err)
	}
	collection, err := db.GetCollection(context.Background(), "cancel")
	if err != nil {
		t.Fatal(err)
	}
	documents, err := db.ListDocuments(context.Background(), collection.ID)
	if err != nil || len(documents) != 0 {
		t.Fatalf("incomplete document was committed: docs=%+v err=%v", documents, err)
	}
}

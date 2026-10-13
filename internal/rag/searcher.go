package rag

import (
	"context"
	"fmt"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/store"
)

// Searcher performs hybrid semantic and lexical searches against indexed collections.
type Searcher struct {
	Store   *store.Store
	Adapter backend.Adapter
}

// NewSearcher creates a new Searcher instance.
func NewSearcher(s *store.Store, adapter backend.Adapter) *Searcher {
	return &Searcher{
		Store:   s,
		Adapter: adapter,
	}
}

// Search executes a hybrid query against a named collection.
func (sr *Searcher) Search(ctx context.Context, collectionName, query string, topK int) ([]store.SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}

	col, err := sr.Store.GetCollection(ctx, collectionName)
	if err != nil {
		return nil, fmt.Errorf("collection %q: %w", collectionName, err)
	}

	var queryVec []float32
	if sr.Adapter != nil {
		truncate := false
		resp, err := sr.Adapter.Embed(ctx, backend.EmbedRequest{
			Model:    col.Model,
			Input:    []string{query},
			Truncate: &truncate,
		})
		if err == nil && resp != nil && len(resp.Embeddings) > 0 {
			queryVec = resp.Embeddings[0]
		}
	}

	results, err := sr.Store.HybridSearch(ctx, col.ID, query, queryVec, store.HybridSearchOptions{
		TopK: topK,
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search on collection %q: %w", collectionName, err)
	}

	return results, nil
}

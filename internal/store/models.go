package store

import (
	"time"
)

// Collection groups documents and chunks pinned to one embedding model and dimension.
type Collection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	CreatedAt time.Time `json:"created_at"`
}

// Document represents an indexed file within a collection.
type Document struct {
	ID           string    `json:"id"`
	CollectionID string    `json:"collection_id"`
	Path         string    `json:"path"`
	Hash         string    `json:"hash"`
	SizeBytes    int64     `json:"size_bytes"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Chunk is a segmented slice of a document with line boundaries and optional vector embedding.
type Chunk struct {
	ID           string    `json:"id"`
	CollectionID string    `json:"collection_id"`
	DocumentID   string    `json:"document_id"`
	ChunkIndex   int       `json:"chunk_index"`
	Path         string    `json:"path"`
	StartLine    int       `json:"start_line"`
	EndLine      int       `json:"end_line"`
	Content      string    `json:"content"`
	Embedding    []float32 `json:"embedding,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// SearchResult represents a retrieved chunk with relevancy score and citation metadata.
type SearchResult struct {
	Chunk       Chunk   `json:"chunk"`
	Score       float64 `json:"score"`
	LexicalRank int     `json:"lexical_rank,omitempty"`
	DenseRank   int     `json:"dense_rank,omitempty"`
	MatchType   string  `json:"match_type"` // "hybrid", "lexical", "dense"
}

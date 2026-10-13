package store

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
)

// EncodeFloats converts a float32 slice into little-endian binary bytes.
func EncodeFloats(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// DecodeFloats parses a little-endian binary byte slice into a float32 slice.
func DecodeFloats(buf []byte) []float32 {
	count := len(buf) / 4
	vec := make([]float32, count)
	for i := 0; i < count; i++ {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}

// Normalize computes the L2 norm and returns a normalized copy of the vector.
func Normalize(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return vec
	}

	normVec := make([]float32, len(vec))
	for i, v := range vec {
		normVec[i] = float32(float64(v) / norm)
	}
	return normVec
}

// DotProduct calculates the dot product between two float32 vectors.
// For L2-normalized vectors, dot product is identical to cosine similarity.
func DotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot float32
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
	}
	return dot
}

// VectorItem represents one vector embedding in the resident slab.
type VectorItem struct {
	ChunkID    string
	DocumentID string
	Path       string
	Embedding  []float32
}

// VectorMatch represents a scored vector match from a dense scan.
type VectorMatch struct {
	ChunkID    string
	DocumentID string
	Path       string
	Score      float32
	Rank       int
}

// VectorSlab holds resident embeddings in RAM for low-latency parallel vector scans.
type VectorSlab struct {
	CollectionID string
	Dimension    int
	Items        []VectorItem
}

// LoadVectorSlab loads all embedded chunks in a collection into an in-memory VectorSlab.
func (s *Store) LoadVectorSlab(ctx context.Context, collectionID string) (*VectorSlab, error) {
	col, err := s.GetCollection(ctx, collectionID)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, document_id, path, embedding FROM chunks WHERE collection_id = ? AND embedding IS NOT NULL;`
	rows, err := s.db.QueryContext(ctx, query, collectionID)
	if err != nil {
		return nil, fmt.Errorf("query chunks with embeddings: %w", err)
	}
	defer rows.Close()

	var items []VectorItem
	for rows.Next() {
		var item VectorItem
		var embBytes []byte
		if err := rows.Scan(&item.ChunkID, &item.DocumentID, &item.Path, &embBytes); err != nil {
			return nil, err
		}
		if len(embBytes) > 0 {
			item.Embedding = DecodeFloats(embBytes)
			if len(item.Embedding) == col.Dimension {
				items = append(items, item)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &VectorSlab{
		CollectionID: collectionID,
		Dimension:    col.Dimension,
		Items:        items,
	}, nil
}

// Scan performs an exact dense scan over the slab, parallelizing across available CPU cores.
func (slab *VectorSlab) Scan(queryVec []float32, topK int) ([]VectorMatch, error) {
	if len(queryVec) != slab.Dimension {
		return nil, fmt.Errorf("query dimension mismatch: expected %d, got %d", slab.Dimension, len(queryVec))
	}
	if topK <= 0 {
		topK = 10
	}

	normQuery := Normalize(queryVec)
	n := len(slab.Items)
	if n == 0 {
		return nil, nil
	}

	// Use parallel worker pool if there are more than 200 items
	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > n/50 {
		numWorkers = n / 50
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	type matchCandidate struct {
		index int
		score float32
	}

	results := make([]matchCandidate, n)
	chunkSize := (n + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				score := DotProduct(normQuery, slab.Items[i].Embedding)
				results[i] = matchCandidate{index: i, score: score}
			}
		}(start, end)
	}
	wg.Wait()

	// Sort descending by score
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topK > len(results) {
		topK = len(results)
	}

	matches := make([]VectorMatch, topK)
	for i := 0; i < topK; i++ {
		c := results[i]
		item := slab.Items[c.index]
		matches[i] = VectorMatch{
			ChunkID:    item.ChunkID,
			DocumentID: item.DocumentID,
			Path:       item.Path,
			Score:      c.score,
			Rank:       i + 1,
		}
	}

	return matches, nil
}

// DenseSearch executes a dense vector search directly using the database and vector slab.
func (s *Store) DenseSearch(ctx context.Context, collectionID string, queryVec []float32, topK int) ([]VectorMatch, error) {
	slab, err := s.LoadVectorSlab(ctx, collectionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("load vector slab for collection %s: %w", collectionID, err)
	}
	return slab.Scan(queryVec, topK)
}

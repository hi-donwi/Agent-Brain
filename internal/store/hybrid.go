package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// DefaultRRFConstant is the standard smoothing constant (k=60) for Reciprocal Rank Fusion.
const DefaultRRFConstant = 60.0

// HybridSearchOptions customizes hybrid lexical and dense fusion.
type HybridSearchOptions struct {
	TopK        int
	RRFConst    float64
	DenseOnly   bool
	LexicalOnly bool
}

// HybridSearch fuses FTS5 lexical ranking and dense vector similarity using Reciprocal Rank Fusion (RRF).
func (s *Store) HybridSearch(ctx context.Context, collectionID, queryText string, queryVec []float32, opts HybridSearchOptions) ([]SearchResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.RRFConst <= 0 {
		opts.RRFConst = DefaultRRFConstant
	}

	type chunkScore struct {
		chunkID     string
		lexicalRank int
		denseRank   int
		rrfScore    float64
		matchType   string
	}

	scores := make(map[string]*chunkScore)

	// 1. Run Lexical Search if not dense-only and queryText is non-empty
	if !opts.DenseOnly && strings.TrimSpace(queryText) != "" {
		lexicalMatches, err := s.LexicalSearch(ctx, collectionID, queryText, opts.TopK*3)
		if err != nil {
			return nil, fmt.Errorf("lexical search failed: %w", err)
		}
		for _, lm := range lexicalMatches {
			score := 1.0 / (opts.RRFConst + float64(lm.Rank))
			scores[lm.ChunkID] = &chunkScore{
				chunkID:     lm.ChunkID,
				lexicalRank: lm.Rank,
				rrfScore:    score,
				matchType:   "lexical",
			}
		}
	}

	// 2. Run Dense Search if not lexical-only and queryVec is non-empty
	if !opts.LexicalOnly && len(queryVec) > 0 {
		denseMatches, err := s.DenseSearch(ctx, collectionID, queryVec, opts.TopK*3)
		if err != nil {
			return nil, fmt.Errorf("dense search failed: %w", err)
		}
		for _, dm := range denseMatches {
			denseScore := 1.0 / (opts.RRFConst + float64(dm.Rank))
			if existing, ok := scores[dm.ChunkID]; ok {
				existing.denseRank = dm.Rank
				existing.rrfScore += denseScore
				existing.matchType = "hybrid"
			} else {
				scores[dm.ChunkID] = &chunkScore{
					chunkID:   dm.ChunkID,
					denseRank: dm.Rank,
					rrfScore:  denseScore,
					matchType: "dense",
				}
			}
		}
	}

	if len(scores) == 0 {
		return nil, nil
	}

	// Sort candidates by descending RRF score
	candidates := make([]*chunkScore, 0, len(scores))
	for _, cs := range scores {
		candidates = append(candidates, cs)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].rrfScore > candidates[j].rrfScore
	})

	if opts.TopK > len(candidates) {
		opts.TopK = len(candidates)
	}
	topCandidates := candidates[:opts.TopK]

	// 3. Hydrate chunk details from database
	chunkIDs := make([]string, len(topCandidates))
	for i, c := range topCandidates {
		chunkIDs[i] = c.chunkID
	}

	hydrated, err := s.getChunksByIDs(ctx, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("hydrate chunks: %w", err)
	}

	results := make([]SearchResult, 0, len(topCandidates))
	for _, c := range topCandidates {
		ch, ok := hydrated[c.chunkID]
		if !ok {
			continue
		}
		results = append(results, SearchResult{
			Chunk:       ch,
			Score:       c.rrfScore,
			LexicalRank: c.lexicalRank,
			DenseRank:   c.denseRank,
			MatchType:   c.matchType,
		})
	}

	return results, nil
}

func (s *Store) getChunksByIDs(ctx context.Context, ids []string) (map[string]Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT id, collection_id, document_id, chunk_index, path, start_line, end_line, content, created_at
		FROM chunks WHERE id IN (%s);`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]Chunk, len(ids))
	for rows.Next() {
		var ch Chunk
		if err := rows.Scan(&ch.ID, &ch.CollectionID, &ch.DocumentID, &ch.ChunkIndex, &ch.Path, &ch.StartLine, &ch.EndLine, &ch.Content, &ch.CreatedAt); err != nil {
			return nil, err
		}
		out[ch.ID] = ch
	}
	return out, rows.Err()
}

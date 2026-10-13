package store

import (
	"context"
	"fmt"
	"strings"
)

// LexicalMatch holds the outcome of a full-text search match from FTS5.
type LexicalMatch struct {
	ChunkID string
	Score   float64
	Rank    int
}

// sanitizeFTSQuery cleans user input into a valid SQLite FTS5 MATCH expression.
func sanitizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Remove problematic FTS5 characters: quotes, asterisks, parentheses, colons
	cleaner := strings.NewReplacer(
		`"`, ` `,
		`*`, ` `,
		`(`, ` `,
		`)`, ` `,
		`:`, ` `,
		`^`, ` `,
		`'`, ` `,
		`{`, ` `,
		`}`, ` `,
	)
	clean := cleaner.Replace(query)

	tokens := strings.Fields(clean)
	if len(tokens) == 0 {
		return ""
	}

	// Build token prefix search: "token1"* OR "token2"*
	var parts []string
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if len(tok) > 0 {
			parts = append(parts, fmt.Sprintf(`"%s"*`, tok))
		}
	}
	return strings.Join(parts, " OR ")
}

// LexicalSearch executes an FTS5 full-text search with BM25 ranking.
func (s *Store) LexicalSearch(ctx context.Context, collectionID, query string, topK int) ([]LexicalMatch, error) {
	if topK <= 0 {
		topK = 10
	}

	ftsExpr := sanitizeFTSQuery(query)
	if ftsExpr == "" {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// In SQLite FTS5, bm25() returns negative numbers; smaller numbers indicate stronger relevance.
	sqlQuery := `SELECT chunk_id, bm25(chunks_fts) as rank
		FROM chunks_fts
		WHERE chunks_fts MATCH ? AND collection_id = ?
		ORDER BY rank ASC
		LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, sqlQuery, ftsExpr, collectionID, topK)
	if err != nil {
		return nil, fmt.Errorf("execute FTS5 query %q: %w", ftsExpr, err)
	}
	defer rows.Close()

	var matches []LexicalMatch
	rank := 1
	for rows.Next() {
		var m LexicalMatch
		if err := rows.Scan(&m.ChunkID, &m.Score); err != nil {
			return nil, err
		}
		m.Rank = rank
		rank++
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("record not found")

// CreateCollection creates a new collection pinned to a specific embedding model and dimension.
func (s *Store) CreateCollection(ctx context.Context, name, model string, dimension int) (*Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	col := &Collection{
		ID:        uuid.NewString(),
		Name:      name,
		Model:     model,
		Dimension: dimension,
		CreatedAt: time.Now().UTC(),
	}

	query := `INSERT INTO collections (id, name, model, dimension, created_at) VALUES (?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, query, col.ID, col.Name, col.Model, col.Dimension, col.CreatedAt); err != nil {
		return nil, fmt.Errorf("create collection %q: %w", name, err)
	}

	return col, nil
}

// GetCollection retrieves a collection by its unique ID or unique name.
func (s *Store) GetCollection(ctx context.Context, idOrName string) (*Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, model, dimension, created_at FROM collections WHERE id = ? OR name = ? LIMIT 1;`
	row := s.db.QueryRowContext(ctx, query, idOrName, idOrName)

	var col Collection
	if err := row.Scan(&col.ID, &col.Name, &col.Model, &col.Dimension, &col.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get collection %q: %w", idOrName, err)
	}

	return &col, nil
}

// ListCollections returns all collections ordered by creation time.
func (s *Store) ListCollections(ctx context.Context) ([]Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, model, dimension, created_at FROM collections ORDER BY created_at ASC;`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	var cols []Collection
	for rows.Next() {
		var col Collection
		if err := rows.Scan(&col.ID, &col.Name, &col.Model, &col.Dimension, &col.CreatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// DeleteCollection deletes a collection and cascades to its documents, chunks, and FTS entries.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM collections WHERE id = ?;`
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete collection %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertDocument inserts or updates a document metadata record.
func (s *Store) UpsertDocument(ctx context.Context, doc *Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if doc.ID == "" {
		doc.ID = uuid.NewString()
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = time.Now().UTC()
	}

	query := `INSERT INTO documents (id, collection_id, path, hash, size_bytes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(collection_id, path) DO UPDATE SET
			hash = excluded.hash,
			size_bytes = excluded.size_bytes,
			updated_at = excluded.updated_at;`

	if _, err := s.db.ExecContext(ctx, query, doc.ID, doc.CollectionID, doc.Path, doc.Hash, doc.SizeBytes, doc.UpdatedAt); err != nil {
		return fmt.Errorf("upsert document %q: %w", doc.Path, err)
	}
	return nil
}

// GetDocument retrieves a document by collection ID and relative path.
func (s *Store) GetDocument(ctx context.Context, collectionID, path string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, collection_id, path, hash, size_bytes, updated_at FROM documents WHERE collection_id = ? AND path = ? LIMIT 1;`
	row := s.db.QueryRowContext(ctx, query, collectionID, path)

	var doc Document
	if err := row.Scan(&doc.ID, &doc.CollectionID, &doc.Path, &doc.Hash, &doc.SizeBytes, &doc.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get document %q in collection %q: %w", path, collectionID, err)
	}
	return &doc, nil
}

// ListDocuments returns all indexed documents in a collection.
func (s *Store) ListDocuments(ctx context.Context, collectionID string) ([]Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, collection_id, path, hash, size_bytes, updated_at FROM documents WHERE collection_id = ? ORDER BY path ASC;`
	rows, err := s.db.QueryContext(ctx, query, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.CollectionID, &doc.Path, &doc.Hash, &doc.SizeBytes, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// DeleteDocument removes a document and its chunks by collection ID and path.
func (s *Store) DeleteDocument(ctx context.Context, collectionID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM documents WHERE collection_id = ? AND path = ?;`
	res, err := s.db.ExecContext(ctx, query, collectionID, path)
	if err != nil {
		return fmt.Errorf("delete document %q: %w", path, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertChunks inserts a batch of chunks into the database.
func (s *Store) InsertChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO chunks (
		id, collection_id, document_id, chunk_index, path, start_line, end_line, content, embedding, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`)
	if err != nil {
		return fmt.Errorf("prepare chunk insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, ch := range chunks {
		id := ch.ID
		if id == "" {
			id = uuid.NewString()
		}
		var embBytes []byte
		if len(ch.Embedding) > 0 {
			embBytes = EncodeFloats(ch.Embedding)
		}

		if _, err := stmt.ExecContext(ctx, id, ch.CollectionID, ch.DocumentID, ch.ChunkIndex, ch.Path, ch.StartLine, ch.EndLine, ch.Content, embBytes, now); err != nil {
			return fmt.Errorf("insert chunk index %d for %s: %w", ch.ChunkIndex, ch.Path, err)
		}
	}

	return tx.Commit()
}

// DeleteDocumentChunks removes all existing chunks belonging to a document.
func (s *Store) DeleteDocumentChunks(ctx context.Context, documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM chunks WHERE document_id = ?;`
	_, err := s.db.ExecContext(ctx, query, documentID)
	return err
}

// ListChunks returns all chunks in a collection.
func (s *Store) ListChunks(ctx context.Context, collectionID string) ([]Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, collection_id, document_id, chunk_index, path, start_line, end_line, content, embedding, created_at
		FROM chunks WHERE collection_id = ? ORDER BY path ASC, chunk_index ASC;`
	rows, err := s.db.QueryContext(ctx, query, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var ch Chunk
		var embBytes []byte
		if err := rows.Scan(&ch.ID, &ch.CollectionID, &ch.DocumentID, &ch.ChunkIndex, &ch.Path, &ch.StartLine, &ch.EndLine, &ch.Content, &embBytes, &ch.CreatedAt); err != nil {
			return nil, err
		}
		if len(embBytes) > 0 {
			ch.Embedding = DecodeFloats(embBytes)
		}
		chunks = append(chunks, ch)
	}
	return chunks, rows.Err()
}

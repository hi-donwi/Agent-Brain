package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Store provides persistence for collections, documents, chunks, and vector index.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open initializes or connects to the SQLite database at dbPath, enabling WAL mode and migrations.
func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// Connect with modernc.org/sqlite
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database at %s: %w", dbPath, err)
	}

	// Restrict connection pool for SQLite file access
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate database schema: %w", err)
	}

	return s, nil
}

// Close closes the underlying SQLite database.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying sql.DB instance for testing.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS collections (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			model TEXT NOT NULL,
			dimension INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		);`,

		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			hash TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(collection_id, path)
		);`,

		`CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY,
			collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB,
			created_at DATETIME NOT NULL
		);`,

		`CREATE INDEX IF NOT EXISTS idx_chunks_collection ON chunks(collection_id);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_document ON chunks(document_id);`,

		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			chunk_id UNINDEXED,
			collection_id UNINDEXED,
			path,
			content,
			tokenize = 'porter unicode61'
		);`,

		`CREATE TRIGGER IF NOT EXISTS trg_chunks_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(chunk_id, collection_id, path, content)
			VALUES (new.id, new.collection_id, new.path, new.content);
		END;`,

		`CREATE TRIGGER IF NOT EXISTS trg_chunks_ad AFTER DELETE ON chunks BEGIN
			DELETE FROM chunks_fts WHERE chunk_id = old.id;
		END;`,
	}

	for _, stmt := range migrations {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec migration stmt: %w\nSQL: %s", err, stmt)
		}
	}

	return nil
}

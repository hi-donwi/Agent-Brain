package rag

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/store"
)

// IndexStats reports summary counters after an indexing pass.
type IndexStats struct {
	CollectionName string        `json:"collection"`
	RootPath       string        `json:"root_path"`
	FilesScanned   int           `json:"files_scanned"`
	FilesIndexed   int           `json:"files_indexed"`
	FilesSkipped   int           `json:"files_skipped"`
	FilesPruned    int           `json:"files_pruned"`
	ChunksCreated  int           `json:"chunks_created"`
	Duration       time.Duration `json:"duration"`
}

// Indexer coordinates walking files, chunking, embedding, and storage.
type Indexer struct {
	Store            *store.Store
	Adapter          backend.Adapter
	BatchSize        int
	AcquireEmbedding func(context.Context, string) (func(), error)
}

// NewIndexer creates an Indexer instance with sensible defaults.
func NewIndexer(s *store.Store, adapter backend.Adapter) *Indexer {
	return &Indexer{
		Store:     s,
		Adapter:   adapter,
		BatchSize: 16,
	}
}

// IndexDirectory indexes all text documents under rootPath into the specified collection.
func (idx *Indexer) IndexDirectory(ctx context.Context, rootPath, collectionName, embedModel string, onProgress func(relPath string, chunkCount int)) (*IndexStats, error) {
	start := time.Now()
	rootAbs, err := filepath.Abs(filepath.Clean(rootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve index root: %w", err)
	}
	rootPath, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("resolve index root: %w", err)
	}
	rootPath = filepath.Clean(rootPath)

	// 1. Ensure collection exists
	col, err := idx.Store.GetCollection(ctx, collectionName)
	if err != nil {
		dimension := 768
		if idx.Adapter != nil && embedModel != "" {
			if details, err := idx.Adapter.ShowModel(ctx, embedModel); err == nil && details.EmbeddingLength > 0 {
				dimension = details.EmbeddingLength
			}
		}
		if embedModel == "" {
			embedModel = "nomic-embed-text"
		}
		newCol, err := idx.Store.CreateCollection(ctx, collectionName, embedModel, dimension)
		if err != nil {
			return nil, fmt.Errorf("create collection %q: %w", collectionName, err)
		}
		col = newCol
	}

	// 2. Walk local files
	files, err := WalkFilesContext(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("scan files in %s: %w", rootPath, err)
	}

	// Map existing documents
	existingDocs, err := idx.Store.ListDocuments(ctx, col.ID)
	if err != nil {
		return nil, fmt.Errorf("list existing documents: %w", err)
	}
	existingMap := make(map[string]*store.Document, len(existingDocs))
	for i := range existingDocs {
		d := existingDocs[i]
		existingMap[d.Path] = &d
	}

	stats := &IndexStats{
		CollectionName: collectionName,
		RootPath:       rootPath,
		FilesScanned:   len(files),
	}

	seenPaths := make(map[string]bool, len(files))

	// 3. Process discovered files
	for _, relPath := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seenPaths[relPath] = true
		contentBytes, err := ReadRootedFile(rootPath, relPath)
		if err != nil {
			continue
		}
		content := string(contentBytes)
		hash := store.HashContent(content)

		// Incremental check: skip if identical hash exists
		if existing, ok := existingMap[relPath]; ok && existing.Hash == hash {
			stats.FilesSkipped++
			continue
		}

		// Chunk document
		drafts := store.ChunkDocument(relPath, content)
		if len(drafts) == 0 {
			stats.FilesSkipped++
			continue
		}

		docID := uuid.NewString()
		if existing, ok := existingMap[relPath]; ok {
			docID = existing.ID
		}

		doc := &store.Document{
			ID:           docID,
			CollectionID: col.ID,
			Path:         relPath,
			Hash:         hash,
			SizeBytes:    int64(len(contentBytes)),
			UpdatedAt:    time.Now().UTC(),
		}

		// Generate embeddings for chunk texts in batches
		var chunks []store.Chunk
		for i := 0; i < len(drafts); i += idx.BatchSize {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end := i + idx.BatchSize
			if end > len(drafts) {
				end = len(drafts)
			}

			batchDrafts := drafts[i:end]
			var batchTexts []string
			for _, d := range batchDrafts {
				batchTexts = append(batchTexts, d.Content)
			}

			var batchEmbeddings [][]float32
			var embeddingErr error
			if idx.Adapter != nil {
				var release func()
				if idx.AcquireEmbedding != nil {
					release, err = idx.AcquireEmbedding(ctx, col.Model)
					if err != nil {
						return nil, fmt.Errorf("acquire embedding model residency: %w", err)
					}
				}
				func() {
					if release != nil {
						defer release()
					}
					truncate := false
					resp, embedErr := idx.Adapter.Embed(ctx, backend.EmbedRequest{
						Model:    col.Model,
						Input:    batchTexts,
						Truncate: &truncate,
					})
					if embedErr == nil && resp != nil {
						batchEmbeddings = resp.Embeddings
					} else if ctx.Err() != nil {
						embeddingErr = ctx.Err()
					}
				}()
			}
			if embeddingErr != nil {
				return nil, embeddingErr
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			for bIdx, d := range batchDrafts {
				var emb []float32
				if bIdx < len(batchEmbeddings) {
					emb = batchEmbeddings[bIdx]
				}

				chunks = append(chunks, store.Chunk{
					CollectionID: col.ID,
					DocumentID:   docID,
					ChunkIndex:   d.ChunkIndex,
					Path:         relPath,
					StartLine:    d.StartLine,
					EndLine:      d.EndLine,
					Content:      d.Content,
					Embedding:    emb,
				})
			}
		}

		// Atomic store update: upsert doc first for foreign key constraint, then replace chunks
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := idx.Store.UpsertDocument(ctx, doc); err != nil {
			return nil, fmt.Errorf("upsert document %s: %w", relPath, err)
		}
		if err := idx.Store.DeleteDocumentChunks(ctx, docID); err != nil {
			return nil, fmt.Errorf("clear chunks for %s: %w", relPath, err)
		}
		if err := idx.Store.InsertChunks(ctx, chunks); err != nil {
			return nil, fmt.Errorf("insert chunks for %s: %w", relPath, err)
		}

		stats.FilesIndexed++
		stats.ChunksCreated += len(chunks)

		if onProgress != nil {
			onProgress(relPath, len(chunks))
		}
	}

	// 4. Prune removed files
	for existingPath := range existingMap {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !seenPaths[existingPath] {
			if err := idx.Store.DeleteDocument(ctx, col.ID, existingPath); err == nil {
				stats.FilesPruned++
			}
		}
	}

	stats.Duration = time.Since(start)
	return stats, nil
}

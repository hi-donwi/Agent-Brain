package store_test

import (
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/store"
)

func TestChunkMarkdown(t *testing.T) {
	md := `# Title
Introduction paragraph here.

## Section 1
First point under section 1.
Second point under section 1.

### Subsection 1.1
Detail in subsection.

## Section 2
Conclusion text.
`
	chunks := store.ChunkMarkdown(md, 500)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	if chunks[0].StartLine != 1 {
		t.Errorf("expected chunk 0 startLine 1, got %d", chunks[0].StartLine)
	}
	if !strings.Contains(chunks[0].Content, "# Title") {
		t.Errorf("chunk 0 missing title: %s", chunks[0].Content)
	}
}

func TestChunkCode(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		sb.WriteString("line of code\n")
	}
	code := sb.String()

	chunks := store.ChunkCode(code, 40, 10)
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	if chunks[0].StartLine != 1 || chunks[0].EndLine != 40 {
		t.Errorf("chunk 0 bounds mismatch: %d-%d", chunks[0].StartLine, chunks[0].EndLine)
	}
	if chunks[1].StartLine != 31 || chunks[1].EndLine != 70 {
		t.Errorf("chunk 1 bounds mismatch: %d-%d", chunks[1].StartLine, chunks[1].EndLine)
	}
}

func TestChunkText(t *testing.T) {
	text := `Paragraph one has multiple sentences.
It continues on the next line.

Paragraph two is distinct from paragraph one.
It discusses a different topic.
`
	chunks := store.ChunkText(text, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestHashContent(t *testing.T) {
	c1 := "Hello World"
	h1 := store.HashContent(c1)
	h2 := store.HashContent(c1)
	if h1 != h2 {
		t.Fatalf("hashes should be deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64 hex chars for SHA-256, got %d", len(h1))
	}
}

func TestChunkDocument_Dispatch(t *testing.T) {
	mdChunks := store.ChunkDocument("doc.md", "# Heading\nText")
	if len(mdChunks) == 0 {
		t.Error("expected markdown chunks")
	}

	codeChunks := store.ChunkDocument("main.go", "package main\n\nfunc main() {}\n")
	if len(codeChunks) == 0 {
		t.Error("expected code chunks")
	}

	txtChunks := store.ChunkDocument("notes.txt", "Plain text notes")
	if len(txtChunks) == 0 {
		t.Error("expected text chunks")
	}
}

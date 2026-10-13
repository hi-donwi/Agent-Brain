package store

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ChunkDraft represents a segmented slice of a document before database insertion.
type ChunkDraft struct {
	ChunkIndex int    `json:"chunk_index"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Content    string `json:"content"`
}

// HashContent computes the SHA-256 hexadecimal hash of document content.
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Default chunking parameters
const (
	DefaultMarkdownMaxChars = 2000
	DefaultCodeWindowLines  = 60
	DefaultCodeOverlapLines = 10
	DefaultTextMaxChars     = 1500
)

// ChunkMarkdown splits markdown documents using heading hierarchy (#, ##, ###).
func ChunkMarkdown(content string, maxChars int) []ChunkDraft {
	if maxChars <= 0 {
		maxChars = DefaultMarkdownMaxChars
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []ChunkDraft
	var curLines []string
	startLine := 1

	for idx, line := range lines {
		lineNum := idx + 1
		isHeading := strings.HasPrefix(strings.TrimSpace(line), "#")

		curLen := 0
		for _, cl := range curLines {
			curLen += len(cl) + 1
		}

		if (isHeading && len(curLines) > 0) || (curLen+len(line) > maxChars && len(curLines) > 0) {
			chunkContent := strings.Join(curLines, "\n")
			if strings.TrimSpace(chunkContent) != "" {
				chunks = append(chunks, ChunkDraft{
					ChunkIndex: len(chunks),
					StartLine:  startLine,
					EndLine:    lineNum - 1,
					Content:    chunkContent,
				})
			}
			curLines = nil
			startLine = lineNum
		}

		curLines = append(curLines, line)
	}

	if len(curLines) > 0 {
		chunkContent := strings.Join(curLines, "\n")
		if strings.TrimSpace(chunkContent) != "" {
			chunks = append(chunks, ChunkDraft{
				ChunkIndex: len(chunks),
				StartLine:  startLine,
				EndLine:    len(lines),
				Content:    chunkContent,
			})
		}
	}

	return chunks
}

// ChunkCode splits source code using windowed line spans with configurable overlap.
func ChunkCode(content string, windowLines, overlapLines int) []ChunkDraft {
	if windowLines <= 0 {
		windowLines = DefaultCodeWindowLines
	}
	if overlapLines < 0 || overlapLines >= windowLines {
		overlapLines = DefaultCodeOverlapLines
	}

	lines := strings.Split(content, "\n")
	n := len(lines)
	if n == 0 {
		return nil
	}

	var chunks []ChunkDraft
	step := windowLines - overlapLines
	if step <= 0 {
		step = 1
	}

	for start := 0; start < n; start += step {
		end := start + windowLines
		if end > n {
			end = n
		}

		windowContent := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(windowContent) != "" {
			chunks = append(chunks, ChunkDraft{
				ChunkIndex: len(chunks),
				StartLine:  start + 1,
				EndLine:    end,
				Content:    windowContent,
			})
		}

		if end == n {
			break
		}
	}

	return chunks
}

// ChunkText splits plain text documents by paragraph boundaries.
func ChunkText(content string, maxChars int) []ChunkDraft {
	if maxChars <= 0 {
		maxChars = DefaultTextMaxChars
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []ChunkDraft
	var curLines []string
	startLine := 1

	for idx, line := range lines {
		lineNum := idx + 1
		isParagraphBreak := strings.TrimSpace(line) == ""

		curLen := 0
		for _, cl := range curLines {
			curLen += len(cl) + 1
		}

		if (isParagraphBreak && curLen > maxChars/2) || (curLen+len(line) > maxChars && len(curLines) > 0) {
			chunkContent := strings.Join(curLines, "\n")
			if strings.TrimSpace(chunkContent) != "" {
				chunks = append(chunks, ChunkDraft{
					ChunkIndex: len(chunks),
					StartLine:  startLine,
					EndLine:    lineNum - 1,
					Content:    chunkContent,
				})
			}
			curLines = nil
			startLine = lineNum
		}

		curLines = append(curLines, line)
	}

	if len(curLines) > 0 {
		chunkContent := strings.Join(curLines, "\n")
		if strings.TrimSpace(chunkContent) != "" {
			chunks = append(chunks, ChunkDraft{
				ChunkIndex: len(chunks),
				StartLine:  startLine,
				EndLine:    len(lines),
				Content:    chunkContent,
			})
		}
	}

	return chunks
}

// ChunkDocument dispatches document chunking according to file extension.
func ChunkDocument(path, content string) []ChunkDraft {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".mdown":
		return ChunkMarkdown(content, DefaultMarkdownMaxChars)
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".c", ".cpp", ".cc", ".h", ".hpp",
		".sh", ".bash", ".zsh", ".sql", ".json", ".yaml", ".yml", ".toml", ".html", ".css", ".xml":
		return ChunkCode(content, DefaultCodeWindowLines, DefaultCodeOverlapLines)
	default:
		return ChunkText(content, DefaultTextMaxChars)
	}
}

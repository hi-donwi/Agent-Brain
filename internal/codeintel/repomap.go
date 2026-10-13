package codeintel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/rag"
)

// FileSymbols aggregates symbols found in one source file.
type FileSymbols struct {
	Path    string   `json:"path"`
	Symbols []Symbol `json:"symbols"`
	Score   float64  `json:"score"`
}

// RepoMap contains the ranked repository outline.
type RepoMap struct {
	Root            string        `json:"root"`
	FileCount       int           `json:"file_count"`
	TotalSymbols    int           `json:"total_symbols"`
	EstimatedTokens int           `json:"estimated_tokens"`
	Files           []FileSymbols `json:"files"`
	Formatted       string        `json:"formatted"`
}

// BuildRepoMap scans rootDir and constructs a ranked repository symbol map within tokenBudget.
func BuildRepoMap(rootDir string, tokenBudget int) (*RepoMap, error) {
	if tokenBudget <= 0 {
		tokenBudget = 2048
	}

	cleanRoot := filepath.Clean(rootDir)
	discovered, err := rag.WalkFiles(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("walk repository %s: %w", cleanRoot, err)
	}

	var fileSyms []FileSymbols
	totalSymCount := 0

	for _, relPath := range discovered {
		fullPath := filepath.Join(cleanRoot, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		symbols, err := ExtractSymbols(relPath, content)
		if err != nil || len(symbols) == 0 {
			continue
		}

		totalSymCount += len(symbols)
		score := scoreFile(relPath, symbols)

		fileSyms = append(fileSyms, FileSymbols{
			Path:    relPath,
			Symbols: symbols,
			Score:   score,
		})
	}

	// Sort files by relevance score descending
	sort.Slice(fileSyms, func(i, j int) bool {
		if fileSyms[i].Score == fileSyms[j].Score {
			return fileSyms[i].Path < fileSyms[j].Path
		}
		return fileSyms[i].Score > fileSyms[j].Score
	})

	var selectedFiles []FileSymbols
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Repository Map (%d code files, %d symbols)\n\n", len(fileSyms), totalSymCount))

	usedTokens := len(sb.String()) / 4

	for _, fs := range fileSyms {
		var fileBlock strings.Builder
		fileBlock.WriteString(fmt.Sprintf("## %s\n", fs.Path))

		for _, sym := range fs.Symbols {
			// Write signature
			sig := sym.Signature
			if sig == "" {
				sig = fmt.Sprintf("%s %s", sym.Kind, sym.Name)
			}
			fileBlock.WriteString(fmt.Sprintf("  %s\n", sig))
		}
		fileBlock.WriteString("\n")

		blockTokens := (fileBlock.Len() + 3) / 4
		if usedTokens+blockTokens > tokenBudget && len(selectedFiles) > 0 {
			// Stop if adding this file exceeds caller budget
			break
		}

		sb.WriteString(fileBlock.String())
		selectedFiles = append(selectedFiles, fs)
		usedTokens += blockTokens
	}

	formatted := sb.String()
	estTokens := (len(formatted) + 3) / 4

	return &RepoMap{
		Root:            cleanRoot,
		FileCount:       len(fileSyms),
		TotalSymbols:    totalSymCount,
		EstimatedTokens: estTokens,
		Files:           selectedFiles,
		Formatted:       formatted,
	}, nil
}

func scoreFile(relPath string, symbols []Symbol) float64 {
	score := 1.0

	// Boost entry points and root files
	base := filepath.Base(relPath)
	if base == "main.go" || strings.HasPrefix(relPath, "cmd/") {
		score += 5.0
	}
	if !strings.Contains(relPath, "/") && !strings.Contains(relPath, "\\") {
		score += 2.0
	}

	// Boost files with core types or interfaces
	for _, s := range symbols {
		switch s.Kind {
		case KindInterface:
			score += 1.5
		case KindStruct, KindClass:
			score += 1.0
		case KindFunction:
			score += 0.5
		}
	}

	return score
}

package codeintel

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/rag"
)

// CodeReference records a usage or reference to a symbol.
type CodeReference struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// SymbolSearchResult contains definitions and references for a symbol query.
type SymbolSearchResult struct {
	Query       string          `json:"query"`
	Definitions []Symbol        `json:"definitions"`
	References  []CodeReference `json:"references"`
}

// CodeMatch records a pattern search hit.
type CodeMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// FindSymbol searches rootDir for definitions and references of symbolName.
func FindSymbol(rootDir, symbolName string, maxReferences int) (*SymbolSearchResult, error) {
	if maxReferences <= 0 {
		maxReferences = 20
	}

	cleanRoot := filepath.Clean(rootDir)
	discovered, err := rag.WalkFiles(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("walk repository %s: %w", cleanRoot, err)
	}

	target := strings.TrimSpace(symbolName)
	if target == "" {
		return nil, fmt.Errorf("symbol name cannot be empty")
	}

	result := &SymbolSearchResult{
		Query: target,
	}

	defLines := make(map[string]map[int]bool) // path -> set of definition lines

	for _, relPath := range discovered {
		fullPath := filepath.Join(cleanRoot, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		symbols, err := ExtractSymbols(relPath, content)
		if err == nil {
			for _, s := range symbols {
				if s.Name == target || strings.EqualFold(s.Name, target) {
					result.Definitions = append(result.Definitions, s)
					if defLines[relPath] == nil {
						defLines[relPath] = make(map[int]bool)
					}
					for l := s.Line; l <= s.EndLine; l++ {
						defLines[relPath][l] = true
					}
				}
			}
		}

		// Scan references if we still have reference quota
		if len(result.References) < maxReferences {
			scanner := bufio.NewScanner(bytes.NewReader(content))
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				if strings.Contains(line, target) {
					// Exclude line if it is part of a definition
					if defLines[relPath] != nil && defLines[relPath][lineNum] {
						continue
					}
					result.References = append(result.References, CodeReference{
						Path:    relPath,
						Line:    lineNum,
						Content: strings.TrimSpace(line),
					})
					if len(result.References) >= maxReferences {
						break
					}
				}
			}
		}
	}

	return result, nil
}

// SearchCode performs a regex search across text files in rootDir.
func SearchCode(rootDir, pattern string, maxResults int) ([]CodeMatch, error) {
	if maxResults <= 0 {
		maxResults = 30
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	cleanRoot := filepath.Clean(rootDir)
	discovered, err := rag.WalkFiles(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("walk repository %s: %w", cleanRoot, err)
	}

	var matches []CodeMatch

	for _, relPath := range discovered {
		fullPath := filepath.Join(cleanRoot, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(bytes.NewReader(content))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, CodeMatch{
					Path:    relPath,
					Line:    lineNum,
					Snippet: strings.TrimSpace(line),
				})
				if len(matches) >= maxResults {
					return matches, nil
				}
			}
		}
	}

	return matches, nil
}

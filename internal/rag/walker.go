package rag

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// MaxFileSizeBytes is the maximum allowed file size for indexing (1 MiB).
	MaxFileSizeBytes = 1 << 20
)

var defaultIgnoredDirs = map[string]bool{
	".git":         true,
	".local":       true,
	".agents":      false, // We may want to index agent instructions
	".idea":        true,
	".vscode":      true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"bin":          true,
	"obj":          true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"dist":         true,
	"build":        true,
}

var binaryExtensions = map[string]bool{
	".exe":    true,
	".dll":    true,
	".so":     true,
	".dylib":  true,
	".class":  true,
	".jar":    true,
	".zip":    true,
	".tar":    true,
	".gz":     true,
	".7z":     true,
	".png":    true,
	".jpg":    true,
	".jpeg":   true,
	".gif":    true,
	".svg":    false, // SVG is text XML
	".ico":    true,
	".webp":   true,
	".pdf":    true,
	".pyc":    true,
	".o":      true,
	".a":      true,
	".db":     true,
	".sqlite": true,
}

// IsBinaryFile inspects the leading bytes of a file to detect binary content.
func IsBinaryFile(path string) (bool, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if binaryExtensions[ext] {
		return true, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	if n == 0 {
		return false, nil // Empty file is considered text
	}

	buf = buf[:n]
	for _, b := range buf {
		if b == 0x00 { // Null byte
			return true, nil
		}
	}

	if !utf8.Valid(buf) {
		return true, nil
	}

	return false, nil
}

// IgnoreRules holds patterns parsed from .gitignore and .lokalignore.
type IgnoreRules struct {
	patterns []string
}

// NewIgnoreRules loads ignore patterns from an optional file.
func NewIgnoreRules(path string) *IgnoreRules {
	r := &IgnoreRules{}
	f, err := os.Open(path)
	if err != nil {
		return r
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r.patterns = append(r.patterns, line)
	}
	return r
}

// Matches tests whether a relative path matches any ignore pattern.
func (r *IgnoreRules) Matches(relPath string) bool {
	cleanRel := filepath.ToSlash(relPath)
	for _, p := range r.patterns {
		p = filepath.ToSlash(strings.TrimPrefix(p, "/"))
		p = strings.TrimSuffix(p, "/")

		matched, err := filepath.Match(p, cleanRel)
		if err == nil && matched {
			return true
		}
		matched, err = filepath.Match(p, filepath.Base(cleanRel))
		if err == nil && matched {
			return true
		}
		if strings.HasPrefix(cleanRel, p+"/") {
			return true
		}
	}
	return false
}

// WalkFiles discovers all indexable text files within a root directory.
func WalkFiles(root string) ([]string, error) {
	root = filepath.Clean(root)

	gitIgnore := NewIgnoreRules(filepath.Join(root, ".gitignore"))
	lokalIgnore := NewIgnoreRules(filepath.Join(root, ".lokalignore"))

	var files []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable paths
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		if info.IsDir() {
			base := info.Name()
			if defaultIgnoredDirs[base] {
				return filepath.SkipDir
			}
			if gitIgnore.Matches(rel) || lokalIgnore.Matches(rel) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden dotfiles (.gitignore, .lokalignore, .DS_Store, etc.)
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Skip files matching ignore patterns
		if gitIgnore.Matches(rel) || lokalIgnore.Matches(rel) {
			return nil
		}

		// Size check
		if info.Size() == 0 || info.Size() > MaxFileSizeBytes {
			return nil
		}

		// Binary check
		isBinary, err := IsBinaryFile(path)
		if err != nil || isBinary {
			return nil
		}

		files = append(files, rel)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return files, nil
}

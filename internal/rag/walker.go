package rag

import (
	"bufio"
	"context"
	"errors"
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

	return isBinaryContent(buf[:n]), nil
}

func isBinaryContent(buf []byte) bool {
	for _, b := range buf {
		if b == 0x00 { // Null byte
			return true
		}
	}

	if !utf8.Valid(buf) {
		return true
	}

	return false
}

// ReadRootedFile opens a regular file below root without following a final symlink,
// checks the resolved target remains below root, and enforces the index file-size cap.
func ReadRootedFile(root, relativePath string) ([]byte, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Stat(rootReal)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("index root is not a readable directory")
	}
	rel := filepath.Clean(relativePath)
	if filepath.IsAbs(relativePath) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("index path is outside its root")
	}
	fullPath := filepath.Join(rootReal, rel)
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return nil, err
	}
	if !pathWithin(rootReal, resolved) {
		return nil, errors.New("index path resolves outside its root")
	}
	linkInfo, err := os.Lstat(fullPath)
	if err != nil || linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return nil, errors.New("index path must be a regular non-symlink file")
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) {
		return nil, errors.New("index file changed while opening")
	}
	if openedInfo.Size() <= 0 || openedInfo.Size() > MaxFileSizeBytes {
		return nil, errors.New("index file size is outside the supported range")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileSizeBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxFileSizeBytes {
		return nil, errors.New("index file size is outside the supported range")
	}
	resolvedAgain, err := filepath.EvalSymlinks(fullPath)
	if err != nil || resolvedAgain != resolved || !pathWithin(rootReal, resolvedAgain) {
		return nil, errors.New("index file changed while reading")
	}
	finalInfo, err := os.Stat(resolvedAgain)
	if err != nil || !os.SameFile(openedInfo, finalInfo) {
		return nil, errors.New("index file changed while reading")
	}
	return data, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
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
	return WalkFilesContext(context.Background(), root)
}

// WalkFilesContext discovers indexable files under root and stops promptly when cancelled.
func WalkFilesContext(ctx context.Context, root string) ([]string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(rootAbs))
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("index root must be a readable directory")
	}

	gitIgnore := NewIgnoreRules(filepath.Join(root, ".gitignore"))
	lokalIgnore := NewIgnoreRules(filepath.Join(root, ".lokalignore"))

	var files []string

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil // Skip unreadable paths
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
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
		if binaryExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}

		// Read through the rooted-file guard so a replaced symlink is never opened.
		content, err := ReadRootedFile(root, rel)
		if err != nil || isBinaryContent(content) {
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

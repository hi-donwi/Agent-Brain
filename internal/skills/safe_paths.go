package skills

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxSkillDocumentBytes = 2 << 20

func resolveSkillDirectory(sourceRoot, name string) (string, error) {
	if !nameRegex.MatchString(name) {
		return "", ErrInvalidPath
	}
	rootAbs, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", ErrInvalidPath
	}
	root, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return "", ErrSkillNotFound
	}
	dir := filepath.Join(root, name)
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", ErrInvalidPath
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(dir) || !skillPathWithin(root, resolved) {
		return "", ErrInvalidPath
	}
	if _, err := readSkillFile(resolved, "SKILL.md", maxSkillDocumentBytes); err != nil {
		return "", err
	}
	return resolved, nil
}

func resolveUserSkillDirectory(userRoot, name string) (string, error) {
	if !nameRegex.MatchString(name) {
		return "", ErrInvalidPath
	}
	rootAbs, err := filepath.Abs(userRoot)
	if err != nil {
		return "", ErrInvalidPath
	}
	root, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	dir := filepath.Join(root, name)
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", ErrInvalidPath
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(dir) || !skillPathWithin(root, resolved) {
		return "", ErrInvalidPath
	}
	return resolved, nil
}

func readSkillFile(root, relative string, maxBytes int64) ([]byte, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrInvalidPath
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, err
	}
	rel := filepath.Clean(relative)
	if filepath.IsAbs(relative) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, ErrInvalidPath
	}
	current := filepath.Clean(rootReal)
	parts := strings.Split(rel, string(filepath.Separator))
	var info os.FileInfo
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, ErrInvalidPath
		}
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrInvalidPath
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, ErrInvalidPath
		}
	}
	if info == nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxBytes {
		return nil, ErrInvalidPath
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !skillPathWithin(rootReal, resolved) {
		return nil, ErrInvalidPath
	}
	f, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, ErrInvalidPath
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrInvalidPath
	}
	resolvedAfterRead, err := filepath.EvalSymlinks(current)
	if err != nil || filepath.Clean(resolvedAfterRead) != filepath.Clean(resolved) || !skillPathWithin(rootReal, resolvedAfterRead) {
		return nil, ErrInvalidPath
	}
	finalInfo, err := os.Stat(resolvedAfterRead)
	if err != nil || !os.SameFile(opened, finalInfo) {
		return nil, ErrInvalidPath
	}
	return data, nil
}

// ReadSafeSkillFile reads a bounded regular file under root without following symlinks.
func ReadSafeSkillFile(root, relative string, maxBytes int64) ([]byte, error) {
	return readSkillFile(root, relative, maxBytes)
}

func skillPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isMissingSkillPath(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

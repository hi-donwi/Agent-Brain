package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadProjectRegistry gives operator directories precedence and confines the
// implicit repository directory, including every symlink in its parent path.
func LoadProjectRegistry(root string, trustedDirs ...string) (*Registry, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(realRoot)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project root must be a directory")
	}
	project := filepath.Join(realRoot, ".brain", "agents")
	resolved, err := filepath.EvalSymlinks(project)
	if os.IsNotExist(err) {
		return LoadRegistry(trustedDirs...)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve project sub-agent directory: %w", err)
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("project sub-agent directory resolves outside the root")
	}
	dirs := append(append([]string(nil), trustedDirs...), resolved)
	return LoadRegistry(dirs...)
}

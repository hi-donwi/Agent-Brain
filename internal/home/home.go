// Package home locates the lokal home: the one directory that holds everything
// lokal writes (config, database, gateway token, skills, candidates, traces).
// Deleting it resets lokal; models stay with their backend.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar overrides the default location, for example on a VM where state belongs
// on a data disk.
const EnvVar = "LOKAL_HOME"

const dirName = ".lokal"

// Layout is the set of paths inside one lokal home.
type Layout struct {
	Root       string
	Config     string // config.yaml
	DB         string // lokal.db: collections, chunks, vectors, trace index, skill registry
	Token      string // serve.token: gateway bearer token, mode 0600
	Skills     string // promoted skills
	Candidates string // skill forge output, never read by the router
	Traces     string // one JSONL file per agent run
}

// Default resolves the layout from the process environment.
func Default() (Layout, error) {
	return Resolve(os.Getenv, os.UserHomeDir)
}

// Resolve returns the layout rooted at $LOKAL_HOME, or at ~/.lokal when it is
// unset. LOKAL_HOME must be absolute: a relative value would scatter state across
// whatever directory lokal happens to run in.
func Resolve(getenv func(string) string, userHomeDir func() (string, error)) (Layout, error) {
	if v := strings.TrimSpace(getenv(EnvVar)); v != "" {
		if !filepath.IsAbs(v) {
			return Layout{}, fmt.Errorf("%s must be an absolute path, got %q", EnvVar, v)
		}
		return layout(filepath.Clean(v)), nil
	}
	dir, err := userHomeDir()
	if err == nil && dir == "" {
		err = errors.New("home directory is empty")
	}
	if err != nil {
		return Layout{}, fmt.Errorf("cannot determine the home directory (%v); set %s", err, EnvVar)
	}
	return layout(filepath.Join(dir, dirName)), nil
}

func layout(root string) Layout {
	return Layout{
		Root:       root,
		Config:     filepath.Join(root, "config.yaml"),
		DB:         filepath.Join(root, "lokal.db"),
		Token:      filepath.Join(root, "serve.token"),
		Skills:     filepath.Join(root, "skills"),
		Candidates: filepath.Join(root, "candidates"),
		Traces:     filepath.Join(root, "traces"),
	}
}

// Ensure creates the home and its directories, readable only by the user.
// Existing directories are left as they are; files are created by their owners.
func (l Layout) Ensure() error {
	for _, dir := range []string{l.Root, l.Skills, l.Candidates, l.Traces} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

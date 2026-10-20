package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultSubagentTurns is the turn budget when a definition does not set one.
	DefaultSubagentTurns = 20
	// MaxSubagentTurns is the highest turn budget a definition may ask for.
	MaxSubagentTurns = 50
	// MaxDefinitionBytes caps one definition file.
	MaxDefinitionBytes = 32 * 1024
)

var subagentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// defaultSubagentTools is what a definition that lists no tools gets: it can look, not touch.
var defaultSubagentTools = []string{ToolRead, ToolSearch}

// Definition is a sub-agent: a named role with its own prompt, a tool allow-list, and a
// turn budget. It is described in a Markdown file with YAML frontmatter, the same shape
// as a skill, so existing editors and review habits apply.
type Definition struct {
	Name        string
	Description string
	// Tools is the allow-list. A definition that omits it is read-only.
	Tools []string
	// Model is an optional model id; empty means the host's choice for the session.
	Model    string
	MaxTurns int
	// Prompt is the Markdown body: the sub-agent's own instructions.
	Prompt string
	// Source is the file the definition came from.
	Source string
}

type definitionFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	Model       string   `yaml:"model"`
	MaxTurns    int      `yaml:"max_turns"`
}

// ParseDefinition reads one definition. file is the file name or path; the definition's
// name must equal the file's base name so a role cannot hide behind another file's name.
func ParseDefinition(file string, content []byte) (Definition, error) {
	text := strings.TrimPrefix(string(content), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	front, body, err := splitFrontmatter(text)
	if err != nil {
		return Definition{}, err
	}

	var fm definitionFrontmatter
	dec := yaml.NewDecoder(strings.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
		return Definition{}, fmt.Errorf("invalid frontmatter: %w", err)
	}

	switch {
	case !subagentName.MatchString(fm.Name):
		return Definition{}, fmt.Errorf("name %q must be lowercase letters, digits, and hyphens, starting with a letter", fm.Name)
	case fm.Name != strings.TrimSuffix(filepath.Base(file), ".md"):
		return Definition{}, fmt.Errorf("name %q must match the file name %q", fm.Name, filepath.Base(file))
	case strings.TrimSpace(fm.Description) == "":
		return Definition{}, errors.New("description is required")
	case fm.MaxTurns < 0 || fm.MaxTurns > MaxSubagentTurns:
		return Definition{}, fmt.Errorf("max_turns %d must be between 1 and %d", fm.MaxTurns, MaxSubagentTurns)
	}
	prompt := strings.TrimSpace(body)
	if prompt == "" {
		return Definition{}, errors.New("the prompt body is empty")
	}

	tools := fm.Tools
	if tools == nil {
		tools = append([]string(nil), defaultSubagentTools...)
	}
	for _, t := range tools {
		if !isKnownTool(t) {
			return Definition{}, fmt.Errorf("unknown tool %q", t)
		}
	}
	turns := fm.MaxTurns
	if turns == 0 {
		turns = DefaultSubagentTurns
	}
	return Definition{
		Name:        fm.Name,
		Description: strings.TrimSpace(fm.Description),
		Tools:       tools,
		Model:       strings.TrimSpace(fm.Model),
		MaxTurns:    turns,
		Prompt:      prompt,
		Source:      file,
	}, nil
}

// splitFrontmatter separates a leading "---" block from the Markdown that follows.
func splitFrontmatter(text string) (front, body string, err error) {
	if !strings.HasPrefix(text, "---\n") {
		return "", "", errors.New("missing leading frontmatter delimiter (---)")
	}
	s := "\n" + text[4:]
	from := 0
	for {
		j := strings.Index(s[from:], "\n---")
		if j < 0 {
			return "", "", errors.New("missing closing frontmatter delimiter (---)")
		}
		j += from
		end := j + 4
		if end == len(s) || s[end] == '\n' {
			if j > 0 {
				front = s[1:j]
			}
			return front, s[end:], nil
		}
		from = j + 1
	}
}

// Effective is the policy a sub-agent runs under: the parent's policy limited to the
// definition's tools. It can only be narrower than the parent's, never wider, and a
// sub-agent cannot start another unless "subagent" is in its list.
func (d Definition) Effective(parent Policy) Policy {
	return parent.Restrict(d.Tools)
}

// Registry holds the sub-agent definitions found in a set of directories.
type Registry struct {
	defs map[string]Definition
	// Conflicts lists definitions ignored because an earlier directory defined the name.
	Conflicts []string
	// Errors lists files that could not be loaded, with the reason.
	Errors []string
}

// LoadRegistry reads *.md definitions from each directory. List the directories from most
// to least trusted: when two define the same name the earlier wins and the later is
// reported as a conflict, so a repository cannot replace the operator's own role. Symbolic
// links are not followed. A file that fails to load is reported and does not stop the
// others; a missing directory is not an error.
func LoadRegistry(dirs ...string) (*Registry, error) {
	reg := &Registry{defs: map[string]Definition{}}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".md" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			def, err := loadDefinitionFile(path)
			if err != nil {
				reg.Errors = append(reg.Errors, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			if prior, dup := reg.defs[def.Name]; dup {
				reg.Conflicts = append(reg.Conflicts, fmt.Sprintf("%s: %s is shadowed by %s", def.Name, path, prior.Source))
				continue
			}
			reg.defs[def.Name] = def
		}
	}
	return reg, nil
}

func loadDefinitionFile(path string) (Definition, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Definition{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Definition{}, errors.New("symbolic links are not followed")
	}
	if !info.Mode().IsRegular() {
		return Definition{}, errors.New("not a regular file")
	}
	if info.Size() > MaxDefinitionBytes {
		return Definition{}, fmt.Errorf("larger than %d bytes", MaxDefinitionBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, err
	}
	return ParseDefinition(path, data)
}

// Get returns the definition with the given name.
func (r *Registry) Get(name string) (Definition, bool) {
	d, ok := r.defs[name]
	return d, ok
}

// List returns every definition, sorted by name.
func (r *Registry) List() []Definition {
	out := make([]Definition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

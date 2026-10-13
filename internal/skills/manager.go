package skills

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrSkillNotFound     = errors.New("skill not found")
	ErrReferenceNotFound = errors.New("reference not found")
	ErrInvalidPath       = errors.New("invalid or unsafe path")
)

// SourceLocation describes a directory checked for skills.
type SourceLocation struct {
	Type SourceType `json:"type"`
	Path string     `json:"path"`
}

// Manager coordinates multi-source skill discovery, resolution precedence, and loading.
type Manager struct {
	sources []SourceLocation
	userDir string
}

// NewManager constructs a Manager adhering to the ADR-0004 precedence order:
// 1. Project skills (.agents/skills inside projectDir if specified)
// 2. User skills (~/.lokal/skills / userDir)
// 3. Configured directories from config.yaml
func NewManager(projectDir, userDir string, configuredDirs []string) *Manager {
	var sources []SourceLocation

	// 1. Project skills
	if projectDir != "" {
		pSkills := filepath.Join(projectDir, ".agents", "skills")
		sources = append(sources, SourceLocation{
			Type: SourceProject,
			Path: filepath.Clean(pSkills),
		})
	}

	// 2. User skills
	cleanUser := filepath.Clean(userDir)
	if cleanUser != "" && cleanUser != "." {
		sources = append(sources, SourceLocation{
			Type: SourceUser,
			Path: cleanUser,
		})
	}

	// 3. Configured directories
	for _, dir := range configuredDirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed != "" {
			sources = append(sources, SourceLocation{
				Type: SourceConfigured,
				Path: filepath.Clean(trimmed),
			})
		}
	}

	return &Manager{
		sources: sources,
		userDir: cleanUser,
	}
}

// Sources returns the configured search locations in precedence order.
func (m *Manager) Sources() []SourceLocation {
	out := make([]SourceLocation, len(m.sources))
	copy(out, m.sources)
	return out
}

// List discovers all available skills, deduplicated by name using source precedence.
func (m *Manager) List() ([]SkillSummary, error) {
	seen := make(map[string]bool)
	var list []SkillSummary

	for _, src := range m.sources {
		entries, err := os.ReadDir(src.Path)
		if err != nil {
			// A source directory might not exist yet; ignore missing directories gracefully
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || seen[name] {
				continue
			}

			skillDir := filepath.Join(src.Path, name)
			skillMD := filepath.Join(skillDir, "SKILL.md")
			if _, err := os.Stat(skillMD); err != nil {
				continue
			}

			summary, err := LoadSkillSummary(skillDir, src.Type)
			if err != nil {
				// Skip malformed entries during listing, but continue
				continue
			}

			seen[name] = true
			list = append(list, *summary)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list, nil
}

// GetSkill resolves a skill by name, returning the first occurrence according to source precedence.
func (m *Manager) GetSkill(name string) (*Skill, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return nil, ErrSkillNotFound
	}

	for _, src := range m.sources {
		skillDir := filepath.Join(src.Path, cleanName)
		skillMD := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillMD); err == nil {
			return LoadSkill(skillDir, src.Type)
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrSkillNotFound, cleanName)
}

// GetReference loads the content of a specific reference file inside a skill.
func (m *Manager) GetReference(skillName, refRelPath string) (string, error) {
	skill, err := m.GetSkill(skillName)
	if err != nil {
		return "", err
	}

	cleanRef := filepath.Clean(refRelPath)
	if filepath.IsAbs(cleanRef) || strings.HasPrefix(cleanRef, "..") || strings.Contains(cleanRef, "/../") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, refRelPath)
	}

	refPath := filepath.Join(skill.Summary.Path, "references", cleanRef)
	data, err := os.ReadFile(refPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %q in skill %q", ErrReferenceNotFound, cleanRef, skillName)
		}
		return "", fmt.Errorf("read reference %s: %w", refPath, err)
	}

	return string(data), nil
}

// AddSkill copies a valid skill from srcPath into the user's skill directory (~/.lokal/skills/<name>).
func (m *Manager) AddSkill(srcPath string) (*Skill, error) {
	if m.userDir == "" {
		return nil, errors.New("user skill directory is not configured")
	}

	validation := ValidateDir(srcPath)
	if !validation.Valid {
		return nil, fmt.Errorf("skill validation failed: %s", validation.Error())
	}

	// Load source skill
	srcSkill, err := LoadSkill(srcPath, SourceUser)
	if err != nil {
		return nil, fmt.Errorf("cannot load skill from %s: %w", srcPath, err)
	}

	destDir := filepath.Join(m.userDir, srcSkill.Summary.Name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create destination directory %s: %w", destDir, err)
	}

	if err := copyDirectory(srcPath, destDir); err != nil {
		return nil, fmt.Errorf("copy skill files: %w", err)
	}

	return LoadSkill(destDir, SourceUser)
}

func copyDirectory(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcChild := filepath.Join(src, entry.Name())
		dstChild := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if err := os.MkdirAll(dstChild, 0o755); err != nil {
				return err
			}
			if err := copyDirectory(srcChild, dstChild); err != nil {
				return err
			}
		} else {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if err := copyFile(srcChild, dstChild); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

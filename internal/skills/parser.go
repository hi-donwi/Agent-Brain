package skills

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseSkillMD splits the frontmatter and body of a SKILL.md document.
func ParseSkillMD(content []byte) (*Frontmatter, string, error) {
	trimmed := bytes.TrimLeft(content, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return nil, "", errors.New("missing leading frontmatter delimiter (---)")
	}

	// Remove the initial delimiter
	rest := trimmed[3:]
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}

	// Find the closing delimiter
	// It should appear on its own line: "\n---" or "\r\n---"
	closingIdx := -1
	lineMarkers := [][]byte{[]byte("\n---"), []byte("\r\n---")}

	for _, marker := range lineMarkers {
		if idx := bytes.Index(rest, marker); idx != -1 {
			if closingIdx == -1 || idx < closingIdx {
				closingIdx = idx
			}
		}
	}

	if closingIdx == -1 {
		return nil, "", errors.New("missing closing frontmatter delimiter (---)")
	}

	yamlBytes := rest[:closingIdx]
	afterClosing := rest[closingIdx:]

	// Advance past delimiter line
	if bytes.HasPrefix(afterClosing, []byte("\r\n---")) {
		afterClosing = afterClosing[5:]
	} else if bytes.HasPrefix(afterClosing, []byte("\n---")) {
		afterClosing = afterClosing[4:]
	}

	// Skip any trailing spaces or newlines on the delimiter line
	for len(afterClosing) > 0 && (afterClosing[0] == '-' || afterClosing[0] == ' ' || afterClosing[0] == '\t') {
		afterClosing = afterClosing[1:]
	}
	if len(afterClosing) > 0 && afterClosing[0] == '\r' {
		afterClosing = afterClosing[1:]
	}
	if len(afterClosing) > 0 && afterClosing[0] == '\n' {
		afterClosing = afterClosing[1:]
	}

	body := strings.TrimSpace(string(afterClosing))

	var fm Frontmatter
	if err := yaml.Unmarshal(yamlBytes, &fm); err != nil {
		return nil, "", fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	return &fm, body, nil
}

// LoadSkill reads a skill directory and parses its SKILL.md and references.
func LoadSkill(dir string, source SourceType) (*Skill, error) {
	skillMDPath := filepath.Join(dir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillMDPath, err)
	}

	fm, body, err := ParseSkillMD(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillMDPath, err)
	}

	summary := extractSummary(fm, dir, source)

	var refs []string
	refsDir := filepath.Join(dir, "references")
	if fi, err := os.Stat(refsDir); err == nil && fi.IsDir() {
		_ = filepath.Walk(refsDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(refsDir, path)
			if relErr == nil && !strings.HasPrefix(filepath.Base(path), ".") {
				refs = append(refs, rel)
			}
			return nil
		})
	}

	return &Skill{
		Summary:     summary,
		Frontmatter: *fm,
		Body:        body,
		References:  refs,
	}, nil
}

// LoadSkillSummary parses only the summary metadata without loading the entire body.
func LoadSkillSummary(dir string, source SourceType) (*SkillSummary, error) {
	skillMDPath := filepath.Join(dir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillMDPath, err)
	}

	fm, _, err := ParseSkillMD(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillMDPath, err)
	}

	summary := extractSummary(fm, dir, source)
	return &summary, nil
}

func extractSummary(fm *Frontmatter, dir string, source SourceType) SkillSummary {
	var pack string
	var keywords []string

	if fm.Metadata != nil {
		if p, ok := fm.Metadata["pack"].(string); ok {
			pack = strings.TrimSpace(p)
		}
		if kwRaw, ok := fm.Metadata["keywords"]; ok {
			switch v := kwRaw.(type) {
			case string:
				for _, part := range strings.Split(v, ",") {
					trimmed := strings.TrimSpace(part)
					if trimmed != "" {
						keywords = append(keywords, trimmed)
					}
				}
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						keywords = append(keywords, strings.TrimSpace(s))
					}
				}
			case []string:
				for _, s := range v {
					if strings.TrimSpace(s) != "" {
						keywords = append(keywords, strings.TrimSpace(s))
					}
				}
			}
		}
	}

	return SkillSummary{
		Name:        strings.TrimSpace(fm.Name),
		Description: strings.TrimSpace(fm.Description),
		Source:      source,
		Path:        dir,
		Pack:        pack,
		Keywords:    keywords,
	}
}

package skills

import (
	"fmt"
	"strings"
)

// SourceType identifies where a skill was loaded from.
type SourceType string

const (
	SourceProject    SourceType = "project"    // .agents/skills/
	SourceUser       SourceType = "user"       // ~/.lokal/skills/
	SourceConfigured SourceType = "configured" // Directories from config.yaml
)

// Frontmatter represents the YAML metadata header in SKILL.md.
type Frontmatter struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	License       string         `yaml:"license,omitempty"`
	Compatibility string         `yaml:"compatibility,omitempty"`
	AllowedTools  any            `yaml:"allowed-tools,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty"`
}

// SkillSummary holds progressive disclosure Level 1 data (fits in prompts).
type SkillSummary struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Source      SourceType `json:"source"`
	Path        string     `json:"path"`
	Pack        string     `json:"pack,omitempty"`
	Keywords    []string   `json:"keywords,omitempty"`
}

// EstimateTokens returns an approximate token count for this summary.
func (s SkillSummary) EstimateTokens() int {
	// A standard rule of thumb for English text is ~4 characters per token.
	textLen := len(s.Name) + len(s.Description) + len(s.Pack)
	for _, kw := range s.Keywords {
		textLen += len(kw)
	}
	tokens := (textLen + 3) / 4
	if tokens < 10 {
		return 10
	}
	return tokens
}

// Skill represents the complete parsed skill directory (Progressive Disclosure Level 2 & 3).
type Skill struct {
	Summary     SkillSummary `json:"summary"`
	Frontmatter Frontmatter  `json:"frontmatter"`
	Body        string       `json:"body"`
	References  []string     `json:"references,omitempty"` // Relative paths inside references/
}

// ValidationError records a specification check failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationResult contains the validation status and all diagnostic errors.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

func (vr ValidationResult) Error() string {
	if vr.Valid || len(vr.Errors) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, err := range vr.Errors {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(err.Error())
	}
	return sb.String()
}

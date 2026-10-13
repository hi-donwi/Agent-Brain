package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const (
	maxNameLength        = 64
	maxDescriptionLength = 1024
)

// Validate checks a parsed Skill against the Agent Skills specification.
// If dirName is non-empty, it verifies that the skill name matches the directory name.
func Validate(s *Skill, dirName string) ValidationResult {
	var errs []ValidationError

	if s == nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{Field: "skill", Message: "nil skill object"},
			},
		}
	}

	name := strings.TrimSpace(s.Frontmatter.Name)
	if name == "" {
		errs = append(errs, ValidationError{
			Field:   "name",
			Message: "name is required and cannot be empty",
		})
	} else {
		if len(name) > maxNameLength {
			errs = append(errs, ValidationError{
				Field:   "name",
				Message: fmt.Sprintf("name exceeds maximum length of %d characters (%d)", maxNameLength, len(name)),
			})
		}
		if !nameRegex.MatchString(name) {
			errs = append(errs, ValidationError{
				Field:   "name",
				Message: "name must be lowercase alphanumeric with single hyphens (e.g. 'git-workflow')",
			})
		}
		if dirName != "" && name != dirName {
			errs = append(errs, ValidationError{
				Field:   "name",
				Message: fmt.Sprintf("skill name %q does not match directory name %q", name, dirName),
			})
		}
	}

	desc := strings.TrimSpace(s.Frontmatter.Description)
	if desc == "" {
		errs = append(errs, ValidationError{
			Field:   "description",
			Message: "description is required and cannot be empty",
		})
	} else if len(desc) > maxDescriptionLength {
		errs = append(errs, ValidationError{
			Field:   "description",
			Message: fmt.Sprintf("description exceeds maximum length of %d characters (%d)", maxDescriptionLength, len(desc)),
		})
	}

	if strings.TrimSpace(s.Body) == "" {
		errs = append(errs, ValidationError{
			Field:   "body",
			Message: "markdown body is empty; SKILL.md must provide process instructions",
		})
	}

	for _, ref := range s.References {
		if filepath.IsAbs(ref) || strings.Contains(ref, "..") {
			errs = append(errs, ValidationError{
				Field:   "references",
				Message: fmt.Sprintf("invalid reference path %q (must be relative without traversal)", ref),
			})
		}
	}

	return ValidationResult{
		Valid:  len(errs) == 0,
		Errors: errs,
	}
}

// ValidateDir inspects a skill folder on disk and validates its SKILL.md.
func ValidateDir(dir string) ValidationResult {
	fi, err := os.Stat(dir)
	if err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{Field: "directory", Message: fmt.Sprintf("cannot access path: %v", err)},
			},
		}
	}
	if !fi.IsDir() {
		// If given a direct path to SKILL.md, use its parent directory
		if filepath.Base(dir) == "SKILL.md" {
			dir = filepath.Dir(dir)
		} else {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{
					{Field: "directory", Message: fmt.Sprintf("%s is not a directory", dir)},
				},
			}
		}
	}

	skillMDPath := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(skillMDPath); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{Field: "SKILL.md", Message: "SKILL.md does not exist in skill directory"},
			},
		}
	}

	skill, err := LoadSkill(dir, SourceUser)
	if err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{Field: "parse", Message: err.Error()},
			},
		}
	}

	dirName := filepath.Base(filepath.Clean(dir))
	return Validate(skill, dirName)
}

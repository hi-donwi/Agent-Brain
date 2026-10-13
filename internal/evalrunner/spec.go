package evalrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecAssertions defines testable properties after task execution.
type SpecAssertions struct {
	FileExists      []string          `yaml:"file_exists,omitempty" json:"file_exists,omitempty"`
	FileContains    map[string]string `yaml:"file_contains,omitempty" json:"file_contains,omitempty"`
	CommandExitZero []string          `yaml:"command_exit_zero,omitempty" json:"command_exit_zero,omitempty"`
	OutputContains  []string          `yaml:"output_contains,omitempty" json:"output_contains,omitempty"`
}

// TaskSpec specifies a bounded evaluation task.
type TaskSpec struct {
	ID             string            `yaml:"id" json:"id"`
	Name           string            `yaml:"name" json:"name"`
	Description    string            `yaml:"description,omitempty" json:"description,omitempty"`
	Model          string            `yaml:"model,omitempty" json:"model,omitempty"`
	TimeoutSeconds int               `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	MaxTokens      int               `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	Prompt         string            `yaml:"prompt" json:"prompt"`
	AgentCommand   string            `yaml:"agent_command,omitempty" json:"agent_command,omitempty"`
	SetupFiles     map[string]string `yaml:"setup_files,omitempty" json:"setup_files,omitempty"`
	Assertions     SpecAssertions    `yaml:"assertions" json:"assertions"`
}

// ParseTaskSpec unmarshals a TaskSpec from YAML or JSON bytes.
func ParseTaskSpec(data []byte) (*TaskSpec, error) {
	var spec TaskSpec
	// Try YAML first (which is a superset of JSON)
	if err := yaml.Unmarshal(data, &spec); err != nil {
		if jsonErr := json.Unmarshal(data, &spec); jsonErr != nil {
			return nil, fmt.Errorf("parse task spec: %w", err)
		}
	}

	if strings.TrimSpace(spec.ID) == "" {
		return nil, errors.New("task spec requires non-empty 'id'")
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = spec.ID
	}
	if spec.TimeoutSeconds <= 0 {
		spec.TimeoutSeconds = 60
	}

	return &spec, nil
}

// LoadTaskSpec reads and parses a TaskSpec file.
func LoadTaskSpec(filePath string) (*TaskSpec, error) {
	data, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return nil, fmt.Errorf("read task spec file %s: %w", filePath, err)
	}
	return ParseTaskSpec(data)
}

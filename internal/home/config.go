package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentConfigVersion is the schema version written by this release of lokal.
const CurrentConfigVersion = 1

// BackendConfig holds network endpoint, optional token, and enable state for one backend engine.
type BackendConfig struct {
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token,omitempty"`
	Enabled  bool   `yaml:"enabled"`
}

// Config represents ~/.lokal/config.yaml.
type Config struct {
	Version      int                      `yaml:"version"`
	Profile      string                   `yaml:"profile"`
	Backends     map[string]BackendConfig `yaml:"backends"`
	Aliases      map[string]string        `yaml:"aliases"`
	SkillSources []string                 `yaml:"skill_sources,omitempty"`
	Inference    InferenceConfig          `yaml:"inference,omitempty"`
}

// InferenceConfig bounds the local runtime independently of a model's advertised capacity.
type InferenceConfig struct {
	ContextLength   int `yaml:"context_length,omitempty"`
	MaxOutputTokens int `yaml:"max_output_tokens,omitempty"`
	MaxConcurrency  int `yaml:"max_concurrency,omitempty"`
}

// DefaultConfig builds an initial valid Config for a chosen profile and aliases.
func DefaultConfig(profile string, aliases map[string]string) *Config {
	aliasCopy := make(map[string]string, len(aliases))
	for k, v := range aliases {
		aliasCopy[k] = v
	}
	return &Config{
		Version: CurrentConfigVersion,
		Profile: profile,
		Backends: map[string]BackendConfig{
			"ollama": {
				Endpoint: "http://127.0.0.1:11434",
				Enabled:  true,
			},
		},
		Aliases:      aliasCopy,
		SkillSources: []string{".agents/skills"},
	}
}

// Validate checks configuration values against supported versions and schemas.
func (c *Config) Validate() error {
	if c.Inference.ContextLength < 0 || c.Inference.MaxOutputTokens < 0 || c.Inference.MaxConcurrency < 0 {
		return errors.New("inference limits cannot be negative")
	}
	if c.Inference.ContextLength > 0 && c.Inference.MaxOutputTokens >= c.Inference.ContextLength {
		return errors.New("inference max_output_tokens must be smaller than context_length")
	}
	if c.Version <= 0 {
		return errors.New("version must be positive")
	}
	if c.Version > CurrentConfigVersion {
		return fmt.Errorf("unsupported config version %d (supported up to %d)", c.Version, CurrentConfigVersion)
	}
	if strings.TrimSpace(c.Profile) == "" {
		return errors.New("profile cannot be empty")
	}
	if len(c.Backends) == 0 {
		return errors.New("at least one backend must be configured")
	}
	for name, b := range c.Backends {
		if strings.TrimSpace(b.Endpoint) == "" {
			return fmt.Errorf("endpoint cannot be empty for backend %s", name)
		}
	}
	if len(c.Aliases) == 0 {
		return errors.New("aliases cannot be empty")
	}
	if strings.TrimSpace(c.Aliases["chat"]) == "" {
		return errors.New("chat alias must be configured")
	}
	return nil
}

// PrimaryBackend returns the name and configuration of the first enabled backend.
func (c *Config) PrimaryBackend() (string, BackendConfig) {
	names := make([]string, 0, len(c.Backends))
	for name := range c.Backends {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b := c.Backends[name]
		if b.Enabled {
			return name, b
		}
	}
	for _, name := range names {
		b := c.Backends[name]
		return name, b
	}
	return "ollama", BackendConfig{
		Endpoint: "http://127.0.0.1:11434",
		Enabled:  true,
	}
}

// LoadConfig reads, parses, and validates the YAML config at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config from %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveConfig atomically writes and flushes the validated configuration with mode 0600.
func SaveConfig(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("cannot save nil config")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config before save: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config yaml: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".%s.tmp", filepath.Base(path)))
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("write temporary config %s: %w", tmpFile, err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename temporary config to %s: %w", path, err)
	}

	return nil
}

// LoadConfig reads the configuration file from this layout.
func (l Layout) LoadConfig() (*Config, error) {
	return LoadConfig(l.Config)
}

// SaveConfig writes the configuration file into this layout.
func (l Layout) SaveConfig(cfg *Config) error {
	return SaveConfig(l.Config, cfg)
}

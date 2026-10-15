package router

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadRouterConfig reads accounts and router settings from a YAML file.
func LoadRouterConfig(path string) (RouterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultRouterConfig(), nil
		}
		return RouterConfig{}, fmt.Errorf("read router config %s: %w", path, err)
	}

	var cfg RouterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RouterConfig{}, fmt.Errorf("parse router config %s: %w", path, err)
	}
	return cfg, nil
}

// SaveRouterConfig writes the router configuration to disk with mode 0600.
func SaveRouterConfig(path string, cfg RouterConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal router config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp := filepath.Join(dir, fmt.Sprintf(".%s.tmp", filepath.Base(path)))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp config to %s: %w", path, err)
	}
	return nil
}

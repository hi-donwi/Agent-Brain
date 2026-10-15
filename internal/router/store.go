package router

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadRouterConfig reads accounts and router settings from a YAML file.
func LoadRouterConfig(path string) (RouterConfig, error) {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return RouterConfig{}, fmt.Errorf("account config must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultRouterConfig(), nil
		}
		return RouterConfig{}, fmt.Errorf("read router config %s: %w", path, err)
	}

	cfg := DefaultRouterConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RouterConfig{}, fmt.Errorf("parse router config %s: %w", path, err)
	}
	ids := make(map[string]bool)
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		a.Provider = NormalizeProvider(a.Provider)
		if _, err := LookupProvider(a.Provider); err != nil {
			return RouterConfig{}, err
		}
		if a.ID == "" || ids[a.ID] {
			return RouterConfig{}, fmt.Errorf("account config contains empty or duplicate IDs")
		}
		ids[a.ID] = true
	}
	if cfg.Strategy != "priority" && cfg.Strategy != "round-robin" {
		return RouterConfig{}, fmt.Errorf("invalid router strategy")
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

	f, err := os.CreateTemp(dir, ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("create account config: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp config to %s: %w", path, err)
	}
	return nil
}

package home_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/home"
)

func TestConfig_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	initial := &home.Config{
		Version: home.CurrentConfigVersion,
		Profile: "standard",
		Backends: map[string]home.BackendConfig{
			"ollama": {
				Endpoint: "http://127.0.0.1:11434",
				Enabled:  true,
			},
		},
		Aliases: map[string]string{
			"chat":  "qwen2.5-coder-7b",
			"code":  "qwen2.5-coder-7b",
			"embed": "nomic-embed-text",
		},
		SkillSources: []string{".agents/skills"},
	}

	if err := home.SaveConfig(cfgPath, initial); err != nil {
		t.Fatalf("SaveConfig() failed: %v", err)
	}

	// Verify permissions are 0600 (owner rw only)
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("os.Stat() failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("expected permissions 0600, got %o", perm)
	}

	loaded, err := home.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if loaded.Version != initial.Version {
		t.Errorf("expected version %d, got %d", initial.Version, loaded.Version)
	}
	if loaded.Profile != initial.Profile {
		t.Errorf("expected profile %s, got %s", initial.Profile, loaded.Profile)
	}
	if loaded.Aliases["chat"] != "qwen2.5-coder-7b" {
		t.Errorf("expected chat alias qwen2.5-coder-7b, got %s", loaded.Aliases["chat"])
	}
	if !loaded.Backends["ollama"].Enabled {
		t.Errorf("expected ollama backend enabled")
	}
}

func TestLayout_ConfigMethods(t *testing.T) {
	tmpDir := t.TempDir()
	l, err := home.Resolve(func(string) string { return tmpDir }, os.UserHomeDir)
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}

	cfg := home.DefaultConfig("tiny", map[string]string{
		"chat":  "qwen2.5-coder-1.5b",
		"embed": "nomic-embed-text",
	})

	if err := l.SaveConfig(cfg); err != nil {
		t.Fatalf("l.SaveConfig() failed: %v", err)
	}

	loaded, err := l.LoadConfig()
	if err != nil {
		t.Fatalf("l.LoadConfig() failed: %v", err)
	}

	if loaded.Profile != "tiny" {
		t.Errorf("expected profile tiny, got %s", loaded.Profile)
	}
	if loaded.Aliases["chat"] != "qwen2.5-coder-1.5b" {
		t.Errorf("expected chat alias qwen2.5-coder-1.5b, got %s", loaded.Aliases["chat"])
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     home.Config
		wantErr string
	}{
		{
			name:    "zero version",
			cfg:     home.Config{Version: 0, Profile: "tiny"},
			wantErr: "version must be positive",
		},
		{
			name:    "future version",
			cfg:     home.Config{Version: 999, Profile: "tiny"},
			wantErr: "unsupported config version",
		},
		{
			name:    "empty profile",
			cfg:     home.Config{Version: 1, Profile: ""},
			wantErr: "profile cannot be empty",
		},
		{
			name: "no backends",
			cfg: home.Config{
				Version:  1,
				Profile:  "tiny",
				Backends: map[string]home.BackendConfig{},
				Aliases:  map[string]string{"chat": "m1"},
			},
			wantErr: "at least one backend",
		},
		{
			name: "empty backend endpoint",
			cfg: home.Config{
				Version: 1,
				Profile: "tiny",
				Backends: map[string]home.BackendConfig{
					"ollama": {Endpoint: ""},
				},
				Aliases: map[string]string{"chat": "m1"},
			},
			wantErr: "endpoint cannot be empty",
		},
		{
			name: "missing chat alias",
			cfg: home.Config{
				Version: 1,
				Profile: "tiny",
				Backends: map[string]home.BackendConfig{
					"ollama": {Endpoint: "http://127.0.0.1:11434"},
				},
				Aliases: map[string]string{"embed": "m1"},
			},
			wantErr: "chat alias must be configured",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestConfig_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistent := filepath.Join(tmpDir, "does-not-exist.yaml")

	_, err := home.LoadConfig(nonexistent)
	if err == nil {
		t.Fatal("expected error loading nonexistent config, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected errors.Is(err, os.ErrNotExist), got: %v", err)
	}
}

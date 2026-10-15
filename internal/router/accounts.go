package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var accountName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var environmentName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// AccountService is shared by scripted and interactive commands.
type AccountService struct{ Path string }

func (a Account) Reference() string { return NormalizeProvider(a.Provider) + "/" + a.Name }

func findAccount(cfg RouterConfig, ref string) (Account, error) {
	var found []Account
	for _, a := range cfg.Accounts {
		if a.ID == ref || a.Reference() == ref || a.Name == ref {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return Account{}, fmt.Errorf("account %q is missing or ambiguous; use provider/name or ID", ref)
	}
	return found[0], nil
}

func (s AccountService) Find(ref string) (Account, error) {
	cfg, err := LoadRouterConfig(s.Path)
	if err != nil {
		return Account{}, err
	}
	return findAccount(cfg, ref)
}

func validateAccount(a *Account) error {
	p, err := LookupProvider(a.Provider)
	if err != nil {
		return err
	}
	a.Provider = p.ID
	if !accountName.MatchString(a.Name) {
		return fmt.Errorf("account name must be 1-64 letters, digits, underscores, or hyphens")
	}
	if a.Auth == "" {
		a.Auth = AuthAPIKey
		if a.Provider == ProviderLocal {
			a.Auth = AuthNone
		}
	}
	if !contains(p.Auth, a.Auth) {
		return fmt.Errorf("provider %s does not support %s authentication: %s", p.Name, a.Auth, p.Note)
	}
	if a.Auth == AuthAPIKey && strings.TrimSpace(a.APIKey) == "" {
		return fmt.Errorf("credential required; use --key env:VARIABLE or --key-stdin")
	}
	for _, ref := range []string{a.APIKey, a.BridgeKey} {
		if strings.ContainsAny(ref, "\r\n\x00") {
			return fmt.Errorf("credential must be a single line")
		}
		if name, ok := strings.CutPrefix(ref, "env:"); ok && !environmentName.MatchString(name) {
			return fmt.Errorf("invalid credential environment reference")
		}
		if path, ok := strings.CutPrefix(ref, "file:"); ok && !filepath.IsAbs(path) {
			return fmt.Errorf("credential file references must be absolute")
		}
	}
	if a.Auth == AuthNative && a.Provider != ProviderClaudeCode && a.Provider != ProviderCodex && !(a.Provider == "cursor" && a.Name == "default") {
		return fmt.Errorf("native account isolation is unavailable for this provider")
	}
	if a.ManagedBridge && a.Auth != AuthOAuth {
		return fmt.Errorf("managed bridges require OAuth authentication")
	}
	if a.Auth == AuthOAuth {
		if a.BridgeURL == "" || a.BridgeKey == "" || a.BaseURL == "" || a.APIKey == "" {
			return fmt.Errorf("OAuth requires a dedicated local CLIProxyAPI v8 endpoint, --bridge-url, --bridge-key, --base-url, and --key")
		}
		if err := ValidateLoopbackEndpoint(a.BridgeURL); err != nil {
			return err
		}
		if err := ValidateLoopbackEndpoint(a.BaseURL); err != nil {
			return err
		}
		if strings.TrimRight(a.BridgeURL, "/") != strings.TrimSuffix(strings.TrimRight(a.BaseURL, "/"), "/v1")+"/v8/management" {
			return fmt.Errorf("inference and management URLs must refer to the same dedicated bridge")
		}
	}
	if (a.Provider == "custom" || a.Provider == "opencode-zen") && (a.BaseURL == "" || len(a.Protocols) == 0) {
		return fmt.Errorf("custom provider requires --base-url and --protocols")
	}
	if a.BaseURL != "" {
		if err := ValidateEndpoint(a.BaseURL); err != nil {
			return err
		}
	}
	for _, protocol := range a.Protocols {
		if !contains(p.Protocols, protocol) {
			return fmt.Errorf("unsupported protocol %q for provider %s", protocol, a.Provider)
		}
	}
	if a.Priority < 0 {
		return fmt.Errorf("priority cannot be negative")
	}
	if a.Tier == "" {
		a.Tier = TierCheap
		if a.Provider == ProviderLocal {
			a.Tier = TierLocal
		}
	}
	if !contains([]string{TierSubscription, TierCheap, TierFree, TierLocal}, a.Tier) {
		return fmt.Errorf("invalid account tier")
	}
	return nil
}

// Add creates a new identity; it never silently replaces a provider/name pair.
func (s AccountService) Add(ctx context.Context, account Account) (Account, error) {
	account.ID = uuid.NewString()
	err := UpdateRouterConfig(ctx, s.Path, func(cfg *RouterConfig) error {
		if account.ManagedBridge {
			if err := prepareManagedAccount(&account, cfg.Accounts); err != nil {
				return err
			}
		}
		if err := validateAccount(&account); err != nil {
			return err
		}
		account.Enabled = true
		account.Status = StatusConfigured
		account.GatewayReady, account.CLIReady = false, false
		if account.Auth == AuthNative || account.Auth == AuthOAuth {
			account.Status = StatusPending
		}
		for _, existing := range cfg.Accounts {
			if existing.Reference() == account.Reference() {
				return fmt.Errorf("account %s already exists; use accounts login or choose another name", account.Reference())
			}
			if account.Auth == AuthOAuth && existing.Auth == AuthOAuth && (strings.TrimRight(existing.BaseURL, "/") == strings.TrimRight(account.BaseURL, "/") || strings.TrimRight(existing.BridgeURL, "/") == strings.TrimRight(account.BridgeURL, "/")) {
				return fmt.Errorf("each OAuth account requires a dedicated bridge endpoint to guarantee account pinning")
			}
		}
		var err error
		account.APIKey, err = s.storeSecret(account.ID+".key", account.APIKey)
		if err != nil {
			return err
		}
		account.BridgeKey, err = s.storeSecret(account.ID+".bridge", account.BridgeKey)
		if err != nil {
			return err
		}
		cfg.Accounts = append(cfg.Accounts, account)
		return nil
	})
	return account, err
}

func (s AccountService) storeSecret(name, value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "env:") || strings.HasPrefix(value, "file:") {
		return value, nil
	}
	dir := filepath.Join(filepath.Dir(s.Path), "secrets")
	if err := privateDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("create private credential: %w", err)
	}
	_, writeErr := f.WriteString(value)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write private credential failed")
	}
	return "file:" + path, nil
}

func (s AccountService) Update(ctx context.Context, ref string, change func(*Account) error) error {
	return UpdateRouterConfig(ctx, s.Path, func(cfg *RouterConfig) error {
		a, err := findAccount(*cfg, ref)
		if err != nil {
			return err
		}
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ID == a.ID {
				return change(&cfg.Accounts[i])
			}
		}
		return fmt.Errorf("account not found")
	})
}

func (s AccountService) Use(ctx context.Context, ref string) error {
	return UpdateRouterConfig(ctx, s.Path, func(cfg *RouterConfig) error {
		a, err := findAccount(*cfg, ref)
		if err != nil {
			return err
		}
		if !a.Enabled {
			return fmt.Errorf("account is disabled")
		}
		if cfg.Defaults == nil {
			cfg.Defaults = make(map[string]string)
		}
		cfg.Defaults[a.Provider] = a.ID
		return nil
	})
}

func (s AccountService) Remove(ctx context.Context, ref string) error {
	return UpdateRouterConfig(ctx, s.Path, func(cfg *RouterConfig) error {
		a, err := findAccount(*cfg, ref)
		if err != nil {
			return err
		}
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ID == a.ID {
				cfg.Accounts = append(cfg.Accounts[:i], cfg.Accounts[i+1:]...)
				break
			}
		}
		if cfg.Defaults[a.Provider] == a.ID {
			delete(cfg.Defaults, a.Provider)
		}
		return nil
	})
}

// UpdateRouterConfig serializes cross-process mutations and commits via atomic rename.
func UpdateRouterConfig(ctx context.Context, path string, mutate func(*RouterConfig) error) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lockPath := path + ".lock"
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_ = f.Close()
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("lock accounts: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("account store is locked; retry or inspect the stale .lock file: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer os.Remove(lockPath)
	cfg, err := LoadRouterConfig(path)
	if err != nil {
		return err
	}
	if err := mutate(&cfg); err != nil {
		return err
	}
	return SaveRouterConfig(path, cfg)
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private account directory is not a regular directory")
	}
	return nil
}

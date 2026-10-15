package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// BridgeClient implements the documented CLIProxyAPI v8 management contract.
// The bridge owns OAuth tokens and renewal; Brain retains only an identity binding.
type BridgeClient struct {
	URL, Key string
	Client   *http.Client
}

type bridgeCredential struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	Disabled    bool   `json:"disabled"`
	Unavailable bool   `json:"unavailable"`
}

func (b BridgeClient) request(ctx context.Context, method, path string, result any) error {
	if err := ValidateLoopbackEndpoint(b.URL); err != nil {
		return err
	}
	u, _ := url.Parse(b.URL)
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v8/management") {
		return fmt.Errorf("bridge URL must end in /v8/management; only the v8 contract is supported")
	}
	key, err := ResolveCredential(b.Key)
	if err != nil || key == "" {
		return fmt.Errorf("bridge management credential is unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(b.URL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("invalid bridge request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach local OAuth bridge")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OAuth bridge returned HTTP %d; check its v8 management endpoint and credential", resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("invalid OAuth bridge response")
	}
	return nil
}

func (b BridgeClient) credentials(ctx context.Context) ([]bridgeCredential, error) {
	var response struct {
		Files []bridgeCredential `json:"files"`
	}
	err := b.request(ctx, http.MethodGet, "/credentials", &response)
	return response.Files, err
}

// Verify rejects bridges with any additional identity, even if currently disabled.
func (b BridgeClient) verifyConfiguration(ctx context.Context) error {
	var config struct {
		Version int                        `json:"config-version"`
		Keys    map[string]json.RawMessage `json:"api-keys"`
		Plugins struct {
			Enabled bool `json:"enabled"`
		} `json:"plugins"`
	}
	if err := b.request(ctx, http.MethodGet, "/config", &config); err != nil {
		return err
	}
	if config.Version != 8 || config.Plugins.Enabled {
		return fmt.Errorf("dedicated OAuth bridge requires v8 configuration with plugins disabled")
	}
	for _, groups := range config.Keys {
		value := strings.TrimSpace(string(groups))
		if value != "null" && value != "[]" {
			return fmt.Errorf("dedicated OAuth bridge must not configure additional upstream API-key groups")
		}
	}
	return nil
}

func (b BridgeClient) Verify(ctx context.Context, provider, expected string) (string, error) {
	if err := b.verifyConfiguration(ctx); err != nil {
		return "", err
	}
	files, err := b.credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(files) != 1 {
		return "", fmt.Errorf("a dedicated bridge must contain exactly one credential, got %d", len(files))
	}
	f := files[0]
	if f.ID == "" || f.Provider != provider || f.Disabled || f.Unavailable || (f.Status != "ready" && f.Status != "active") {
		return "", fmt.Errorf("bridge identity is not ready for provider %s", provider)
	}
	if expected != "" && f.ID != expected {
		return "", fmt.Errorf("bridge identity changed; reconnect this account explicitly")
	}
	return f.ID, nil
}

// Check verifies the management contract without requiring a logged-in identity.
func (b BridgeClient) Check(ctx context.Context) error { return b.verifyConfiguration(ctx) }

// Models reads the bound credential's catalog, never the bridge's global catalog.
func (b BridgeClient) Models(ctx context.Context, provider, expected string) ([]string, error) {
	id, err := b.Verify(ctx, provider, expected)
	if err != nil {
		return nil, err
	}
	var response struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := b.request(ctx, http.MethodGet, "/credentials/models?name="+url.QueryEscape(id), &response); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var models []string
	for _, model := range response.Models {
		if model.ID == "" || len(model.ID) > 512 || strings.ContainsFunc(model.ID, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			return nil, fmt.Errorf("bridge returned an invalid model ID")
		}
		if !seen[model.ID] {
			models = append(models, model.ID)
			seen[model.ID] = true
		}
	}
	if len(models) == 0 {
		return nil, ErrModelCatalogPending
	}
	sort.Strings(models)
	return models, nil
}

// Login prints/opens only a validated provider authorization URL, then verifies identity.
func (b BridgeClient) Login(ctx context.Context, provider string, onURL func(string) error) (id string, err error) {
	if err := b.verifyConfiguration(ctx); err != nil {
		return "", err
	}
	if provider != ProviderAntigravity && provider != ProviderCodex {
		return "", fmt.Errorf("OAuth bridge authentication is not supported for this provider")
	}
	files, err := b.credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(files) > 0 {
		return "", fmt.Errorf("new OAuth login requires an empty dedicated bridge; use accounts test for an existing connection")
	}
	var start struct{ Status, URL, State string }
	path := "/oauth/auth-url?provider=" + url.QueryEscape(provider) + "&is_webui=true"
	if err = b.request(ctx, http.MethodGet, path, &start); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	completed := false
	defer func() {
		if !completed && start.State != "" {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_ = b.request(cleanup, http.MethodDelete, "/oauth/session?state="+url.QueryEscape(start.State), nil)
		}
	}()
	u, parseErr := url.Parse(start.URL)
	allowed := parseErr == nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && start.State != "" && u.Query().Get("state") == start.State
	if allowed {
		host := u.Hostname()
		allowed = (provider == ProviderAntigravity && host == "accounts.google.com") || (provider == ProviderCodex && host == "auth.openai.com")
	}
	if start.Status != "ok" || !allowed {
		return "", fmt.Errorf("bridge returned an invalid provider authorization URL or state")
	}
	if onURL != nil {
		if err = onURL(start.URL); err != nil {
			return "", err
		}
	}
	for {
		var status struct{ Status string }
		if err = b.request(ctx, http.MethodGet, "/oauth/status?state="+url.QueryEscape(start.State), &status); err != nil {
			return "", err
		}
		switch status.Status {
		case "ok":
			id, err = b.Verify(ctx, provider, "")
			completed = err == nil
			return id, err
		case "wait":
		default:
			return "", fmt.Errorf("provider login was denied or expired; retry accounts login")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("provider login cancelled or timed out")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// DiscoverModels reads a credential-bound provider catalog without issuing inference.
func DiscoverModels(ctx context.Context, account Account) ([]string, error) {
	if account.Auth == AuthOAuth || account.Auth == AuthNative {
		return nil, fmt.Errorf("OAuth and native accounts require their provider-specific model discovery flow")
	}
	provider, err := LookupProvider(account.Provider)
	if err != nil {
		return nil, err
	}
	endpoint := account.BaseURL
	if endpoint == "" {
		endpoint = provider.BaseURL
	}
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	path := "/v1/models"
	gemini := provider.ID == ProviderGemini || (account.Supports(ProtocolGemini) && !account.Supports(ProtocolChat) && !account.Supports(ProtocolResponses) && !account.Supports(ProtocolMessages))
	if gemini {
		path = "/v1beta/models"
	}
	if provider.ID == ProviderDeepSeek {
		path = "/models"
	}
	if strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		path = strings.TrimPrefix(path, "/v1")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid model discovery endpoint")
	}
	key, err := ResolveCredential(account.APIKey)
	if err != nil {
		return nil, fmt.Errorf("provider credential is unavailable")
	}
	if key != "" {
		switch {
		case (provider.ID == ProviderAnthropic || (account.Supports(ProtocolMessages) && !account.Supports(ProtocolChat))) && account.Auth != AuthOAuth:
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case gemini && account.Auth != AuthOAuth:
			req.Header.Set("x-goog-api-key", key)
		default:
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach provider model discovery endpoint")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider credential/model discovery returned HTTP %d", resp.StatusCode)
	}
	var data struct {
		Data   []struct{ ID string }   `json:"data"`
		Models []struct{ Name string } `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&data); err != nil {
		return nil, fmt.Errorf("provider returned invalid model metadata")
	}
	var models []string
	for _, item := range data.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	for _, item := range data.Models {
		if item.Name != "" {
			models = append(models, strings.TrimPrefix(item.Name, "models/"))
		}
	}
	for _, id := range models {
		if len(id) > 512 || strings.ContainsFunc(id, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			return nil, fmt.Errorf("provider returned an invalid model ID")
		}
	}
	sort.Strings(models)
	models = slices.Compact(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("provider returned no available models for this account")
	}
	return models, nil
}

package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

type accountScopeKey struct{}

// ScopedToken authenticates a launcher while limiting inference to one account.
func ScopedToken(master, id string) string {
	if id == "" {
		return master
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(id))
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = mac.Write([]byte("brain-account:" + payload))
	return "brain." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func tokenAccount(master, token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "brain" {
		return "", false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(data) == 0 || len(data) > 256 {
		return "", false
	}
	return string(data), hmac.Equal([]byte(token), []byte(ScopedToken(master, string(data))))
}

func accountSelection(r *http.Request) string {
	if value, ok := r.Context().Value(accountScopeKey{}).(string); ok {
		return value
	}
	return r.Header.Get("X-Brain-Account")
}

func (s *Server) poolAccount(ref string) (router.Account, error) {
	if s.cfg.AccountPool == nil {
		return router.Account{}, fmt.Errorf("no account pool configured")
	}
	var found []router.Account
	for _, a := range s.cfg.AccountPool.ListAccounts() {
		if a.ID == ref || a.Reference() == ref || a.Name == ref {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return router.Account{}, fmt.Errorf("account is missing or ambiguous")
	}
	return found[0], nil
}

// tryProxy dispatches raw protocol payloads before local-only request decoding.
func (s *Server) tryProxy(w http.ResponseWriter, r *http.Request, body []byte, protocol, forced string) bool {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	var model string
	_ = json.Unmarshal(payload["model"], &model)
	targetPath := r.URL.Path
	if protocol == router.ProtocolGemini {
		name := strings.TrimPrefix(targetPath, "/v1beta/models/")
		model, _, _ = strings.Cut(name, ":")
	}
	provider := forced
	if provider == "" {
		provider = router.DetectProvider(model)
	}
	explicit := false
	if prefix, remaining, ok := strings.Cut(model, "/"); ok {
		if p, err := router.LookupProvider(prefix); err == nil {
			provider = p.ID
			model = remaining
			explicit = true
		}
	}
	if value := r.Header.Get("X-Brain-Provider"); value != "" {
		p, err := router.LookupProvider(value)
		if err != nil {
			writeAPIError(w, 400, "unknown provider", "invalid_request_error", "unknown_provider")
			return true
		}
		if explicit && p.ID != provider {
			writeAPIError(w, 400, "provider selectors conflict", "invalid_request_error", "provider_mismatch")
			return true
		}
		provider = p.ID
		explicit = true
	}
	pinned := ""
	if ref := accountSelection(r); ref != "" {
		a, err := s.poolAccount(ref)
		if err != nil {
			writeAPIError(w, 404, "selected account was not found", "invalid_request_error", "account_not_found")
			return true
		}
		if explicit && a.Provider != provider {
			writeAPIError(w, 403, "account and provider selectors conflict", "invalid_request_error", "provider_mismatch")
			return true
		}
		provider = a.Provider
		pinned = a.ID
		explicit = true
	}
	if !explicit && (s.proxy == nil || !s.cfg.AccountPool.HasProvider(provider)) {
		return false
	}
	if provider == router.ProviderLocal && pinned == "" {
		return false
	}
	if s.proxy == nil {
		writeAPIError(w, 503, "provider account is not configured", "api_error", "account_unavailable")
		return true
	}
	if model == "" {
		writeAPIError(w, 400, "model is required", "invalid_request_error", "bad_request")
		return true
	}
	if protocol == router.ProtocolGemini {
		_, suffix, _ := strings.Cut(strings.TrimPrefix(targetPath, "/v1beta/models/"), ":")
		targetPath = "/v1beta/models/" + model + ":" + suffix
		if r.URL.RawQuery != "" {
			targetPath += "?" + r.URL.RawQuery
		}
	} else {
		payload["model"], _ = json.Marshal(model)
		body, _ = json.Marshal(payload)
	}
	resp, _, err := s.proxy.ForwardSelectedRequest(r.Context(), provider, pinned, targetPath, body, r.Header)
	if err != nil {
		writeAPIError(w, 502, "provider request failed: "+err.Error(), "api_error", "upstream_error")
		return true
	}
	_ = router.StreamResponse(w, resp)
	return true
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, 400, "failed to read request body", "invalid_request_error", "bad_request")
		return
	}
	if s.tryProxy(w, r, body, router.ProtocolResponses, "") {
		return
	}
	writeAPIError(w, 400, "Responses requires an account whose endpoint supports the Responses protocol; configure a local compatible endpoint or a cloud provider", "invalid_request_error", "protocol_not_supported")
}

func withAccountScope(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, accountScopeKey{}, id)
}

func (s *Server) handleGeminiModelMetadata(w http.ResponseWriter, r *http.Request) {
	provider := router.ProviderGemini
	pinned := ""
	if selected := accountSelection(r); selected != "" {
		a, err := s.poolAccount(selected)
		if err != nil {
			writeAPIError(w, 404, "selected account was not found", "invalid_request_error", "account_not_found")
			return
		}
		provider = a.Provider
		pinned = a.ID
		if !a.Supports(router.ProtocolGemini) {
			writeAPIError(w, 400, "selected account does not support Gemini metadata", "invalid_request_error", "protocol_not_supported")
			return
		}
	}
	if s.proxy == nil {
		writeAPIError(w, 503, "no Gemini-compatible provider account configured", "api_error", "account_unavailable")
		return
	}
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	resp, _, err := s.proxy.ForwardMetadataRequest(r.Context(), provider, pinned, path, r.Header)
	if err != nil {
		writeAPIError(w, 502, "provider metadata request failed", "api_error", "upstream_error")
		return
	}
	_ = router.StreamResponse(w, resp)
}

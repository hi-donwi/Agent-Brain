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
		if hasInferenceScope(r.Context()) {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON payload", "invalid_request_error", "bad_request")
			return true
		}
		return false
	}
	if scope, scoped := InferenceScopeFromContext(r.Context()); scoped {
		profile, found := router.FindRoutingProfile(s.cfg.RoutingProfiles, scope.ProfileID)
		if !found {
			writeAPIError(w, http.StatusForbidden, "routing profile is not available", "invalid_request_error", "profile_scope")
			return true
		}
		var requestedModel string
		_ = json.Unmarshal(payload["model"], &requestedModel)
		targetPath := r.URL.Path
		if protocol == router.ProtocolGemini {
			if r.URL.Query().Has("key") {
				writeAPIError(w, http.StatusForbidden, "profile inference does not accept upstream API keys", "invalid_request_error", "profile_scope")
				return true
			}
			requestedModel = strings.TrimPrefix(targetPath, "/v1beta/models/")
			requestedModel, _, _ = strings.Cut(requestedModel, ":")
		}
		target, ok := router.FindRoutingTarget(profile, requestedModel, protocol)
		if !ok {
			writeAPIError(w, http.StatusForbidden, "model is outside the authorized routing profile", "invalid_request_error", "profile_scope")
			return true
		}
		if target.Provider == router.ProviderLocal {
			return false
		}
		if s.proxy == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "profile target router is unavailable", "api_error", "account_unavailable")
			return true
		}
		if protocol == router.ProtocolGemini {
			_, suffix, _ := strings.Cut(strings.TrimPrefix(targetPath, "/v1beta/models/"), ":")
			targetPath = "/v1beta/models/" + target.UpstreamModel + ":" + suffix
			if r.URL.RawQuery != "" {
				targetPath += "?" + r.URL.RawQuery
			}
		} else {
			payload["model"], _ = json.Marshal(target.UpstreamModel)
			body, _ = json.Marshal(payload)
		}
		resp, _, err := s.proxy.ForwardProfileRequest(r.Context(), target, targetPath, body, r.Header)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "profile inference request failed", "api_error", "upstream_error")
			return true
		}
		_ = router.StreamResponse(w, resp)
		return true
	}
	if s.proxy == nil && accountSelection(r) == "" && r.Header.Get("X-Brain-Provider") == "" {
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
	if explicit && pinned == "" && r.Header.Get("X-Brain-Provider") == "" && (s.cfg.AccountPool == nil || !s.cfg.AccountPool.HasProvider(provider)) {
		return false
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

func hasInferenceScope(ctx context.Context) bool {
	_, ok := InferenceScopeFromContext(ctx)
	return ok
}

func (s *Server) scopedLocalTarget(r *http.Request, model, protocol string) (router.RoutingTarget, bool) {
	scope, ok := InferenceScopeFromContext(r.Context())
	if !ok {
		return router.RoutingTarget{}, false
	}
	profile, ok := router.FindRoutingProfile(s.cfg.RoutingProfiles, scope.ProfileID)
	if !ok {
		return router.RoutingTarget{}, false
	}
	target, ok := router.FindRoutingTarget(profile, model, protocol)
	return target, ok && target.Provider == router.ProviderLocal
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
	if scope, scoped := InferenceScopeFromContext(r.Context()); scoped {
		profile, found := router.FindRoutingProfile(s.cfg.RoutingProfiles, scope.ProfileID)
		if !found {
			writeAPIError(w, http.StatusForbidden, "routing profile is not available", "invalid_request_error", "profile_scope")
			return
		}
		models := make([]map[string]any, 0, len(profile.Targets))
		requested := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
		requested = strings.TrimPrefix(requested, "models/")
		for _, target := range profile.Targets {
			if !containsString(target.Protocols, router.ProtocolGemini) || !containsString(scope.Protocols, router.ProtocolGemini) {
				continue
			}
			modelName := strings.TrimPrefix(target.Model, "models/")
			if r.URL.Path != "/v1beta/models" && requested != modelName {
				continue
			}
			models = append(models, map[string]any{
				"name":                       "models/" + modelName,
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			})
		}
		if r.URL.Path != "/v1beta/models" && len(models) == 0 {
			writeAPIError(w, http.StatusForbidden, "model is outside the authorized routing profile", "invalid_request_error", "profile_scope")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		return
	}
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

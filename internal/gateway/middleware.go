package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

// InferenceAuthConfig supplies the exact profile catalogue used to validate scoped credentials.
type InferenceAuthConfig struct {
	Profiles      []router.RoutingProfile
	Accounts      []router.Account
	Pool          *router.AccountPool
	LocalBackend  string
	LocalEndpoint string
}

// APIError adheres to the OpenAI error envelope format.
type APIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var resp APIError
	resp.Error.Message = message
	resp.Error.Type = errType
	resp.Error.Code = code
	_ = json.NewEncoder(w).Encode(resp)
}

// AuthMiddleware enforces Bearer token authentication on all paths except /healthz.
func AuthMiddleware(expectedToken string, inferenceConfigs ...InferenceAuthConfig) func(http.Handler) http.Handler {
	var inferenceConfig InferenceAuthConfig
	if len(inferenceConfigs) > 0 {
		inferenceConfig = inferenceConfigs[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
			auth := r.Header.Get("Authorization")
			alternateAuth := false
			if auth == "" {
				key := r.Header.Get("x-api-key")
				if key == "" {
					key = r.Header.Get("x-goog-api-key")
				}
				if key == "" && strings.HasPrefix(r.URL.Path, "/v1beta/") {
					key = r.URL.Query().Get("key")
				}
				if key != "" {
					auth = "Bearer " + key
					alternateAuth = true
				}
			}
			if auth == "" {
				writeAPIError(w, http.StatusUnauthorized, "missing Authorization header", "invalid_request_error", "unauthorized")
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				writeAPIError(w, http.StatusUnauthorized, "invalid Authorization header format; expected Bearer token", "invalid_request_error", "unauthorized")
				return
			}

			token := strings.TrimSpace(parts[1])
			if expectedToken == "" {
				writeAPIError(w, 401, "gateway authentication is not configured", "invalid_request_error", "unauthorized")
				return
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
				if scope, valid := VerifyInferenceToken(expectedToken, token); valid {
					if alternateAuth {
						writeAPIError(w, http.StatusForbidden, "profile credentials require the Authorization: Bearer header", "invalid_request_error", "profile_scope")
						return
					}
					if !authorizeInferenceRequest(w, r, scope, inferenceConfig) {
						return
					}
					r = r.WithContext(context.WithValue(r.Context(), inferenceScopeContextKey{}, scope))
					next.ServeHTTP(w, r)
					return
				}
				id, valid := tokenAccount(expectedToken, token)
				if valid {
					if selector := r.Header.Get("X-Brain-Account"); selector != "" && selector != id {
						writeAPIError(w, 403, "account token cannot change its scope", "invalid_request_error", "account_scope")
						return
					}
					r = r.WithContext(withAccountScope(r.Context(), id))
					next.ServeHTTP(w, r)
					return
				}
				writeAPIError(w, http.StatusUnauthorized, "invalid bearer token", "invalid_request_error", "unauthorized")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func authorizeInferenceRequest(w http.ResponseWriter, r *http.Request, scope InferenceScope, config InferenceAuthConfig) bool {
	profileID := r.Header.Values("X-Brain-Profile")
	revisions := r.Header.Values("X-Brain-Revision")
	if len(profileID) != 1 || len(revisions) != 1 || profileID[0] != scope.ProfileID || revisions[0] != scope.Revision {
		writeAPIError(w, http.StatusForbidden, "request profile does not match the inference credential", "invalid_request_error", "profile_scope")
		return false
	}
	profile, ok := router.FindRoutingProfile(config.Profiles, scope.ProfileID)
	if !ok {
		writeAPIError(w, http.StatusForbidden, "routing profile is not available", "invalid_request_error", "profile_scope")
		return false
	}
	accounts := config.Accounts
	if config.Pool != nil {
		accounts = config.Pool.ListAccounts()
	}
	revision, err := router.RoutingProfileRevisionFor(profile, accounts, config.LocalBackend, config.LocalEndpoint)
	if err != nil || revision != scope.Revision {
		writeAPIError(w, http.StatusForbidden, "routing profile revision is no longer available", "invalid_request_error", "profile_scope")
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		return true
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/v1beta/models" || strings.HasPrefix(r.URL.Path, "/v1beta/models/")) {
		if containsString(scope.Protocols, router.ProtocolGemini) {
			return true
		}
		writeAPIError(w, http.StatusForbidden, "metadata protocol is outside the credential scope", "invalid_request_error", "profile_scope")
		return false
	}
	protocol, ok := inferenceProtocol(r.Method, r.URL.Path)
	if !ok || !containsString(scope.Protocols, protocol) {
		writeAPIError(w, http.StatusForbidden, "inference protocol is outside the credential scope", "invalid_request_error", "profile_scope")
		return false
	}
	return true
}

func inferenceProtocol(method, path string) (string, bool) {
	if method != http.MethodPost {
		return "", false
	}
	switch {
	case path == "/v1/chat/completions":
		return router.ProtocolChat, true
	case path == "/v1/responses":
		return router.ProtocolResponses, true
	case path == "/v1/messages":
		return router.ProtocolMessages, true
	case path == "/v1/embeddings":
		return router.ProtocolEmbeddings, true
	case strings.HasPrefix(path, "/v1beta/models/"):
		modelAction := strings.TrimPrefix(path, "/v1beta/models/")
		model, action, ok := strings.Cut(modelAction, ":")
		if ok && model != "" && (action == "generateContent" || action == "streamGenerateContent") {
			return router.ProtocolGemini, true
		}
	default:
		return "", false
	}
	return "", false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// HostValidationMiddleware protects loopback servers against DNS rebinding attacks
// by validating incoming Host headers against loopback and allowed hostnames.
func HostValidationMiddleware(extraAllowed []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{
		"127.0.0.1": true,
		"localhost": true,
		"::1":       true,
		"[::1]":     true,
	}
	allowAll := false
	for _, h := range extraAllowed {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "*" {
			allowAll = true
		}
		allowed[h] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowAll {
				next.ServeHTTP(w, r)
				return
			}
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = strings.ToLower(strings.TrimSpace(host))

			if !allowed[host] {
				writeAPIError(w, http.StatusForbidden, "forbidden host header: DNS rebinding protection active", "invalid_request_error", "forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

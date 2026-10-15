package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

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
func AuthMiddleware(expectedToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
			auth := r.Header.Get("Authorization")
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

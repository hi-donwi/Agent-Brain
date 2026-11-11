package gateway

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

//go:embed management-ui/*
var managementUI embed.FS

type managementAccount struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Provider         string               `json:"provider"`
	BaseURL          string               `json:"base_url,omitempty"`
	Tier             string               `json:"tier"`
	Priority         int                  `json:"priority"`
	Weight           int                  `json:"weight,omitempty"`
	Enabled          bool                 `json:"enabled"`
	Status           router.AccountStatus `json:"status"`
	Auth             string               `json:"auth"`
	CredentialSet    bool                 `json:"credential_set"`
	Models           []string             `json:"models,omitempty"`
	DiscoveredModels []string             `json:"discovered_models,omitempty"`
	Protocols        []string             `json:"protocols,omitempty"`
	CLIReady         bool                 `json:"cli_ready"`
	GatewayReady     bool                 `json:"gateway_ready"`
	ManagedBridge    bool                 `json:"managed_bridge,omitempty"`
}

func redactManagementAccount(account router.Account) managementAccount {
	auth := account.Auth
	if auth == "" {
		auth = router.AuthAPIKey
	}
	credentialSet := account.APIKey != ""
	if account.Auth == router.AuthOAuth {
		credentialSet = account.BridgeCredential != ""
	}
	return managementAccount{
		ID: account.ID, Name: account.Name, Provider: account.Provider, BaseURL: account.BaseURL,
		Tier: account.Tier, Priority: account.Priority, Weight: account.Weight, Enabled: account.Enabled,
		Status: account.Status, Auth: auth, CredentialSet: credentialSet,
		Models: account.Models, DiscoveredModels: account.DiscoveredModels, Protocols: account.Protocols,
		CLIReady: account.CLIReady, GatewayReady: account.GatewayReady, ManagedBridge: account.ManagedBridge,
	}
}

func (s *Server) handleManagementProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, router.Providers())
}

func (s *Server) handleManagementAccounts(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	config, err := router.LoadRouterConfig(s.cfg.AccountConfigPath)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "account metadata is unavailable", "api_error", "management_unavailable")
		return
	}
	accounts := make([]managementAccount, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		accounts = append(accounts, redactManagementAccount(account))
	}
	writeJSON(w, map[string]any{"accounts": accounts, "strategy": config.Strategy, "gateway_reload_required": true})
}

func (s *Server) handleManagementProfiles(w http.ResponseWriter, _ *http.Request) {
	config, err := s.loadManagementRouterConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "routing profiles are unavailable", "api_error", "management_unavailable")
		return
	}
	profiles := make([]map[string]any, 0, len(config.RoutingProfiles))
	for _, profile := range config.RoutingProfiles {
		revision, err := router.RoutingProfileRevisionFor(profile, config.Accounts, s.cfg.LocalBackend, s.cfg.LocalEndpoint)
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "routing profile validation failed", "api_error", "management_unavailable")
			return
		}
		profiles = append(profiles, map[string]any{"profile": profile, "revision": revision})
	}
	writeJSON(w, map[string]any{"profiles": profiles, "gateway_reload_required": true})
}

func (s *Server) handleManagementPutProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if !router.ProfileIDValid(id) {
		writeAPIError(w, http.StatusBadRequest, "invalid profile ID", "invalid_request_error", "bad_request")
		return
	}
	var profile router.RoutingProfile
	if err := decodeManagementJSON(w, r, &profile); err != nil || profile.ID != id {
		writeAPIError(w, http.StatusBadRequest, "invalid routing profile", "invalid_request_error", "bad_request")
		return
	}
	var revision string
	err := router.UpdateRouterConfig(r.Context(), s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		replaced := false
		for i := range config.RoutingProfiles {
			if config.RoutingProfiles[i].ID == id {
				config.RoutingProfiles[i] = profile
				replaced = true
				break
			}
		}
		if !replaced {
			config.RoutingProfiles = append(config.RoutingProfiles, profile)
		}
		if err := router.ValidateRoutingProfiles(config.RoutingProfiles, config.Accounts); err != nil {
			return err
		}
		var err error
		revision, err = router.RoutingProfileRevisionFor(profile, config.Accounts, s.cfg.LocalBackend, s.cfg.LocalEndpoint)
		return err
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "routing profile was rejected by Brain validation", "invalid_request_error", "invalid_profile")
		return
	}
	writeJSON(w, map[string]any{"profile": profile, "revision": revision, "gateway_reload_required": true})
}

func (s *Server) handleManagementDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if !router.ProfileIDValid(id) {
		writeAPIError(w, http.StatusBadRequest, "invalid profile ID", "invalid_request_error", "bad_request")
		return
	}
	err := router.UpdateRouterConfig(r.Context(), s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		for i, profile := range config.RoutingProfiles {
			if profile.ID == id {
				config.RoutingProfiles = append(config.RoutingProfiles[:i], config.RoutingProfiles[i+1:]...)
				return nil
			}
		}
		return errors.New("profile not found")
	})
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "routing profile was not found", "invalid_request_error", "not_found")
		return
	}
	writeJSON(w, map[string]any{"removed": true, "gateway_reload_required": true})
}

func (s *Server) loadManagementRouterConfig() (router.RouterConfig, error) {
	if s.cfg.AccountConfigPath == "" {
		return router.RouterConfig{}, errors.New("account management is not configured")
	}
	return router.LoadRouterConfig(s.cfg.AccountConfigPath)
}

type managementAddAccount struct {
	Provider      string   `json:"provider"`
	Name          string   `json:"name"`
	Auth          string   `json:"auth"`
	Credential    string   `json:"credential,omitempty"`
	CredentialRef string   `json:"credential_ref,omitempty"`
	BaseURL       string   `json:"base_url,omitempty"`
	Tier          string   `json:"tier,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	Weight        int      `json:"weight,omitempty"`
	Models        []string `json:"models,omitempty"`
	Protocols     []string `json:"protocols,omitempty"`
	ManagedBridge bool     `json:"managed_bridge,omitempty"`
}

func (s *Server) handleManagementAddAccount(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	var input managementAddAccount
	if err := decodeManagementJSON(w, r, &input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid account request", "invalid_request_error", "bad_request")
		return
	}
	if len(input.Credential) > 4095 || (input.Credential != "" && input.CredentialRef != "") || strings.ContainsAny(input.Credential, "\r\n\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid credential input", "invalid_request_error", "bad_request")
		return
	}
	if input.Auth == "" {
		input.Auth = router.AuthAPIKey
	}
	if input.Auth != router.AuthAPIKey && input.Auth != router.AuthNone && input.Auth != router.AuthOAuth && input.Auth != router.AuthNative {
		writeAPIError(w, http.StatusBadRequest, "this authentication flow must be completed with the Brain CLI", "invalid_request_error", "unsupported_auth_flow")
		return
	}
	if input.Auth == router.AuthOAuth && (!input.ManagedBridge || input.Credential != "" || input.CredentialRef != "" || input.BaseURL != "") {
		writeAPIError(w, http.StatusBadRequest, "OAuth accounts must use Brain's isolated managed bridge", "invalid_request_error", "invalid_oauth_account")
		return
	}
	if input.Auth == router.AuthNative && (input.ManagedBridge || input.Credential != "" || input.CredentialRef != "" || input.BaseURL != "") {
		writeAPIError(w, http.StatusBadRequest, "native accounts use their installed official CLI", "invalid_request_error", "invalid_native_account")
		return
	}
	if input.CredentialRef != "" && !strings.HasPrefix(input.CredentialRef, "env:") && !strings.HasPrefix(input.CredentialRef, "file:") {
		writeAPIError(w, http.StatusBadRequest, "credential references must use env: or file:", "invalid_request_error", "bad_request")
		return
	}
	if (input.Auth == router.AuthNone || input.Auth == router.AuthNative) && (input.Credential != "" || input.CredentialRef != "") {
		writeAPIError(w, http.StatusBadRequest, "credentials are not supported for this authentication mode", "invalid_request_error", "bad_request")
		return
	}
	credential := input.Credential
	if input.CredentialRef != "" {
		credential = input.CredentialRef
	}
	service := router.AccountService{Path: s.cfg.AccountConfigPath}
	account, err := service.Add(r.Context(), router.Account{
		Provider: input.Provider, Name: input.Name, Auth: input.Auth, APIKey: credential, ManagedBridge: input.ManagedBridge,
		BaseURL: input.BaseURL, Tier: input.Tier, Priority: input.Priority, Weight: input.Weight,
		Models: input.Models, Protocols: input.Protocols,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "account was rejected by Brain validation", "invalid_request_error", "invalid_account")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"account": redactManagementAccount(account), "gateway_reload_required": true})
}

type managementUpdateAccount struct {
	Enabled     *bool `json:"enabled,omitempty"`
	Priority    *int  `json:"priority,omitempty"`
	Weight      *int  `json:"weight,omitempty"`
	MakeDefault bool  `json:"make_default,omitempty"`
}

func (s *Server) handleManagementUpdateAccount(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid account ID", "invalid_request_error", "bad_request")
		return
	}
	var input managementUpdateAccount
	if err := decodeManagementJSON(w, r, &input); err != nil || (input.Enabled == nil && input.Priority == nil && input.Weight == nil && !input.MakeDefault) {
		writeAPIError(w, http.StatusBadRequest, "invalid account update", "invalid_request_error", "bad_request")
		return
	}
	if (input.Priority != nil && *input.Priority < 0) || (input.Weight != nil && (*input.Weight < 0 || *input.Weight > router.MaxAccountWeight)) {
		writeAPIError(w, http.StatusBadRequest, "priority or weight is outside its allowed range", "invalid_request_error", "bad_request")
		return
	}
	var updated router.Account
	err := router.UpdateRouterConfig(r.Context(), s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		for i := range config.Accounts {
			account := &config.Accounts[i]
			if account.ID != id {
				continue
			}
			if input.MakeDefault && input.Enabled != nil && !*input.Enabled {
				return errors.New("disabled account cannot be made default")
			}
			if input.Enabled != nil {
				account.Enabled = *input.Enabled
				if *input.Enabled {
					account.Status = router.StatusConfigured
				} else {
					account.Status = router.StatusDisabled
				}
			}
			if input.Priority != nil {
				account.Priority = *input.Priority
			}
			if input.Weight != nil {
				account.Weight = *input.Weight
			}
			if input.MakeDefault {
				if !account.Enabled {
					return errors.New("disabled account cannot be made default")
				}
				if config.Defaults == nil {
					config.Defaults = make(map[string]string)
				}
				config.Defaults[account.Provider] = account.ID
			}
			updated = *account
			return nil
		}
		return errors.New("account not found")
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "account update was rejected", "invalid_request_error", "invalid_account")
		return
	}
	writeJSON(w, map[string]any{"account": redactManagementAccount(updated), "gateway_reload_required": true})
}

func (s *Server) handleManagementRemoveAccount(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid account ID", "invalid_request_error", "bad_request")
		return
	}
	if s.hasActiveManagementLogin(id) {
		writeAPIError(w, http.StatusConflict, "cancel the active OAuth login before removing this account binding", "invalid_request_error", "oauth_login_in_progress")
		return
	}
	if err := (router.AccountService{Path: s.cfg.AccountConfigPath}).Remove(r.Context(), id); err != nil {
		writeAPIError(w, http.StatusNotFound, "account was not found or could not be removed", "invalid_request_error", "not_found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"removed": true, "gateway_reload_required": true})
}

func decodeManagementJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeManagementJSONLimit(w, r, target, 64<<10)
}

func decodeManagementJSONLimit(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) handleManagementUI(w http.ResponseWriter, r *http.Request) {
	assets := map[string]struct{ file, contentType string }{
		"/manage-ui/":          {"management-ui/index.html", "text/html; charset=utf-8"},
		"/manage-ui/app.js":    {"management-ui/app.js", "text/javascript; charset=utf-8"},
		"/manage-ui/style.css": {"management-ui/style.css", "text/css; charset=utf-8"},
	}
	asset, ok := assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	content, err := managementUI.ReadFile(asset.file)
	if err != nil {
		http.Error(w, "management UI asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'self' http://127.0.0.1:3141 http://localhost:3141 http://127.0.0.1:5173 http://localhost:5173 tauri://localhost")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func ManagementOriginMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isManagementRoute(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" {
				sameOrigin := origin == "http://"+r.Host || (r.TLS != nil && origin == "https://"+r.Host)
				if !sameOrigin {
					writeAPIError(w, http.StatusForbidden, "management origin is not allowed", "invalid_request_error", "origin_denied")
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				if origin == "" {
					writeAPIError(w, http.StatusForbidden, "management preflight requires an allowed origin", "invalid_request_error", "origin_denied")
					return
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

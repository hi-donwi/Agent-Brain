package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/oauthbridge"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

var errNativeSessionNeedsCLI = errors.New("native account login and verification require the official local CLI")

func checkManagementAccount(ctx context.Context, root string, account router.Account) (router.Account, error) {
	if account.Auth == router.AuthNative {
		return account, errNativeSessionNeedsCLI
	}
	stop, err := (oauthbridge.Manager{Root: root}).Use(ctx, account)
	if err != nil {
		return account, err
	}
	defer stop()

	var models []string
	if account.Auth == router.AuthOAuth {
		bridge := router.BridgeClient{URL: account.BridgeURL, Key: account.BridgeKey}
		identity, err := bridge.Verify(ctx, account.Provider, account.BridgeCredential)
		if err != nil {
			return account, err
		}
		models, err = bridge.WaitModels(ctx, account.Provider, identity, nil)
		if err != nil {
			return account, err
		}
		account.BridgeCredential = identity
	} else {
		models, err = router.DiscoverModels(ctx, account)
		if err != nil {
			return account, err
		}
	}
	account.DiscoveredModels = models
	if len(account.Models) == 0 {
		account.Models = append([]string(nil), models...)
	}
	account.Status = router.StatusActive
	account.GatewayReady = true
	account.CLIReady = false
	return account, nil
}

func (s *Server) handleManagementTestAccount(w http.ResponseWriter, r *http.Request) {
	s.handleManagementAccountCheck(w, r, "test")
}

func (s *Server) handleManagementRefreshModels(w http.ResponseWriter, r *http.Request) {
	s.handleManagementAccountCheck(w, r, "refresh")
}

func (s *Server) handleManagementAccountCheck(w http.ResponseWriter, r *http.Request, operation string) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid account ID", "invalid_request_error", "bad_request")
		return
	}
	service := router.AccountService{Path: s.cfg.AccountConfigPath}
	original, err := service.Find(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "account not found", "invalid_request_error", "not_found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	checked, err := checkManagementAccount(ctx, s.managementRoot(), original)
	if errors.Is(err, errNativeSessionNeedsCLI) {
		writeAPIError(w, http.StatusConflict, "native account status must be checked with the official Brain CLI", "invalid_request_error", "native_cli_required")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "Brain could not verify this account or refresh its model catalog", "api_error", "account_check_failed")
		return
	}
	if err := router.UpdateRouterConfig(ctx, s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		for i := range config.Accounts {
			current := &config.Accounts[i]
			if current.ID != id {
				continue
			}
			if current.APIKey != original.APIKey || current.BridgeKey != original.BridgeKey || current.BridgeCredential != original.BridgeCredential || current.BaseURL != original.BaseURL {
				return errors.New("account changed during verification")
			}
			current.Status = checked.Status
			current.CLIReady = checked.CLIReady
			current.GatewayReady = checked.GatewayReady
			current.BridgeCredential = checked.BridgeCredential
			current.DiscoveredModels = checked.DiscoveredModels
			current.Models = checked.Models
			return nil
		}
		return errors.New("account was removed during verification")
	}); err != nil {
		writeAPIError(w, http.StatusConflict, "account changed during verification; retry", "invalid_request_error", "account_changed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"account": redactManagementAccount(checked), "gateway_reload_required": true, "inference_probe_performed": false, "operation": operation})
}

func (s *Server) managementRoot() string {
	return filepath.Dir(s.cfg.AccountConfigPath)
}

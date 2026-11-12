package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/oauthbridge"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

const maxActiveManagementLogins = 4

type managementLoginSession struct {
	id               string
	accountID        string
	provider         string
	state            string
	authorizationURL string
	status           string
	errorCode        string
	expiresAt        time.Time
	cancel           context.CancelFunc
}

func (s *Server) handleManagementStartLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccountConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "account management is not configured", "api_error", "management_unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid account ID", "invalid_request_error", "bad_request")
		return
	}
	account, err := (router.AccountService{Path: s.cfg.AccountConfigPath}).Find(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "account not found", "invalid_request_error", "not_found")
		return
	}
	if account.Auth == router.AuthNative {
		writeAPIError(w, http.StatusConflict, "native account login must run in its official local CLI", "invalid_request_error", "native_cli_required")
		return
	}
	if account.Auth != router.AuthOAuth {
		writeAPIError(w, http.StatusConflict, "OAuth login is only available for OAuth accounts", "invalid_request_error", "oauth_required")
		return
	}
	if account.BridgeCredential != "" {
		writeAPIError(w, http.StatusConflict, "this account already has a provider identity; test it or use the Brain CLI to reauthenticate", "invalid_request_error", "account_already_authenticated")
		return
	}
	s.pruneManagementLoginSessions()
	s.managementLoginMu.Lock()
	var existing *managementLoginSession
	for _, session := range s.managementLoginSessions {
		if session.accountID == id && isActiveLoginStatus(session.status) {
			copy := *session
			existing = &copy
			break
		}
	}
	if existing != nil {
		s.managementLoginMu.Unlock()
		s.writeLoginSession(w, http.StatusAccepted, existing)
		return
	}
	active := 0
	for _, session := range s.managementLoginSessions {
		if isActiveLoginStatus(session.status) {
			active++
		}
	}
	if active >= maxActiveManagementLogins {
		s.managementLoginMu.Unlock()
		writeAPIError(w, http.StatusTooManyRequests, "too many OAuth logins are active", "api_error", "login_limit")
		return
	}
	loginID, err := newManagementLoginID()
	if err != nil {
		s.managementLoginMu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "could not start OAuth login", "api_error", "login_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	session := &managementLoginSession{
		id: loginID, accountID: id, provider: account.Provider, status: "starting",
		expiresAt: time.Now().Add(10 * time.Minute), cancel: cancel,
	}
	s.managementLoginSessions[loginID] = session
	s.managementLoginWG.Add(1)
	s.managementLoginMu.Unlock()
	go func() {
		defer s.managementLoginWG.Done()
		s.runManagementLogin(ctx, session, account)
	}()
	s.writeLoginSession(w, http.StatusAccepted, session)
}

func (s *Server) runManagementLogin(ctx context.Context, session *managementLoginSession, account router.Account) {
	defer session.cancel()
	bridgeStop, err := (oauthbridge.Manager{Root: filepath.Dir(s.cfg.AccountConfigPath)}).Use(ctx, account)
	if err != nil {
		s.finishManagementLogin(session, "failed", "login_start_failed", nil)
		return
	}
	defer bridgeStop()
	bridge := router.BridgeClient{URL: account.BridgeURL, Key: account.BridgeKey}
	authorizationURL, state, err := bridge.BeginLogin(ctx, account.Provider)
	if err != nil {
		status := "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
		s.finishManagementLogin(session, status, "login_start_failed", nil)
		return
	}
	s.managementLoginMu.Lock()
	cancelled := ctx.Err() != nil || session.status == "canceling"
	if !cancelled {
		session.state = state
		session.authorizationURL = authorizationURL
		session.status = "pending"
	}
	s.managementLoginMu.Unlock()
	if cancelled {
		cancelManagementOAuthState(bridge, state)
		s.finishManagementLogin(session, "cancelled", "", nil)
		return
	}

	identity, err := bridge.WaitLogin(ctx, account.Provider, state)
	if err != nil {
		cancelManagementOAuthState(bridge, state)
		status := "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
		s.finishManagementLogin(session, status, "provider_login_failed", nil)
		return
	}
	if !s.beginManagementLoginCompletion(ctx, session) {
		cancelManagementOAuthState(bridge, state)
		s.finishManagementLogin(session, "cancelled", "", nil)
		return
	}
	persistCtx, persistCancel := managementPersistenceContext()
	identityErr := s.saveOAuthIdentity(persistCtx, account, identity)
	persistCancel()
	if identityErr != nil {
		s.finishManagementLogin(session, "failed", "account_changed", nil)
		return
	}

	models, modelErr := bridge.WaitModels(ctx, account.Provider, identity, nil)
	if modelErr != nil {
		updated, _ := (router.AccountService{Path: s.cfg.AccountConfigPath}).Find(account.ID)
		s.finishManagementLogin(session, "complete", "model_catalog_unavailable", &updated)
		return
	}
	persistCtx, persistCancel = managementPersistenceContext()
	modelsErr := s.saveOAuthModels(persistCtx, account, identity, models)
	persistCancel()
	if modelsErr != nil {
		s.finishManagementLogin(session, "failed", "account_changed", nil)
		return
	}
	updated, _ := (router.AccountService{Path: s.cfg.AccountConfigPath}).Find(account.ID)
	s.finishManagementLogin(session, "complete", "", &updated)
}

func managementPersistenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func (s *Server) beginManagementLoginCompletion(ctx context.Context, session *managementLoginSession) bool {
	s.managementLoginMu.Lock()
	defer s.managementLoginMu.Unlock()
	if ctx.Err() != nil || session.status == "canceling" {
		return false
	}
	session.status = "completing"
	return true
}

func cancelManagementOAuthState(bridge router.BridgeClient, state string) {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bridge.CancelLogin(cleanup, state)
}

func (s *Server) saveOAuthIdentity(ctx context.Context, original router.Account, identity string) error {
	return router.UpdateRouterConfig(ctx, s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		for i := range config.Accounts {
			current := &config.Accounts[i]
			if current.ID != original.ID {
				continue
			}
			if current.Auth != router.AuthOAuth || current.APIKey != original.APIKey || current.BridgeKey != original.BridgeKey || current.BaseURL != original.BaseURL {
				return errors.New("account changed during OAuth login")
			}
			current.BridgeCredential = identity
			current.Status = router.StatusConfigured
			current.CLIReady, current.GatewayReady = false, false
			return nil
		}
		return errors.New("account was removed during OAuth login")
	})
}

func (s *Server) saveOAuthModels(ctx context.Context, original router.Account, identity string, models []string) error {
	return router.UpdateRouterConfig(ctx, s.cfg.AccountConfigPath, func(config *router.RouterConfig) error {
		for i := range config.Accounts {
			current := &config.Accounts[i]
			if current.ID != original.ID {
				continue
			}
			if current.BridgeCredential != identity || current.BridgeKey != original.BridgeKey {
				return errors.New("account changed during model discovery")
			}
			current.DiscoveredModels = models
			if len(current.Models) == 0 {
				current.Models = append([]string(nil), models...)
			}
			current.Status = router.StatusActive
			current.CLIReady = false
			current.GatewayReady = true
			return nil
		}
		return errors.New("account was removed during model discovery")
	})
}

func (s *Server) handleManagementLoginStatus(w http.ResponseWriter, r *http.Request) {
	session := s.managementLoginSession(r.PathValue("id"), r.PathValue("session"))
	if session == nil {
		writeAPIError(w, http.StatusNotFound, "OAuth login session not found", "invalid_request_error", "not_found")
		return
	}
	s.writeLoginSession(w, http.StatusOK, session)
}

func (s *Server) handleManagementCancelLogin(w http.ResponseWriter, r *http.Request) {
	accountID, sessionID := r.PathValue("id"), r.PathValue("session")
	if accountID == "" || sessionID == "" || strings.ContainsAny(accountID+sessionID, "/\\\x00") {
		writeAPIError(w, http.StatusNotFound, "OAuth login session not found", "invalid_request_error", "not_found")
		return
	}
	s.managementLoginMu.Lock()
	session := s.managementLoginSessions[sessionID]
	if session == nil || session.accountID != accountID {
		s.managementLoginMu.Unlock()
		writeAPIError(w, http.StatusNotFound, "OAuth login session not found", "invalid_request_error", "not_found")
		return
	}
	if session.status == "starting" || session.status == "pending" {
		session.status = "canceling"
		if session.cancel != nil {
			session.cancel()
		}
	}
	copy := *session
	s.managementLoginMu.Unlock()
	s.writeLoginSession(w, http.StatusAccepted, &copy)
}

func (s *Server) managementLoginSession(accountID, sessionID string) *managementLoginSession {
	if accountID == "" || sessionID == "" || strings.ContainsAny(accountID+sessionID, "/\\\x00") {
		return nil
	}
	s.pruneManagementLoginSessions()
	s.managementLoginMu.Lock()
	defer s.managementLoginMu.Unlock()
	session := s.managementLoginSessions[sessionID]
	if session == nil || session.accountID != accountID {
		return nil
	}
	copy := *session
	return &copy
}

func (s *Server) pruneManagementLoginSessions() {
	now := time.Now()
	s.managementLoginMu.Lock()
	defer s.managementLoginMu.Unlock()
	for id, session := range s.managementLoginSessions {
		if !session.expiresAt.After(now) {
			if session.cancel != nil {
				session.cancel()
			}
			delete(s.managementLoginSessions, id)
		}
	}
}

func (s *Server) hasActiveManagementLogin(accountID string) bool {
	s.pruneManagementLoginSessions()
	s.managementLoginMu.Lock()
	defer s.managementLoginMu.Unlock()
	for _, session := range s.managementLoginSessions {
		if session.accountID == accountID && isActiveLoginStatus(session.status) {
			return true
		}
	}
	return false
}

func (s *Server) finishManagementLogin(session *managementLoginSession, status, errorCode string, account *router.Account) {
	s.managementLoginMu.Lock()
	defer s.managementLoginMu.Unlock()
	session.status = status
	session.errorCode = errorCode
	session.state = ""
	session.authorizationURL = ""
	session.expiresAt = time.Now().Add(5 * time.Minute)
	if account != nil {
		session.provider = account.Provider
	}
}

func (s *Server) writeLoginSession(w http.ResponseWriter, status int, session *managementLoginSession) {
	s.managementLoginMu.Lock()
	copy := *session
	s.managementLoginMu.Unlock()
	session = &copy
	response := map[string]any{
		"session_id": session.id, "account_id": session.accountID, "status": session.status,
		"expires_at": session.expiresAt.UTC().Format(time.RFC3339),
	}
	if session.authorizationURL != "" && session.status == "pending" {
		response["authorization_url"] = session.authorizationURL
	}
	if session.errorCode != "" {
		response["error_code"] = session.errorCode
	}
	if session.status == "complete" {
		account, err := (router.AccountService{Path: s.cfg.AccountConfigPath}).Find(session.accountID)
		if err == nil {
			response["account"] = redactManagementAccount(account)
			response["gateway_reload_required"] = true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func isActiveLoginStatus(status string) bool {
	return status == "starting" || status == "pending" || status == "completing" || status == "canceling"
}

func newManagementLoginID() (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random[:]), nil
}

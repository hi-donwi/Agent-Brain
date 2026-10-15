package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"net"
)

func prepareManagedAccount(a *Account, existing []Account) error {
	if a.Auth != AuthOAuth || (NormalizeProvider(a.Provider) != ProviderAntigravity && NormalizeProvider(a.Provider) != ProviderCodex) {
		return fmt.Errorf("managed bridges support Antigravity or Codex OAuth accounts only")
	}
	if a.BaseURL != "" || a.BridgeURL != "" || a.APIKey != "" || a.BridgeKey != "" {
		return fmt.Errorf("managed bridge setup cannot be combined with external bridge URLs or credentials")
	}
	for attempt := 0; attempt < 20; attempt++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("allocate a local OAuth bridge port: %w", err)
		}
		endpoint := "http://" + listener.Addr().String()
		_ = listener.Close()
		reserved := false
		for _, account := range existing {
			reserved = reserved || account.BaseURL == endpoint
		}
		if !reserved {
			a.BaseURL = endpoint
			a.BridgeURL = endpoint + "/v8/management"
			break
		}
	}
	if a.BaseURL == "" {
		return fmt.Errorf("cannot allocate an independent OAuth bridge port")
	}
	for _, key := range []*string{&a.APIKey, &a.BridgeKey} {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return fmt.Errorf("generate local bridge credentials: %w", err)
		}
		*key = hex.EncodeToString(random[:])
	}
	return nil
}

// EnableManaged explicitly replaces an external binding with an isolated managed
// bridge. It retains the account ID; upstream credentials are not copied or deleted.
func (s AccountService) EnableManaged(ctx context.Context, ref string) (Account, error) {
	var result Account
	err := UpdateRouterConfig(ctx, s.Path, func(cfg *RouterConfig) error {
		a, err := findAccount(*cfg, ref)
		if err != nil {
			return err
		}
		if a.ManagedBridge {
			result = a
			return nil
		}
		a.ManagedBridge = true
		a.BaseURL, a.BridgeURL, a.APIKey, a.BridgeKey = "", "", "", ""
		if err := prepareManagedAccount(&a, cfg.Accounts); err != nil {
			return err
		}
		secretID := uuid.NewString()
		a.APIKey, err = s.storeSecret(secretID+".managed.key", a.APIKey)
		if err != nil {
			return err
		}
		a.BridgeKey, err = s.storeSecret(secretID+".managed.bridge", a.BridgeKey)
		if err != nil {
			return err
		}
		a.BridgeCredential = ""
		a.Models = nil
		a.Status = StatusPending
		a.CLIReady, a.GatewayReady = false, false
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ID == a.ID {
				cfg.Accounts[i] = a
			}
		}
		result = a
		return nil
	})
	return result, err
}

package router

import (
	"sync"
	"time"
)

// Provider constants identify upstream AI service vendors.
const (
	ProviderAnthropic   = "anthropic"
	ProviderOpenAI      = "openai"
	ProviderGemini      = "google-ai-studio"
	ProviderDeepSeek    = "deepseek"
	ProviderOpenRouter  = "openrouter"
	ProviderLocal       = "local"
	ProviderAntigravity = "antigravity"
	ProviderCodex       = "codex"
	ProviderClaudeCode  = "claude-code"
)

// Tier constants prioritize account routing from subscription to free to local.
const (
	TierSubscription = "subscription" // Flat-rate monthly / Pro accounts
	TierCheap        = "cheap"        // Cost-effective pay-as-you-go
	TierFree         = "free"         // Free-tier accounts / trials
	TierLocal        = "local"        // Self-hosted local inference
)

// AccountStatus represents the operational readiness of an account.
type AccountStatus string

const (
	StatusActive      AccountStatus = "active"
	StatusRateLimited AccountStatus = "rate_limited"
	StatusExhausted   AccountStatus = "exhausted"
	StatusDisabled    AccountStatus = "disabled"
	StatusPending     AccountStatus = "pending"
	StatusReauth      AccountStatus = "reauth_required"
	StatusConfigured  AccountStatus = "configured"
)

// Account holds credential and routing metadata for one provider identity.
type Account struct {
	ID               string            `json:"id" yaml:"id"`
	Name             string            `json:"name" yaml:"name"`
	Provider         string            `json:"provider" yaml:"provider"`   // anthropic, openai, gemini, etc.
	APIKey           string            `json:"-" yaml:"api_key,omitempty"` // legacy raw key, env:NAME, or file:PATH
	BaseURL          string            `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Tier             string            `json:"tier" yaml:"tier"`         // subscription, cheap, free, local
	Priority         int               `json:"priority" yaml:"priority"` // lower number = higher priority
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	Status           AccountStatus     `json:"status" yaml:"status"`
	Cooldown         time.Time         `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	FailCount        int               `json:"fail_count" yaml:"fail_count"`
	Headers          map[string]string `json:"-" yaml:"headers,omitempty"`
	Auth             string            `json:"auth,omitempty" yaml:"auth,omitempty"`
	Models           []string          `json:"models,omitempty" yaml:"models,omitempty"`
	DiscoveredModels []string          `json:"discovered_models,omitempty" yaml:"discovered_models,omitempty"`
	Protocols        []string          `json:"protocols,omitempty" yaml:"protocols,omitempty"`
	CLIReady         bool              `json:"cli_ready" yaml:"cli_ready,omitempty"`
	GatewayReady     bool              `json:"gateway_ready" yaml:"gateway_ready,omitempty"`
	BridgeURL        string            `json:"bridge_url,omitempty" yaml:"bridge_url,omitempty"`
	BridgeKey        string            `json:"-" yaml:"bridge_key,omitempty"`
	BridgeCredential string            `json:"bridge_credential,omitempty" yaml:"bridge_credential,omitempty"`
	ManagedBridge    bool              `json:"managed_bridge,omitempty" yaml:"managed_bridge,omitempty"`
}

// RouterConfig holds settings for multi-account routing, auto-fallback, and token saver.
type RouterConfig struct {
	Accounts       []Account         `json:"accounts" yaml:"accounts"`
	Strategy       string            `json:"strategy" yaml:"strategy"` // priority, round-robin
	TokenSaver     bool              `json:"token_saver" yaml:"token_saver"`
	MaxCooldownSec int               `json:"max_cooldown_sec" yaml:"max_cooldown_sec"`
	Defaults       map[string]string `json:"defaults,omitempty" yaml:"defaults,omitempty"`
}

// DefaultRouterConfig returns safe defaults.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		Accounts:       []Account{},
		Strategy:       "priority",
		TokenSaver:     true,
		MaxCooldownSec: 60,
	}
}

// AccountPool coordinates accounts, round-robin rotation, and rate-limit recovery.
type AccountPool struct {
	mu             sync.RWMutex
	accounts       []*Account
	strategy       string
	maxCooldownSec int
	rrIndices      map[string]int // provider -> round-robin pointer
}

package router

import (
	"sync"
	"time"
)

// Provider constants identify upstream AI service vendors.
const (
	ProviderAnthropic  = "anthropic"
	ProviderOpenAI     = "openai"
	ProviderGemini     = "gemini"
	ProviderDeepSeek   = "deepseek"
	ProviderOpenRouter = "openrouter"
	ProviderLocal      = "local"
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
)

// Account holds credential and routing metadata for one provider identity.
type Account struct {
	ID        string            `json:"id" yaml:"id"`
	Name      string            `json:"name" yaml:"name"`
	Provider  string            `json:"provider" yaml:"provider"` // anthropic, openai, gemini, etc.
	APIKey    string            `json:"api_key" yaml:"api_key"`   // raw key or env:NAME reference
	BaseURL   string            `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Tier      string            `json:"tier" yaml:"tier"`         // subscription, cheap, free, local
	Priority  int               `json:"priority" yaml:"priority"` // lower number = higher priority
	Enabled   bool              `json:"enabled" yaml:"enabled"`
	Status    AccountStatus     `json:"status" yaml:"status"`
	Cooldown  time.Time         `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	FailCount int               `json:"fail_count" yaml:"fail_count"`
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// RouterConfig holds settings for multi-account routing, auto-fallback, and token saver.
type RouterConfig struct {
	Accounts       []Account `json:"accounts" yaml:"accounts"`
	Strategy       string    `json:"strategy" yaml:"strategy"` // priority, round-robin
	TokenSaver     bool      `json:"token_saver" yaml:"token_saver"`
	MaxCooldownSec int       `json:"max_cooldown_sec" yaml:"max_cooldown_sec"`
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

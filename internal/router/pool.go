package router

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"time"
)

// ErrNoAvailableAccount is returned when all accounts for a provider are rate-limited or exhausted.
var ErrNoAvailableAccount = errors.New("no active accounts available for provider")

// NewAccountPool creates a thread-safe pool with the given configuration.
func NewAccountPool(cfg RouterConfig) *AccountPool {
	p := &AccountPool{
		strategy:       cfg.Strategy,
		maxCooldownSec: cfg.MaxCooldownSec,
		rrIndices:      make(map[string]int),
		weightedStates: make(map[string]map[string]int),
		accounts:       make([]*Account, len(cfg.Accounts)),
	}
	if p.maxCooldownSec <= 0 {
		p.maxCooldownSec = 60
	}
	if p.strategy == "" {
		p.strategy = "priority"
	}
	for i, a := range cfg.Accounts {
		cp := *copyAccount(&a)
		cp.Provider = NormalizeProvider(cp.Provider)
		if cp.Weight == 0 {
			cp.Weight = 1
		}
		if cp.Status == "" {
			cp.Status = StatusConfigured
		}
		p.accounts[i] = &cp
	}
	return p
}

// AddAccount adds or updates an account in the pool.
func (p *AccountPool) AddAccount(acc Account) {
	p.mu.Lock()
	defer p.mu.Unlock()

	acc.Provider = NormalizeProvider(acc.Provider)
	acc = *copyAccount(&acc)
	if acc.Weight == 0 {
		acc.Weight = 1
	}
	if acc.Status == "" {
		acc.Status = StatusConfigured
	}

	for i, existing := range p.accounts {
		if existing.ID == acc.ID {
			p.accounts[i] = &acc
			delete(p.weightedStates, acc.Provider)
			return
		}
	}
	p.accounts = append(p.accounts, &acc)
	delete(p.weightedStates, acc.Provider)
}

// RemoveAccount deletes an account by ID.
func (p *AccountPool) RemoveAccount(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, existing := range p.accounts {
		if existing.ID == id {
			provider := existing.Provider
			p.accounts = append(p.accounts[:i], p.accounts[i+1:]...)
			delete(p.weightedStates, provider)
			return true
		}
	}
	return false
}

// ListAccounts returns a snapshot of all accounts in the pool.
func (p *AccountPool) ListAccounts() []Account {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	res := make([]Account, len(p.accounts))
	for i, a := range p.accounts {
		cp := *copyAccount(a)
		if cp.Status == StatusRateLimited && !cp.Cooldown.IsZero() && now.After(cp.Cooldown) {
			cp.Status = StatusActive
		}
		res[i] = cp
	}
	return res
}

// SelectAccount chooses an active account for the specified provider using the configured strategy.
func (p *AccountPool) SelectAccount(provider string) (*Account, error) {
	return p.selectAccount(provider, "", nil)
}

func (p *AccountPool) selectAccount(provider, pinned string, tried map[string]bool) (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	provider = NormalizeProvider(provider)

	now := time.Now()

	// 1. Refresh any expired cooldowns
	for _, a := range p.accounts {
		if a.Status == StatusRateLimited && !a.Cooldown.IsZero() && now.After(a.Cooldown) {
			a.Status = StatusActive
			a.Cooldown = time.Time{}
		}
	}

	// 2. Filter active candidates for provider
	var candidates []*Account
	for _, a := range p.accounts {
		if !a.Enabled || a.Auth == AuthNative || tried[a.ID] || (pinned != "" && pinned != a.ID) {
			continue
		}
		if provider != "" && a.Provider != provider {
			continue
		}
		if a.Status == StatusActive || a.Status == StatusConfigured {
			candidates = append(candidates, a)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAvailableAccount, provider)
	}

	// 3. Selection strategy
	if p.strategy == "round-robin" {
		idx := p.rrIndices[provider] % len(candidates)
		p.rrIndices[provider] = (idx + 1) % len(candidates)
		return copyAccount(candidates[idx]), nil
	}
	if p.strategy == "weighted" {
		return copyAccount(p.selectWeighted(provider, candidates)), nil
	}

	// Priority order: Tier weight (subscription > cheap > free > local) then Priority int
	tierRank := func(tier string) int {
		switch tier {
		case TierSubscription:
			return 1
		case TierCheap:
			return 2
		case TierFree:
			return 3
		case TierLocal:
			return 4
		default:
			return 5
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		rI := tierRank(candidates[i].Tier)
		rJ := tierRank(candidates[j].Tier)
		if rI != rJ {
			return rI < rJ
		}
		return candidates[i].Priority < candidates[j].Priority
	})

	return copyAccount(candidates[0]), nil
}

// selectWeighted applies smooth weighted round-robin among currently eligible accounts.
// Candidates are visited in config order, making ties deterministic and testable.
func (p *AccountPool) selectWeighted(provider string, candidates []*Account) *Account {
	state := p.weightedStates[provider]
	if state == nil {
		state = make(map[string]int)
		p.weightedStates[provider] = state
	}
	eligible := make(map[string]bool, len(candidates))
	totalWeight := 0
	var selected *Account
	for _, account := range candidates {
		eligible[account.ID] = true
		weight := account.Weight
		if weight <= 0 {
			weight = 1
		}
		state[account.ID] += weight
		totalWeight += weight
		if selected == nil || state[account.ID] > state[selected.ID] {
			selected = account
		}
	}
	for id := range state {
		if !eligible[id] {
			delete(state, id)
		}
	}
	state[selected.ID] -= totalWeight
	return selected
}

func copyAccount(a *Account) *Account {
	cp := *a
	cp.Headers = maps.Clone(a.Headers)
	cp.Models = append([]string(nil), a.Models...)
	cp.DiscoveredModels = append([]string(nil), a.DiscoveredModels...)
	cp.Protocols = append([]string(nil), a.Protocols...)
	return &cp
}

// ReportResult updates an account's state based on the HTTP status returned by upstream.
func (p *AccountPool) ReportResult(accountID string, statusCode int, retryAfter time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, a := range p.accounts {
		if a.ID != accountID {
			continue
		}

		switch {
		case statusCode == 429 || statusCode == 529:
			a.Status = StatusRateLimited
			a.FailCount++
			cooldown := retryAfter
			if cooldown <= 0 {
				cooldown = time.Duration(p.maxCooldownSec) * time.Second
			}
			a.Cooldown = now.Add(cooldown)

		case statusCode == 401 || statusCode == 402 || statusCode == 403:
			a.Status = StatusExhausted
			a.GatewayReady = false
			a.FailCount++

		case statusCode >= 200 && statusCode < 300:
			a.Status = StatusActive
			a.GatewayReady = true
			a.FailCount = 0
			a.Cooldown = time.Time{}
		}
		return
	}
}

// ResolveAPIKey returns the effective key, resolving `env:NAME` references.
func ResolveAPIKey(rawKey string) string {
	raw := strings.TrimSpace(rawKey)
	if strings.HasPrefix(raw, "env:") {
		envName := strings.TrimPrefix(raw, "env:")
		return os.Getenv(envName)
	}
	return raw
}

// DetectProvider infers the upstream AI provider from a model name or alias.
func DetectProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if prefix, _, found := strings.Cut(m, "/"); found {
		if p, err := LookupProvider(prefix); err == nil {
			return p.ID
		}
	}
	switch {
	case strings.HasPrefix(m, "claude") || strings.HasPrefix(m, "anthropic/"):
		return ProviderAnthropic
	case strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "text-embedding-3") || strings.HasPrefix(m, "openai/"):
		return ProviderOpenAI
	case strings.HasPrefix(m, "gemini") || strings.HasPrefix(m, "google/"):
		return ProviderGemini
	case m == "deepseek-chat" || m == "deepseek-reasoner" || strings.HasPrefix(m, "deepseek/"):
		return ProviderDeepSeek
	case strings.Contains(m, "/"):
		return ProviderOpenRouter
	default:
		return ProviderLocal
	}
}

// HasProvider checks if the pool has any enabled accounts for the given provider.
func (p *AccountPool) HasProvider(provider string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	provider = NormalizeProvider(provider)
	for _, a := range p.accounts {
		if a.Enabled && a.Auth != AuthNative && (provider == "" || a.Provider == provider) {
			return true
		}
	}
	return false
}

// HasAccounts checks if any accounts are configured in the pool.
func (p *AccountPool) HasAccounts() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.accounts) > 0
}

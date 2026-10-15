package router

import (
	"errors"
	"fmt"
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
		accounts:       make([]*Account, len(cfg.Accounts)),
	}
	if p.maxCooldownSec <= 0 {
		p.maxCooldownSec = 60
	}
	if p.strategy == "" {
		p.strategy = "priority"
	}
	for i, a := range cfg.Accounts {
		cp := a
		if cp.Status == "" {
			cp.Status = StatusActive
		}
		p.accounts[i] = &cp
	}
	return p
}

// AddAccount adds or updates an account in the pool.
func (p *AccountPool) AddAccount(acc Account) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if acc.Status == "" {
		acc.Status = StatusActive
	}

	for i, existing := range p.accounts {
		if existing.ID == acc.ID {
			p.accounts[i] = &acc
			return
		}
	}
	p.accounts = append(p.accounts, &acc)
}

// RemoveAccount deletes an account by ID.
func (p *AccountPool) RemoveAccount(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, existing := range p.accounts {
		if existing.ID == id {
			p.accounts = append(p.accounts[:i], p.accounts[i+1:]...)
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
		cp := *a
		if cp.Status == StatusRateLimited && !cp.Cooldown.IsZero() && now.After(cp.Cooldown) {
			cp.Status = StatusActive
		}
		res[i] = cp
	}
	return res
}

// SelectAccount chooses an active account for the specified provider based on tier, priority, or round-robin.
func (p *AccountPool) SelectAccount(provider string) (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

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
		if !a.Enabled {
			continue
		}
		if provider != "" && a.Provider != provider {
			continue
		}
		if a.Status == StatusActive {
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
		return candidates[idx], nil
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

	return candidates[0], nil
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
			a.FailCount++

		case statusCode >= 200 && statusCode < 300:
			a.Status = StatusActive
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

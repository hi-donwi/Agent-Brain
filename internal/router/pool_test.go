package router

import (
	"strings"
	"testing"
	"time"
)

func TestAccountPool_PrioritySelectionAndFailover(t *testing.T) {
	cfg := RouterConfig{
		Strategy:       "priority",
		MaxCooldownSec: 10,
		Accounts: []Account{
			{
				ID:       "acc-sub-1",
				Name:     "Anthropic Pro 1",
				Provider: ProviderAnthropic,
				Tier:     TierSubscription,
				Priority: 1,
				Enabled:  true,
			},
			{
				ID:       "acc-sub-2",
				Name:     "Anthropic Pro 2",
				Provider: ProviderAnthropic,
				Tier:     TierSubscription,
				Priority: 2,
				Enabled:  true,
			},
			{
				ID:       "acc-cheap",
				Name:     "Anthropic Cheap Fallback",
				Provider: ProviderAnthropic,
				Tier:     TierCheap,
				Priority: 1,
				Enabled:  true,
			},
		},
	}

	pool := NewAccountPool(cfg)

	// Initially, should pick highest priority subscription account
	acc, err := pool.SelectAccount(ProviderAnthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acc.ID != "acc-sub-1" {
		t.Errorf("expected acc-sub-1, got %s", acc.ID)
	}

	// Report rate limit 429 on acc-sub-1
	pool.ReportResult("acc-sub-1", 429, 2*time.Second)

	// Now should failover to acc-sub-2
	acc, err = pool.SelectAccount(ProviderAnthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acc.ID != "acc-sub-2" {
		t.Errorf("expected acc-sub-2 after 429, got %s", acc.ID)
	}

	// Report rate limit on acc-sub-2 as well
	pool.ReportResult("acc-sub-2", 429, 2*time.Second)

	// Now should failover to tier 2 (acc-cheap)
	acc, err = pool.SelectAccount(ProviderAnthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acc.ID != "acc-cheap" {
		t.Errorf("expected acc-cheap after all subscriptions limited, got %s", acc.ID)
	}
}

func TestAccountPool_RoundRobin(t *testing.T) {
	cfg := RouterConfig{
		Strategy: "round-robin",
		Accounts: []Account{
			{ID: "oai-1", Provider: ProviderOpenAI, Enabled: true},
			{ID: "oai-2", Provider: ProviderOpenAI, Enabled: true},
		},
	}
	pool := NewAccountPool(cfg)

	first, err := pool.SelectAccount(ProviderOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.SelectAccount(ProviderOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	third, err := pool.SelectAccount(ProviderOpenAI)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID == second.ID {
		t.Errorf("expected different accounts in round-robin, got both %s", first.ID)
	}
	if third.ID != first.ID {
		t.Errorf("expected round-robin cycle back to %s, got %s", first.ID, third.ID)
	}
}

func TestCompressToolResult(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("\x1b[32mPASS\x1b[0m test/sample.go\n\n\n\n\n")
	for i := 0; i < 30; i++ {
		builder.WriteString("INFO 2026-10-15: connection pool healthy in worker process\n")
	}
	builder.WriteString("DONE\n")

	raw := builder.String()
	compressed := CompressToolResult(raw)

	if testing.Verbose() {
		t.Logf("Raw size: %d bytes, Compressed size: %d bytes", len(raw), len(compressed))
	}

	if len(compressed) >= len(raw) {
		t.Errorf("expected compression to reduce size, raw %d vs compressed %d", len(raw), len(compressed))
	}
	if strings.Contains(compressed, "\x1b[32m") {
		t.Errorf("expected ANSI escape code to be stripped")
	}
	if !strings.Contains(compressed, "...[x") {
		t.Errorf("expected repetitive lines to be compressed with marker")
	}
}

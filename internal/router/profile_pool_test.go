package router

import "testing"

func TestSelectAccountFromRestrictsCandidatesToProfileAllowlist(t *testing.T) {
	pool := NewAccountPool(RouterConfig{
		Strategy: "priority",
		Accounts: []Account{
			{ID: "approved", Provider: ProviderOpenAI, Enabled: true, Status: StatusActive, Priority: 1},
			{ID: "unapproved", Provider: ProviderOpenAI, Enabled: true, Status: StatusActive, Priority: 0},
		},
	})
	selected, err := pool.SelectAccountFrom(ProviderOpenAI, []string{"approved"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "approved" {
		t.Fatalf("profile selected unapproved account %q", selected.ID)
	}
	if _, err := pool.SelectAccountFrom(ProviderOpenAI, []string{"missing"}); err == nil {
		t.Fatal("profile with no eligible account should fail closed")
	}
}

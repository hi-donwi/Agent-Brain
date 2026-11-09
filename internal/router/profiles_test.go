package router

import (
	"strings"
	"testing"
)

func testProfile() RoutingProfile {
	return RoutingProfile{
		ID: "agent-control",
		Targets: []RoutingTarget{{
			Model:         "assistant",
			Provider:      ProviderOpenAI,
			UpstreamModel: "gpt-test",
			AccountIDs:    []string{"account-a"},
			Protocols:     []string{ProtocolChat},
		}},
	}
}

func TestValidateRoutingProfilesRequiresExplicitCompatibleTargets(t *testing.T) {
	accounts := []Account{{ID: "account-a", Provider: ProviderOpenAI, Enabled: true, Protocols: []string{ProtocolChat}}}
	tests := []struct {
		name    string
		profile RoutingProfile
	}{
		{name: "missing targets", profile: RoutingProfile{ID: "agent-control"}},
		{name: "unsafe model id", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "bad\r\nmodel", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{ProtocolChat}}}}},
		{name: "missing account allowlist", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "assistant", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", Protocols: []string{ProtocolChat}}}}},
		{name: "unknown account", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "assistant", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"missing"}, Protocols: []string{ProtocolChat}}}}},
		{name: "provider mismatch", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "assistant", Provider: ProviderAnthropic, UpstreamModel: "claude-test", AccountIDs: []string{"account-a"}, Protocols: []string{ProtocolMessages}}}}},
		{name: "unsupported protocol", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "assistant", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{ProtocolMessages}}}}},
		{name: "duplicate model", profile: RoutingProfile{ID: "agent-control", Targets: []RoutingTarget{{Model: "assistant", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{ProtocolChat}}, {Model: "assistant", Provider: ProviderOpenAI, UpstreamModel: "gpt-test", AccountIDs: []string{"account-a"}, Protocols: []string{ProtocolChat}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateRoutingProfiles([]RoutingProfile{tt.profile}, accounts); err == nil {
				t.Fatal("expected invalid profile to be rejected")
			}
		})
	}
}

func TestRoutingProfileRevisionBindsResolvedTargetButNotCredential(t *testing.T) {
	profile := testProfile()
	account := Account{ID: "account-a", Provider: ProviderOpenAI, BaseURL: "https://one.example.invalid/v1", APIKey: "secret-one", Enabled: true, Protocols: []string{ProtocolChat}}
	one, err := RoutingProfileRevision(profile, []Account{account})
	if err != nil {
		t.Fatal(err)
	}

	account.APIKey = "secret-two"
	two, err := RoutingProfileRevision(profile, []Account{account})
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatal("credential rotation must not change a routing profile revision")
	}

	account.BaseURL = "https://two.example.invalid/v1"
	three, err := RoutingProfileRevision(profile, []Account{account})
	if err != nil {
		t.Fatal(err)
	}
	if one == three || !strings.HasPrefix(three, "sha256:") {
		t.Fatalf("resolved endpoint changes must create a SHA-256 revision: %q, %q", one, three)
	}
	account.BaseURL = "https://one.example.invalid/v1"
	account.Provider = ProviderAnthropic
	four, err := RoutingProfileRevision(profile, []Account{account})
	if err != nil {
		t.Fatal(err)
	}
	if one == four {
		t.Fatal("provider changes must create a new profile revision")
	}
}

func TestRoutingProfileRevisionIsStableAcrossAccountOrder(t *testing.T) {
	profile := testProfile()
	accounts := []Account{
		{ID: "account-b", Provider: ProviderOpenAI, BaseURL: "https://b.example.invalid/v1", Enabled: true, Protocols: []string{ProtocolChat}},
		{ID: "account-a", Provider: ProviderOpenAI, BaseURL: "https://a.example.invalid/v1", Enabled: true, Protocols: []string{ProtocolChat}},
	}
	profile.Targets[0].AccountIDs = []string{"account-a", "account-b"}
	one, err := RoutingProfileRevision(profile, accounts)
	if err != nil {
		t.Fatal(err)
	}
	two, err := RoutingProfileRevision(profile, []Account{accounts[1], accounts[0]})
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("account configuration order must not affect a profile revision: %q != %q", one, two)
	}
}

func TestValidateRoutingProfilesHonorsAccountModelRestrictions(t *testing.T) {
	profile := testProfile()
	accounts := []Account{{ID: "account-a", Provider: ProviderOpenAI, Enabled: true, Protocols: []string{ProtocolChat}, Models: []string{"another-model"}}}
	if err := ValidateRoutingProfiles([]RoutingProfile{profile}, accounts); err == nil {
		t.Fatal("profile target outside the account model allowlist should be rejected")
	}
}

func TestLocalRoutingProfileRevisionBindsLoopbackBackendAndRejectsRemoteEndpoint(t *testing.T) {
	profile := RoutingProfile{ID: "local", Targets: []RoutingTarget{{
		Model: "assistant", Provider: ProviderLocal, UpstreamModel: "qwen-test", Protocols: []string{ProtocolChat},
	}}}
	one, err := RoutingProfileRevisionFor(profile, nil, "ollama", "http://127.0.0.1:11434")
	if err != nil {
		t.Fatal(err)
	}
	two, err := RoutingProfileRevisionFor(profile, nil, "ollama", "http://127.0.0.1:11435")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("local backend changes must create a new profile revision")
	}
	if _, err := RoutingProfileRevisionFor(profile, nil, "ollama", "https://remote.example.invalid"); err == nil {
		t.Fatal("local profile must reject a non-loopback backend")
	}
}

func TestValidateRoutingProfilesRejectsGeminiPathDelimiters(t *testing.T) {
	account := Account{ID: "gemini-account", Provider: ProviderGemini, Enabled: true, Protocols: []string{ProtocolGemini}}
	for _, target := range []RoutingTarget{
		{Model: "gemini:preview", Provider: ProviderGemini, UpstreamModel: "gemini-test", AccountIDs: []string{account.ID}, Protocols: []string{ProtocolGemini}},
		{Model: "assistant", Provider: ProviderGemini, UpstreamModel: "gemini:preview", AccountIDs: []string{account.ID}, Protocols: []string{ProtocolGemini}},
	} {
		if err := ValidateRoutingProfiles([]RoutingProfile{{ID: "gemini", Targets: []RoutingTarget{target}}}, []Account{account}); err == nil {
			t.Fatalf("Gemini target path delimiter was accepted: %#v", target)
		}
	}
}

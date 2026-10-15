package cli

import (
	"encoding/json"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"strings"
	"testing"
)

func TestOpenCodeLaunchUsesExplicitProviderAndScopedCredential(t *testing.T) {
	a := router.Account{ID: "one", Name: "work", Provider: router.ProviderOpenAI, APIKey: "fake-upstream", Models: []string{"gpt-test"}}
	plan, err := buildLaunchPlan("opencode", a, t.TempDir(), "http://127.0.0.1:52140", "scoped-token", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	for _, entry := range plan.Env {
		if value, ok := strings.CutPrefix(entry, "OPENCODE_CONFIG_CONTENT="); ok {
			if err := json.Unmarshal([]byte(value), &config); err != nil {
				t.Fatal(err)
			}
		}
	}
	if config["model"] != "brain/openai/gpt-test" || !strings.Contains(strings.Join(plan.Env, "\n"), "BRAIN_CLIENT_KEY=scoped-token") || strings.Contains(strings.Join(plan.Env, "\n"), "fake-upstream") {
		t.Fatalf("invalid routed client configuration: %v", config)
	}
}

func TestCodexLaunchSelectsResponsesProvider(t *testing.T) {
	a := router.Account{ID: "one", Provider: router.ProviderOpenAI, Models: []string{"gpt-test"}}
	plan, err := buildLaunchPlan("codex", a, t.TempDir(), "http://127.0.0.1:52140", "scoped-token", false, []string{"--model", "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, `wire_api="responses"`) || !strings.Contains(joined, `requires_openai_auth=false`) {
		t.Fatal("Codex gateway provider was not configured explicitly")
	}
}

func TestIncompatibleClientCannotUseSelectedAccount(t *testing.T) {
	a := router.Account{ID: "one", Provider: router.ProviderAnthropic, APIKey: "fake"}
	if _, err := buildLaunchPlan("codex", a, t.TempDir(), "http://127.0.0.1:52140", "scoped", false, nil); err == nil {
		t.Fatal("Responses client was allowed to use a Messages-only account")
	}
}

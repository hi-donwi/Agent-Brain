package snippets_test

import (
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/snippets"
)

func TestSnippets_All(t *testing.T) {
	p := snippets.Params{
		BaseURL:    "http://127.0.0.1:52140/v1",
		Token:      "secret-token-123456",
		ChatModel:  "chat",
		CodeModel:  "code",
		EmbedModel: "embed",
	}

	all := snippets.All(p)
	if len(all) != 7 {
		t.Fatalf("expected 7 snippets, got %d", len(all))
	}

	for _, s := range all {
		if s.ID == "" || s.Name == "" || s.Code == "" {
			t.Errorf("snippet missing required fields: %+v", s)
		}
		if !strings.Contains(s.Code, "52140") {
			t.Errorf("snippet %s does not contain base URL: %s", s.ID, s.Code)
		}
		if !strings.Contains(s.Code, "secret-token-123456") {
			t.Errorf("snippet %s does not contain token: %s", s.ID, s.Code)
		}
	}
}

func TestSnippets_Defaults(t *testing.T) {
	p := snippets.Params{
		Token: "my-token",
	}
	all := snippets.All(p)
	if len(all) == 0 {
		t.Fatalf("expected snippets with defaults")
	}

	agentControl := all[0]
	if !strings.Contains(agentControl.Code, "http://127.0.0.1:52140/v1") {
		t.Errorf("expected default base URL in snippet")
	}
	if !strings.Contains(agentControl.Code, `"chat": "chat"`) {
		t.Errorf("expected default chat role in snippet")
	}
}

func TestSnippets_FormatText(t *testing.T) {
	p := snippets.Params{
		Token: "test-token",
	}
	all := snippets.All(p)
	formatted := snippets.FormatText(all)

	if !strings.Contains(formatted, "[Agent-Control]") {
		t.Errorf("expected [Agent-Control] in formatted text")
	}
	if !strings.Contains(formatted, "[OpenAI Python SDK]") {
		t.Errorf("expected [OpenAI Python SDK] in formatted text")
	}
	if !strings.Contains(formatted, "------------------------------------------------------------") {
		t.Errorf("expected section divider in formatted text")
	}
}

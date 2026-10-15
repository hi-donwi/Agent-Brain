package router

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	AuthAPIKey         = "api-key"
	AuthNative         = "native"
	AuthOAuth          = "oauth"
	AuthNone           = "none"
	ProtocolChat       = "chat"
	ProtocolResponses  = "responses"
	ProtocolMessages   = "messages"
	ProtocolGemini     = "gemini"
	ProtocolEmbeddings = "embeddings"
)

// Provider declares authentication and wire capabilities independently of model vendors.
type Provider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Auth      []string `json:"auth"`
	Protocols []string `json:"protocols"`
	BaseURL   string   `json:"base_url,omitempty"`
	KeyURL    string   `json:"key_url,omitempty"`
	Tool      string   `json:"tool,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// Providers returns the supported integrations and their explicit prerequisites.
func Providers() []Provider {
	return []Provider{
		{ProviderAntigravity, "Antigravity", []string{AuthOAuth}, []string{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGemini}, "", "", "agy", "Requires a dedicated local CLIProxyAPI v8 endpoint per account; native Google session isolation is unverified"},
		{ProviderGemini, "Google AI Studio", []string{AuthAPIKey}, []string{ProtocolChat, ProtocolGemini, ProtocolEmbeddings}, "https://generativelanguage.googleapis.com", "https://aistudio.google.com/apikey", "", "Gemini Developer API keys; separate from Antigravity sign-in"},
		{ProviderClaudeCode, "Claude Code subscription", []string{AuthNative}, nil, "", "", "claude", "Official CLI only; use Anthropic API for gateway inference"},
		{ProviderCodex, "Codex / ChatGPT", []string{AuthNative, AuthOAuth}, []string{ProtocolChat, ProtocolResponses}, "", "", "codex", "Native CLI or dedicated local OAuth bridge; ChatGPT plan access is separate from OpenAI API billing"},
		{ProviderOpenAI, "OpenAI API", []string{AuthAPIKey}, []string{ProtocolChat, ProtocolResponses, ProtocolEmbeddings}, "https://api.openai.com", "https://platform.openai.com/api-keys", "", ""},
		{ProviderAnthropic, "Anthropic API", []string{AuthAPIKey}, []string{ProtocolMessages}, "https://api.anthropic.com", "https://platform.claude.com/settings/keys", "", ""},
		{"cursor", "Cursor", []string{AuthAPIKey, AuthNative}, nil, "", "https://cursor.com/dashboard", "cursor-agent", "Per-account Cursor user API keys; browser login supports only the default native session, not isolated gateway accounts"},
		{"opencode", "OpenCode client", nil, nil, "", "", "opencode", "Choose an underlying provider account; Brain writes an explicit custom-provider configuration"},
		{"opencode-zen", "OpenCode service", []string{AuthAPIKey}, []string{ProtocolChat, ProtocolMessages}, "", "https://opencode.ai/auth", "", "Supply the service endpoint and supported protocols explicitly"},
		{ProviderDeepSeek, "DeepSeek", []string{AuthAPIKey}, []string{ProtocolChat}, "https://api.deepseek.com", "https://platform.deepseek.com/api_keys", "", ""},
		{ProviderOpenRouter, "OpenRouter", []string{AuthAPIKey}, []string{ProtocolChat}, "https://openrouter.ai/api", "https://openrouter.ai/keys", "", ""},
		{ProviderLocal, "Local AI endpoint", []string{AuthNone, AuthAPIKey}, []string{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGemini, ProtocolEmbeddings}, "http://127.0.0.1:11434", "", "", "OpenAI-compatible local servers; native backend orchestration remains available through setup/models"},
		{"custom", "Custom compatible provider", []string{AuthAPIKey, AuthNone}, []string{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGemini, ProtocolEmbeddings}, "", "", "", "Requires an explicit endpoint and protocol list"},
	}
}

func NormalizeProvider(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "gemini" || id == "ai-studio" {
		return ProviderGemini
	}
	if id == "claude" {
		return ProviderClaudeCode
	}
	if id == "chatgpt" {
		return ProviderCodex
	}
	return id
}

func LookupProvider(id string) (Provider, error) {
	id = NormalizeProvider(id)
	for _, p := range Providers() {
		if p.ID == id {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("unsupported provider %q; use brain providers list", id)
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// ValidateEndpoint accepts HTTPS services and explicit loopback HTTP endpoints only.
func ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("endpoint must be an absolute URL without credentials, query, or fragment")
	}
	host := u.Hostname()
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return fmt.Errorf("endpoint must use HTTPS or loopback HTTP")
	}
	return nil
}

func ValidateLoopbackEndpoint(raw string) error {
	if err := ValidateEndpoint(raw); err != nil {
		return err
	}
	u, _ := url.Parse(raw)
	if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return fmt.Errorf("OAuth bridge must be on loopback")
	}
	return nil
}

func (a Account) Supports(protocol string) bool {
	if a.Auth == AuthNative {
		return false
	}
	if len(a.Protocols) > 0 {
		return contains(a.Protocols, protocol)
	}
	p, err := LookupProvider(a.Provider)
	return err == nil && contains(p.Protocols, protocol)
}

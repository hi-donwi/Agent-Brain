package snippets

import (
	"fmt"
	"strings"
)

// Params contains parameters required to render client snippets.
type Params struct {
	BaseURL    string // e.g. "http://127.0.0.1:52140/v1"
	Token      string // bearer token
	ChatModel  string // default chat role / model, e.g. "chat"
	CodeModel  string // default code role / model, e.g. "code"
	EmbedModel string // default embed role / model, e.g. "embed"
}

// ClientSnippet holds metadata and formatted code for one client integration.
type ClientSnippet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Lang        string `json:"lang"`
	Code        string `json:"code"`
}

// WithDefaults fills missing parameters with standard lokal defaults.
func (p Params) WithDefaults() Params {
	if p.BaseURL == "" {
		p.BaseURL = "http://127.0.0.1:52140/v1"
	}
	if p.ChatModel == "" {
		p.ChatModel = "chat"
	}
	if p.CodeModel == "" {
		p.CodeModel = "code"
	}
	if p.EmbedModel == "" {
		p.EmbedModel = "embed"
	}
	return p
}

// All generates snippets for all supported client tools and SDKs.
func All(p Params) []ClientSnippet {
	p = p.WithDefaults()
	return []ClientSnippet{
		AgentControl(p),
		ClaudeCode(p),
		Opencode(p),
		Continue(p),
		Python(p),
		TypeScript(p),
		CURL(p),
	}
}

// AgentControl returns configuration for Agent-Control.
func AgentControl(p Params) ClientSnippet {
	code := fmt.Sprintf(`// Agent-Control provider configuration
{
  "provider": "lokal",
  "baseURL": "%s",
  "apiKey": "%s",
  "models": {
    "chat": "%s",
    "code": "%s",
    "embed": "%s"
  }
}`, p.BaseURL, p.Token, p.ChatModel, p.CodeModel, p.EmbedModel)
	return ClientSnippet{
		ID:          "agent-control",
		Name:        "Agent-Control",
		Description: "Configure Agent-Control to connect to lokal serve",
		Lang:        "json",
		Code:        code,
	}
}

// ClaudeCode returns shell environment configuration for Claude Code proxies.
func ClaudeCode(p Params) ClientSnippet {
	code := fmt.Sprintf(`# Environment configuration for Claude Code compatible tools
export ANTHROPIC_BASE_URL="%s"
export ANTHROPIC_API_KEY="%s"
export ANTHROPIC_MODEL="%s"`, p.BaseURL, p.Token, p.ChatModel)
	return ClientSnippet{
		ID:          "claude-code",
		Name:        "Claude Code Compatible",
		Description: "Shell environment variables for Claude Code compatible proxies",
		Lang:        "bash",
		Code:        code,
	}
}

// Opencode returns configuration entry for opencode.
func Opencode(p Params) ClientSnippet {
	code := fmt.Sprintf(`// opencode.json snippet
{
  "providers": [
    {
      "name": "lokal",
      "type": "openai",
      "baseURL": "%s",
      "apiKey": "%s",
      "models": ["%s", "%s"]
    }
  ]
}`, p.BaseURL, p.Token, p.ChatModel, p.CodeModel)
	return ClientSnippet{
		ID:          "opencode",
		Name:        "opencode",
		Description: "Configuration entry for opencode",
		Lang:        "json",
		Code:        code,
	}
}

// Continue returns configuration snippet for Continue extension.
func Continue(p Params) ClientSnippet {
	code := fmt.Sprintf(`// Continue ~/.continue/config.json models block
{
  "models": [
    {
      "title": "lokal (%s)",
      "provider": "openai",
      "model": "%s",
      "apiBase": "%s",
      "apiKey": "%s"
    }
  ],
  "tabAutocompleteModel": {
    "title": "lokal (%s)",
    "provider": "openai",
    "model": "%s",
    "apiBase": "%s",
    "apiKey": "%s"
  }
}`, p.ChatModel, p.ChatModel, p.BaseURL, p.Token, p.CodeModel, p.CodeModel, p.BaseURL, p.Token)
	return ClientSnippet{
		ID:          "continue",
		Name:        "Continue",
		Description: "VS Code / JetBrains Continue extension configuration",
		Lang:        "json",
		Code:        code,
	}
}

// Python returns standard OpenAI Python SDK snippet.
func Python(p Params) ClientSnippet {
	code := fmt.Sprintf(`from openai import OpenAI

client = OpenAI(
    base_url="%s",
    api_key="%s",
)

response = client.chat.completions.create(
    model="%s",
    messages=[{"role": "user", "content": "Hello lokal!"}],
)
print(response.choices[0].message.content)`, p.BaseURL, p.Token, p.ChatModel)
	return ClientSnippet{
		ID:          "python",
		Name:        "OpenAI Python SDK",
		Description: "Official Python openai package client",
		Lang:        "python",
		Code:        code,
	}
}

// TypeScript returns standard OpenAI Node/TypeScript SDK snippet.
func TypeScript(p Params) ClientSnippet {
	code := fmt.Sprintf(`import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "%s",
  apiKey: "%s",
});

const response = await client.chat.completions.create({
  model: "%s",
  messages: [{ role: "user", content: "Hello lokal!" }],
});
console.log(response.choices[0].message.content);`, p.BaseURL, p.Token, p.ChatModel)
	return ClientSnippet{
		ID:          "typescript",
		Name:        "OpenAI TypeScript / Node SDK",
		Description: "Official Node/TypeScript openai package client",
		Lang:        "typescript",
		Code:        code,
	}
}

// CURL returns terminal cURL test snippet.
func CURL(p Params) ClientSnippet {
	code := fmt.Sprintf(`curl -s %s/chat/completions \
  -H "Authorization: Bearer %s" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "%s",
    "messages": [{"role": "user", "content": "ping"}]
  }'`, p.BaseURL, p.Token, p.ChatModel)
	return ClientSnippet{
		ID:          "curl",
		Name:        "cURL Test",
		Description: "Direct terminal cURL completion test",
		Lang:        "bash",
		Code:        code,
	}
}

// FormatText formats a list of client snippets into human-readable text.
func FormatText(list []ClientSnippet) string {
	var sb strings.Builder
	for i, s := range list {
		if i > 0 {
			sb.WriteString("\n------------------------------------------------------------\n\n")
		}
		sb.WriteString(fmt.Sprintf("[%s] - %s\n", s.Name, s.Description))
		sb.WriteString(s.Code)
		sb.WriteString("\n")
	}
	return sb.String()
}

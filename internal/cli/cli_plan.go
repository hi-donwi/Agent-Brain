package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

type launchPlan struct{ Args, Env []string }

func buildLaunchPlan(tool string, a router.Account, root, gatewayURL, token string, direct bool, args []string) (launchPlan, error) {
	baseEnv, err := accountEnvironment(root)
	if err != nil {
		return launchPlan{}, err
	}
	plan := launchPlan{Args: args, Env: baseEnv}
	dir := profileDirectory(root, a)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return plan, err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return plan, fmt.Errorf("profile directory is not a regular private directory")
	}
	plan.Env = mergeProcessEnv(plan.Env, getToolProfileEnv(tool, a.Name, dir))
	if a.Auth == router.AuthNative {
		p, _ := router.LookupProvider(a.Provider)
		if p.Tool != tool {
			return plan, fmt.Errorf("native account belongs to %s, not %s", p.Tool, tool)
		}
		if !a.CLIReady {
			return plan, fmt.Errorf("native account is not connected; run brain accounts login %s", a.Reference())
		}
		if tool == "codex" {
			plan.Args = append([]string{"-c", `cli_auth_credentials_store="file"`}, args...)
		}
		return plan, nil
	}
	if tool == "cursor-agent" {
		if a.Provider != "cursor" {
			return plan, fmt.Errorf("Cursor CLI requires its own Cursor account credential")
		}
		key, err := router.ResolveCredential(a.APIKey)
		if err != nil || key == "" {
			return plan, fmt.Errorf("Cursor credential unavailable")
		}
		plan.Env = mergeProcessEnv(plan.Env, []string{"CURSOR_API_KEY=" + key})
		return plan, nil
	}
	endpoint := gatewayURL
	key := token
	if direct {
		p, err := router.LookupProvider(a.Provider)
		if err != nil {
			return plan, err
		}
		endpoint = a.BaseURL
		if endpoint == "" {
			endpoint = p.BaseURL
		}
		if err := router.ValidateEndpoint(endpoint); err != nil {
			return plan, err
		}
		key, err = router.ResolveCredential(a.APIKey)
		if err != nil {
			return plan, err
		}
	}
	base := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	model := ""
	if len(a.Models) > 0 {
		model = a.Models[0]
	}
	qualified := model
	if !direct && model != "" {
		qualified = a.Provider + "/" + model
	}
	plan.Env = mergeProcessEnv(plan.Env, []string{"BRAIN_CLIENT_KEY=" + key})
	switch tool {
	case "claude":
		if !a.Supports(router.ProtocolMessages) {
			return plan, fmt.Errorf("selected account does not support the Anthropic Messages protocol")
		}
		plan.Env = mergeProcessEnv(plan.Env, []string{"ANTHROPIC_BASE_URL=" + strings.TrimSuffix(base, "/v1"), "ANTHROPIC_API_KEY=" + key})
	case "codex":
		if !a.Supports(router.ProtocolResponses) {
			return plan, fmt.Errorf("selected account does not support Responses")
		}
		overrides := []string{"-c", `model_provider="brain"`, "-c", `model_providers.brain.name="Brain"`, "-c", "model_providers.brain.base_url=" + strconv.Quote(base), "-c", `model_providers.brain.env_key="BRAIN_CLIENT_KEY"`, "-c", `model_providers.brain.wire_api="responses"`, "-c", `model_providers.brain.requires_openai_auth=false`, "-c", `model_providers.brain.supports_websockets=false`}
		if model != "" && !toolHasModel(args) {
			overrides = append(overrides, "-c", "model="+strconv.Quote(qualified))
		}
		plan.Args = append(overrides, args...)
	case "opencode":
		npm := "@ai-sdk/openai-compatible"
		if !a.Supports(router.ProtocolChat) {
			if a.Supports(router.ProtocolMessages) {
				npm = "@ai-sdk/anthropic"
			} else {
				return plan, fmt.Errorf("OpenCode account must support chat or Messages")
			}
		}
		if model == "" {
			return plan, fmt.Errorf("OpenCode requires model metadata; run brain accounts test %s or add --models", a.Reference())
		}
		if direct && a.Provider == router.ProviderGemini {
			base = strings.TrimRight(endpoint, "/") + "/v1beta/openai"
		}
		models := make(map[string]any)
		for _, name := range a.Models {
			id := name
			if !direct {
				id = a.Provider + "/" + name
			}
			models[id] = map[string]string{"name": name, "id": id}
		}
		config := map[string]any{"enabled_providers": []string{"brain"}, "provider": map[string]any{"brain": map[string]any{"npm": npm, "name": "Brain", "options": map[string]string{"baseURL": base, "apiKey": "{env:BRAIN_CLIENT_KEY}"}, "models": models}}, "model": "brain/" + qualified}
		data, _ := json.Marshal(config)
		plan.Env = mergeProcessEnv(plan.Env, []string{"OPENCODE_CONFIG_CONTENT=" + string(data)})
	case "aider":
		if !a.Supports(router.ProtocolChat) {
			return plan, fmt.Errorf("Aider requires a chat-compatible account")
		}
		if direct && a.Provider == router.ProviderGemini {
			base = strings.TrimRight(endpoint, "/") + "/v1beta/openai"
		}
		plan.Env = mergeProcessEnv(plan.Env, []string{"OPENAI_API_BASE=" + base, "OPENAI_API_KEY=" + key})
	case "agy":
		if !a.Supports(router.ProtocolGemini) {
			return plan, fmt.Errorf("Antigravity API mode needs a Gemini-compatible account endpoint")
		}
		userDir, err := os.UserHomeDir()
		if err != nil {
			return plan, err
		}
		data, err := os.ReadFile(filepath.Join(userDir, ".gemini", "antigravity-cli", "settings.json"))
		var settings struct {
			ModelProvider string `json:"modelProvider"`
		}
		_ = json.Unmarshal(data, &settings)
		if err != nil || settings.ModelProvider != "gemini" {
			return plan, fmt.Errorf("enable the documented Antigravity API mode first: set modelProvider to gemini in its settings.json; native Google session switching is not verified")
		}
		plan.Env = mergeProcessEnv(plan.Env, []string{"GEMINI_API_KEY=" + key, "GOOGLE_GEMINI_BASE_URL=" + endpoint})
	default:
		return plan, fmt.Errorf("unsupported CLI tool %q", tool)
	}
	return plan, nil
}

func getToolProfileEnv(tool, account, dir string) []string {
	switch tool {
	case "claude":
		return []string{"CLAUDE_CONFIG_DIR=" + dir}
	case "codex":
		return []string{"CODEX_HOME=" + dir}
	case "opencode":
		return []string{"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_STATE_HOME=" + filepath.Join(dir, "state")}
	}
	return nil
}

func toolHasModel(args []string) bool {
	for _, arg := range args {
		if arg == "--model" || arg == "-m" || strings.HasPrefix(arg, "--model=") {
			return true
		}
	}
	return false
}

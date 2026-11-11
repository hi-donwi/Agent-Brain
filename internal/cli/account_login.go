package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/oauthbridge"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func profileDirectory(root string, a router.Account) string {
	sum := sha256.Sum256([]byte(a.ID))
	return filepath.Join(root, "profiles", a.Provider, hex.EncodeToString(sum[:16]))
}

func connectAccount(cmd *cobra.Command, deps AccountDeps, s router.AccountService, a router.Account, noBrowser bool) error {
	var updated router.Account
	var err error
	if deps.Login != nil {
		updated, err = deps.Login(cmd, a, noBrowser)
	} else {
		updated, err = loginAccount(cmd, a, noBrowser, filepath.Dir(s.Path))
	}
	if err != nil {
		_ = s.Update(cmd.Context(), a.ID, func(current *router.Account) error {
			current.Status = router.StatusReauth
			if updated.BridgeCredential != "" {
				current.BridgeCredential = updated.BridgeCredential
				current.Status = router.StatusConfigured
			}
			current.CLIReady = false
			current.GatewayReady = false
			return nil
		})
		return err
	}
	return s.Update(cmd.Context(), a.ID, func(current *router.Account) error {
		current.Status = updated.Status
		current.CLIReady = updated.CLIReady
		current.GatewayReady = updated.GatewayReady
		current.BridgeCredential = updated.BridgeCredential
		current.DiscoveredModels = updated.DiscoveredModels
		if len(updated.Models) > 0 {
			current.Models = updated.Models
		}
		return nil
	})
}

func newAccountLoginCmd(deps AccountDeps) *cobra.Command {
	var noBrowser, managed bool
	cmd := &cobra.Command{Use: "login <provider/name-or-id>", Short: "Connect a pending native account or start its OAuth login", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := accountService()
		if err != nil {
			return err
		}
		a, err := s.Find(args[0])
		if err != nil {
			return err
		}
		if managed {
			a, err = s.EnableManaged(cmd.Context(), a.ID)
			if err != nil {
				return err
			}
		}
		return connectAccount(cmd, deps, s, a, noBrowser)
	}}
	cmd.Flags().BoolVar(&managed, "managed-bridge", false, "Replace an external OAuth binding with automatic bridge setup")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print a login URL or use native device authentication")
	return cmd
}

func loginAccount(cmd *cobra.Command, a router.Account, noBrowser bool, root string) (router.Account, error) {
	if a.Auth == router.AuthOAuth {
		stop, err := (oauthbridge.Manager{Root: root, Output: cmd.ErrOrStderr()}).Use(cmd.Context(), a)
		if err != nil {
			return a, err
		}
		defer stop()
		bridge := router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey}
		var id string
		if a.BridgeCredential != "" {
			id, err = bridge.Verify(cmd.Context(), a.Provider, a.BridgeCredential)
		} else {
			id, err = bridge.Login(cmd.Context(), a.Provider, func(url string) error {
				fmt.Fprintln(cmd.ErrOrStderr(), "Login URL:", url)
				if !noBrowser {
					if err := openBrowser(cmd.Context(), url); err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), "Open the printed URL manually to continue.")
					}
				}
				return nil
			})
		}
		if err != nil {
			return a, err
		}
		a.BridgeCredential = id
		a.GatewayReady = false
		a.Status = router.StatusConfigured
		fmt.Fprintln(cmd.ErrOrStderr(), "Signed in. Discovering the models available to this account...")
		verified, err := checkAccount(cmd.Context(), a, root, false, "", cmd.ErrOrStderr())
		if err != nil {
			return a, err
		}
		a = verified
		fmt.Fprintln(cmd.OutOrStdout(), "OAuth identity verified. Gateway ready; native CLI session isolation is a separate capability.")
		fmt.Fprintf(cmd.OutOrStdout(), "Available models for %s (%d):\n", a.Reference(), len(a.DiscoveredModels))
		for _, model := range a.DiscoveredModels {
			fmt.Fprintln(cmd.OutOrStdout(), "  "+model)
		}

		return a, nil
	}
	if a.Auth != router.AuthNative {
		return checkAccount(cmd.Context(), a, root, false, "", cmd.ErrOrStderr())
	}
	tool, args, env, err := nativeCommand(a, root, noBrowser, false)
	if err != nil {
		return a, err
	}
	child := exec.CommandContext(cmd.Context(), tool, args...)
	child.Env = env
	child.Stdin = cmd.InOrStdin()
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()
	if err := child.Run(); err != nil {
		return a, fmt.Errorf("native login did not complete; retry accounts login: %w", err)
	}
	return checkAccount(cmd.Context(), a, root, false, "", cmd.ErrOrStderr())
}

func nativeCommand(a router.Account, root string, noBrowser, status bool) (string, []string, []string, error) {
	p, err := router.LookupProvider(a.Provider)
	if err != nil {
		return "", nil, nil, err
	}
	tool, err := exec.LookPath(p.Tool)
	if err != nil {
		return "", nil, nil, fmt.Errorf("install the official %s CLI before connecting this account", p.Tool)
	}
	dir := profileDirectory(root, a)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, nil, err
	}
	env, err := accountEnvironment(root)
	if err != nil {
		return "", nil, nil, err
	}
	var args []string
	switch a.Provider {
	case router.ProviderClaudeCode:
		if noBrowser && !status {
			return "", nil, nil, fmt.Errorf("this Claude integration delegates browser handling to the official CLI; omit --no-browser and use its displayed login URL when needed")
		}
		env = mergeProcessEnv(env, []string{"CLAUDE_CONFIG_DIR=" + dir})
		args = []string{"auth", "login"}
		if status {
			args = []string{"auth", "status"}
		}
	case router.ProviderCodex:
		env = mergeProcessEnv(env, []string{"CODEX_HOME=" + dir})
		args = []string{"-c", `cli_auth_credentials_store="file"`, "login"}
		if status {
			args = append(args, "status")
		} else if noBrowser {
			args = append(args, "--device-auth")
		}
	case "cursor":
		if a.Auth == router.AuthNative && a.Name != "default" {
			return "", nil, nil, fmt.Errorf("Cursor browser login supports only the default native session; use separate Cursor API keys for multiple accounts")
		}
		args = []string{"login"}
		if noBrowser {
			env = mergeProcessEnv(env, []string{"NO_OPEN_BROWSER=1"})
		}
		if status {
			args = []string{"status"}
		}
	default:
		return "", nil, nil, fmt.Errorf("native account isolation is not verified for %s", a.Provider)
	}
	return tool, args, env, nil
}

var credentialEnvKeys = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "CURSOR_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE", "GOOGLE_GEMINI_BASE_URL", "GEMINI_API_BASE", "OPENCODE_CONFIG_CONTENT", "BRAIN_CLIENT_KEY"}

func mergeProcessEnv(base, overrides []string, remove ...string) []string {
	values := make(map[string]string)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			canonical := strings.ToUpper(key)
			values[canonical] = entry
		}
	}
	for _, key := range remove {
		delete(values, strings.ToUpper(key))
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.ToUpper(key)] = entry
		}
	}
	result := make([]string, 0, len(values))
	for _, entry := range values {
		result = append(result, entry)
	}
	return result
}

func accountEnvironment(root string) ([]string, error) {
	remove := append([]string(nil), credentialEnvKeys...)
	remove = append(remove, "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_PROFILE", "ANTHROPIC_MODEL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_FOUNDRY_BASE_URL")
	cfg, err := router.LoadRouterConfig(filepath.Join(root, "accounts.yaml"))
	if err != nil {
		return nil, err
	}
	for _, a := range cfg.Accounts {
		for _, ref := range []string{a.APIKey, a.BridgeKey} {
			if name, ok := strings.CutPrefix(ref, "env:"); ok {
				remove = append(remove, name)
			}
		}
	}
	return mergeProcessEnv(os.Environ(), nil, remove...), nil
}

func openBrowser(ctx context.Context, url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{url}
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		command = "xdg-open"
		args = []string{url}
	}
	return exec.CommandContext(ctx, command, args...).Run()
}

func newAccountTestCmd() *cobra.Command {
	var probe bool
	var model string
	cmd := &cobra.Command{Use: "test <provider/name-or-id>", Short: "Verify credentials and discover models; no inference unless --probe is supplied", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := accountService()
		if err != nil {
			return err
		}
		a, err := s.Find(args[0])
		if err != nil {
			return err
		}
		updated, err := checkAccount(cmd.Context(), a, filepath.Dir(s.Path), probe, model, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if err = s.Update(cmd.Context(), a.ID, func(current *router.Account) error {
			current.Status = updated.Status
			current.CLIReady = updated.CLIReady
			current.GatewayReady = updated.GatewayReady
			current.BridgeCredential = updated.BridgeCredential
			current.DiscoveredModels = updated.DiscoveredModels
			current.Models = updated.Models
			return nil
		}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Verified %s: CLI=%t gateway=%t models=%d\n", a.Reference(), updated.CLIReady, updated.GatewayReady, len(updated.Models))
		fmt.Fprintln(cmd.ErrOrStderr(), "Restart a running gateway after account changes.")
		return nil
	}}
	cmd.Flags().BoolVar(&probe, "probe", false, "Send an explicit minimal inference request (may incur usage)")
	cmd.Flags().StringVar(&model, "model", "", "Model for the optional inference probe")
	return cmd
}

func checkAccount(ctx context.Context, a router.Account, root string, probe bool, model string, progress io.Writer) (router.Account, error) {
	stop, err := (oauthbridge.Manager{Root: root}).Use(ctx, a)
	if err != nil {
		return a, err
	}
	defer stop()
	if len(a.DiscoveredModels) > 0 && slices.Equal(a.Models, a.DiscoveredModels) {
		a.Models = nil
	}

	if a.Auth == router.AuthNative || a.Provider == "cursor" {
		tool, args, env, err := nativeCommand(a, root, false, true)
		if err != nil {
			return a, err
		}
		if a.Provider == "cursor" && a.Auth != router.AuthNative {
			key, err := router.ResolveCredential(a.APIKey)
			if err != nil || key == "" {
				return a, fmt.Errorf("Cursor credential unavailable")
			}
			env = mergeProcessEnv(env, []string{"CURSOR_API_KEY=" + key})
		}
		command := exec.CommandContext(ctx, tool, args...)
		command.Env = env
		data, err := command.CombinedOutput()
		if err != nil || !nativeAuthenticated(data) {
			return a, fmt.Errorf("native account is not authenticated; run accounts login")
		}
		a.CLIReady = true
		a.GatewayReady = false
		a.Status = router.StatusActive
		return a, nil
	}
	if a.Auth == router.AuthOAuth {
		bridge := router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey}
		id, err := bridge.Verify(ctx, a.Provider, a.BridgeCredential)
		if err != nil {
			return a, err
		}
		a.BridgeCredential = id
		models, err := bridge.WaitModels(ctx, a.Provider, id, func() {
			if progress != nil {
				fmt.Fprintln(progress, "Waiting for the account's model catalog to register; refreshing automatically for up to 60 seconds...")
			}
		})
		if err != nil {
			return a, err
		}
		a.DiscoveredModels = models
		if len(a.Models) == 0 {
			a.Models = append([]string(nil), models...)
		}
	} else {
		discovered, err := router.DiscoverModels(ctx, a)
		if err != nil {
			return a, err
		}
		a.DiscoveredModels = discovered
		if len(a.Models) == 0 {
			a.Models = append([]string(nil), discovered...)
		}
	}
	if probe {
		if model == "" && len(a.Models) > 0 {
			model = a.Models[0]
		}
		if model == "" {
			return a, fmt.Errorf("--probe requires a model; pass --model")
		}
		path := "/v1/chat/completions"
		payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply OK"}}, "max_tokens": 1}
		if a.Provider == router.ProviderAnthropic {
			path = "/v1/messages"
		}
		body, _ := json.Marshal(payload)
		a.Status = router.StatusActive
		pool := router.NewAccountPool(router.RouterConfig{Accounts: []router.Account{a}})
		reply, _, err := router.NewProxyHandler(pool, false).ForwardSelectedRequest(ctx, a.Provider, a.ID, path, body, nil)
		if err != nil {
			return a, err
		}
		_ = reply.Body.Close()
		if reply.StatusCode < 200 || reply.StatusCode >= 300 {
			return a, fmt.Errorf("inference probe returned HTTP %d", reply.StatusCode)
		}
	}
	a.Status = router.StatusActive
	a.GatewayReady = true
	return a, nil
}

func nativeAuthenticated(data []byte) bool {
	var status struct {
		LoggedIn      bool `json:"loggedIn"`
		LoggedInSnake bool `json:"logged_in"`
		Authenticated bool `json:"authenticated"`
	}
	if json.Unmarshal(data, &status) == nil {
		return status.LoggedIn || status.LoggedInSnake || status.Authenticated
	}
	text := strings.ToLower(string(data))
	if strings.Contains(text, "not logged") || strings.Contains(text, "not authenticated") {
		return false
	}
	return strings.Contains(text, "logged in") || strings.Contains(text, "authenticated with") || strings.Contains(text, "authenticated as") || strings.Contains(text, "authenticated: true")
}

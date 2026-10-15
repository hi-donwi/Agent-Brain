package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/oauthbridge"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

type cliOptions struct {
	tool, account string
	args          []string
	direct, login bool
	port          int
}

func parseCLIArgs(args []string) (cliOptions, error) {
	o := cliOptions{port: gateway.DefaultPort}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.args = append(o.args, args[i+1:]...)
			break
		}
		switch {
		case arg == "--account" || arg == "-a":
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return o, fmt.Errorf("--account requires provider/name")
			}
			i++
			o.account = args[i]
		case strings.HasPrefix(arg, "--account="):
			o.account = strings.TrimPrefix(arg, "--account=")
			if o.account == "" {
				return o, fmt.Errorf("--account requires provider/name")
			}
		case arg == "--direct":
			o.direct = true
		case arg == "--port":
			if i+1 == len(args) {
				return o, fmt.Errorf("--port requires a number")
			}
			i++
			value, err := strconv.Atoi(args[i])
			if err != nil || value < 1 || value > 65535 {
				return o, fmt.Errorf("port must be between 1 and 65535")
			}
			o.port = value
		case arg == "login" && o.tool == "":
			o.login = true
		case o.tool == "" && !strings.HasPrefix(arg, "-"):
			o.tool = arg
		case o.tool == "":
			return o, fmt.Errorf("unknown launcher option %q", arg)
		default:
			o.args = append(o.args, arg)
		}
	}
	if o.tool == "" {
		return o, fmt.Errorf("specify a CLI tool: claude, codex, agy, opencode, aider, cursor")
	}
	return o, nil
}

func canonicalTool(name string) (string, error) {
	switch name {
	case "antigravity", "agy":
		return "agy", nil
	case "cursor", "cursor-agent":
		return "cursor-agent", nil
	case "claude", "codex", "opencode", "aider":
		return name, nil
	}
	return "", fmt.Errorf("unsupported CLI tool %q; the generic executable name agent is intentionally not assumed to be Cursor", name)
}

func newCliCmd() *cobra.Command {
	return &cobra.Command{Use: "cli [--account provider/name] [--direct] <tool> [args...]", Short: "Launch a native session or a selected provider account", Long: `Launch claude, codex, agy, opencode, aider, or cursor.
Use --account to select a registered account. Native subscription accounts keep their
own login. Gateway accounts require brain serve unless --direct is supplied.
Use -- before tool arguments that share names with Brain launcher flags.`, DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
			return cmd.Help()
		}
		if args[0] == "profiles" || args[0] == "list-profiles" {
			list := newAccountsListCmd()
			list.SetOut(cmd.OutOrStdout())
			list.SetErr(cmd.ErrOrStderr())
			list.SetArgs(nil)
			return list.ExecuteContext(cmd.Context())
		}
		options, err := parseCLIArgs(args)
		if err != nil {
			return err
		}
		tool, err := canonicalTool(options.tool)
		if err != nil {
			return err
		}
		layout, err := home.Default()
		if err != nil {
			return err
		}
		service := router.AccountService{Path: filepath.Join(layout.Root, "accounts.yaml")}
		var account router.Account
		selected := options.account != ""
		if options.login {
			provider := ""
			switch tool {
			case "claude":
				provider = router.ProviderClaudeCode
			case "codex":
				provider = router.ProviderCodex
			case "cursor-agent":
				provider = "cursor"
			default:
				return fmt.Errorf("use brain accounts add to select an authentication integration for %s", tool)
			}
			ref := options.account
			if ref == "" {
				ref = provider + "/default"
			}
			account, err = service.Find(ref)
			if err != nil {
				if _, _, found := strings.Cut(ref, "/"); !found {
					ref = provider + "/" + ref
				}
				prefix, name, _ := strings.Cut(ref, "/")
				if prefix != provider {
					return fmt.Errorf("native login provider does not match the selected tool")
				}
				account, err = service.Add(cmd.Context(), router.Account{Provider: provider, Name: name, Auth: router.AuthNative})
				if err != nil {
					return err
				}
			}
			return connectAccount(cmd, AccountDeps{}, service, account, false)
		}
		if selected {
			account, err = service.Find(options.account)
			if err != nil {
				return err
			}
		} else {
			cfg, err := router.LoadRouterConfig(service.Path)
			if err != nil {
				return err
			}
			var candidates []router.Account
			for _, a := range cfg.Accounts {
				if cfg.Defaults[a.Provider] != a.ID {
					continue
				}
				p, _ := router.LookupProvider(a.Provider)
				if a.Auth == router.AuthNative && p.Tool == tool {
					candidates = []router.Account{a}
					break
				}
				if (tool == "opencode" && (a.Supports(router.ProtocolChat) || a.Supports(router.ProtocolMessages))) || (tool == "codex" && a.Supports(router.ProtocolResponses)) || (tool == "claude" && a.Supports(router.ProtocolMessages)) || (tool == "aider" && a.Supports(router.ProtocolChat)) || (tool == "agy" && a.Supports(router.ProtocolGemini)) || (tool == "cursor-agent" && a.Provider == "cursor") {
					candidates = append(candidates, a)
				}
			}
			if len(candidates) > 1 {
				return fmt.Errorf("several provider defaults match this tool; specify --account provider/name")
			}
			if len(candidates) == 1 {
				account = candidates[0]
				selected = true
			}
		}
		target, err := exec.LookPath(tool)
		if err != nil {
			return fmt.Errorf("install the official %s CLI first", tool)
		}
		if tool == "cursor-agent" {
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			data, err := exec.CommandContext(ctx, target, "login", "--help").CombinedOutput()
			if err != nil || !strings.Contains(string(data), "Cursor") {
				return fmt.Errorf("executable could not be verified as the Cursor CLI")
			}
		}
		child := exec.CommandContext(cmd.Context(), target, options.args...)
		child.Stdin = cmd.InOrStdin()
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		if !selected {
			child.Env = os.Environ()
			fmt.Fprintln(cmd.ErrOrStderr(), "Native CLI session; no Brain account selected.")
			return child.Run()
		}
		if !account.Enabled {
			return fmt.Errorf("selected account is disabled")
		}
		url := fmt.Sprintf("http://127.0.0.1:%d", options.port)
		token := ""
		if account.Auth != router.AuthNative && tool != "cursor-agent" && !options.direct {
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get(url + "/healthz")
			if err != nil {
				return fmt.Errorf("Brain gateway is offline; start brain serve or explicitly use --direct")
			}
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				return fmt.Errorf("Brain gateway health check failed")
			}
			data, err := os.ReadFile(layout.Token)
			if err != nil || strings.TrimSpace(string(data)) == "" {
				return fmt.Errorf("gateway token is unavailable; start brain serve")
			}
			token = gateway.ScopedToken(strings.TrimSpace(string(data)), account.ID)
		}
		if options.direct && account.ManagedBridge {
			stop, err := (oauthbridge.Manager{Root: layout.Root, Output: cmd.ErrOrStderr()}).Use(cmd.Context(), account)
			if err != nil {
				return err
			}
			defer stop()
		}
		if account.Auth == router.AuthOAuth {
			if !account.GatewayReady {
				return fmt.Errorf("OAuth account is not ready; run accounts login or accounts test")
			}
			bridge := router.BridgeClient{URL: account.BridgeURL, Key: account.BridgeKey}
			if _, err := bridge.Verify(cmd.Context(), account.Provider, account.BridgeCredential); err != nil {
				return err
			}
		}
		plan, err := buildLaunchPlan(tool, account, layout.Root, url, token, options.direct, options.args)
		if err != nil {
			return err
		}
		child.Args = append([]string{target}, plan.Args...)
		child.Env = plan.Env
		fmt.Fprintf(cmd.ErrOrStderr(), "Account: %s; native=%t; direct=%t\n", account.Reference(), account.Auth == router.AuthNative, options.direct || tool == "cursor-agent")
		return child.Run()
	}}
}

func runListProfiles(profilesBaseDir string) error {
	entries, err := os.ReadDir(profilesBaseDir)
	if err != nil || len(entries) == 0 {
		fmt.Println("No CLI profiles found.")
		fmt.Println("Login to create one: brain cli login <tool> --account <name>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "TOOL\tACCOUNT\tPROFILE DIRECTORY\tMODIFIED")

	count := 0
	for _, toolEntry := range entries {
		if !toolEntry.IsDir() {
			continue
		}
		toolName := toolEntry.Name()
		toolDir := filepath.Join(profilesBaseDir, toolName)
		accEntries, err := os.ReadDir(toolDir)
		if err != nil {
			continue
		}
		for _, accEntry := range accEntries {
			if !accEntry.IsDir() {
				continue
			}
			accName := accEntry.Name()
			fullPath := filepath.Join(toolDir, accName)
			info, _ := accEntry.Info()
			modTime := "-"
			if info != nil {
				modTime = info.ModTime().Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", toolName, accName, fullPath, modTime)
			count++
		}
	}

	if count == 0 {
		fmt.Println("No CLI profiles found.")
		fmt.Println("Login to create one: brain cli login <tool> --account <name>")
		return nil
	}

	return w.Flush()
}

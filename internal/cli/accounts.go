package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

type AccountDeps struct {
	Interactive func() bool
	Login       func(*cobra.Command, router.Account, bool) (router.Account, error)
}

func accountsConfigPath() (string, error) {
	l, err := home.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(l.Root, "accounts.yaml"), nil
}
func accountService() (router.AccountService, error) {
	path, err := accountsConfigPath()
	return router.AccountService{Path: path}, err
}

func newAccountsCmd() *cobra.Command { return newAccountsCmdWithDeps(AccountDeps{}) }
func newAccountsCmdWithDeps(deps AccountDeps) *cobra.Command {
	cmd := &cobra.Command{Use: "accounts", Aliases: []string{"acc", "account"}, Short: "Manage provider accounts, native login, and gateway credentials"}
	cmd.AddCommand(newAccountsListCmd(), newAccountsAddCmdWithDeps(deps), newAccountsRemoveCmd(), newAccountUseCmd(), newAccountLoginCmd(deps), newAccountTestCmd(), newAccountModelsCmd())
	for _, enabled := range []bool{true, false} {
		cmd.AddCommand(newAccountEnabledCmd(enabled))
	}
	return cmd
}

func newAccountsListCmd() *cobra.Command {
	var jsonOutput bool
	var provider string
	cmd := &cobra.Command{Use: "list", Short: "List redacted account metadata (restart a running gateway after changes)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := accountsConfigPath()
		if err != nil {
			return err
		}
		cfg, err := router.LoadRouterConfig(path)
		if err != nil {
			return err
		}
		accounts := make([]router.Account, 0, len(cfg.Accounts))
		for _, a := range cfg.Accounts {
			if provider == "" || a.Provider == router.NormalizeProvider(provider) {
				accounts = append(accounts, a)
			}
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(accounts)
		}
		if len(accounts) == 0 {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No accounts configured. Run: brain accounts add")
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ACCOUNT\tAUTH\tSTATUS\tCLI\tGATEWAY\tDEFAULT")
		for _, a := range accounts {
			auth := a.Auth
			if auth == "" {
				auth = router.AuthAPIKey
			}
			status := a.Status
			if status == "" {
				status = router.StatusConfigured
			}
			if !a.Enabled {
				status = router.StatusDisabled
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%t\t%t\n", a.Reference(), auth, status, a.CLIReady, a.GatewayReady, cfg.Defaults[a.Provider] == a.ID)
		}
		return w.Flush()
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print metadata without secrets")
	cmd.Flags().StringVar(&provider, "provider", "", "Filter provider")
	return cmd
}

func newAccountsAddCmd() *cobra.Command { return newAccountsAddCmdWithDeps(AccountDeps{}) }
func newAccountsAddCmdWithDeps(deps AccountDeps) *cobra.Command {
	var a router.Account
	var keyStdin, noBrowser, noLogin bool
	var target string
	cmd := &cobra.Command{Use: "add [provider]", Short: "Add an account interactively or with explicit flags", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		interactive := terminalInput(cmd)
		if deps.Interactive != nil {
			interactive = deps.Interactive()
		}
		if len(args) > 0 {
			a.Provider = args[0]
		}
		if interactive && (a.Provider == "" || a.Name == "" || (a.APIKey == "" && !keyStdin && a.Auth != router.AuthNative && a.Auth != router.AuthNone)) {
			if err := accountWizard(cmd, &a); err != nil {
				return err
			}
		} else if a.Provider == "" || a.Name == "" {
			return fmt.Errorf("noninteractive add requires <provider> and --name; use a terminal for the wizard")
		}
		if keyStdin {
			if a.APIKey != "" {
				return fmt.Errorf("choose either --key or --key-stdin")
			}
			data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65537))
			if err != nil || len(data) > 65536 {
				return fmt.Errorf("cannot read credential from stdin")
			}
			a.APIKey = strings.TrimSpace(string(data))
		}
		if target != "" && target != "cli" && target != "gateway" && target != "both" {
			return fmt.Errorf("--for must be cli, gateway, or both")
		}
		p, err := router.LookupProvider(a.Provider)
		if err != nil {
			return err
		}
		if target == "gateway" || target == "both" {
			if a.Auth == router.AuthNative || len(p.Protocols) == 0 {
				return fmt.Errorf("this account supports native CLI login only; choose an API or OAuth gateway account")
			}
		}
		s, err := accountService()
		if err != nil {
			return err
		}
		a.Provider = p.ID
		if a.Provider == router.ProviderAntigravity && a.Auth == "" {
			a.Auth = router.AuthOAuth
		}
		if a.Auth == router.AuthOAuth && !externalBridgeRequested(cmd) && a.BaseURL == "" && a.BridgeURL == "" && a.APIKey == "" && a.BridgeKey == "" {
			a.ManagedBridge = true
		}
		var added router.Account
		existing, findErr := s.Find(a.Reference())
		incomplete := existing.Status == router.StatusPending || existing.Status == router.StatusReauth || (existing.ManagedBridge && existing.Status == router.StatusConfigured && existing.BridgeCredential != "")
		if findErr == nil && a.ManagedBridge && existing.Auth == router.AuthOAuth && !existing.GatewayReady && incomplete {
			added, err = s.EnableManaged(cmd.Context(), existing.ID)
			if err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Resuming incomplete account %s with automatic bridge setup.\n", added.Reference())
			}
		} else {
			added, err = s.Add(cmd.Context(), a)
		}
		if err != nil {
			return err
		}
		if !noLogin && (added.Auth == router.AuthNative || added.Auth == router.AuthOAuth) {
			if err = connectAccount(cmd, deps, s, added, noBrowser); err != nil {
				return err
			}
		}
		if !noLogin && added.Auth != router.AuthNative && added.Auth != router.AuthOAuth && len(p.Protocols) > 0 {
			key, keyErr := router.ResolveCredential(added.APIKey)
			if added.Auth == router.AuthNone || (keyErr == nil && key != "") {
				verified, discoveryErr := checkAccount(cmd.Context(), added, filepath.Dir(s.Path), false, "", cmd.ErrOrStderr())
				if discoveryErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Account saved; model discovery is incomplete: %v. Retry: brain accounts models %s --refresh\n", discoveryErr, added.Reference())
				} else {
					if err := saveVerifiedAccount(cmd.Context(), s, verified); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Discovered %d models for %s. List them with: brain accounts models %s\n", len(verified.DiscoveredModels), added.Reference(), added.Reference())
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Credential is not available yet. After configuring it, run: brain accounts models %s --refresh\n", added.Reference())
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Account %s saved. Verify with: brain accounts test %s\n", added.Reference(), added.Reference())
		fmt.Fprintln(cmd.ErrOrStderr(), "Restart a running brain serve to load account changes.")
		return nil
	}}
	f := cmd.Flags()
	f.StringVar(&a.Name, "name", "", "Account name within the provider")
	f.StringVar(&a.Auth, "auth", "", "Authentication: api-key, native, oauth, none")
	f.StringVar(&a.APIKey, "key", "", "Credential reference (env:NAME or file:PATH); prefer --key-stdin for a literal key")
	f.BoolVar(&keyStdin, "key-stdin", false, "Read a secret from stdin without printing it")
	f.StringVar(&a.Tier, "tier", "", "Routing tier: subscription, cheap, free, local")
	f.IntVar(&a.Priority, "priority", 1, "Routing priority; smaller is preferred")
	f.StringVar(&a.BaseURL, "base-url", "", "Explicit upstream base URL (host or /v1 root)")
	f.StringSliceVar(&a.Models, "models", nil, "Allowed model IDs, comma separated")
	f.StringSliceVar(&a.Protocols, "protocols", nil, "Supported protocols: chat,responses,messages,gemini")
	f.StringVar(&a.BridgeURL, "bridge-url", "", "Dedicated CLIProxyAPI v8 management URL")
	f.StringVar(&a.BridgeKey, "bridge-key", "", "Bridge management credential reference")
	f.StringVar(&target, "for", "", "Requested connection: cli, gateway, both")
	f.BoolVar(&noBrowser, "no-browser", false, "Print OAuth URL or use the native device flow")
	f.Bool("external-bridge", false, "Configure an externally managed OAuth bridge instead of automatic setup")
	f.BoolVar(&noLogin, "no-login", false, "Save without signing in or discovering models")
	return cmd
}

func newAccountsRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove <provider/name-or-id>", Aliases: []string{"rm", "delete"}, Short: "Remove an account binding; native sessions and upstream credentials remain", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := accountService()
		if err != nil {
			return err
		}
		if err = s.Remove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Account removed. Native sessions and upstream credentials were not revoked. Restart a running gateway.")
		return nil
	}}
}

func newAccountUseCmd() *cobra.Command {
	return &cobra.Command{Use: "use <provider/name-or-id>", Short: "Choose the default CLI account for its provider", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := accountService()
		if err != nil {
			return err
		}
		if err = s.Use(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Default account selected.")
		return nil
	}}
}

func newAccountEnabledCmd(enabled bool) *cobra.Command {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	return &cobra.Command{Use: verb + " <provider/name-or-id>", Short: verb + " an account", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := accountService()
		if err != nil {
			return err
		}
		return s.Update(cmd.Context(), args[0], func(a *router.Account) error {
			a.Enabled = enabled
			if enabled {
				a.Status = router.StatusConfigured
				if a.Auth == router.AuthNative || a.Auth == router.AuthOAuth {
					a.Status = router.StatusPending
				}
			} else {
				a.Status = router.StatusDisabled
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Restart a running gateway after changes.")
			return nil
		})
	}}
}

func newProvidersCmd() *cobra.Command {
	var jsonOutput bool
	list := &cobra.Command{Use: "list", Short: "Show provider authentication, protocols, and prerequisites", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(router.Providers())
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "PROVIDER\tAUTH\tGATEWAY PROTOCOLS\tDETAILS")
		for _, p := range router.Providers() {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, strings.Join(p.Auth, ","), strings.Join(p.Protocols, ","), p.Note)
		}
		return w.Flush()
	}}
	list.Flags().BoolVar(&jsonOutput, "json", false, "Print capabilities as JSON")
	cmd := &cobra.Command{Use: "providers", Short: "Inspect supported providers and their capability requirements"}
	cmd.AddCommand(list)
	return cmd
}

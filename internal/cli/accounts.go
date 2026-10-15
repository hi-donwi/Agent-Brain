package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func accountsConfigPath() (string, error) {
	l, err := home.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(l.Root, "accounts.yaml"), nil
}

func newAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounts",
		Aliases: []string{"acc", "account"},
		Short:   "Manage multi-account CLI and cloud AI provider identities",
		Long: `accounts configures, lists, and tests provider accounts (Anthropic, OpenAI,
Gemini, DeepSeek, OpenRouter) used by the AI Router for load-balancing and auto-failover.`,
	}

	cmd.AddCommand(newAccountsListCmd())
	cmd.AddCommand(newAccountsAddCmd())
	cmd.AddCommand(newAccountsRemoveCmd())
	return cmd
}

func newAccountsListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured accounts and their statuses",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := accountsConfigPath()
			if err != nil {
				return err
			}

			cfg, err := router.LoadRouterConfig(path)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg.Accounts)
			}

			if len(cfg.Accounts) == 0 {
				fmt.Println("No accounts configured yet. Add one with: brain accounts add <provider> --name <id> --key <api-key>")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tPROVIDER\tTIER\tPRIORITY\tSTATUS\tKEY REF")
			for _, a := range cfg.Accounts {
				keyDisplay := a.APIKey
				if len(keyDisplay) > 12 && !isEnvRef(keyDisplay) {
					keyDisplay = keyDisplay[:6] + "..." + keyDisplay[len(keyDisplay)-4:]
				}
				status := a.Status
				if status == "" {
					status = router.StatusActive
				}
				if status == router.StatusRateLimited && !a.Cooldown.IsZero() {
					remaining := time.Until(a.Cooldown).Round(time.Second)
					if remaining > 0 {
						status = router.AccountStatus(fmt.Sprintf("rate_limited(%s)", remaining))
					}
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
					a.ID, a.Provider, a.Tier, a.Priority, status, keyDisplay)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func newAccountsAddCmd() *cobra.Command {
	var (
		name     string
		apiKey   string
		tier     string
		priority int
		baseURL  string
	)

	cmd := &cobra.Command{
		Use:   "add <provider>",
		Short: "Add or update a provider account",
		Example: `  brain accounts add anthropic --name work-pro --key env:ANTHROPIC_WORK_KEY --tier subscription
  brain accounts add openai --name personal --key sk-... --tier cheap --priority 2
  brain accounts add gemini --name google-ai --key env:GEMINI_API_KEY`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			definition, err := router.LookupProvider(args[0])
			if err != nil {
				return err
			}
			provider := definition.ID
			if apiKey == "" && provider != router.ProviderLocal {
				return fmt.Errorf("API credential is required; use --key env:VARIABLE")
			}
			if name == "" {
				name = fmt.Sprintf("%s-%d", provider, time.Now().Unix()%10000)
			}
			if tier == "" {
				tier = router.TierSubscription
			}
			if priority <= 0 {
				priority = 1
			}

			path, err := accountsConfigPath()
			if err != nil {
				return err
			}

			cfg, err := router.LoadRouterConfig(path)
			if err != nil {
				return err
			}

			newAcc := router.Account{
				ID:       name,
				Name:     name,
				Provider: provider,
				APIKey:   apiKey,
				BaseURL:  baseURL,
				Tier:     tier,
				Priority: priority,
				Enabled:  true,
				Status:   router.StatusActive,
			}

			// Upsert account
			updated := false
			for i, a := range cfg.Accounts {
				if a.ID == name {
					if router.NormalizeProvider(a.Provider) != provider {
						return fmt.Errorf("account name is already used by another provider")
					}
					cfg.Accounts[i] = newAcc
					updated = true
					break
				}
			}
			if !updated {
				cfg.Accounts = append(cfg.Accounts, newAcc)
			}

			if err := router.SaveRouterConfig(path, cfg); err != nil {
				return err
			}

			fmt.Printf("✓ Account %q (%s, %s tier) saved successfully to %s\n", name, provider, tier, path)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Account identifier (e.g. work, personal)")
	cmd.Flags().StringVar(&apiKey, "key", "", "API key or env:VAR_NAME reference")
	cmd.Flags().StringVar(&tier, "tier", "subscription", "Account tier (subscription, cheap, free, local)")
	cmd.Flags().IntVar(&priority, "priority", 1, "Priority order (1 = highest)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "Custom upstream base URL")
	return cmd
}

func newAccountsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <id>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove an account by ID",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			path, err := accountsConfigPath()
			if err != nil {
				return err
			}

			cfg, err := router.LoadRouterConfig(path)
			if err != nil {
				return err
			}

			found := false
			var remaining []router.Account
			for _, a := range cfg.Accounts {
				if a.ID == id {
					found = true
					continue
				}
				remaining = append(remaining, a)
			}

			if !found {
				return fmt.Errorf("account with id %q not found", id)
			}

			cfg.Accounts = remaining
			if err := router.SaveRouterConfig(path, cfg); err != nil {
				return err
			}

			fmt.Printf("✓ Removed account %q\n", id)
			return nil
		},
	}
	return cmd
}

func isEnvRef(s string) bool {
	return len(s) > 4 && s[:4] == "env:"
}

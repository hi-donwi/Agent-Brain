package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func saveVerifiedAccount(ctx context.Context, s router.AccountService, updated router.Account) error {
	return s.Update(ctx, updated.ID, func(current *router.Account) error {
		current.Status = updated.Status
		current.CLIReady, current.GatewayReady = updated.CLIReady, updated.GatewayReady
		current.BridgeCredential = updated.BridgeCredential
		current.Models = updated.Models
		current.DiscoveredModels = updated.DiscoveredModels
		return nil
	})
}

func newAccountModelsCmd() *cobra.Command {
	var refresh, jsonOutput bool
	cmd := &cobra.Command{Use: "models <provider/name-or-id>", Short: "List the selected account's available models; refresh from its authenticated API", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		service, err := accountService()
		if err != nil {
			return err
		}
		account, err := service.Find(args[0])
		if err != nil {
			return err
		}
		provider, err := router.LookupProvider(account.Provider)
		if err != nil {
			return err
		}
		if account.Auth == router.AuthNative || len(provider.Protocols) == 0 {
			return fmt.Errorf("native CLI sessions do not expose a Brain model catalog; select an API or OAuth gateway account")
		}
		models := account.DiscoveredModels
		if refresh || len(models) == 0 {
			account, err = checkAccount(cmd.Context(), account, filepath.Dir(service.Path), false, "")
			if err != nil {
				return err
			}
			if err = saveVerifiedAccount(cmd.Context(), service, account); err != nil {
				return err
			}
			models = account.DiscoveredModels
			fmt.Fprintln(cmd.ErrOrStderr(), "Model catalog refreshed for this account. Restart a running gateway to load changes.")
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(models)
		}
		for _, model := range models {
			fmt.Fprintln(cmd.OutOrStdout(), model)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Fetch the model catalog from this account's API")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print model IDs as JSON")
	return cmd
}

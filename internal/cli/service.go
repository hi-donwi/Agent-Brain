package cli

import (
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Brain/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the background lokal serve service",
		Long: `Manage the background lokal serve daemon as a systemd user service on Linux
or launchd agent on macOS. The service ensures the gateway starts on boot.`,
	}

	cmd.AddCommand(newServiceInstallCmd())
	cmd.AddCommand(newServiceUninstallCmd())
	cmd.AddCommand(newServiceStatusCmd())

	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the background gateway service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := service.Install(service.Options{})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[service] %s\n", res.Message)
			return nil
		},
	}
}

func newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the background gateway service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := service.Uninstall(service.Options{})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[service] %s\n", res.Message)
			return nil
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check the status of the background gateway service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stat, err := service.Status(service.Options{})
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(stat)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "[service] %s\n", stat.Message)
			fmt.Fprintf(cmd.OutOrStdout(), "  Type:      %s\n", stat.ServiceType)
			fmt.Fprintf(cmd.OutOrStdout(), "  Installed: %t (%s)\n", stat.Installed, stat.Path)
			fmt.Fprintf(cmd.OutOrStdout(), "  Running:   %t\n", stat.Running)
			if stat.Running && stat.Version != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Version:   %s\n", stat.Version)
				fmt.Fprintf(cmd.OutOrStdout(), "  Profile:   %s\n", stat.Profile)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output status in JSON format")
	return cmd
}

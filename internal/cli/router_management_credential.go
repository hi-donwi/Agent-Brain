package cli

import (
	"fmt"
	"path/filepath"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/spf13/cobra"
)

func newRouterManagementCredentialCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "management-credential",
		Short: "Create a Brain management credential for the local operator UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := home.Default()
			if err != nil {
				return err
			}
			if output == "" {
				output = filepath.Join(layout.Root, "agent-control.management.token")
			}
			if !filepath.IsAbs(output) {
				return fmt.Errorf("--output must be an absolute path")
			}
			master, err := readGatewayMaster(layout.Token)
			if err != nil {
				return err
			}
			if err := writeExclusiveFile(output, gateway.NewManagementToken(master)+"\n"); err != nil {
				return fmt.Errorf("write management credential: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created owner-only management credential at %s. Transfer it to the operator UI through a trusted local channel; it is not printed.\n", output)
			return err
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "New owner-only credential file path (default: <brain-home>/agent-control.management.token)")
	return cmd
}

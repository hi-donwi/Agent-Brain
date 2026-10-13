package cli

import (
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/hi-donwi/Agent-Local/internal/gateway"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/snippets"
	"github.com/spf13/cobra"
)

// newDoctorCmd takes the check list as a function so tests can run the command
// against fixed outcomes instead of the live host.
func newDoctorCmd(checks func() []doctor.Check) *cobra.Command {
	var asJSON bool
	var showSnippets bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check this machine and lokal's setup, and say how to fix problems",
		Long: `doctor runs lokal's health checks and prints one line per check, with a fix
under each problem. Exit code: 0 when everything passes, 1 when there are
warnings, 2 when a check failed.`,
		Args: cobra.NoArgs,
		// A failed check is reported in the output and the exit code; it is not
		// a usage error, so cobra must not print usage or the error again.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := doctor.Run(cmd.Context(), checks())
			code := report.ExitCode()

			var snips []snippets.ClientSnippet
			if showSnippets {
				token := ""
				if h, err := home.Default(); err == nil {
					token, _ = gateway.LoadOrCreateToken(h.Token)
				}
				params := snippets.Params{
					Token: token,
				}
				snips = snippets.All(params)
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				payload := struct {
					doctor.Report
					ExitCode int                      `json:"exit_code"`
					Snippets []snippets.ClientSnippet `json:"snippets,omitempty"`
				}{
					Report:   report,
					ExitCode: code,
					Snippets: snips,
				}
				if err := enc.Encode(payload); err != nil {
					return err
				}
			} else {
				if err := report.WriteText(cmd.OutOrStdout()); err != nil {
					return err
				}
				if showSnippets && len(snips) > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
					fmt.Fprintln(cmd.OutOrStdout(), "=== Client Configuration Snippets ===")
					fmt.Fprintln(cmd.OutOrStdout())
					fmt.Fprint(cmd.OutOrStdout(), snippets.FormatText(snips))
				}
			}
			if code != 0 {
				return &ExitCodeError{Code: code}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	cmd.Flags().BoolVar(&showSnippets, "snippets", false, "print integration snippets for client tools and SDKs")
	return cmd
}

package cli

import (
	"encoding/json"

	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/spf13/cobra"
)

// newDoctorCmd takes the check list as a function so tests can run the command
// against fixed outcomes instead of the live host.
func newDoctorCmd(checks func() []doctor.Check) *cobra.Command {
	var asJSON bool
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
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				err := enc.Encode(struct {
					doctor.Report
					ExitCode int `json:"exit_code"`
				}{report, code})
				if err != nil {
					return err
				}
			} else if err := report.WriteText(cmd.OutOrStdout()); err != nil {
				return err
			}
			if code != 0 {
				return &ExitCodeError{Code: code}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}

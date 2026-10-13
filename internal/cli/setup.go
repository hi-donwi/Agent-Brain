package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
	"github.com/hi-donwi/Agent-Local/internal/setup"
	"github.com/spf13/cobra"
)

// SetupDeps supplies the environment inspection functions for setup.
type SetupDeps struct {
	DetectHW     func() (hw.Info, error)
	ResolveHome  func() (home.Layout, error)
	FreeBytes    func(string) (uint64, error)
	CheckBackend func(context.Context) (bool, []string, error)
}

func defaultSetupDeps() SetupDeps {
	return SetupDeps{
		DetectHW:    hw.Detect,
		ResolveHome: home.Default,
		FreeBytes:   hw.FreeBytes,
		CheckBackend: func(ctx context.Context) (bool, []string, error) {
			ad := backend.NewOllama(backend.DefaultOllamaEndpoint, nil)
			models, err := ad.ListModels(ctx)
			if err != nil {
				return false, nil, nil
			}
			names := make([]string, len(models))
			for i, m := range models {
				names[i] = m.Name
			}
			return true, names, nil
		},
	}
}

func newSetupCmd(deps SetupDeps) *cobra.Command {
	var (
		profile string
		dryRun  bool
		yes     bool
		asJSON  bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Plan or install the local AI stack on this machine",
		Long: `setup inspects your hardware, selects an appropriate profile, and displays
or executes the installation plan (inference backend and models).

Use --dry-run to inspect the plan and disk calculations before downloading anything.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			info, err := deps.DetectHW()
			if err != nil {
				return fmt.Errorf("detect hardware: %w", err)
			}

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			freeBytes, _ := deps.FreeBytes(layout.Root)

			backendReady, installedModels, _ := deps.CheckBackend(ctx)

			plan, err := setup.BuildPlan(setup.PlanInput{
				HW:                info,
				Catalog:           catalog.Default(),
				InstalledModelIDs: installedModels,
				BackendReady:      backendReady,
				RequestedProfile:  catalog.ProfileName(profile),
				FreeDiskBytes:     freeBytes,
			})
			if err != nil {
				return err
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(plan); err != nil {
					return err
				}
			} else {
				if err := plan.WriteText(cmd.OutOrStdout()); err != nil {
					return err
				}
				if dryRun {
					fmt.Fprintln(cmd.OutOrStdout(), "\n[dry-run] Plan calculated successfully. No changes were made.")
				} else if !yes {
					fmt.Fprintln(cmd.OutOrStdout(), "\nPass --yes to proceed with download and installation, or --dry-run to preview.")
				}
			}

			if !plan.DiskSufficient {
				return &ExitCodeError{Code: 2}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "override hardware profile (tiny, standard, workstation)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "display the plan without making changes")
	cmd.Flags().BoolVar(&yes, "yes", false, "proceed with setup without prompting")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output the plan as JSON")

	return cmd
}

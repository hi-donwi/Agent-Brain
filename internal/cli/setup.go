package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
	"github.com/hi-donwi/Agent-Local/internal/setup"
	"github.com/spf13/cobra"
)

// SetupDeps supplies the environment inspection and execution functions for setup.
type SetupDeps struct {
	DetectHW       func() (hw.Info, error)
	ResolveHome    func() (home.Layout, error)
	FreeBytes      func(string) (uint64, error)
	CheckBackend   func(context.Context) (bool, []string, error)
	ResolveAdapter func() (backend.Adapter, error)
	RunDoctor      func(context.Context) (doctor.Report, error)
}

func defaultSetupDeps() SetupDeps {
	return SetupDeps{
		DetectHW:    hw.Detect,
		ResolveHome: home.Default,
		FreeBytes:   hw.FreeBytes,
		CheckBackend: func(ctx context.Context) (bool, []string, error) {
			var ad backend.Adapter = backend.NewOllama(backend.DefaultOllamaEndpoint, nil)
			if l, err := home.Default(); err == nil {
				if cfg, err := l.LoadConfig(); err == nil && cfg != nil {
					engine, bCfg := cfg.PrimaryBackend()
					if customAd, err := backend.NewAdapter(engine, bCfg.Endpoint, bCfg.Token, nil); err == nil {
						ad = customAd
					}
				}
			}
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
		ResolveAdapter: func() (backend.Adapter, error) {
			if l, err := home.Default(); err == nil {
				if cfg, err := l.LoadConfig(); err == nil && cfg != nil {
					engine, bCfg := cfg.PrimaryBackend()
					return backend.NewAdapter(engine, bCfg.Endpoint, bCfg.Token, nil)
				}
			}
			return backend.NewOllama(backend.DefaultOllamaEndpoint, nil), nil
		},

		RunDoctor: func(ctx context.Context) (doctor.Report, error) {
			return doctor.Run(ctx, doctor.Default()), nil
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

			if dryRun || !yes {
				return nil
			}

			// Unattended execution (--yes)
			if !plan.Backend.Installed {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[setup] Cannot proceed unattended: inference backend '%s' is not running or not installed.\n%s\n",
					plan.Backend.Name, plan.Backend.Instruction)
				return &ExitCodeError{Code: 2}
			}

			adapter, err := deps.ResolveAdapter()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[setup] Failed to initialize backend adapter: %v\n", err)
				return &ExitCodeError{Code: 2}
			}

			for _, m := range plan.Models {
				if m.AlreadyPresent {
					if !asJSON {
						fmt.Fprintf(cmd.OutOrStdout(), "\n[setup] Model %s (%s) is already installed.\n", m.BackendID, m.ModelID)
					}
					continue
				}

				if !asJSON {
					fmt.Fprintf(cmd.OutOrStdout(), "\n[setup] Pulling model %s (%s)...\n", m.BackendID, doctor.HumanBytes(m.DownloadBytes))
				}
				lastReport := time.Now()
				err := adapter.Pull(ctx, m.BackendID, func(p backend.Progress) {
					if asJSON {
						return
					}
					if p.Total > 0 {
						percent := float64(p.Completed) / float64(p.Total) * 100.0
						if time.Since(lastReport) > 250*time.Millisecond || p.Completed == p.Total {
							fmt.Fprintf(cmd.OutOrStdout(), "\r[setup] Pulling %s: %.1f%% (%s / %s)",
								m.BackendID, percent, doctor.HumanBytes(p.Completed), doctor.HumanBytes(p.Total))
							lastReport = time.Now()
						}
					} else if p.Status != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "\r[setup] %s: %s", m.BackendID, p.Status)
					}
				})
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "\n[setup] Failed to pull model %s: %v\n", m.BackendID, err)
					return &ExitCodeError{Code: 2}
				}
				if !asJSON {
					fmt.Fprintf(cmd.OutOrStdout(), "\n[setup] Model %s ready.\n", m.BackendID)
				}
			}

			if err := layout.Ensure(); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[setup] Failed to create home directory %s: %v\n", layout.Root, err)
				return &ExitCodeError{Code: 2}
			}

			if err := layout.SaveConfig(plan.Config); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[setup] Failed to save config: %v\n", err)
				return &ExitCodeError{Code: 2}
			}
			if !asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "\n[setup] Saved configuration to %s\n", layout.Config)
			}

			if deps.RunDoctor != nil {
				if !asJSON {
					fmt.Fprintln(cmd.OutOrStdout(), "\n[setup] Running doctor verification...")
				}
				report, err := deps.RunDoctor(ctx)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "[setup] Doctor verification failed to run: %v\n", err)
					return &ExitCodeError{Code: 2}
				}
				if !asJSON {
					if err := report.WriteText(cmd.OutOrStdout()); err != nil {
						return err
					}
				}
				code := report.ExitCode()
				if code != 0 {
					return &ExitCodeError{Code: code}
				}
			}

			if !asJSON {
				fmt.Fprintln(cmd.OutOrStdout(), "\nSetup completed successfully.")
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

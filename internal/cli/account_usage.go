package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/codexusage"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

type codexUsageReader func(context.Context, string, string) (codexusage.Snapshot, error)

func newAccountUsageCmd() *cobra.Command {
	return newAccountUsageCmdWithReader(readNativeCodexUsage)
}

func newAccountUsageCmdWithReader(read codexUsageReader) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "usage <provider/name-or-id>",
		Short: "Read native Codex usage and reset windows without changing the account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, err := accountService()
			if err != nil {
				return err
			}
			account, err := service.Find(args[0])
			if err != nil {
				return err
			}
			if account.Provider != router.ProviderCodex || account.Auth != router.AuthNative {
				return fmt.Errorf("usage snapshots are available only for native Codex accounts")
			}
			if !account.CLIReady {
				return fmt.Errorf("native Codex account is not verified; run brain accounts test %s first", account.Reference())
			}

			root := filepath.Dir(service.Path)
			snapshot, err := read(cmd.Context(), root, profileDirectory(root, account))
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			return printCodexUsage(cmd.OutOrStdout(), account.Reference(), snapshot)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print the snapshot as JSON")
	return cmd
}

func readNativeCodexUsage(parent context.Context, root, codexHome string) (codexusage.Snapshot, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return codexusage.Snapshot{}, fmt.Errorf("install the official Codex CLI before reading usage")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	env, err := accountEnvironment(root)
	if err != nil {
		return codexusage.Snapshot{}, fmt.Errorf("prepare isolated Codex environment: %w", err)
	}
	cmd.Env = mergeProcessEnv(env, []string{"CODEX_HOME=" + codexHome})
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexusage.Snapshot{}, fmt.Errorf("open Codex App Server output")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexusage.Snapshot{}, fmt.Errorf("open Codex App Server input")
	}
	if err := cmd.Start(); err != nil {
		return codexusage.Snapshot{}, fmt.Errorf("start Codex App Server")
	}

	snapshot, readErr := codexusage.ReadSnapshot(stdout, stdin)
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return codexusage.Snapshot{}, fmt.Errorf("Codex usage request timed out or was canceled")
	}
	if readErr != nil {
		return codexusage.Snapshot{}, readErr
	}
	if waitErr != nil {
		return codexusage.Snapshot{}, fmt.Errorf("Codex App Server exited unsuccessfully")
	}
	return snapshot, nil
}

func printCodexUsage(w io.Writer, account string, snapshot codexusage.Snapshot) error {
	if _, err := fmt.Fprintf(w, "Codex usage snapshot for %s\n", account); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Quota windows:"); err != nil {
		return err
	}
	if len(snapshot.RateLimits) == 0 {
		if _, err := fmt.Fprintln(w, "  Rate-limit metadata unavailable."); err != nil {
			return err
		}
	}
	for _, bucket := range snapshot.RateLimits {
		label := bucket.LimitID
		if bucket.LimitName != nil && *bucket.LimitName != "" {
			label = *bucket.LimitName + " (" + bucket.LimitID + ")"
		}
		if _, err := fmt.Fprintf(w, "  %q:\n", label); err != nil {
			return err
		}
		if err := printCodexWindow(w, "primary", bucket.Primary); err != nil {
			return err
		}
		if err := printCodexWindow(w, "secondary", bucket.Secondary); err != nil {
			return err
		}
		if bucket.RateLimitReachedType != nil {
			if _, err := fmt.Fprintf(w, "    reached: %q\n", *bucket.RateLimitReachedType); err != nil {
				return err
			}
		}
	}
	if snapshot.UsageStatus != "available" || snapshot.Usage == nil {
		_, err := fmt.Fprintln(w, "Token activity: unavailable from Codex App Server.")
		return err
	}
	if _, err := fmt.Fprintln(w, "Token activity (separate from quota windows):"); err != nil {
		return err
	}
	if summary := snapshot.Usage.Summary; summary != nil {
		if err := printOptionalCount(w, "lifetime tokens", summary.LifetimeTokens); err != nil {
			return err
		}
		if err := printOptionalCount(w, "peak daily tokens", summary.PeakDailyTokens); err != nil {
			return err
		}
		if err := printOptionalCount(w, "longest running turn seconds", summary.LongestRunningTurnSec); err != nil {
			return err
		}
		if err := printOptionalCount(w, "current streak days", summary.CurrentStreakDays); err != nil {
			return err
		}
		if err := printOptionalCount(w, "longest streak days", summary.LongestStreakDays); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, "  Summary: not provided."); err != nil {
		return err
	}
	for _, bucket := range snapshot.Usage.DailyUsageBuckets {
		if _, err := fmt.Fprintf(w, "  day %q: %d tokens\n", bucket.StartDate, bucket.Tokens); err != nil {
			return err
		}
	}
	return nil
}

func printCodexWindow(w io.Writer, label string, window *codexusage.RateLimit) error {
	if window == nil {
		_, err := fmt.Fprintf(w, "    %s: not reported\n", label)
		return err
	}
	if _, err := fmt.Fprintf(w, "    %s: ", label); err != nil {
		return err
	}
	if window.UsedPercent == nil {
		if _, err := fmt.Fprint(w, "usage unavailable"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w, "%.1f%% used", *window.UsedPercent); err != nil {
		return err
	}
	if window.WindowDurationMins != nil {
		if _, err := fmt.Fprintf(w, ", %d-minute window", *window.WindowDurationMins); err != nil {
			return err
		}
	}
	if window.ResetsAt == nil {
		_, err := fmt.Fprintln(w, ", reset unavailable from provider")
		return err
	}
	_, err := fmt.Fprintf(w, ", resets at %s\n", window.ResetsAt.UTC().Format(time.RFC3339))
	return err
}

func printOptionalCount(w io.Writer, label string, value *int64) error {
	if value == nil {
		_, err := fmt.Fprintf(w, "  %s: unavailable\n", label)
		return err
	}
	_, err := fmt.Fprintf(w, "  %s: %d\n", label, *value)
	return err
}

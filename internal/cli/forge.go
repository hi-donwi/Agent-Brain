package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/forge"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/trace"
	"github.com/spf13/cobra"
)

// ForgeDeps provides dependencies for lokal forge commands.
type ForgeDeps struct {
	ResolveHome    func() (home.Layout, error)
	ResolveAdapter func() (backend.Adapter, error)
}

func defaultForgeDeps() ForgeDeps {
	return ForgeDeps{
		ResolveHome: home.Default,
		ResolveAdapter: func() (backend.Adapter, error) {
			l, err := home.Default()
			if err == nil {
				if cfg, err := l.LoadConfig(); err == nil && cfg != nil {
					engine, bCfg := cfg.PrimaryBackend()
					return backend.NewAdapter(engine, bCfg.Endpoint, bCfg.Token, nil)
				}
			}
			return backend.NewOllama(backend.DefaultOllamaEndpoint, nil), nil
		},
	}
}

func newForgeCmd(deps ForgeDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Skill Forge - extract, evaluate, and learn reusable agent skills",
		Long: `forge mines repetitive successful patterns from local agent traces,
synthesizes candidate skills into ~/.lokal/candidates/, benchmarks them against
baseline fixtures, and hardens their descriptions and procedural workflows.`,
	}

	cmd.AddCommand(newForgeMineCmd(deps))
	cmd.AddCommand(newForgeDraftCmd(deps))
	cmd.AddCommand(newForgeEvalCmd(deps))
	cmd.AddCommand(newForgeHardenCmd(deps))

	return cmd
}

func newForgeMineCmd(deps ForgeDeps) *cobra.Command {
	var (
		minRuns    int
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "mine",
		Short: "Cluster local traces to identify candidate skill patterns",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			store := trace.NewStore(layout.Traces)
			miner := forge.NewMiner(store, minRuns)

			clusters, err := miner.Mine(ctx)
			if err != nil {
				return fmt.Errorf("mine traces: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				return enc.Encode(clusters)
			}

			if len(clusters) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No repeatable trace patterns found qualifying minimum runs.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "CLUSTER ID\tRUNS\tSUGGESTED NAME\tSIGNATURE\tINTENT")
			for _, cl := range clusters {
				sig := cl.Signature
				if sig == "" {
					sig = "-"
				}
				if len(sig) > 40 {
					sig = sig[:37] + "..."
				}
				intent := cl.Intent
				if len(intent) > 35 {
					intent = intent[:32] + "..."
				}
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
					cl.ID, cl.SuccessfulRuns, cl.SuggestedName, sig, intent)
			}
			return w.Flush()
		},
	}

	cmd.Flags().IntVar(&minRuns, "min-runs", 2, "minimum number of successful sessions required for a cluster")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output clusters as JSON")
	return cmd
}

func newForgeDraftCmd(deps ForgeDeps) *cobra.Command {
	var nameOverride string

	cmd := &cobra.Command{
		Use:   "draft <cluster-id>",
		Short: "Draft a candidate skill from a mined cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			clusterID := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home: %w", err)
			}

			store := trace.NewStore(layout.Traces)
			miner := forge.NewMiner(store, 1)
			clusters, err := miner.Mine(ctx)
			if err != nil {
				return fmt.Errorf("mine clusters: %w", err)
			}

			var targetCluster *forge.TraceCluster
			for _, cl := range clusters {
				if cl.ID == clusterID {
					targetCluster = &cl
					break
				}
			}

			if targetCluster == nil {
				return fmt.Errorf("cluster %s not found in traces", clusterID)
			}

			var adapter backend.Adapter
			if deps.ResolveAdapter != nil {
				adapter, _ = deps.ResolveAdapter()
			}

			drafter := forge.NewDrafter(layout.Candidates, store, adapter)
			candidate, err := drafter.Draft(ctx, *targetCluster, nameOverride)
			if err != nil {
				return fmt.Errorf("draft candidate: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Drafted candidate skill: %s\n", candidate.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Path:                    %s\n", candidate.Path)
			fmt.Fprintf(cmd.OutOrStdout(), "Description:             %s\n", candidate.Description)
			fmt.Fprintf(cmd.OutOrStdout(), "\nNext steps: run 'lokal forge eval %s' to benchmark against test fixtures.\n", candidate.Name)

			return nil
		},
	}

	cmd.Flags().StringVar(&nameOverride, "name", "", "override candidate skill name")
	return cmd
}

func newForgeEvalCmd(deps ForgeDeps) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "eval <candidate-name>",
		Short: "Evaluate and benchmark a candidate skill against test fixtures",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			candidateName := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			var adapter backend.Adapter
			if deps.ResolveAdapter != nil {
				adapter, _ = deps.ResolveAdapter()
			}

			evaluator := forge.NewEvaluator(layout.Candidates, adapter)
			report, err := evaluator.EvaluateCandidate(ctx, candidateName)
			if err != nil {
				return fmt.Errorf("evaluate candidate: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
				if !report.Passed {
					return &ExitCodeError{Code: 1}
				}
				return nil
			}

			status := "PASS"
			if !report.Passed {
				status = "FAIL"
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Candidate Evaluation: %s\n", report.SkillName)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:               %s\n", status)
			fmt.Fprintf(cmd.OutOrStdout(), "Precision:            %.1f%%\n", report.Precision*100.0)
			fmt.Fprintf(cmd.OutOrStdout(), "Recall:               %.1f%%\n", report.Recall*100.0)
			fmt.Fprintf(cmd.OutOrStdout(), "Token Efficiency:     %.1f%% savings over baseline\n", report.TokenSavingsPct)
			fmt.Fprintf(cmd.OutOrStdout(), "Safety Check:         %s\n", map[bool]string{true: "CLEAN", false: "VIOLATION"}[report.SafetyPassed])

			if len(report.SafetyViolations) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "\nSafety Violations:")
				for _, v := range report.SafetyViolations {
					fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", v)
				}
			}

			fmt.Fprintln(cmd.OutOrStdout(), "\nFixtures:")
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			for _, bm := range report.Benchmarks {
				bstatus := "[PASS]"
				if !bm.Passed {
					bstatus = "[FAIL]"
				}
				note := bm.Notes
				if note == "" {
					note = fmt.Sprintf("triggered=%v (expected=%v)", bm.Triggered, bm.ExpectedTrigger)
				}
				fmt.Fprintf(w, "  %s\t%s (%s)\t%s\n", bstatus, bm.FixtureName, bm.FixtureType, note)
			}
			_ = w.Flush()

			if !report.Passed {
				return &ExitCodeError{Code: 1}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output report as JSON")
	return cmd
}

func newForgeHardenCmd(deps ForgeDeps) *cobra.Command {
	var maxRounds int

	cmd := &cobra.Command{
		Use:   "harden <candidate-name>",
		Short: "Refine and optimize candidate description and procedural workflow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			candidateName := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			var adapter backend.Adapter
			if deps.ResolveAdapter != nil {
				adapter, _ = deps.ResolveAdapter()
			}

			evaluator := forge.NewEvaluator(layout.Candidates, adapter)
			hardener := forge.NewHardener(layout.Candidates, evaluator, maxRounds)

			result, err := hardener.Harden(ctx, candidateName)
			if err != nil {
				return fmt.Errorf("harden candidate: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Hardening Complete for: %s (%d rounds)\n", result.SkillName, result.Rounds)
			fmt.Fprintf(cmd.OutOrStdout(), "Precision:              %.1f%% -> %.1f%%\n",
				result.InitialPrecision*100.0, result.FinalPrecision*100.0)
			fmt.Fprintf(cmd.OutOrStdout(), "Recall:                 %.1f%% -> %.1f%%\n",
				result.InitialRecall*100.0, result.FinalRecall*100.0)
			fmt.Fprintf(cmd.OutOrStdout(), "Improved:               %v\n", result.Improved)

			return nil
		},
	}

	cmd.Flags().IntVar(&maxRounds, "rounds", 3, "maximum refinement iterations")
	return cmd
}

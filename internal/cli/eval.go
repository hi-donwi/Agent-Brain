package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/evalrunner"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/spf13/cobra"
)

// EvalDeps provides dependencies for the lokal eval command.
type EvalDeps struct {
	ResolveAdapter func() (backend.Adapter, error)
}

func defaultEvalDeps() EvalDeps {
	return EvalDeps{
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

func newEvalCmd(deps EvalDeps) *cobra.Command {
	var (
		modelOverride string
		jsonOutput    bool
	)

	cmd := &cobra.Command{
		Use:   "eval <task-spec.yaml>",
		Short: "Execute a bounded agent evaluation task in an isolated sandbox",
		Long: `eval runs a self-contained evaluation spec (.yaml or .json) inside a throwaway
sandbox directory with time and token bounds, verifying specified assertions.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			specPath := args[0]

			spec, err := evalrunner.LoadTaskSpec(specPath)
			if err != nil {
				return fmt.Errorf("load task spec: %w", err)
			}

			if modelOverride != "" {
				spec.Model = modelOverride
			}
			if spec.Model == "" {
				if l, err := home.Default(); err == nil {
					if cfg, err := l.LoadConfig(); err == nil && cfg != nil {
						if chatModel, ok := cfg.Aliases["chat"]; ok && chatModel != "" {
							spec.Model = chatModel
						}
					}
				}
				if spec.Model == "" {
					spec.Model = "chat"
				}
			}

			var adapter backend.Adapter
			if deps.ResolveAdapter != nil {
				adapter, _ = deps.ResolveAdapter()
			}

			runner := evalrunner.NewRunner(adapter, spec.Model)
			result, err := runner.Run(ctx, spec)
			if err != nil {
				return fmt.Errorf("eval execution error: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return err
				}
				if !result.Passed {
					return &ExitCodeError{Code: 1}
				}
				return nil
			}

			status := "PASS"
			if !result.Passed {
				status = "FAIL"
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Evaluation Task: %s [%s]\n", result.TaskName, result.TaskID)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:          %s (in %dms)\n", status, result.DurationMs)
			if result.Model != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Model:           %s\n", result.Model)
			}
			if result.PromptTokens > 0 || result.EvalTokens > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Tokens:          %d prompt / %d eval\n", result.PromptTokens, result.EvalTokens)
			}
			if result.Error != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Error:           %s\n", result.Error)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "\nAssertions:")
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			for _, a := range result.Assertions {
				astatus := "[PASS]"
				if !a.Passed {
					astatus = "[FAIL]"
				}
				detail := a.Target
				if a.Message != "" {
					detail += " - " + a.Message
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\n", astatus, a.Type, detail)
			}
			_ = w.Flush()

			if !result.Passed {
				return &ExitCodeError{Code: 1}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&modelOverride, "model", "", "override model name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output results as JSON")

	return cmd
}

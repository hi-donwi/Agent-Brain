package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
	"github.com/spf13/cobra"
)

// TracesDeps provides dependencies for lokal traces commands.
type TracesDeps struct {
	ResolveHome func() (home.Layout, error)
}

func defaultTracesDeps() TracesDeps {
	return TracesDeps{
		ResolveHome: home.Default,
	}
}

func newTracesCmd(deps TracesDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traces",
		Short: "Inspect, manage, and import agent session traces",
		Long: `traces manages execution traces recorded by the gateway or imported from
external agents (Agent-Control, Claude Code). Traces are stored in ~/.lokal/traces/
as append-only JSONL files.`,
	}

	cmd.AddCommand(newTracesListCmd(deps))
	cmd.AddCommand(newTracesShowCmd(deps))
	cmd.AddCommand(newTracesImportCmd(deps))

	return cmd
}

func newTracesListCmd(deps TracesDeps) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List recorded trace sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			store := trace.NewStore(layout.Traces)
			summaries, err := store.ListSessions()
			if err != nil {
				return fmt.Errorf("list trace sessions: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(summaries)
			}

			if len(summaries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No trace sessions recorded yet.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SESSION ID\tTURNS\tMODELS\tPROMPT TOK\tEVAL TOK\tERRORS\tLAST ACTIVE")
			for _, s := range summaries {
				modelsStr := fmt.Sprintf("%v", s.Models)
				if len(s.Models) == 0 {
					modelsStr = "-"
				}
				errorsStr := "no"
				if s.HasErrors {
					errorsStr = "yes"
				}
				lastActive := s.LastTimestamp.Format(time.RFC3339)
				if s.LastTimestamp.IsZero() {
					lastActive = "-"
				}

				fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%d\t%s\t%s\n",
					s.SessionID, s.RecordCount, modelsStr, s.TotalPromptToks, s.TotalEvalToks, errorsStr, lastActive)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output sessions as JSON")
	return cmd
}

func newTracesShowCmd(deps TracesDeps) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show details and turns of a trace session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			store := trace.NewStore(layout.Traces)
			records, err := store.ReadSession(sessionID)
			if err != nil {
				return fmt.Errorf("read session %s: %w", sessionID, err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(records)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Session: %s (%d turns)\n\n", sessionID, len(records))
			for i, r := range records {
				fmt.Fprintf(cmd.OutOrStdout(), "--- Turn %d [%s] (model: %s, duration: %dms, tokens: %d in / %d out) ---\n",
					i+1, r.TurnID, r.Model, r.DurationMs, r.PromptTokens, r.EvalTokens)

				if len(r.Messages) > 0 {
					for _, m := range r.Messages {
						fmt.Fprintf(cmd.OutOrStdout(), "[%s]: %s\n", m.Role, m.Content)
					}
				}
				if r.Response != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "[response]: %s\n", r.Response)
				}
				if len(r.ToolCalls) > 0 {
					for _, tc := range r.ToolCalls {
						fmt.Fprintf(cmd.OutOrStdout(), "[tool_call]: %s (args: %v, result: %s)\n", tc.Name, tc.Arguments, tc.Result)
					}
				}
				if r.Error != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "[error]: %s\n", r.Error)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output records as JSON")
	return cmd
}

func newTracesImportCmd(deps TracesDeps) *cobra.Command {
	var sessionID string

	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import session logs from Agent-Control or Claude Code",
		Long: `import parses conversation logs and records them into ~/.lokal/traces/.
Supports Agent-Control JSON logs and Claude Code JSONL transcripts.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			if _, err := os.Stat(filePath); err != nil {
				return fmt.Errorf("file not found: %s", filePath)
			}

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home: %w", err)
			}

			store := trace.NewStore(layout.Traces)
			count, err := trace.ImportLogFile(store, filePath, sessionID)
			if err != nil {
				return fmt.Errorf("import log file: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully imported %d records from %s into %s\n",
				count, filePath, layout.Traces)
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "custom session ID to assign")
	return cmd
}

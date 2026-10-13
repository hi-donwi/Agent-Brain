package cli

import (
	"encoding/json"
	"fmt"

	"github.com/hi-donwi/Agent-Local/internal/codeintel"
	"github.com/spf13/cobra"
)

func newRepoMapCmd() *cobra.Command {
	var (
		tokenBudget int
		jsonOutput  bool
	)

	cmd := &cobra.Command{
		Use:   "repomap [<path>]",
		Short: "Generate a ranked outline of repository files and symbol definitions",
		Long: `repomap scans the repository at <path> (or current directory) and produces a
concise outline of exported functions, methods, and types ranked to fit a token budget.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := "."
			if len(args) > 0 {
				targetDir = args[0]
			}

			rm, err := codeintel.BuildRepoMap(targetDir, tokenBudget)
			if err != nil {
				return fmt.Errorf("build repository map: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rm)
			}

			fmt.Fprint(cmd.OutOrStdout(), rm.Formatted)
			return nil
		},
	}

	cmd.Flags().IntVar(&tokenBudget, "tokens", 2048, "Maximum token budget for the repository outline")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newSymbolsCmd() *cobra.Command {
	var (
		query      string
		regexPat   string
		limit      int
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "symbols [<path>]",
		Short: "Search symbol definitions, references, or code patterns across repository",
		Long: `symbols inspects source code definitions and usages using pure-Go AST parsing
and code search across Go, Python, TypeScript/JavaScript, and Rust.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := "."
			if len(args) > 0 {
				targetDir = args[0]
			}

			// Mode 1: Regex code search
			if regexPat != "" {
				matches, err := codeintel.SearchCode(targetDir, regexPat, limit)
				if err != nil {
					return fmt.Errorf("search code: %w", err)
				}
				if jsonOutput {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(matches)
				}
				if len(matches) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "No matches found for pattern %q.\n", regexPat)
					return nil
				}
				for _, m := range matches {
					fmt.Fprintf(cmd.OutOrStdout(), "[File: %s:%d]: %s\n", m.Path, m.Line, m.Snippet)
				}
				return nil
			}

			// Mode 2: Symbol definition and reference search
			if query != "" {
				res, err := codeintel.FindSymbol(targetDir, query, limit)
				if err != nil {
					return fmt.Errorf("find symbol %q: %w", query, err)
				}
				if jsonOutput {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(res)
				}

				if len(res.Definitions) == 0 && len(res.References) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "No definitions or references found for %q.\n", query)
					return nil
				}

				if len(res.Definitions) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Definitions:")
					for _, d := range res.Definitions {
						fmt.Fprintf(cmd.OutOrStdout(), "  - %s (%s) at %s:%d\n", d.Name, d.Kind, d.Path, d.Line)
						if d.Signature != "" {
							fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", d.Signature)
						}
					}
				}

				if len(res.References) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "References (%d):\n", len(res.References))
					for _, ref := range res.References {
						fmt.Fprintf(cmd.OutOrStdout(), "  - %s:%d: %s\n", ref.Path, ref.Line, ref.Content)
					}
				}
				return nil
			}

			// Mode 3: Overview repository map
			rm, err := codeintel.BuildRepoMap(targetDir, 2048)
			if err != nil {
				return err
			}
			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rm)
			}
			fmt.Fprint(cmd.OutOrStdout(), rm.Formatted)
			return nil
		},
	}

	cmd.Flags().StringVarP(&query, "query", "q", "", "Find definitions and references of a symbol name")
	cmd.Flags().StringVarP(&regexPat, "regex", "r", "", "Search code using regular expressions")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of results to display")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

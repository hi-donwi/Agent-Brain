// Package cli wires the lokal command tree. Commands are built by constructors
// rather than package-level globals so every test gets a fresh tree and its own
// output buffers.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd builds the full lokal command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lokal",
		Short: "lokal - set up and run AI on your own machine",
		Long: `lokal sets up local inference backends, serves local models behind one
OpenAI-compatible endpoint, indexes your files and code for retrieval, serves
that knowledge and your skills to agents over MCP, and learns reusable skills
from how your agents work. It is the local layer under your agents, not
another agent.

Everything stays on this machine unless you explicitly configure otherwise.
Most commands are not implemented yet; see ROADMAP.md for what lands when.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs lokal with the process arguments and exits non-zero on error.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "lokal: %v\n", err)
		os.Exit(1)
	}
}

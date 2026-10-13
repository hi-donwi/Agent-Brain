// Package cli wires the lokal command tree. Commands are built by constructors
// rather than package-level globals so every test gets a fresh tree and its own
// output buffers.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/hi-donwi/Agent-Local/internal/doctor"
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
	root.AddCommand(newDoctorCmd(doctor.Default))
	root.AddCommand(newSetupCmd(defaultSetupDeps()))
	root.AddCommand(newModelsCmd(defaultModelsDeps()))
	root.AddCommand(newServeCmd(defaultServeDeps()))
	root.AddCommand(newServiceCmd())
	root.AddCommand(newIndexCmd(defaultRagDeps()))
	root.AddCommand(newSearchCmd(defaultRagDeps()))
	root.AddCommand(newAskCmd(defaultRagDeps()))
	root.AddCommand(newSkillsCmd(defaultSkillsDeps()))
	root.AddCommand(newMcpCmd(defaultMcpDeps()))
	root.AddCommand(newRepoMapCmd())
	root.AddCommand(newSymbolsCmd())
	root.AddCommand(newTracesCmd(defaultTracesDeps()))
	root.AddCommand(newEvalCmd(defaultEvalDeps()))
	root.AddCommand(newForgeCmd(defaultForgeDeps()))
	return root
}

// ExitCodeError ends lokal with a specific exit code after the command has
// already printed its own output, as doctor does for warnings and failures.
type ExitCodeError struct {
	Code int
}

func (e *ExitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Execute runs lokal with the process arguments and exits non-zero on error.
func Execute() {
	code, msg := exitStatus(NewRootCmd().Execute())
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	if code != 0 {
		os.Exit(code)
	}
}

// exitStatus maps a command error to the process exit code and the message to
// print; an ExitCodeError carries its code and prints nothing more.
func exitStatus(err error) (int, string) {
	var exit *ExitCodeError
	switch {
	case err == nil:
		return 0, ""
	case errors.As(err, &exit):
		return exit.Code, ""
	}
	return 1, "lokal: " + err.Error()
}

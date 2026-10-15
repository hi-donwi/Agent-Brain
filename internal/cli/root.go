// Package cli wires the lokal command tree. Commands are built by constructors
// rather than package-level globals so every test gets a fresh tree and its own
// output buffers.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/hi-donwi/Agent-Brain/internal/doctor"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the full brain command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "brain",
		Aliases: []string{"lokal"},
		Short:   "brain - Central intelligence platform, AI router, and model gateway",
		Long: `brain is the central intelligence layer for AI agents: it serves as an AI router
supporting multi-account CLI tools (claude, codex, agy, opencode, aider) across multiple
cloud providers with token optimization and auto-failover, coordinates local inference
backends, indexes code and files with hybrid retrieval (RAG), and serves knowledge over MCP.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newDoctorCmd(doctor.Default))
	root.AddCommand(newSetupCmd(defaultSetupDeps()))
	root.AddCommand(newModelsCmd(defaultModelsDeps()))
	root.AddCommand(newServeCmd(defaultServeDeps()))
	root.AddCommand(newServiceCmd())
	root.AddCommand(newAccountsCmd())
	root.AddCommand(newProvidersCmd())
	root.AddCommand(newRouterCmd())
	root.AddCommand(newCliCmd())
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

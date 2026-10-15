package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/spf13/cobra"
)

func newCliCmd() *cobra.Command {
	var (
		port int
	)

	cmd := &cobra.Command{
		Use:   "cli <tool> [args...]",
		Short: "Launch a coding agent CLI connected to the AI Router",
		Long: `cli launches coding tools (claude, codex, agy, opencode, aider) with their
environment variables pre-configured to route through the Brain AI Gateway.

This automatically enables:
- Multi-account round-robin and auto-failover on 429
- Token Saver (RTK) compression on tool outputs
- Unified billing and fallback from subscription to free or local inference`,
		Example: `  brain cli claude
  brain cli codex --model gpt-4o
  brain cli opencode
  brain cli agy
  brain cli aider --model openai/code`,
		DisableFlagParsing: true, // Pass all flags after tool directly to the subprocess
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("please specify a CLI tool to run (e.g. brain cli claude, brain cli codex)")
			}

			tool := args[0]
			toolArgs := args[1:]

			if port <= 0 {
				port = gateway.DefaultPort
			}

			// Read gateway token
			token := ""
			if l, err := home.Default(); err == nil {
				tokenPath := filepath.Join(l.Root, "serve.token")
				if data, err := os.ReadFile(tokenPath); err == nil {
					token = strings.TrimSpace(string(data))
				}
			}
			if token == "" {
				token = "brain-local-dev-token"
			}

			targetPath, err := exec.LookPath(tool)
			if err != nil {
				return fmt.Errorf("tool %q not found on PATH: please install it first", tool)
			}

			childCmd := exec.Command(targetPath, toolArgs...)
			childCmd.Stdin = os.Stdin
			childCmd.Stdout = os.Stdout
			childCmd.Stderr = os.Stderr

			// Copy existing environment
			env := os.Environ()

			// Wire up Brain Gateway proxy URLs
			baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			openAIBase := fmt.Sprintf("http://127.0.0.1:%d/v1", port)

			injected := []string{
				"ANTHROPIC_BASE_URL=" + baseURL,
				"ANTHROPIC_API_KEY=" + token,
				"OPENAI_BASE_URL=" + openAIBase,
				"OPENAI_API_BASE=" + openAIBase,
				"OPENAI_API_KEY=" + token,
				"GEMINI_API_BASE=" + baseURL,
			}

			childCmd.Env = append(env, injected...)

			fmt.Fprintf(os.Stderr, "🧠 [Agent-Brain] Routing %s through http://127.0.0.1:%d (Multi-Account & Token-Saver active)\n", tool, port)
			return childCmd.Run()
		},
	}

	return cmd
}

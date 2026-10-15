package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/spf13/cobra"
)

func newCliCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "cli [--account <name>] [--direct] <tool> [args...]",
		Short: "Launch a coding agent CLI connected to AI Router with multi-account support",
		Long: `cli launches coding tools (claude, codex, agy, opencode, aider) with their
environment variables pre-configured to route through the Brain AI Gateway.

Features:
- Multi-Account CLI Profiles: Isolate OAuth/browser logins per account (e.g. work vs personal)
- AI Router Integration: Multi-account round-robin, priority tiering, and 429 auto-failover
- Token Saver (RTK): Real-time token compression on tool execution outputs

Subcommands & Examples:
  brain cli claude                          # Run claude routed through Brain gateway
  brain cli --account work claude           # Run claude using 'work' profile
  brain cli login claude --account work     # Login to claude under 'work' profile
  brain cli login agy --account google-dev  # Login to antigravity CLI under 'google-dev'
  brain cli profiles                        # List all saved CLI profiles
  brain cli codex --model gpt-4o            # Run codex CLI through router
  brain cli opencode                        # Run opencode CLI through router`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}

			l, err := home.Default()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			profilesBaseDir := filepath.Join(l.Root, "profiles")

			// Check for 'profiles' subcommand
			if args[0] == "profiles" || args[0] == "list-profiles" {
				return runListProfiles(profilesBaseDir)
			}

			var (
				account string
				direct  bool
				isLogin bool
				tool    string
				rawArgs []string
			)

			// Parse brain cli flags before tool name
			for i := 0; i < len(args); i++ {
				arg := args[i]
				switch {
				case arg == "--account" || arg == "-a":
					if i+1 < len(args) {
						account = args[i+1]
						i++
					}
				case strings.HasPrefix(arg, "--account="):
					account = strings.TrimPrefix(arg, "--account=")
				case arg == "--direct":
					direct = true
				case arg == "--port":
					if i+1 < len(args) {
						_, _ = fmt.Sscanf(args[i+1], "%d", &port)
						i++
					}
				case arg == "login":
					isLogin = true
				case !strings.HasPrefix(arg, "-"):
					if tool == "" {
						tool = arg
						rawArgs = args[i+1:]
						goto Parsed
					}
				}
			}

		Parsed:
			if tool == "" {
				return fmt.Errorf("please specify a CLI tool to run (e.g. brain cli claude, brain cli login agy)")
			}

			// Also filter out any trailing --account flags passed after the tool
			var toolArgs []string
			for j := 0; j < len(rawArgs); j++ {
				arg := rawArgs[j]
				if (arg == "--account" || arg == "-a") && j+1 < len(rawArgs) {
					account = rawArgs[j+1]
					j++
					continue
				} else if strings.HasPrefix(arg, "--account=") {
					account = strings.TrimPrefix(arg, "--account=")
					continue
				}
				toolArgs = append(toolArgs, arg)
			}

			if port <= 0 {
				port = gateway.DefaultPort
			}

			targetPath, err := exec.LookPath(tool)
			if err != nil {
				return fmt.Errorf("tool %q not found on PATH: please install it first", tool)
			}

			// Handle interactive tool login with profile isolation
			if isLogin {
				if account == "" {
					account = "default"
				}
				profileDir := filepath.Join(profilesBaseDir, tool, account)
				if err := os.MkdirAll(profileDir, 0o700); err != nil {
					return fmt.Errorf("create profile directory: %w", err)
				}

				loginArgs := toolArgs
				if len(loginArgs) == 0 {
					switch tool {
					case "agy", "antigravity":
						loginArgs = []string{"auth", "login"}
					case "opencode":
						loginArgs = []string{"auth", "login"}
					default:
						loginArgs = []string{"login"}
					}
				}

				loginCmd := exec.Command(targetPath, loginArgs...)
				loginCmd.Stdin = os.Stdin
				loginCmd.Stdout = os.Stdout
				loginCmd.Stderr = os.Stderr
				loginCmd.Env = append(os.Environ(), getToolProfileEnv(tool, profileDir)...)

				fmt.Printf("🔐 [Agent-Brain] Launching %s login under profile %q (%s)\n", tool, account, profileDir)
				return loginCmd.Run()
			}

			// Regular tool invocation
			childCmd := exec.Command(targetPath, toolArgs...)
			childCmd.Stdin = os.Stdin
			childCmd.Stdout = os.Stdout
			childCmd.Stderr = os.Stderr

			childEnv := os.Environ()

			// Apply profile isolation if account is provided
			if account != "" {
				profileDir := filepath.Join(profilesBaseDir, tool, account)
				if err := os.MkdirAll(profileDir, 0o700); err != nil {
					return fmt.Errorf("create profile directory: %w", err)
				}
				childEnv = append(childEnv, getToolProfileEnv(tool, profileDir)...)
				fmt.Fprintf(os.Stderr, "👤 [Agent-Brain] Active CLI Profile: %s (%s)\n", account, profileDir)
			}

			// Wire up Brain Gateway proxy URLs unless --direct is requested
			if !direct {
				token := ""
				tokenPath := filepath.Join(l.Root, "serve.token")
				if data, err := os.ReadFile(tokenPath); err == nil {
					token = strings.TrimSpace(string(data))
				}
				if token == "" {
					token = "brain-local-dev-token"
				}

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
				childEnv = append(childEnv, injected...)
				fmt.Fprintf(os.Stderr, "🧠 [Agent-Brain] Routing %s through http://127.0.0.1:%d (Multi-Account & Token-Saver active)\n", tool, port)
			}

			childCmd.Env = childEnv
			return childCmd.Run()
		},
	}

	return cmd
}

func getToolProfileEnv(tool, profileDir string) []string {
	return []string{
		"CLAUDE_CONFIG_DIR=" + profileDir,
		"ANTIGRAVITY_CONFIG_DIR=" + profileDir,
		"CODEX_CONFIG_DIR=" + profileDir,
		"OPENCODE_CONFIG_DIR=" + profileDir,
		"AIDER_CONFIG_DIR=" + profileDir,
		"XDG_CONFIG_HOME=" + profileDir,
	}
}

func runListProfiles(profilesBaseDir string) error {
	entries, err := os.ReadDir(profilesBaseDir)
	if err != nil || len(entries) == 0 {
		fmt.Println("No CLI profiles found.")
		fmt.Println("Login to create one: brain cli login <tool> --account <name>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(w, "TOOL\tACCOUNT\tPROFILE DIRECTORY\tMODIFIED")

	count := 0
	for _, toolEntry := range entries {
		if !toolEntry.IsDir() {
			continue
		}
		toolName := toolEntry.Name()
		toolDir := filepath.Join(profilesBaseDir, toolName)
		accEntries, err := os.ReadDir(toolDir)
		if err != nil {
			continue
		}
		for _, accEntry := range accEntries {
			if !accEntry.IsDir() {
				continue
			}
			accName := accEntry.Name()
			fullPath := filepath.Join(toolDir, accName)
			info, _ := accEntry.Info()
			modTime := "-"
			if info != nil {
				modTime = info.ModTime().Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", toolName, accName, fullPath, modTime)
			count++
		}
	}

	if count == 0 {
		fmt.Println("No CLI profiles found.")
		fmt.Println("Login to create one: brain cli login <tool> --account <name>")
		return nil
	}

	return w.Flush()
}

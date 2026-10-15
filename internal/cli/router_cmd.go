package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func newRouterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "router",
		Short: "Inspect AI Router health, active pools, and token optimizer",
		Long: `router provides diagnostics and controls for the AI Router, including
multi-account status, failover cooldowns, and tool result token compression.`,
	}

	cmd.AddCommand(newRouterStatusCmd())
	cmd.AddCommand(newRouterOptimizeCmd())
	return cmd
}

func newRouterStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display router status and connected gateway health",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := accountsConfigPath()
			cfg, _ := router.LoadRouterConfig(path)

			// Try pinging local gateway
			gatewayURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", gateway.DefaultPort)
			client := &http.Client{Timeout: 1 * time.Second}
			resp, err := client.Get(gatewayURL)
			gatewayOnline := false
			if err == nil && resp.StatusCode == http.StatusOK {
				gatewayOnline = true
				_ = resp.Body.Close()
			}

			if jsonOutput {
				out := map[string]interface{}{
					"gateway_online": gatewayOnline,
					"accounts_count": len(cfg.Accounts),
					"strategy":       cfg.Strategy,
					"token_saver":    cfg.TokenSaver,
					"accounts":       cfg.Accounts,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			fmt.Println("🧠 Agent-Brain AI Router Status")
			fmt.Println("--------------------------------")
			if gatewayOnline {
				fmt.Printf("Gateway HTTP Server:  ● RUNNING (127.0.0.1:%d)\n", gateway.DefaultPort)
			} else {
				fmt.Printf("Gateway HTTP Server:  ○ STOPPED (start with: brain serve)\n")
			}
			fmt.Printf("Routing Strategy:     %s\n", cfg.Strategy)
			fmt.Printf("Token Saver (RTK):    %v\n", cfg.TokenSaver)
			fmt.Printf("Configured Accounts:  %d\n", len(cfg.Accounts))

			if len(cfg.Accounts) > 0 {
				fmt.Println("\nAccounts Overview:")
				for _, a := range cfg.Accounts {
					status := a.Status
					if status == "" {
						status = router.StatusActive
					}
					fmt.Printf("  • [%s] %s (%s, tier: %s, priority: %d)\n",
						status, a.ID, a.Provider, a.Tier, a.Priority)
				}
			} else {
				fmt.Println("\n(No accounts configured. Use 'brain accounts add' to add accounts)")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func newRouterOptimizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "optimize [file]",
		Short: "Benchmark or test Token Saver compression on a text file or stdin",
		Example: `  git diff | brain router optimize
  brain router optimize test.log`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var input []byte
			var err error

			if len(args) > 0 {
				input, err = os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("read %s: %w", args[0], err)
				}
			} else {
				input, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			}

			if len(input) == 0 {
				fmt.Println("Empty input. Provide a file or pipe data into stdin.")
				return nil
			}

			compressed := router.CompressToolResult(string(input))

			rawLen := len(input)
			compLen := len(compressed)
			saved := rawLen - compLen
			pct := 0.0
			if rawLen > 0 {
				pct = (float64(saved) / float64(rawLen)) * 100.0
			}

			fmt.Fprintf(os.Stderr, "✓ Raw size: %d bytes | Compressed size: %d bytes | Saved: %d bytes (%.1f%%)\n\n",
				rawLen, compLen, saved, pct)
			fmt.Println(compressed)
			return nil
		},
	}
	return cmd
}

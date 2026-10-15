package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	cmd.AddCommand(&cobra.Command{Use: "strategy <priority|round-robin>", Short: "Choose the account-pool strategy", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "priority" && args[0] != "round-robin" {
			return fmt.Errorf("strategy must be priority or round-robin")
		}
		path, err := accountsConfigPath()
		if err != nil {
			return err
		}
		err = router.UpdateRouterConfig(cmd.Context(), path, func(cfg *router.RouterConfig) error { cfg.Strategy = args[0]; return nil })
		if err == nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "Restart a running gateway to apply the strategy.")
		}
		return err
	}})
	return cmd
}

func newRouterStatusCmd() *cobra.Command {
	var jsonOutput bool
	var port int
	cmd := &cobra.Command{Use: "status", Short: "Show live gateway accounts when available, otherwise configured metadata", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := accountsConfigPath()
		if err != nil {
			return err
		}
		cfg, err := router.LoadRouterConfig(path)
		if err != nil {
			return err
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Get(base + "/healthz")
		online := err == nil && response.StatusCode == 200
		if response != nil {
			_ = response.Body.Close()
		}
		accounts := cfg.Accounts
		source := "configuration"
		if online {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "serve.token"))
			if err == nil {
				request, _ := http.NewRequestWithContext(cmd.Context(), "GET", base+"/v1/accounts", nil)
				request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(data)))
				live, err := client.Do(request)
				if err == nil {
					defer live.Body.Close()
					if live.StatusCode == 200 {
						var current []router.Account
						if json.NewDecoder(io.LimitReader(live.Body, 4<<20)).Decode(&current) == nil {
							accounts = current
							source = "gateway"
						}
					}
				}
			}
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"gateway_online": online, "accounts_source": source, "configured_accounts_count": len(cfg.Accounts), "accounts_count": len(accounts), "strategy": cfg.Strategy, "token_saver": cfg.TokenSaver, "accounts": accounts})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Gateway online: %t (%s)\nAccount status source: %s\nConfigured strategy: %s\n", online, base, source, cfg.Strategy)
		for _, a := range accounts {
			status := a.Status
			if status == "" {
				status = router.StatusConfigured
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s (priority %d)\n", status, a.Reference(), a.Priority)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print redacted status JSON")
	cmd.Flags().IntVar(&port, "port", gateway.DefaultPort, "Gateway port")
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

			fmt.Fprintf(os.Stderr, "Raw size: %d bytes | Compressed size: %d bytes | Saved: %d bytes (%.1f%%)\n\n",
				rawLen, compLen, saved, pct)
			fmt.Println(compressed)
			return nil
		},
	}
	return cmd
}

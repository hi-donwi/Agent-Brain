package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/buildinfo"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/gateway"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/trace"
	"github.com/spf13/cobra"
)

// ServeDeps provides dependencies for the lokal serve command.
type ServeDeps struct {
	ResolveHome    func() (home.Layout, error)
	ResolveAdapter func() (backend.Adapter, error)
	Catalog        *catalog.Catalog
}

func defaultServeDeps() ServeDeps {
	return ServeDeps{
		ResolveHome: home.Default,
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
		Catalog: catalog.Default(),
	}
}

func newServeCmd(deps ServeDeps) *cobra.Command {
	var (
		port         int
		profile      string
		allowRemote  bool
		recordTraces bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the OpenAI-compatible local AI gateway",
		Long: `serve runs the local loopback gateway serving local models behind an
OpenAI-compatible HTTP interface (/v1/chat/completions, /v1/embeddings, /v1/models).

All requests require bearer token authentication. The token is generated and persisted
to ~/.lokal/serve.token with mode 0600.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home layout: %w", err)
			}

			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home directories: %w", err)
			}

			token, err := gateway.LoadOrCreateToken(layout.Token)
			if err != nil {
				return fmt.Errorf("initialize bearer token: %w", err)
			}

			adapter, err := deps.ResolveAdapter()
			if err != nil {
				return fmt.Errorf("initialize backend adapter: %w", err)
			}

			// Load user config or fall back to default
			cfg, err := layout.LoadConfig()
			if err != nil {
				cfg = home.DefaultConfig("tiny", map[string]string{
					"chat":  "qwen2.5-coder-1.5b",
					"code":  "qwen2.5-coder-1.5b",
					"embed": "nomic-embed-text",
				})
			}
			if profile != "" {
				cfg.Profile = profile
			}

			bindHost := "127.0.0.1"
			if allowRemote {
				fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: binding to 0.0.0.0 exposes the gateway to your local network. Ensure firewall rules and token protection are active.")
				bindHost = "0.0.0.0"
			}
			addr := fmt.Sprintf("%s:%d", bindHost, port)

			var traceStore *trace.Store
			if recordTraces {
				traceStore = trace.NewStore(layout.Traces)
			}

			srv, err := gateway.NewServer(gateway.ServerConfig{
				Addr:         addr,
				Token:        token,
				AllowedHosts: []string{},
				Adapter:      adapter,
				Catalog:      deps.Catalog,
				UserConfig:   cfg,
				TraceStore:   traceStore,
				Version:      buildinfo.Version,
			})
			if err != nil {
				return fmt.Errorf("configure gateway server: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "lokal gateway starting on http://%s\n", addr)
			fmt.Fprintf(cmd.OutOrStdout(), "Bearer token: %s (saved in %s)\n", token, layout.Token)
			fmt.Fprintf(cmd.OutOrStdout(), "OpenAI endpoint: http://%s/v1\n", addr)
			fmt.Fprintf(cmd.OutOrStdout(), "Healthcheck:     http://%s/healthz\n", addr)
			if recordTraces {
				fmt.Fprintf(cmd.OutOrStdout(), "Traces recorded: %s\n", layout.Traces)
			}

			serverErr := make(chan error, 1)
			go func() {
				if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					serverErr <- err
				}
			}()

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

			select {
			case <-ctx.Done():
				fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down gateway...")
			case sig := <-sigChan:
				fmt.Fprintf(cmd.OutOrStdout(), "\nReceived signal %v, shutting down gateway...\n", sig)
			case err := <-serverErr:
				return fmt.Errorf("server error: %w", err)
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		},
	}

	cmd.Flags().IntVar(&port, "port", gateway.DefaultPort, "port to listen on")
	cmd.Flags().StringVar(&profile, "profile", "", "override hardware profile (tiny, standard, workstation)")
	cmd.Flags().BoolVar(&allowRemote, "allow-remote", false, "allow non-loopback connections (0.0.0.0)")
	cmd.Flags().BoolVar(&recordTraces, "trace", false, "record prompt and response traces to ~/.lokal/traces/")

	return cmd
}

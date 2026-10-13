package cli

import (
	"fmt"
	"os"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/buildinfo"
	"github.com/hi-donwi/Agent-Local/internal/home"
	localmcp "github.com/hi-donwi/Agent-Local/internal/mcp"
	"github.com/hi-donwi/Agent-Local/internal/rag"
	"github.com/hi-donwi/Agent-Local/internal/skills"
	"github.com/hi-donwi/Agent-Local/internal/store"
	"github.com/spf13/cobra"
)

// McpDeps provides dependencies for the mcp stdio command.
type McpDeps struct {
	ResolveHome    func() (home.Layout, error)
	ResolveAdapter func() (backend.Adapter, error)
	OpenStore      func(dbPath string) (*store.Store, error)
}

func defaultMcpDeps() McpDeps {
	return McpDeps{
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
		OpenStore: store.Open,
	}
}

func newMcpCmd(deps McpDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run read-only Model Context Protocol (MCP) server over stdio",
		Long: `mcp starts a standard Model Context Protocol server over stdio, exposing
lokal's local knowledge retrieval, Agent Skills catalog, and model inspection.

All exposed tools are strictly read-only (ADR-0008). Agent hosts retain full control
over tool execution policies, code changes, and loop execution.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home: %w", err)
			}

			// 1. Open SQLite store and create Searcher
			var searcher *rag.Searcher
			st, err := deps.OpenStore(layout.DB)
			if err == nil && st != nil {
				defer st.Close()
				ad, _ := deps.ResolveAdapter()
				searcher = rag.NewSearcher(st, ad)
			}

			// 2. Create Skills Manager
			cwd, _ := os.Getwd()
			var configuredDirs []string
			var aliases map[string]string

			if cfg, err := layout.LoadConfig(); err == nil && cfg != nil {
				configuredDirs = cfg.SkillSources
				aliases = cfg.Aliases
			}
			skillsMgr := skills.NewManager(cwd, layout.Skills, configuredDirs)

			// 3. Resolve backend adapter
			ad, _ := deps.ResolveAdapter()

			// 4. Construct MCP server
			srv := localmcp.NewServer(localmcp.Config{
				Name:      "lokal",
				Version:   buildinfo.Version,
				Searcher:  searcher,
				SkillsMgr: skillsMgr,
				Adapter:   ad,
				Aliases:   aliases,
			})

			// 5. Start stdio loop
			return srv.ServeStdio()
		},
	}

	return cmd
}

package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/rag"
	"github.com/hi-donwi/Agent-Local/internal/store"
	"github.com/spf13/cobra"
)

// RagDeps provides dependencies for index, search, and ask commands.
type RagDeps struct {
	ResolveHome    func() (home.Layout, error)
	ResolveAdapter func() (backend.Adapter, error)
	OpenStore      func(dbPath string) (*store.Store, error)
}

func defaultRagDeps() RagDeps {
	return RagDeps{
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

func newIndexCmd(deps RagDeps) *cobra.Command {
	var (
		collection string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "index <path>",
		Short: "Index local documents into SQLite hybrid store",
		Long: `index walks the directory at <path>, chunks text documents, generates vector
embeddings, and persists both lexical and vector representations into ~/.lokal/lokal.db.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			targetPath := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home directories: %w", err)
			}

			s, err := deps.OpenStore(layout.DB)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer s.Close()

			ad, err := deps.ResolveAdapter()
			if err != nil {
				return fmt.Errorf("resolve adapter: %w", err)
			}

			// Resolve embedding model from config if available
			embedModel := "nomic-embed-text"
			if cfg, err := layout.LoadConfig(); err == nil && cfg != nil {
				if aliasModel, ok := cfg.Aliases["embed"]; ok && aliasModel != "" {
					embedModel = aliasModel
				}
			}

			indexer := rag.NewIndexer(s, ad)

			stats, err := indexer.IndexDirectory(ctx, targetPath, collection, embedModel, func(relPath string, count int) {
				if !jsonOutput {
					fmt.Fprintf(cmd.OutOrStdout(), "indexed %s (%d chunks)\n", relPath, count)
				}
			})
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(stats)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nIndexing complete:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Collection:     %s\n", stats.CollectionName)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files scanned:  %d\n", stats.FilesScanned)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files indexed:  %d\n", stats.FilesIndexed)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files skipped:  %d\n", stats.FilesSkipped)
			fmt.Fprintf(cmd.OutOrStdout(), "  Files pruned:   %d\n", stats.FilesPruned)
			fmt.Fprintf(cmd.OutOrStdout(), "  Chunks created: %d\n", stats.ChunksCreated)
			fmt.Fprintf(cmd.OutOrStdout(), "  Duration:       %s\n", stats.Duration)
			return nil
		},
	}

	cmd.Flags().StringVarP(&collection, "collection", "c", "default", "Collection name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newSearchCmd(deps RagDeps) *cobra.Command {
	var (
		collection string
		topK       int
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search indexed files using hybrid lexical and dense retrieval",
		Long: `search queries the SQLite store using Reciprocal Rank Fusion over FTS5 BM25
and dense vector cosine similarity, printing matching chunks with citations.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			query := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			s, err := deps.OpenStore(layout.DB)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer s.Close()

			ad, _ := deps.ResolveAdapter()
			searcher := rag.NewSearcher(s, ad)

			results, err := searcher.Search(ctx, collection, query, topK)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching results found in collection", collection)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Found %d results in collection %q:\n\n", len(results), collection)
			for idx, res := range results {
				ch := res.Chunk
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s:%d-%d (score: %.4f, match: %s)\n",
					idx+1, ch.Path, ch.StartLine, ch.EndLine, res.Score, res.MatchType)

				// Print excerpt snippet
				lines := strings.Split(strings.TrimSpace(ch.Content), "\n")
				snippetLimit := 3
				if len(lines) < snippetLimit {
					snippetLimit = len(lines)
				}
				for _, line := range lines[:snippetLimit] {
					fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", line)
				}
				if len(lines) > snippetLimit {
					fmt.Fprintln(cmd.OutOrStdout(), "    ...")
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&collection, "collection", "c", "default", "Collection name")
	cmd.Flags().IntVarP(&topK, "top-k", "k", 5, "Number of results to retrieve")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newAskCmd(deps RagDeps) *cobra.Command {
	var (
		collection string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask questions grounded in indexed local files",
		Long: `ask retrieves relevant excerpts from the indexed collection and asks the local model
to answer strictly from local context with file citations.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			question := args[0]

			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			s, err := deps.OpenStore(layout.DB)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer s.Close()

			ad, err := deps.ResolveAdapter()
			if err != nil {
				return fmt.Errorf("resolve adapter: %w", err)
			}

			chatModel := "chat"
			if cfg, err := layout.LoadConfig(); err == nil && cfg != nil {
				if alias, ok := cfg.Aliases["chat"]; ok && alias != "" {
					chatModel = alias
				}
			}

			searcher := rag.NewSearcher(s, ad)
			askEngine := rag.NewAskEngine(searcher, ad)

			resp, err := askEngine.Ask(ctx, collection, question, chatModel)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n", resp.Answer)
			if len(resp.Citations) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Citations:")
				for _, cit := range resp.Citations {
					fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", cit)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&collection, "collection", "c", "default", "Collection name")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
	"github.com/hi-donwi/Agent-Local/internal/setup"
	"github.com/spf13/cobra"
)

// ModelsDeps supplies dependencies for model management subcommands.
type ModelsDeps struct {
	ResolveAdapter func() (backend.Adapter, error)
	ResolveHome    func() (home.Layout, error)
	FreeBytes      func(string) (uint64, error)
	Catalog        *catalog.Catalog
}

func defaultModelsDeps() ModelsDeps {
	return ModelsDeps{
		ResolveAdapter: func() (backend.Adapter, error) {
			return backend.NewOllama(backend.DefaultOllamaEndpoint, nil), nil
		},
		ResolveHome: home.Default,
		FreeBytes:   hw.FreeBytes,
		Catalog:     catalog.Default(),
	}
}

// ModelListing combines backend model information with catalog metadata.
type ModelListing struct {
	Name       string    `json:"name"`
	CatalogID  string    `json:"catalog_id,omitempty"`
	Roles      []string  `json:"roles,omitempty"`
	Size       uint64    `json:"size"`
	Digest     string    `json:"digest,omitempty"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

func newModelsCmd(deps ModelsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage local models installed in the inference backend",
		Long: `models provides thin inspection and management wrappers over the local
inference backend, cross-referencing installed models with the catalog and enforcing
free disk safety margins.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newModelsListCmd(deps))
	cmd.AddCommand(newModelsPullCmd(deps))
	cmd.AddCommand(newModelsRmCmd(deps))

	return cmd
}

func resolveCatalogModel(cat *catalog.Catalog, query, backendName string) (catalog.Model, bool) {
	if cat == nil {
		return catalog.Model{}, false
	}
	if m, ok := cat.GetModel(query); ok {
		return m, true
	}
	for _, m := range cat.Models {
		if tag, ok := m.Backends[backendName]; ok && tag == query {
			return m, true
		}
	}
	return catalog.Model{}, false
}

func rolesToStrings(roles []catalog.Role) []string {
	strs := make([]string, len(roles))
	for i, r := range roles {
		strs[i] = string(r)
	}
	return strs
}

func newModelsListCmd(deps ModelsDeps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:           "ls",
		Short:         "List installed models in the backend",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			adapter, err := deps.ResolveAdapter()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "failed to initialize backend: %v\n", err)
				return &ExitCodeError{Code: 2}
			}

			models, err := adapter.ListModels(ctx)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "failed to list models from backend: %v\nfix: verify backend is running at %s\n", err, adapter.Endpoint())
				return &ExitCodeError{Code: 2}
			}

			listings := []ModelListing{}
			cat := deps.Catalog
			if cat == nil {
				cat = catalog.Default()
			}

			for _, m := range models {
				listing := ModelListing{
					Name:       m.Name,
					Size:       m.Size,
					Digest:     m.Digest,
					ModifiedAt: m.ModifiedAt,
				}

				// Cross-reference with catalog
				for _, cm := range cat.Models {
					if tag, ok := cm.Backends[adapter.Name()]; ok {
						if m.Name == tag || m.Name == tag+":latest" || strings.HasPrefix(m.Name, tag) {
							listing.CatalogID = cm.ID
							listing.Roles = rolesToStrings(cm.Roles)
							break
						}
					}
					if m.Name == cm.ID {
						listing.CatalogID = cm.ID
						listing.Roles = rolesToStrings(cm.Roles)
						break
					}
				}
				listings = append(listings, listing)
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(listings)
			}

			if len(listings) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No models installed. Run 'lokal setup' or 'lokal models pull <name>' to install models.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tROLES\tSIZE\tMODIFIED")
			for _, l := range listings {
				roles := "-"
				if len(l.Roles) > 0 {
					roles = strings.Join(l.Roles, ",")
				}
				modStr := "-"
				if !l.ModifiedAt.IsZero() {
					modStr = l.ModifiedAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Name, roles, doctor.HumanBytes(l.Size), modStr)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output models list as JSON")
	return cmd
}

func newModelsPullCmd(deps ModelsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pull <model>",
		Short:         "Pull a model from the registry with disk safety margin checks",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target := args[0]

			adapter, err := deps.ResolveAdapter()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "failed to initialize backend: %v\n", err)
				return &ExitCodeError{Code: 2}
			}

			cat := deps.Catalog
			if cat == nil {
				cat = catalog.Default()
			}

			backendTag := target
			var downloadBytes uint64
			if cm, ok := resolveCatalogModel(cat, target, adapter.Name()); ok {
				if tag, exists := cm.Backends[adapter.Name()]; exists {
					backendTag = tag
				}
				downloadBytes = cm.DownloadBytes
			}

			// Disk safety check
			if deps.ResolveHome != nil && deps.FreeBytes != nil {
				layout, err := deps.ResolveHome()
				if err == nil {
					free, err := deps.FreeBytes(layout.Root)
					if err == nil {
						required := downloadBytes
						margin := setup.DefaultSafetyMarginBytes
						if free < required+margin {
							fmt.Fprintf(cmd.ErrOrStderr(), "insufficient disk space: pulling %s requires %s plus %s safety margin, but only %s is free\n",
								backendTag, doctor.HumanBytes(required), doctor.HumanBytes(margin), doctor.HumanBytes(free))
							return &ExitCodeError{Code: 2}
						}
					}
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Pulling %s...\n", backendTag)
			lastReport := time.Now()
			err = adapter.Pull(ctx, backendTag, func(p backend.Progress) {
				if p.Total > 0 {
					percent := float64(p.Completed) / float64(p.Total) * 100.0
					if time.Since(lastReport) > 250*time.Millisecond || p.Completed == p.Total {
						fmt.Fprintf(cmd.OutOrStdout(), "\r[%s] %.1f%% (%s / %s)",
							p.Status, percent, doctor.HumanBytes(p.Completed), doctor.HumanBytes(p.Total))
						lastReport = time.Now()
					}
				} else if p.Status != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\r[%s]", p.Status)
				}
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\npull failed: %v\n", err)
				return &ExitCodeError{Code: 2}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nmodel %s pulled successfully\n", backendTag)
			return nil
		},
	}

	return cmd
}

func newModelsRmCmd(deps ModelsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "rm <model>",
		Short:         "Remove a model from the inference backend",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target := args[0]

			adapter, err := deps.ResolveAdapter()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "failed to initialize backend: %v\n", err)
				return &ExitCodeError{Code: 2}
			}

			cat := deps.Catalog
			if cat == nil {
				cat = catalog.Default()
			}

			backendTag := target
			if cm, ok := resolveCatalogModel(cat, target, adapter.Name()); ok {
				if tag, exists := cm.Backends[adapter.Name()]; exists {
					backendTag = tag
				}
			}

			if err := adapter.Delete(ctx, backendTag); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "failed to remove model %s: %v\n", backendTag, err)
				return &ExitCodeError{Code: 2}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "model %s removed\n", backendTag)
			return nil
		},
	}

	return cmd
}

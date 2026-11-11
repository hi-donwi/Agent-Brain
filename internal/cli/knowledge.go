package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/spf13/cobra"
)

func newKnowledgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "knowledge",
		Short: "Manage Brain's registered local knowledge sources",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newKnowledgeSourceCmd())
	return cmd
}

func newKnowledgeSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Register local source roots for Brain indexing",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newKnowledgeSourceAddCmd())
	cmd.AddCommand(newKnowledgeSourceListCmd())
	cmd.AddCommand(newKnowledgeSourceRemoveCmd())
	return cmd
}

func newKnowledgeSourceAddCmd() *cobra.Command {
	var id, name, root, collection, embeddingModel string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a canonical local root for Brain indexing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := home.Default()
			if err != nil {
				return err
			}
			config, err := layout.LoadConfig()
			if err != nil {
				return fmt.Errorf("load Brain config before registering knowledge: %w", err)
			}
			canonicalRoot, err := canonicalKnowledgeRoot(layout, root)
			if err != nil {
				return err
			}
			if collection == "" {
				collection = id
			}
			if embeddingModel == "" {
				embeddingModel = config.Aliases["embed"]
			}
			if embeddingModel == "" {
				embeddingModel = "nomic-embed-text"
			}
			config.KnowledgeSources = append(config.KnowledgeSources, home.KnowledgeSource{
				ID: id, Name: name, Root: canonicalRoot, Collection: collection, EmbeddingModel: embeddingModel,
			})
			if err := layout.SaveConfig(config); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Registered knowledge source %s (collection %s). Its root is stored in Brain's owner-only config.\n", id, collection)
			return err
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Stable source ID (lowercase letters, digits, '_' or '-')")
	cmd.Flags().StringVar(&name, "name", "", "Operator-facing source name")
	cmd.Flags().StringVar(&root, "root", "", "Local directory to index")
	cmd.Flags().StringVar(&collection, "collection", "", "Brain knowledge collection (default: source ID)")
	cmd.Flags().StringVar(&embeddingModel, "embedding-model", "", "Local embedding model (default: configured embed alias)")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("root")
	return cmd
}

func newKnowledgeSourceListCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered knowledge source metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := home.Default()
			if err != nil {
				return err
			}
			config, err := layout.LoadConfig()
			if err != nil {
				return err
			}
			if jsonOutput {
				items := make([]knowledgeSourceSummary, 0, len(config.KnowledgeSources))
				for _, source := range config.KnowledgeSources {
					items = append(items, knowledgeSourceSummary{ID: source.ID, Name: source.Name, Collection: source.Collection, EmbeddingModel: source.EmbeddingModel})
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
			}
			for _, source := range config.KnowledgeSources {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", source.ID, source.Name, source.Collection); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output source metadata as JSON (root paths omitted)")
	return cmd
}

type knowledgeSourceSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Collection     string `json:"collection"`
	EmbeddingModel string `json:"embedding_model"`
}

func newKnowledgeSourceRemoveCmd() *cobra.Command {
	var id, confirmation string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Unregister a knowledge source without deleting its collection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if id == "" || confirmation != id {
				return fmt.Errorf("source removal requires --confirm to match --id")
			}
			layout, err := home.Default()
			if err != nil {
				return err
			}
			config, err := layout.LoadConfig()
			if err != nil {
				return err
			}
			filtered := make([]home.KnowledgeSource, 0, len(config.KnowledgeSources))
			removed := false
			for _, source := range config.KnowledgeSources {
				if source.ID == id {
					removed = true
					continue
				}
				filtered = append(filtered, source)
			}
			if !removed {
				return fmt.Errorf("knowledge source %q not found", id)
			}
			config.KnowledgeSources = filtered
			if err := layout.SaveConfig(config); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unregistered %s; its indexed collection remains unchanged.\n", id)
			return err
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Knowledge source ID")
	cmd.Flags().StringVar(&confirmation, "confirm", "", "Must exactly match --id")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("confirm")
	return cmd
}

func canonicalKnowledgeRoot(layout home.Layout, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("knowledge source root is required")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve knowledge source root: %w", err)
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve knowledge source root: %w", err)
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("knowledge source root must be an existing directory")
	}
	if filepath.Dir(root) == root {
		return "", errors.New("filesystem root cannot be registered as a knowledge source")
	}
	homeRoot, err := filepath.Abs(layout.Root)
	if err != nil {
		return "", err
	}
	if resolvedHome, err := filepath.EvalSymlinks(homeRoot); err == nil {
		homeRoot = filepath.Clean(resolvedHome)
	} else {
		homeRoot = filepath.Clean(homeRoot)
	}
	if pathContains(root, homeRoot) || pathContains(homeRoot, root) {
		return "", errors.New("knowledge source must not contain or be inside Brain's private home")
	}
	return root, nil
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/forge"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/skills"
	"github.com/spf13/cobra"
)

// SkillsDeps provides dependencies for the skills subcommands.
type SkillsDeps struct {
	ResolveHome    func() (home.Layout, error)
	ResolveAdapter func() (backend.Adapter, error)
}

func defaultSkillsDeps() SkillsDeps {
	return SkillsDeps{
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
	}
}

func newSkillsCmd(deps SkillsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage and inspect Agent Skills",
		Long: `skills inspects, validates, adds, and routes reusable Agent Skills
according to the open Agent Skills specification (SKILL.md) and progressive disclosure.`,
	}

	cmd.AddCommand(newSkillsListCmd(deps))
	cmd.AddCommand(newSkillsShowCmd(deps))
	cmd.AddCommand(newSkillsValidateCmd())
	cmd.AddCommand(newSkillsAddCmd(deps))
	cmd.AddCommand(newSkillsPromoteCmd(deps))
	cmd.AddCommand(newSkillsRollbackCmd(deps))
	cmd.AddCommand(newSkillsExportCmd(deps))

	return cmd
}

func newSkillsListCmd(deps SkillsDeps) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List discovered Agent Skills across project, user, and configured sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := resolveSkillsManager(deps)
			if err != nil {
				return err
			}

			list, err := mgr.List()
			if err != nil {
				return fmt.Errorf("list skills: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}

			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No skills found. Add skills to .agents/skills/ or ~/.lokal/skills/.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %-10s %s\n", "NAME", "SOURCE", "PACK", "DESCRIPTION")
			for _, s := range list {
				pack := s.Pack
				if pack == "" {
					pack = "-"
				}
				desc := s.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %-10s %s\n", s.Name, s.Source, pack, desc)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func newSkillsShowCmd(deps SkillsDeps) *cobra.Command {
	var (
		showBody   bool
		refFile    string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details, instructions, or references for a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mgr, err := resolveSkillsManager(deps)
			if err != nil {
				return err
			}

			if refFile != "" {
				content, err := mgr.GetReference(name, refFile)
				if err != nil {
					return err
				}
				if jsonOutput {
					data := map[string]string{
						"skill":     name,
						"reference": refFile,
						"content":   content,
					}
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(data)
				}
				fmt.Fprint(cmd.OutOrStdout(), content)
				return nil
			}

			sk, err := mgr.GetSkill(name)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(sk)
			}

			if showBody {
				fmt.Fprintln(cmd.OutOrStdout(), sk.Body)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Name:        %s\n", sk.Summary.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Source:      %s\n", sk.Summary.Source)
			fmt.Fprintf(cmd.OutOrStdout(), "Path:        %s\n", sk.Summary.Path)
			if sk.Summary.Pack != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Pack:        %s\n", sk.Summary.Pack)
			}
			if len(sk.Summary.Keywords) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Keywords:    %s\n", strings.Join(sk.Summary.Keywords, ", "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Description: %s\n", sk.Summary.Description)
			if len(sk.References) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "References:  %s\n", strings.Join(sk.References, ", "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n--- Body Instructions ---\n%s\n", sk.Body)

			return nil
		},
	}

	cmd.Flags().BoolVar(&showBody, "body", false, "Output only the Markdown instructions body")
	cmd.Flags().StringVar(&refFile, "ref", "", "Show a specific reference file from references/")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newSkillsValidateCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate a skill folder or SKILL.md against the Agent Skills specification",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			res := skills.ValidateDir(path)

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(res)
			} else {
				if res.Valid {
					fmt.Fprintf(cmd.OutOrStdout(), "Valid: %s satisfies Agent Skills specification.\n", path)
				} else {
					fmt.Fprintf(cmd.OutOrStderr(), "Validation failed for %s:\n", path)
					for _, err := range res.Errors {
						fmt.Fprintf(cmd.OutOrStderr(), "  - [%s] %s\n", err.Field, err.Message)
					}
				}
			}

			if !res.Valid {
				return &ExitCodeError{Code: 1}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output validation result as JSON")
	return cmd
}

func newSkillsAddCmd(deps SkillsDeps) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Install a validated skill into ~/.lokal/skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home: %w", err)
			}

			mgr := skills.NewManager("", layout.Skills, nil)
			added, err := mgr.AddSkill(path)
			if err != nil {
				return fmt.Errorf("add skill: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(added)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully installed skill %q to %s\n", added.Summary.Name, added.Summary.Path)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func resolveSkillsManager(deps SkillsDeps) (*skills.Manager, error) {
	layout, err := deps.ResolveHome()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}

	cwd, _ := os.Getwd()
	var configuredDirs []string

	if cfg, err := layout.LoadConfig(); err == nil && cfg != nil {
		configuredDirs = cfg.SkillSources
	}

	if layout.Skills == "" {
		return nil, errors.New("lokal home skills path is not set")
	}

	return skills.NewManager(cwd, layout.Skills, configuredDirs), nil
}

func newSkillsPromoteCmd(deps SkillsDeps) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "promote <candidate-name>",
		Short: "Promote an evaluated candidate skill to user skills",
		Long: `promote verifies evaluation benchmarks, performs safety scans, versions
by content hash, and promotes a skill from ~/.lokal/candidates/ to ~/.lokal/skills/.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			candidateName := args[0]
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			if err := layout.Ensure(); err != nil {
				return fmt.Errorf("ensure home: %w", err)
			}

			promoter := forge.NewPromoter(layout.Candidates, layout.Skills)
			res, err := promoter.Promote(candidateName, force)
			if err != nil {
				return fmt.Errorf("promote skill: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully promoted %s (version: %s)\n", res.SkillName, res.ContentHash)
			fmt.Fprintf(cmd.OutOrStdout(), "Installed path: %s\n", res.TargetDir)
			if res.BackupPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Previous version backed up to: %s\n", res.BackupPath)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "force promotion without evaluation report check")
	return cmd
}

func newSkillsRollbackCmd(deps SkillsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback <skill-name>",
		Short: "Rollback a promoted skill to its previous backup version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			skillName := args[0]
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			promoter := forge.NewPromoter(layout.Candidates, layout.Skills)
			backupPath, err := promoter.Rollback(skillName)
			if err != nil {
				return fmt.Errorf("rollback skill: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully rolled back %s from backup %s\n", skillName, backupPath)
			return nil
		},
	}
	return cmd
}

func newSkillsExportCmd(deps SkillsDeps) *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:   "export <skill-name>",
		Short: "Export a skill formatted for Agent-Skills repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			skillName := args[0]
			layout, err := deps.ResolveHome()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}

			if outDir == "" {
				outDir = "./exported-skills"
			}

			promoter := forge.NewPromoter(layout.Candidates, layout.Skills)
			destPath, err := promoter.Export(skillName, outDir)
			if err != nil {
				return fmt.Errorf("export skill: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully exported skill %s to %s\n", skillName, destPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "destination directory (defaults to ./exported-skills)")
	return cmd
}

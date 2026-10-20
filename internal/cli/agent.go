package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hi-donwi/Agent-Brain/internal/agent"
	"github.com/spf13/cobra"
)

// Agent commands inspect contracts; they never execute a repository tool.
func newAgentCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{Use: "agent", Short: "Inspect project instructions, tool permissions, and sub-agent definitions"}
	cmd.PersistentFlags().StringVar(&root, "root", ".", "Project root; all project paths remain inside it")
	var dir string
	instructions := &cobra.Command{Use: "instructions", Short: "Read applicable project instructions", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		in, err := agent.LoadInstructions(root, dir)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]interface{}{"instructions": in, "rendered": in.Render()})
	}}
	instructions.Flags().StringVar(&dir, "directory", ".", "Working directory relative to root")
	var tool, path, command, policyFile, role string
	var definitions []string
	check := &cobra.Command{Use: "check", Short: "Decide a tool request without executing it", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		p := agent.Policy{Root: abs}
		if policyFile != "" {
			info, err := os.Stat(policyFile)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > 96*1024 {
				return fmt.Errorf("policy must be a regular file no larger than 96 KiB")
			}
			data, err := os.ReadFile(policyFile)
			if err != nil {
				return err
			}
			p, err = agent.ParsePolicy(data)
			if err != nil {
				return err
			}
			p.Root = abs
		}
		if role != "" {
			reg, err := agent.LoadProjectRegistry(abs, definitions...)
			if err != nil {
				return err
			}
			d, ok := reg.Get(role)
			if !ok {
				return fmt.Errorf("unknown sub-agent %q", role)
			}
			p = d.Effective(p)
		}
		decision := p.Decide(agent.Request{Tool: tool, Path: path, Command: command})
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]interface{}{"decision": decision, "executed": false})
	}}
	check.Flags().StringVar(&tool, "tool", "", "Tool name to judge")
	_ = check.MarkFlagRequired("tool")
	check.Flags().StringVar(&path, "path", "", "Requested project path")
	check.Flags().StringVar(&command, "command", "", "Requested shell command; never executed")
	check.Flags().StringVar(&policyFile, "policy", "", "Operator-selected trusted YAML policy")
	check.Flags().StringVar(&role, "role", "", "Restrict the decision to this sub-agent")
	check.Flags().StringSliceVar(&definitions, "definitions", nil, "Definition directories, most trusted first")
	var registryDirs []string
	subagents := &cobra.Command{Use: "subagents", Short: "Inspect available sub-agents without spawning them", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		reg, err := agent.LoadProjectRegistry(root, registryDirs...)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]interface{}{"definitions": reg.List(), "conflicts": reg.Conflicts, "errors": reg.Errors})
	}}
	subagents.Flags().StringSliceVar(&registryDirs, "definitions", nil, "Definition directories, most trusted first")
	cmd.AddCommand(instructions, check, subagents)
	return cmd
}

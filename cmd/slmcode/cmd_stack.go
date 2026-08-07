package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/stacks"
)

func stackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stack",
		Short: "List / show / apply provider+model presets (stacks/*.yaml)",
		Long: cli.Dim(`Stacks set global provider/model/harness defaults. Per-agent overrides in
.slmcode/agents/ still win at runtime (empty model/provider = inherit stack).

Examples:
  slmcode stack list
  slmcode stack show omlx-local
  slmcode stack apply deepseek
  slmcode stack apply ollama-local --clear-agent-llm
  slmcode stack apply openai --agents`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return stackList(cmd, args)
		},
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List available stacks", RunE: stackList},
		&cobra.Command{
			Use:   "show [name]",
			Short: "Print a stack YAML",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := stacks.Load(args[0])
				if err != nil {
					return err
				}
				data, err := os.ReadFile(st.Path)
				if err != nil {
					return err
				}
				cli.Header("stack:" + st.ID)
				cli.KeyVal("provider", st.Provider)
				cli.KeyVal("model", st.Model)
				cli.KeyVal("endpoint", st.Endpoint)
				if len(st.Agents) > 0 {
					var roles []string
					for role := range st.Agents {
						roles = append(roles, role)
					}
					cli.KeyVal("agent_defaults", strings.Join(roles, ", "))
				}
				fmt.Println()
				fmt.Print(string(data))
				return nil
			},
		},
	)

	var clearAgentLLM, applyAgents, forceAgents bool
	apply := &cobra.Command{
		Use:   "apply [name]",
		Short: "Merge stack into .slmcode/config.yaml (keeps listen/skills/mcp)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			st, err := stacks.Load(args[0])
			if err != nil {
				return err
			}
			res, err := stacks.Apply(ws.Config, st, ws.Config.AgentsDir(), stacks.ApplyOptions{
				ClearAgentLLM:      clearAgentLLM,
				ApplyAgentDefaults: applyAgents,
				ForceAgents:        forceAgents,
			})
			if err != nil {
				return err
			}
			if err := ws.Config.Save(); err != nil {
				return err
			}
			cli.Header("stack applied: " + res.StackID)
			cli.KeyVal("provider", res.Provider)
			cli.KeyVal("model", res.Model)
			cli.KeyVal("endpoint", res.Endpoint)
			if len(res.AgentsUpdated) > 0 {
				cli.KeyVal("agents_updated", strings.Join(res.AgentsUpdated, ", "))
			}
			if len(res.AgentsCleared) > 0 {
				cli.KeyVal("agents_cleared", strings.Join(res.AgentsCleared, ", "))
			}
			if len(res.ConflictingAgents) > 0 {
				fmt.Println()
				fmt.Println(cli.Dim("  Note: these agents still pin provider/model (override stack):"))
				fmt.Println(cli.Dim("    " + strings.Join(res.ConflictingAgents, ", ")))
				fmt.Println(cli.Dim("  Re-run with --clear-agent-llm to inherit the new stack."))
			}
			fmt.Println()
			fmt.Println(cli.Dim("  Resolution: agent.model ?? stack/global.model"))
			fmt.Println(cli.Dim("              agent.provider ?? stack/global.provider"))
			return nil
		},
	}
	apply.Flags().BoolVar(&clearAgentLLM, "clear-agent-llm", false, "clear per-agent model/provider/endpoint so agents inherit stack")
	apply.Flags().BoolVar(&applyAgents, "agents", false, "apply optional stack agents: role defaults into .slmcode/agents/")
	apply.Flags().BoolVar(&forceAgents, "force-agents", false, "overwrite existing per-agent LLM pins when using --agents")
	cmd.AddCommand(apply)
	return cmd
}

func stackList(cmd *cobra.Command, args []string) error {
	list, err := stacks.List()
	if err != nil {
		return err
	}
	cli.Header("Stacks")
	dir := stacks.FindDir()
	fmt.Println(cli.Dim("  " + dir))
	fmt.Println()
	for _, s := range list {
		agentN := ""
		if len(s.Agents) > 0 {
			agentN = fmt.Sprintf("  [%d agent defaults]", len(s.Agents))
		}
		fmt.Printf("  %s  %-12s  %s%s\n",
			cli.Accent(fmt.Sprintf("%-18s", s.ID)),
			cli.Dim(s.Provider),
			cli.Dim(s.Model),
			cli.Dim(agentN),
		)
	}
	fmt.Println()
	fmt.Println(cli.Dim("  Apply:  slmcode stack apply <name>"))
	fmt.Println(cli.Dim("  Show:   slmcode stack show <name>"))
	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
)

func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "List / show / edit specialist agents (effective LLM after stack inheritance)",
		Long: cli.Dim(`Per-agent provider/model/endpoint overrides live in .slmcode/agents/.
Empty fields inherit the active stack / global config.

Bulk role pins:  slmcode stack apply <name> --agents
Clear pins:      slmcode stack apply <name> --clear-agent-llm`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentList(cmd, args)
		},
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List agents with effective LLM", RunE: agentList},
		&cobra.Command{
			Use:   "show [id]",
			Short: "Show one agent (includes effective_model)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := openWorkspace()
				if err != nil {
					return err
				}
				custom, _ := cli.LoadProjectCustoms(ws.Config.AgentsDir())
				a := agents.AgentDetail(args[0], custom)
				if a == nil {
					return fmt.Errorf("agent %q not found", args[0])
				}
				enriched := agents.EnrichPublicSpecs(
					[]map[string]interface{}{a},
					config.NormalizeProvider(ws.Config.Provider),
					ws.Config.Model,
					ws.Config.ActiveStack,
				)
				cli.Header("agent:" + args[0])
				fmt.Print(cli.FormatAgentShow(enriched[0]))
				return nil
			},
		},
		&cobra.Command{
			Use:     "edit [id] [key=value…]",
			Short:   "Patch agent fields (model= provider= endpoint= …); no fields = interactive form",
			Args:    cobra.MinimumNArgs(1),
			Example: "  slmcode agent edit worker model=qwen2.5-coder:32b\n  slmcode agent edit worker            # interactive form",
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := openWorkspace()
				if err != nil {
					return err
				}
				id := strings.ToLower(strings.TrimSpace(args[0]))
				fields := map[string]string{}
				for _, a := range args[1:] {
					k, v, ok := strings.Cut(a, "=")
					if !ok {
						continue
					}
					fields[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
				}
				path := filepath.Join(ws.Config.AgentsDir(), id+".yaml")
				var base agents.CustomSpec
				if got, rerr := agents.ReadCustomFile(path); rerr == nil {
					base = got
				} else {
					base.ID = id
					if agents.BuiltinIDs()[id] {
						base.Override = true
						base.Builtin = true
					}
				}
				if len(fields) == 0 {
					// This command runs in cooked mode, so the guided form is
					// usable here (the TUI's /agent uses inline fields instead).
					if !cli.IsInteractive() {
						return failf(2, "usage: slmcode agent edit <id> model=… provider=… endpoint=…")
					}
					filled, ferr := cli.PromptAgentForm(os.Stdin, os.Stdout, base, false)
					if ferr != nil {
						return ferr
					}
					base = filled
				} else {
					applyAgentFields(&base, fields)
				}
				if _, err := agents.WriteCustom(ws.Config.AgentsDir(), base); err != nil {
					return err
				}
				cli.Header("updated " + id)
				custom, _ := cli.LoadProjectCustoms(ws.Config.AgentsDir())
				a := agents.AgentDetail(id, custom)
				enriched := agents.EnrichPublicSpecs(
					[]map[string]interface{}{a},
					config.NormalizeProvider(ws.Config.Provider),
					ws.Config.Model,
					ws.Config.ActiveStack,
				)
				fmt.Print(cli.FormatAgentShow(enriched[0]))
				fmt.Println(cli.Dim("  Restart studio/tui or rebuild to pick up LLM changes."))
				return nil
			},
		},
		&cobra.Command{
			Use:   "clear-llm [id]",
			Short: "Clear model/provider/endpoint so the agent inherits the stack",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := openWorkspace()
				if err != nil {
					return err
				}
				id := strings.ToLower(strings.TrimSpace(args[0]))
				path := filepath.Join(ws.Config.AgentsDir(), id+".yaml")
				got, err := agents.ReadCustomFile(path)
				if err != nil {
					return fmt.Errorf("no override file for %q — already inheriting", id)
				}
				got.Model = ""
				got.Provider = ""
				got.Endpoint = ""
				if _, err := agents.WriteCustom(ws.Config.AgentsDir(), got); err != nil {
					return err
				}
				fmt.Println(cli.Success(id + " now inherits stack/global LLM"))
				return nil
			},
		},
	)
	return cmd
}

func agentList(cmd *cobra.Command, args []string) error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	custom, _ := cli.LoadProjectCustoms(ws.Config.AgentsDir())
	cli.Header("Agents")
	if ws.Config.ActiveStack != "" {
		cli.KeyVal("active_stack", ws.Config.ActiveStack)
	}
	cli.KeyVal("global", config.NormalizeProvider(ws.Config.Provider)+"/"+ws.Config.Model)
	fmt.Println()
	fmt.Print(cli.FormatAgentListWithGlobals(custom, config.NormalizeProvider(ws.Config.Provider), ws.Config.Model))
	return nil
}

func applyAgentFields(c *agents.CustomSpec, fields map[string]string) {
	for k, v := range fields {
		switch k {
		case "title":
			c.Title = v
		case "description":
			c.Description = v
		case "model":
			c.Model = v
		case "provider":
			c.Provider = v
		case "endpoint":
			c.Endpoint = v
		case "system_prompt", "prompt":
			c.SystemPrompt = v
		case "skills":
			var skills []string
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					skills = append(skills, s)
				}
			}
			c.Skills = skills
		}
	}
}

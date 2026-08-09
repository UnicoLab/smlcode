package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/cli"
)

func blockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blocks",
		Short: "List / show / apply / validate building blocks (pipelines, agents, quality, packs)",
		Long: cli.Dim(`Building blocks are reusable YAML presets: pipelines, specialist agents,
quality-check packs, and language packs that compose them.

Examples:
  slmcode blocks list
  slmcode blocks show pipeline go
  slmcode blocks show agent go-worker
  slmcode blocks show pack go
  slmcode blocks apply go
  slmcode blocks apply go --materialize-agents
  slmcode blocks apply go --force
  slmcode blocks validate`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockList(cmd, args)
		},
	}

	cmd.AddCommand(
		&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List available building blocks", RunE: blockList},
		&cobra.Command{
			Use:   "show [kind] [id]",
			Short: "Show details of a specific block (pipeline|agent|quality|pack)",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return blockShow(args[0], args[1])
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Load and validate all block YAML configs",
			RunE: func(cmd *cobra.Command, args []string) error {
				return blockValidate()
			},
		},
	)

	var materializeAgents, forceAgents bool
	applyCmd := &cobra.Command{
		Use:   "apply [pack-id]",
		Short: "Apply a language pack to the project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockApply(args[0], materializeAgents, forceAgents)
		},
	}
	applyCmd.Flags().BoolVar(&materializeAgents, "materialize-agents", false, "write agent YAMLs to .slmcode/agents/")
	applyCmd.Flags().BoolVar(&forceAgents, "force", false, "overwrite existing agent files")
	cmd.AddCommand(applyCmd)

	return cmd
}

func blockList(cmd *cobra.Command, args []string) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	reg, err := blocks.Load(root)
	if err != nil {
		return err
	}

	entries := reg.Catalog("")
	cli.Header("Building Blocks")

	groups := map[string][]blocks.CatalogEntry{}
	kinds := []string{blocks.KindPack, blocks.KindPipeline, blocks.KindAgent, blocks.KindQuality}
	for _, k := range kinds {
		groups[k] = nil
	}
	for _, e := range entries {
		groups[e.Kind] = append(groups[e.Kind], e)
	}

	for _, kind := range kinds {
		list := groups[kind]
		if len(list) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(cli.Bold("  " + strings.ToUpper(kind) + "S"))
		for _, e := range list {
			sourceTag := cli.Dim("  [builtin]")
			if e.Custom {
				sourceTag = cli.Dim("  [custom]")
			}
			lang := ""
			if e.Language != "" {
				lang = cli.Dim(fmt.Sprintf("  (%s)", e.Language))
			}
			fmt.Printf("  %s  %s%s%s\n",
				cli.Accent(fmt.Sprintf("%-24s", e.ID)),
				cli.Dim(e.Name),
				sourceTag,
				lang,
			)
		}
	}

	fmt.Println()
	fmt.Println(cli.Dim("  Show:   slmcode blocks show <pipeline|agent|quality|pack> <id>"))
	fmt.Println(cli.Dim("  Apply:  slmcode blocks apply <pack-id>"))
	fmt.Println(cli.Dim("  Validate: slmcode blocks validate"))
	return nil
}

func blockShow(kind, id string) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	id = strings.ToLower(strings.TrimSpace(id))

	switch kind {
	case "pipeline", "pipelines":
		kind = blocks.KindPipeline
	case "agent", "agents":
		kind = blocks.KindAgent
	case "quality", "qualities":
		kind = blocks.KindQuality
	case "pack", "packs":
		kind = blocks.KindPack
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	reg, err := blocks.Load(root)
	if err != nil {
		return err
	}

	switch kind {
	case blocks.KindPipeline:
		b, ok := reg.GetPipeline(id)
		if !ok {
			return fmt.Errorf("pipeline %q not found", id)
		}
		printBlockMeta("pipeline:"+b.ID, b.Meta, b.Path)
		if b.Path != "" {
			data, err := os.ReadFile(b.Path)
			if err == nil {
				fmt.Println()
				fmt.Print(string(data))
			}
		}
	case blocks.KindAgent:
		b, ok := reg.GetAgent(id)
		if !ok {
			return fmt.Errorf("agent %q not found", id)
		}
		printBlockMeta("agent:"+b.ID, b.Meta, b.Path)
		if b.Path != "" {
			data, err := os.ReadFile(b.Path)
			if err == nil {
				fmt.Println()
				fmt.Print(string(data))
			}
		}
	case blocks.KindQuality:
		b, ok := reg.GetQuality(id)
		if !ok {
			return fmt.Errorf("quality %q not found", id)
		}
		printBlockMeta("quality:"+b.ID, b.Meta, b.Path)
		if b.Path != "" {
			data, err := os.ReadFile(b.Path)
			if err == nil {
				fmt.Println()
				fmt.Print(string(data))
			}
		}
	case blocks.KindPack:
		b, ok := reg.GetPack(id)
		if !ok {
			return fmt.Errorf("pack %q not found", id)
		}
		printBlockMeta("pack:"+b.ID, b.Meta, b.Path)
		cli.KeyVal("pipeline", b.Spec.Pipeline)
		cli.KeyVal("quality", b.Spec.Quality)
		if len(b.Spec.Agents) > 0 {
			cli.KeyVal("agents", strings.Join(b.Spec.Agents, ", "))
		}
		if len(b.Spec.Skills) > 0 {
			cli.KeyVal("skills", strings.Join(b.Spec.Skills, ", "))
			cli.KeyVal("pin_skills", fmt.Sprintf("%v", b.Spec.PinSkills))
		}
		if b.Spec.OverrideTester != "" {
			cli.KeyVal("override_tester", b.Spec.OverrideTester)
		}
		if b.Spec.OverrideWorker != "" {
			cli.KeyVal("override_worker", b.Spec.OverrideWorker)
		}
		if b.Path != "" {
			data, err := os.ReadFile(b.Path)
			if err == nil {
				fmt.Println()
				fmt.Print(string(data))
			}
		}
	default:
		return fmt.Errorf("unknown block kind %q (use: pipeline, agent, quality, pack)", kind)
	}
	return nil
}

func printBlockMeta(title string, m blocks.Meta, path string) {
	cli.Header(title)
	cli.KeyVal("name", m.Name)
	cli.KeyVal("version", m.Version)
	cli.KeyVal("author", m.Author)
	cli.KeyVal("license", m.License)
	if m.Language != "" {
		cli.KeyVal("language", m.Language)
	}
	if len(m.Tags) > 0 {
		cli.KeyVal("tags", strings.Join(m.Tags, ", "))
	}
	cli.KeyVal("source", m.Source)
	if m.Description != "" {
		fmt.Println()
		fmt.Println(cli.Dim("  " + m.Description))
	}
}

func blockApply(packID string, materializeAgents, forceAgents bool) error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	reg, err := blocks.Load(root)
	if err != nil {
		return err
	}

	res, err := blocks.ApplyPack(ws.Config, reg, packID, blocks.ApplyOptions{
		MaterializeAgents: materializeAgents,
		ForceAgents:       forceAgents,
	})
	if err != nil {
		return err
	}

	if err := ws.Config.Save(); err != nil {
		return err
	}

	cli.Header("pack applied: " + res.PackID)
	if res.PipelineID != "" {
		cli.KeyVal("pipeline", res.PipelineID)
		cli.KeyVal("pipeline_path", res.PipelinePath)
	}
	if res.QualityID != "" {
		cli.KeyVal("quality", res.QualityID)
	}
	if res.QAGateCommand != "" {
		cli.KeyVal("qa_gate", res.QAGateCommand)
	}
	if len(res.AgentsWritten) > 0 {
		cli.KeyVal("agents_written", strings.Join(res.AgentsWritten, ", "))
	}
	if len(res.SkillsPinned) > 0 {
		cli.KeyVal("skills_pinned", strings.Join(res.SkillsPinned, ", "))
	}
	fmt.Println()
	fmt.Println(cli.Dim("  Re-run studio/tui to pick up pack changes."))
	return nil
}

func blockValidate() error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	reg, err := blocks.Load(root)
	if err != nil {
		return err
	}

	entries := reg.Catalog("")
	if len(entries) == 0 {
		fmt.Println(cli.Warn("no blocks found"))
		return nil
	}

	cli.Header("Block Validation")

	var errors []string
	validCount := 0

	for _, e := range entries {
		var validateErr error
		switch e.Kind {
		case blocks.KindPipeline:
			b, ok := reg.GetPipeline(e.ID)
			if !ok {
				errors = append(errors, fmt.Sprintf("pipeline %q: not found in registry", e.ID))
				continue
			}
			validateErr = b.Validate()
		case blocks.KindAgent:
			b, ok := reg.GetAgent(e.ID)
			if !ok {
				errors = append(errors, fmt.Sprintf("agent %q: not found in registry", e.ID))
				continue
			}
			validateErr = b.Validate()
		case blocks.KindQuality:
			b, ok := reg.GetQuality(e.ID)
			if !ok {
				errors = append(errors, fmt.Sprintf("quality %q: not found in registry", e.ID))
				continue
			}
			validateErr = b.Validate()
		case blocks.KindPack:
			b, ok := reg.GetPack(e.ID)
			if !ok {
				errors = append(errors, fmt.Sprintf("pack %q: not found in registry", e.ID))
				continue
			}
			validateErr = b.Validate()
			if validateErr == nil {
				validateErr = reg.ResolvePackRefs(b)
			}
		}

		if validateErr != nil {
			errors = append(errors, fmt.Sprintf("%s %q: %v", e.Kind, e.ID, validateErr))
		} else {
			validCount++
		}
	}

	icon := "✔"
	if len(errors) > 0 {
		icon = "✖"
	}
	fmt.Printf("  %s %d valid, %d with errors\n", icon, validCount, len(errors))

	if len(errors) > 0 {
		fmt.Println()
		fmt.Println(cli.Bold("  Errors:"))
		for _, msg := range errors {
			fmt.Println(cli.Error("  " + msg))
		}
		return fmt.Errorf("%d block(s) failed validation", len(errors))
	}

	fmt.Println()
	fmt.Println(cli.Success("all blocks pass validation"))
	return nil
}

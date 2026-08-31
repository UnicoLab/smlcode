package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/cli"
)

func blockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blocks",
		Short: "Create / edit / delete / list / show / apply / validate building blocks (pipelines, agents, quality, packs)",
		Long: cli.Dim(`Building blocks are reusable YAML presets: pipelines, specialist agents,
quality-check packs, and language packs that compose them.

Create / edit / delete project blocks (saved to .slmcode/blocks/):
  slmcode blocks new agent my-agent --file agent.yaml
  slmcode blocks new agent my-agent --name "My Agent"
  slmcode blocks edit agent my-agent --file agent.yaml
  slmcode blocks delete agent my-agent

Inspect and apply:
  slmcode blocks list
  slmcode blocks show pipeline go
  slmcode blocks show agent go-worker
  slmcode blocks show pack go
  slmcode blocks apply go
  slmcode blocks apply go --materialize-agents
  slmcode blocks apply go --force
  slmcode blocks validate`),
		Example: "  slmcode blocks list\n  slmcode blocks show pack go\n  slmcode blocks apply python",
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockList(cmd, args)
		},
	}

	cmd.AddCommand(
		blockListCmd(),
		&cobra.Command{
			Use:   "show [kind] [id]",
			Short: "Show details of a specific block (pipeline|agent|quality|pack)",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return blockShow(args[0], args[1])
			},
		},
	)

	var newFile, newName string
	newCmd := &cobra.Command{
		Use:     "new <kind> <id>",
		Aliases: []string{"create", "add"},
		Short:   "Create a new project block from a YAML file (--name scaffolds a minimal agent)",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockNew(args[0], args[1], newFile, newName)
		},
	}
	newCmd.Flags().StringVar(&newFile, "file", "", "path to the block YAML to save")
	newCmd.Flags().StringVar(&newName, "name", "", "display name for a --file-less agent scaffold")

	var editFile string
	editCmd := &cobra.Command{
		Use:   "edit <kind> <id>",
		Short: "Update a project block from a YAML file (creates an override for builtins)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockEdit(args[0], args[1], editFile)
		},
	}
	editCmd.Flags().StringVar(&editFile, "file", "", "path to the block YAML to save")

	delCmd := &cobra.Command{
		Use:   "delete <kind> <id>",
		Short: "Delete a project block (builtin blocks are protected)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return blockDelete(args[0], args[1])
		},
	}
	cmd.AddCommand(newCmd, editCmd, delCmd)
	cmd.AddCommand(
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

func blockListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available building blocks",
		Example: "  slmcode blocks list\n  slmcode blocks list --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			if asJSON {
				root, err := projectRoot()
				if err != nil {
					return err
				}
				reg, err := blocks.Load(root)
				if err != nil {
					return err
				}
				return emitJSON(map[string]any{"blocks": reg.Catalog("")})
			}
			return blockList(cmd, args)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
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
	kinds := []string{blocks.KindPack, blocks.KindPipeline, blocks.KindAgent, blocks.KindQuality, blocks.KindTeam}
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
	fmt.Println(cli.Dim("  New:     slmcode blocks new <kind> <id> --file <path.yaml>"))
	fmt.Println(cli.Dim("  Edit:    slmcode blocks edit <kind> <id> --file <path.yaml>"))
	fmt.Println(cli.Dim("  Delete:  slmcode blocks delete <kind> <id>"))
	fmt.Println(cli.Dim("  Show:    slmcode blocks show <pipeline|agent|quality|pack|team> <id>"))
	fmt.Println(cli.Dim("  Apply:   slmcode blocks apply <pack-id>"))
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
	case "team", "teams":
		kind = blocks.KindTeam
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
	case blocks.KindTeam:
		b, ok := reg.GetTeam(id)
		if !ok {
			return fmt.Errorf("team %q not found", id)
		}
		printBlockMeta("team:"+b.ID, b.Meta, b.Path)
		cli.KeyVal("owns", strings.Join(b.Spec.Owns, ", "))
		cli.KeyVal("acceptance", b.Spec.Acceptance)
		for label, id := range map[string]string{
			"worker": b.Spec.Worker, "reviewer": b.Spec.Reviewer,
			"tester": b.Spec.Tester, "manager": b.Spec.Manager,
		} {
			if id != "" {
				cli.KeyVal(label, id)
			}
		}
		if b.Path != "" {
			data, err := os.ReadFile(b.Path)
			if err == nil {
				fmt.Println()
				fmt.Print(string(data))
			}
		}
	default:
		return fmt.Errorf("unknown block kind %q (use: pipeline, agent, quality, pack, team)", kind)
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

// blockNew creates a project-level block from a YAML file (any kind) or a
// --name scaffold (agents only). Mirrors the Studio POST /api/blocks/{kind}.
func blockNew(kind, id, filePath, name string) error {
	kind = blocks.NormalizeKind(kind)
	if kind == "" {
		return fmt.Errorf("unknown block kind %q (use: pipeline, agent, quality, pack, team)", kind)
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if filePath == "" && name == "" {
		return fmt.Errorf("blocks new requires --file <path.yaml> or --name (agent scaffold)")
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}

	var block any
	if filePath != "" {
		block, id, err = readBlockFile(kind, id, filePath)
		if err != nil {
			return err
		}
	} else {
		if kind != blocks.KindAgent {
			return fmt.Errorf("scaffolding without --file is only supported for agent blocks — pass --file <path.yaml> for %s blocks", kind)
		}
		block = scaffoldAgentBlock(id, name)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		return err
	}
	if projectBlockExists(reg, kind, id) {
		return fmt.Errorf("%s block %q already exists in the project — use 'slmcode blocks edit' instead", kind, id)
	}

	path, err := blocks.Save(root, block)
	if err != nil {
		return err
	}
	fmt.Println(cli.Success(fmt.Sprintf("%s block %q saved", kind, id)))
	cli.KeyVal("path", path)
	return nil
}

// blockEdit overwrites a project block from a YAML file (creating an override
// for builtins) and materializes agent specs so the runtime picks them up
// immediately. Mirrors the Studio PUT /api/blocks/{kind}/{id}.
func blockEdit(kind, id, filePath string) error {
	kind = blocks.NormalizeKind(kind)
	if kind == "" {
		return fmt.Errorf("unknown block kind %q (use: pipeline, agent, quality, pack, team)", kind)
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if filePath == "" {
		return fmt.Errorf("blocks edit requires --file <path.yaml>")
	}

	block, fileID, err := readBlockFile(kind, id, filePath)
	if err != nil {
		return err
	}
	if fileID != id {
		return fmt.Errorf("block id %q in %s does not match %q", fileID, filePath, id)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	path, err := blocks.Save(root, block)
	if err != nil {
		return err
	}

	// Mirrors the GUI: write the spec to .slmcode/agents/ so the override is live.
	agentPath := ""
	if kind == blocks.KindAgent {
		ws, err := openWorkspace()
		if err != nil {
			return fmt.Errorf("block saved but agent materialization failed: %w", err)
		}
		agentPath, err = agents.WriteCustom(ws.Config.AgentsDir(), block.(*blocks.AgentBlock).Spec)
		if err != nil {
			return fmt.Errorf("block saved but agent materialization failed: %w", err)
		}
	}

	fmt.Println(cli.Success(fmt.Sprintf("%s block %q saved", kind, id)))
	cli.KeyVal("path", path)
	if agentPath != "" {
		cli.KeyVal("agent_override", agentPath)
	}
	return nil
}

// blockDelete removes a project block and any materialized agent override.
// Mirrors the Studio DELETE /api/blocks/{kind}/{id}.
func blockDelete(kind, id string) error {
	kind = blocks.NormalizeKind(kind)
	if kind == "" {
		return fmt.Errorf("unknown block kind %q (use: pipeline, agent, quality, pack, team)", kind)
	}
	id = strings.ToLower(strings.TrimSpace(id))

	root, err := projectRoot()
	if err != nil {
		return err
	}
	found, err := blocks.Delete(root, kind, id)
	if err != nil {
		return err
	}

	// Best-effort: drop the materialized agent override too (.yaml or .yml).
	if kind == blocks.KindAgent {
		agentsDir := filepath.Join(root, ".slmcode", "agents")
		for _, ext := range []string{".yaml", ".yml"} {
			p := filepath.Join(agentsDir, id+ext)
			if _, statErr := os.Stat(p); statErr != nil {
				if !os.IsNotExist(statErr) {
					fmt.Println(cli.Warn(fmt.Sprintf("cannot inspect agent file %s: %v", p, statErr)))
				}
				continue
			}
			if rmErr := agents.DeleteCustom(agentsDir, id); rmErr != nil {
				fmt.Println(cli.Warn(fmt.Sprintf("cannot remove agent file %s: %v", p, rmErr)))
			}
			break
		}
	}

	fmt.Println(cli.Success(fmt.Sprintf("%s block %q deleted", kind, id)))
	if !found {
		fmt.Println(cli.Dim("  (no project file existed)"))
	}
	return nil
}

// readBlockFile reads a block YAML, checks its kind against the CLI arg, and
// parses + validates it. The file's own id wins over the CLI arg (a missing
// id falls back to the arg). Returns the typed block and its canonical id.
func readBlockFile(kind, argID, filePath string) (any, string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, "", err
	}
	var meta blocks.Meta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, "", fmt.Errorf("%s: %w", filePath, err)
	}
	if fileKind := blocks.NormalizeKind(meta.Kind); fileKind != "" && fileKind != kind {
		return nil, "", fmt.Errorf("%s: kind %q does not match %q", filePath, meta.Kind, kind)
	}
	if strings.TrimSpace(meta.ID) == "" {
		// No id in the file — inject the CLI id so validation sees a complete block.
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err == nil {
			doc["id"] = argID
			if out, err := yaml.Marshal(doc); err == nil {
				data = out
			}
		}
	}
	block, err := blocks.ParseAndValidateBlock(kind, data)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", filePath, err)
	}
	return block, blockMetaOf(block).ID, nil
}

// blockMetaOf returns the shared Meta of any typed block pointer.
func blockMetaOf(block any) *blocks.Meta {
	switch b := block.(type) {
	case *blocks.PipelineBlock:
		return &b.Meta
	case *blocks.AgentBlock:
		return &b.Meta
	case *blocks.QualityBlock:
		return &b.Meta
	case *blocks.PackBlock:
		return &b.Meta
	default:
		return nil
	}
}

// scaffoldAgentBlock builds a minimal working custom agent. Normalization
// fills the remaining defaults (generic system prompt, tools on, max_iter 10,
// temperature 0.2, max_tokens 2048).
func scaffoldAgentBlock(id, name string) *blocks.AgentBlock {
	b := &blocks.AgentBlock{}
	b.Kind = blocks.KindAgent
	b.ID = id
	b.Spec.ID = id
	if strings.TrimSpace(name) != "" {
		b.Name = name
		b.Spec.Title = name
	}
	return b
}

// projectBlockExists reports whether a project-level block with the id already
// exists in .slmcode/blocks. Builtins and user/extra blocks may be overridden
// with 'blocks new' / 'blocks edit', so only project blocks block creation.
func projectBlockExists(reg *blocks.Registry, kind, id string) bool {
	switch kind {
	case blocks.KindPipeline:
		b, ok := reg.GetPipeline(id)
		return ok && b.Source == blocks.SourceProject
	case blocks.KindAgent:
		b, ok := reg.GetAgent(id)
		return ok && b.Source == blocks.SourceProject
	case blocks.KindQuality:
		b, ok := reg.GetQuality(id)
		return ok && b.Source == blocks.SourceProject
	case blocks.KindPack:
		b, ok := reg.GetPack(id)
		return ok && b.Source == blocks.SourceProject
	default:
		return false
	}
}

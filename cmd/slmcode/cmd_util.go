package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/readiness"
	"github.com/UnicoLab/slmcode/pkg/retrieval"
	"github.com/UnicoLab/slmcode/pkg/skills"
)

func skillsCmd() *cobra.Command {
	listFn := func(cmd *cobra.Command, args []string) error {
		ws, err := openWorkspace()
		if err != nil {
			return err
		}
		list, err := ws.Skills.List()
		if err != nil {
			return err
		}
		cli.Header("Skills")
		if len(list) == 0 {
			fmt.Println(cli.Dim("  (none — add .slmcode/skills/<name>/SKILL.md)"))
			return nil
		}
		for _, s := range list {
			agents := "*"
			if len(s.Agents) > 0 {
				agents = strings.Join(s.Agents, ",")
			}
			fmt.Printf("  %s  %-10s  %s\n",
				cli.Accent(fmt.Sprintf("%-24s", s.Name)),
				cli.Dim(agents),
				cli.Dim(s.Description))
		}
		fmt.Println()
		fmt.Println(cli.Dim("  Reference in queries: @skill:name   or   /skill name"))
		fmt.Println(cli.Dim("  Project skills:       .slmcode/skills/<name>/SKILL.md"))
		return nil
	}

	cmd := &cobra.Command{Use: "skills", Short: "List / show / create / edit skills (Claude Code–style)", RunE: listFn}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List skills", RunE: listFn})

	cmd.AddCommand(&cobra.Command{
		Use:   "show [name]",
		Short: "Print a skill (frontmatter + body)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			sk, ok := ws.Skills.Get(args[0])
			if !ok {
				return fmt.Errorf("skill %q not found — try: slmcode skills list", args[0])
			}
			cli.Header("skill:" + sk.Name)
			cli.KeyVal("description", sk.Description)
			cli.KeyVal("agents", strings.Join(sk.Agents, ", "))
			cli.KeyVal("triggers", strings.Join(sk.Triggers, ", "))
			cli.KeyVal("path", sk.Path)
			fmt.Println()
			fmt.Println(sk.Body)
			return nil
		},
	})

	newCmd := &cobra.Command{
		Use:   "new [name]",
		Short: "Create .slmcode/skills/<name>/SKILL.md",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentsFlag, _ := cmd.Flags().GetString("agents")
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(ws.Config.SkillsDir(), 0o755); err != nil {
				return err
			}
			if _, ok := ws.Skills.Get(args[0]); ok {
				return fmt.Errorf("skill %q already exists — use: slmcode skills edit %s", args[0], args[0])
			}
			sk := skills.Template(args[0], agentsFlag)
			path, err := skills.WriteSkill(ws.Config.SkillsDir(), sk)
			if err != nil {
				return err
			}
			fmt.Println(cli.Success("created " + path))
			fmt.Println(cli.Dim("  Reference with @skill:" + sk.Name + " in run/chat queries"))
			return nil
		},
	}
	newCmd.Flags().String("agents", "", "comma-separated specialist ids (empty = all)")
	cmd.AddCommand(newCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "edit [name]",
		Short: "Open skill in $EDITOR (creates project copy if bundled-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			name := args[0]
			sk, ok := ws.Skills.Get(name)
			if !ok {
				return fmt.Errorf("skill %q not found", name)
			}
			projPath := filepath.Join(ws.Config.SkillsDir(), sanitizeSkillName(name), "SKILL.md")
			if _, err := os.Stat(projPath); err != nil {
				sk.Name = name
				if _, err := skills.WriteSkill(ws.Config.SkillsDir(), sk); err != nil {
					return err
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, projPath)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "path [name]",
		Short: "Print skill file path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			sk, ok := ws.Skills.Get(args[0])
			if !ok {
				return fmt.Errorf("skill %q not found", args[0])
			}
			fmt.Println(sk.Path)
			return nil
		},
	})

	return cmd
}

func sanitizeSkillName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "skill"
	}
	return b.String()
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or set harness config"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print effective config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			c := ws.Config.Public()
			cli.Header("Config")
			cli.KeyVal("provider", c.Provider)
			cli.KeyVal("endpoint", c.Endpoint)
			cli.KeyVal("model", c.Model)
			cli.KeyVal("backend", c.Backend)
			cli.KeyVal("mode", c.Mode)
			cli.KeyVal("specialist", c.Specialist)
			cli.KeyVal("dynamic_pipeline", fmt.Sprintf("%v", c.DynamicPipeline))
			cli.KeyVal("pinned_skills", strings.Join(c.PinnedSkills, ", "))
			cli.KeyVal("think_passes", fmt.Sprintf("%d", c.ThinkPasses))
			cli.KeyVal("max_parallel", fmt.Sprintf("%d", c.MaxParallel))
			cli.KeyVal("max_retries", fmt.Sprintf("%d", c.MaxRetries))
			cli.KeyVal("max_context_kb", fmt.Sprintf("%d", c.MaxContextKB))
			cli.KeyVal("qa_gate", fmt.Sprintf("%v", c.QAGate))
			cli.KeyVal("qa_gate_command", c.QAGateCommand)
			cli.KeyVal("qa_gate_max_rounds", fmt.Sprintf("%d", c.QAGateMaxRounds))
			cli.KeyVal("permission", c.Permission)
			cli.KeyVal("shell_permission", c.ShellPermission)
			cli.KeyVal("shell_whitelist", fmt.Sprintf("%v", c.ShellWhitelist))
			cli.KeyVal("write_guard", fmt.Sprintf("%v", c.WriteGuard))
			cli.KeyVal("read_before_edit", fmt.Sprintf("%v", c.ReadBeforeEdit))
			cli.KeyVal("shell_write_guard", fmt.Sprintf("%v", c.ShellWriteGuard))
			cli.KeyVal("file_checkpoints", fmt.Sprintf("%v", c.FileCheckpoints))
			cli.KeyVal("require_smoke", fmt.Sprintf("%v", c.RequireSmoke))
			cli.KeyVal("claims_gate", fmt.Sprintf("%v", c.ClaimsGate))
			cli.KeyVal("over_edit_guard", fmt.Sprintf("%v", c.OverEditGuard))
			cli.KeyVal("context_compact", fmt.Sprintf("%v", c.ContextCompact))
			cli.KeyVal("react_compact", fmt.Sprintf("%v", c.ReactCompact))
			cli.KeyVal("session_event_log", fmt.Sprintf("%v", c.SessionEventLog))
			cli.KeyVal("auto_text_tools", fmt.Sprintf("%v", c.AutoTextTools))
			cli.KeyVal("read_head_lines", fmt.Sprintf("%d", c.ReadHeadLines))
			cli.KeyVal("dry_run", fmt.Sprintf("%v", c.DryRun))
			cli.KeyVal("listen", c.Listen)
			cli.KeyVal("api_key", c.APIKey)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set [key] [value]",
		Short: "Set model|provider|endpoint|backend|qa_gate|mode|specialist|permission|…",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			k, v := strings.ToLower(args[0]), args[1]
			c := ws.Config
			switch k {
			case "model":
				c.Model = v
			case "fast_model":
				c.FastModel = v
			case "provider":
				next := config.NormalizeProvider(v)
				if next != config.NormalizeProvider(c.Provider) && flagEndpoint == "" {
					c.Endpoint = config.DefaultEndpointFor(next)
				}
				c.Provider = next
			case "endpoint":
				c.Endpoint = v
			case "backend":
				c.Backend = v
			case "mode":
				c.Mode = v
			case "specialist", "agent":
				c.Specialist = v
				if v != "" {
					c.Mode = config.ModeSpecialist
				}
			case "dynamic_pipeline", "dynamic", "composer":
				c.DynamicPipeline = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
			case "pinned_skills", "skills":
				if v == "" || v == "-" {
					c.PinnedSkills = nil
				} else {
					c.PinnedSkills = splitCSV(v)
				}
			case "think_passes", "think":
				fmt.Sscanf(v, "%d", &c.ThinkPasses)
			case "parallel", "max_parallel":
				fmt.Sscanf(v, "%d", &c.MaxParallel)
			case "retries", "max_retries":
				fmt.Sscanf(v, "%d", &c.MaxRetries)
			case "max_context_kb", "context_kb":
				fmt.Sscanf(v, "%d", &c.MaxContextKB)
			case "qa_gate":
				c.QAGate = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
			case "qa_gate_command", "qa_cmd":
				c.QAGateCommand = v
			case "qa_gate_max_rounds", "qa_rounds":
				fmt.Sscanf(v, "%d", &c.QAGateMaxRounds)
			case "dry_run", "dry-run":
				c.DryRun = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
				if c.DryRun {
					c.Permission = "dry-run"
				}
			case "permission", "perm":
				c.Permission = strings.ToLower(v)
				c.DryRun = c.Permission == "dry-run"
			case "listen":
				c.Listen = v
			case "verbose":
				c.Verbose = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
			default:
				patch, ok, err := configPatchFromSchemaValue(k, v)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("unknown key %q", k)
				}
				c.ApplyPatch(patch)
			}
			if err := c.Save(); err != nil {
				return err
			}
			fmt.Println(cli.Success(fmt.Sprintf("set %s = %s", k, v)))
			return nil
		},
	})
	return cmd
}

func configPatchFromSchemaValue(key, value string) (config.Patch, bool, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, field := range config.Schema() {
		if field.Key != key || !field.Patchable {
			continue
		}
		parsed, err := parseConfigValue(field, value)
		if err != nil {
			return config.Patch{}, true, err
		}
		b, err := json.Marshal(map[string]interface{}{field.Key: parsed})
		if err != nil {
			return config.Patch{}, true, err
		}
		var patch config.Patch
		if err := json.Unmarshal(b, &patch); err != nil {
			return config.Patch{}, true, err
		}
		return patch, true, nil
	}
	return config.Patch{}, false, nil
}

func parseConfigValue(field config.FieldSchema, value string) (interface{}, error) {
	value = strings.TrimSpace(value)
	if len(field.Enum) > 0 {
		ok := false
		for _, allowed := range field.Enum {
			if value == allowed {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%s must be one of: %s", field.Key, strings.Join(field.Enum, ", "))
		}
	}
	switch field.Type {
	case "bool":
		v, err := parseConfigBool(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.Key, err)
		}
		return v, nil
	case "int":
		v, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer", field.Key)
		}
		return v, nil
	case "float":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number", field.Key)
		}
		return v, nil
	case "string[]":
		if value == "" || value == "-" {
			return []string{}, nil
		}
		return splitCSV(value), nil
	default:
		return value, nil
	}
}

func parseConfigBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "no", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("must be true/false")
	}
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check active provider/model, LLM reachability, workspace, board, skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	cli.Header("Doctor")
	cli.KeyVal("root", ws.Config.Root)
	cli.KeyVal("provider", ws.Config.Provider)
	cli.KeyVal("model", ws.Config.Model)
	cli.KeyVal("endpoint", ws.Config.Endpoint)
	if ws.Config.ActiveStack != "" {
		cli.KeyVal("active_stack", ws.Config.ActiveStack)
	} else {
		cli.KeyVal("active_stack", "(none — manual provider/model)")
	}
	cli.KeyVal("backend", ws.Config.Backend)
	cli.KeyVal("permission", ws.Config.Permission)
	cli.KeyVal("shell", ws.Config.ShellPermission)
	cli.KeyVal("dynamic_pipeline", fmt.Sprintf("%v", ws.Config.DynamicPipeline))
	cli.KeyVal("mode", ws.Config.Mode)
	cli.KeyVal("specialist", ws.Config.Specialist)
	cli.KeyVal("qa_gate", fmt.Sprintf("%v (rounds=%d)", ws.Config.QAGate, ws.Config.QAGateMaxRounds))
	prof := config.ResolveModelProfile(ws.Config.ModelProfiles, ws.Config.Model)
	cli.KeyVal("model_profile", fmt.Sprintf("ctx=%d max_tokens=%d think=%d skills=%d knowledge=%d turns=%d",
		prof.ContextLimit, prof.MaxTokens, prof.ThinkingBudgetTokens,
		prof.SkillTokenBudget, prof.KnowledgeTokenBudget, prof.MaxTurns))
	cli.KeyVal("guards", fmt.Sprintf("write=%v shell_write=%v read_before_edit=%v smoke=%v claims=%v over_edit=%v",
		ws.Config.WriteGuard, ws.Config.ShellWriteGuard, ws.Config.ReadBeforeEdit,
		ws.Config.RequireSmoke, ws.Config.ClaimsGate, ws.Config.OverEditGuard))
	embCfg := retrieval.Config{
		Enabled:  ws.Config.EmbeddingEnabled,
		Endpoint: ws.Config.EmbeddingEndpoint,
		Model:    ws.Config.EmbeddingModel,
		APIKey:   ws.Config.EmbeddingAPIKey,
		TopK:     ws.Config.EmbeddingTopK,
	}
	_, embMode := retrieval.ResolveEmbedder(context.Background(), embCfg)
	cli.KeyVal("embedding", fmt.Sprintf("%s enabled=%v model=%s endpoint=%s top_k=%d",
		embMode, ws.Config.EmbeddingEnabled, ws.Config.EmbeddingModel, ws.Config.EmbeddingEndpoint, ws.Config.EmbeddingTopK))
	if _, err := os.Stat(ws.Config.SlmDir()); err != nil {
		fmt.Println(cli.Warn(".slmcode missing — run slmcode init"))
	} else {
		fmt.Println(cli.Success(".slmcode present"))
	}
	_ = ws.Board.Load()
	b := ws.Board.Snapshot()
	fmt.Println(cli.Success(fmt.Sprintf("board: %d tasks", len(b.Tasks))))

	endpoint := ws.Config.Endpoint
	ws.Config.ResolveAPIKey()
	url := strings.TrimRight(endpoint, "/") + "/models"
	if config.IsOllama(ws.Config.Provider) {
		base := strings.TrimSuffix(strings.TrimRight(ws.Config.Endpoint, "/"), "/v1")
		url = strings.TrimRight(base, "/") + "/api/tags"
	} else if !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		url = strings.TrimRight(endpoint, "/") + "/v1/models"
	}
	req, _ := http.NewRequest("GET", url, nil)
	if ws.Config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ws.Config.APIKey)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(cli.Error(fmt.Sprintf("LLM unreachable at %s: %v", url, err)))
		fmt.Println(cli.Dim("  tip: start your provider, or override with --provider / --endpoint / --model"))
		fmt.Println(cli.Dim("  examples: omlx start · ollama serve · LM Studio local server"))
	} else {
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fmt.Println(cli.Success(fmt.Sprintf("LLM ok — %s / %s (HTTP %d)", ws.Config.Provider, ws.Config.Model, resp.StatusCode)))
		} else if resp.StatusCode == 401 || resp.StatusCode == 403 {
			fmt.Println(cli.Error(fmt.Sprintf("LLM auth failed (HTTP %d) at %s", resp.StatusCode, url)))
			if ws.Config.APIKey == "" {
				fmt.Println(cli.Dim("  tip: set OMLX_API_KEY / SLMCODE_API_KEY, or ~/.omlx/settings.json → auth.api_key"))
			} else {
				fmt.Println(cli.Dim("  tip: api_key is set but rejected — refresh key from provider settings"))
			}
		} else {
			fmt.Println(cli.Warn(fmt.Sprintf("LLM responded %d at %s", resp.StatusCode, url)))
		}
	}
	auth := models.ResolveAuth(ws.Config)
	if auth.Configured {
		fmt.Println(cli.Success(fmt.Sprintf("auth OK (%s)", auth.Source)))
	} else if auth.Required {
		fmt.Println(cli.Error(auth.Message))
	} else {
		fmt.Println(cli.Dim("auth: local provider — key optional"))
	}
	custom, _ := agents.LoadCustomSpecs(append([]string{ws.Config.AgentsDir()}, agents.GlobalAgentRoots()...)...)
	var pinned []string
	for _, a := range custom {
		if a.Model != "" || a.Provider != "" || a.Endpoint != "" {
			pinned = append(pinned, a.ID)
		}
	}
	if len(pinned) > 0 {
		fmt.Println(cli.Warn(fmt.Sprintf("agents pinning LLM (override stack): %s", strings.Join(pinned, ", "))))
		fmt.Println(cli.Dim("  tip: slmcode stack apply <name> --clear-agent-llm   or   slmcode agent clear-llm <id>"))
	} else {
		fmt.Println(cli.Success("agents inherit stack/global LLM"))
	}
	sk, _ := ws.Skills.List()
	fmt.Println(cli.Success(fmt.Sprintf("%d skills loaded", len(sk))))
	report := readiness.Build(ws.Config, len(sk))
	if readinessHasFailedChecks(report) {
		fmt.Println(cli.Warn(fmt.Sprintf("readiness: %d/100 (%s)", report.Score, report.Status)))
		fmt.Println(cli.Dim("  failed: " + strings.Join(readinessFailedIDs(report), ", ")))
		fmt.Println(cli.Dim("  fix: slmcode readiness --fix"))
	} else {
		fmt.Println(cli.Success(fmt.Sprintf("readiness: %d/100 (%s)", report.Score, report.Status)))
	}
	return nil
}

func readinessFailedIDs(r readiness.Report) []string {
	var out []string
	for _, check := range r.Checks {
		if !check.OK {
			out = append(out, check.ID)
		}
	}
	return out
}

func watchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Refresh kanban in the terminal (live while agents run)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			fmt.Println(cli.Info("watching board — Ctrl+C to stop"))
			for {
				_ = ws.Board.Load()
				b := ws.Board.Snapshot()
				fmt.Print("\033[H\033[2J")
				fmt.Print(cli.Banner())
				cli.KeyVal("updated", time.Now().Format(time.Kitchen))
				fmt.Println()
				by := b.ByColumn()
				for _, col := range plan.Columns() {
					tasks := by[col]
					fmt.Printf("%s %s\n", cli.ColumnColor("●"), cli.Bold(plan.ColumnLabel(col))+cli.Dim(fmt.Sprintf(" (%d)", len(tasks))))
					for _, t := range tasks {
						fmt.Printf("    %s  %s\n", cli.Accent(t.ID), t.Title)
					}
				}
				select {
				case <-time.After(2 * time.Second):
				case <-cmd.Context().Done():
					return nil
				}
			}
		},
	}
}

// openBrowser tries to open url in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url)
	case runtime.GOOS == "linux":
		cmd = exec.Command("xdg-open", url)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Run()
	}
}

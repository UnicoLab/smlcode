package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/piotrlaczkowski/slmcode/pkg/cli"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
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
			cli.KeyVal("pinned_skills", strings.Join(c.PinnedSkills, ", "))
			cli.KeyVal("think_passes", fmt.Sprintf("%d", c.ThinkPasses))
			cli.KeyVal("max_parallel", fmt.Sprintf("%d", c.MaxParallel))
			cli.KeyVal("max_retries", fmt.Sprintf("%d", c.MaxRetries))
			cli.KeyVal("max_context_kb", fmt.Sprintf("%d", c.MaxContextKB))
			cli.KeyVal("permission", c.Permission)
			cli.KeyVal("dry_run", fmt.Sprintf("%v", c.DryRun))
			cli.KeyVal("listen", c.Listen)
			cli.KeyVal("api_key", c.APIKey)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set [key] [value]",
		Short: "Set model|provider|backend|mode|specialist|pinned_skills|permission|…",
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
			case "provider":
				c.Provider = v
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
				return fmt.Errorf("unknown key %q", k)
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

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check oMLX/Ollama, workspace, board, skills",
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
	cli.KeyVal("permission", ws.Config.Permission)
	cli.KeyVal("mode", ws.Config.Mode)
	cli.KeyVal("specialist", ws.Config.Specialist)
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
	if ws.Config.Provider == "ollama" {
		url = strings.TrimRight(ws.Config.Endpoint, "/") + "/api/tags"
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
		fmt.Println(cli.Dim("  tip: omlx start   or   ollama serve"))
	} else {
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fmt.Println(cli.Success(fmt.Sprintf("LLM ok (%s) status=%d", ws.Config.Provider, resp.StatusCode)))
		} else {
			fmt.Println(cli.Warn(fmt.Sprintf("LLM responded %d at %s", resp.StatusCode, url)))
		}
	}
	sk, _ := ws.Skills.List()
	fmt.Println(cli.Success(fmt.Sprintf("%d skills loaded", len(sk))))
	return nil
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

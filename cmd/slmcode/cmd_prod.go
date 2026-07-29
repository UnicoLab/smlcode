package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func chatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Interactive REPL (slash commands + multi-turn runs)",
		Long: `Interactive coding harness REPL.

Slash commands:
  /help /board /status /diff /skills /doctor /quit
  /run <query>     full pipeline
  /permission <auto|dry-run|review>
  /model <id>
Any other line runs the full SLM pipeline.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			_ = h.EnsureInitialized()
			fmt.Print(cli.Banner())
			fmt.Println(cli.Info("Interactive mode — type a task or /help. Ctrl+C / /quit to exit."))
			cli.KeyVal("model", h.Config.Model)
			cli.KeyVal("permission", h.Config.Permission)
			fmt.Println()

			runLine := func(q string) error {
				ctx, cancel := signalContext()
				defer cancel()
				status := cli.NewStatusTracker()
				h.Orchestrator.OnEvent(func(e orchestrator.Event) { cli.PrintEventWithStatus(e, status) })
				res, err := h.Run(ctx, q)
				if err != nil {
					return err
				}
				fmt.Println(status.Footer())
				if res.Success {
					fmt.Println(cli.Success(res.Summary))
				} else {
					fmt.Println(cli.Warn(res.Summary))
				}
				return nil
			}

			in := bufio.NewScanner(os.Stdin)
			for {
				fmt.Print(cli.Accent("slm › "))
				if !in.Scan() {
					break
				}
				line := strings.TrimSpace(in.Text())
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "/") {
					quit, err := chatSlash(h, line, runLine)
					if err != nil {
						fmt.Println(cli.Error(err.Error()))
					}
					if quit {
						return nil
					}
					continue
				}
				if err := runLine(line); err != nil {
					fmt.Println(cli.Warn(err.Error()))
				}
			}
			return in.Err()
		},
	}
}

func chatSlash(h *harness.Harness, line string, run func(string) error) (bool, error) {
	parts := strings.Fields(line)
	cmd := strings.ToLower(parts[0])
	arg := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
	switch cmd {
	case "/quit", "/exit", "/q":
		fmt.Println(cli.Dim("bye"))
		return true, nil
	case "/help", "/?":
		fmt.Println(`  /run <q>   /board   /status   /diff   /skills   /doctor
  /permission auto|dry-run|review   /model <id>   /quit`)
		return false, nil
	case "/board":
		_ = h.Orchestrator.Board().Load()
		b := h.Orchestrator.Board().Snapshot()
		fmt.Println(cli.Bold(b.Plan.Summary))
		for _, t := range b.Tasks {
			t.Normalize()
			fmt.Printf("  %s  %s  @%s  %s\n", t.ID, cli.ColumnColor(t.Column), t.Role, t.Title)
		}
		return false, nil
	case "/status":
		cli.KeyVal("root", h.Config.Root)
		cli.KeyVal("model", h.Config.Model)
		cli.KeyVal("permission", h.Config.Permission)
		return false, nil
	case "/diff":
		return false, showDiff(h.Config.Root, "")
	case "/skills":
		list, _ := h.Orchestrator.Skills().List()
		for _, s := range list {
			fmt.Printf("  • %s — %s\n", s.Name, s.Description)
		}
		return false, nil
	case "/doctor":
		return false, runDoctor()
	case "/permission":
		if arg == "" {
			return false, fmt.Errorf("usage: /permission auto|dry-run|review")
		}
		h.Config.Permission = arg
		h.Config.DryRun = arg == "dry-run"
		_ = h.Config.Save()
		fmt.Println(cli.Success("permission = " + arg + " (restart chat to rebuild tools)"))
		return false, nil
	case "/model":
		if arg == "" {
			return false, fmt.Errorf("usage: /model <id>")
		}
		h.Config.Model = arg
		_ = h.Config.Save()
		fmt.Println(cli.Success("model = " + arg + " (restart chat to rebuild agents)"))
		return false, nil
	case "/run":
		if arg == "" {
			return false, fmt.Errorf("usage: /run <query>")
		}
		return false, run(arg)
	default:
		return false, fmt.Errorf("unknown slash command %s — try /help", cmd)
	}
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "List / show / resume saved runs"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			list, err := session.List(ws.Config.SlmDir())
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println(cli.Dim("no sessions yet — run something first"))
				return nil
			}
			for _, s := range list {
				mark := "·"
				if s.Success {
					mark = "✔"
				}
				fmt.Printf("  %s %s  %s\n    %s\n", mark, cli.Accent(s.ID), cli.Dim(s.UpdatedAt), truncateCLI(s.Query, 80))
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:  "show [id]",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			s, err := session.Load(ws.Config.SlmDir(), args[0])
			if err != nil {
				return err
			}
			fmt.Println(cli.Bold(s.ID))
			cli.KeyVal("query", s.Query)
			cli.KeyVal("success", fmt.Sprintf("%v", s.Success))
			cli.KeyVal("summary", s.Summary)
			cli.KeyVal("tasks", fmt.Sprintf("%d", len(s.Board.Tasks)))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "resume [id]",
		Short: "Restore board.json from a session (then promote/run)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			s, err := session.Load(ws.Config.SlmDir(), args[0])
			if err != nil {
				return err
			}
			for i := range s.Board.Tasks {
				t := &s.Board.Tasks[i]
				t.Normalize()
				if t.Column == plan.ColBlocked || t.Column == plan.ColInProgress || t.Column == plan.ColInReview {
					t.MoveTo(plan.ColReadyToDev)
				}
			}
			if err := ws.Board.Replace(s.Board); err != nil {
				return err
			}
			fmt.Println(cli.Success("board restored from " + s.ID))
			fmt.Println(cli.Info("edit/promote tasks, then: slmcode run \"continue previous work\""))
			return nil
		},
	})
	return cmd
}

func diffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff [path]",
		Short: "Show git diff (working tree)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			return showDiff(root, path)
		},
	}
}

func commitCmd() *cobra.Command {
	var msg string
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Git add -A && commit (harness helper)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if msg == "" {
				msg = "slmcode: apply agent changes"
			}
			root, err := projectRoot()
			if err != nil {
				return err
			}
			if !isGitRepo(root) {
				return fmt.Errorf("not a git repository (run git init in %s)", root)
			}
			c := exec.Command("git", "add", "-A")
			c.Dir = root
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("git add: %s %v", out, err)
			}
			c = exec.Command("git", "commit", "-m", msg)
			c.Dir = root
			out, err := c.CombinedOutput()
			fmt.Print(string(out))
			return err
		},
	}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "commit message")
	return cmd
}

func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply pending review-mode file writes from .slmcode/pending/",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			dir := filepath.Join(ws.Config.SlmDir(), "pending")
			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println(cli.Dim("nothing pending"))
					return nil
				}
				return err
			}
			n := 0
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch.json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					continue
				}
				var p struct {
					Path    string `json:"path"`
					Kind    string `json:"kind"`
					Content string `json:"content"`
				}
				if json.Unmarshal(data, &p) != nil || p.Path == "" {
					continue
				}
				abs := filepath.Join(ws.Config.Root, p.Path)
				_ = os.MkdirAll(filepath.Dir(abs), 0o755)
				if err := os.WriteFile(abs, []byte(p.Content), 0o644); err != nil {
					fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
					continue
				}
				_ = os.Remove(filepath.Join(dir, e.Name()))
				fmt.Println(cli.Success("applied " + p.Path))
				n++
			}
			fmt.Println(cli.Info(fmt.Sprintf("%d file(s) applied", n)))
			return nil
		},
	}
}

func isGitRepo(root string) bool {
	c := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	c.Dir = root
	out, err := c.CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func showDiff(root, path string) error {
	if !isGitRepo(root) {
		fmt.Println(cli.Dim("not a git repository — nothing to diff"))
		fmt.Println(cli.Dim("tip: git init   or   slmcode apply  (for review-mode pending writes)"))
		return nil
	}
	args := []string{"diff", "--color=always"}
	if path != "" {
		args = append(args, "--", path)
	}
	c := exec.Command("git", args...)
	c.Dir = root
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		// empty diff exits 0; other failures surface cleanly
		return fmt.Errorf("git diff failed: %w", err)
	}
	return nil
}

func truncateCLI(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

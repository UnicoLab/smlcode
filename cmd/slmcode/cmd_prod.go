package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func chatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Interactive REPL (same steerable engine as the TUI, plain transcript)",
		Long: `Interactive coding harness REPL.

Identical to the premium TUI except that the boxed dashboard is not painted:
you get a plain append-only transcript with the same sticky status line, the
same slash commands, the same inline HITL gates, and the same Esc-to-redirect
steering. Type ? for the command list.`,
		Example: "  slmcode chat\n  slmcode chat --log-level=debug",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractiveSession(true)
		},
	}
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "session",
		Short:   "List / show / resume saved runs",
		Example: "  slmcode session list\n  slmcode session show run-1234\n  slmcode session resume        # pick the interrupted run back up",
	}
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
		Short: "Resume an interrupted query turn (or restore a completed session board)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			defer closeHarness(h)
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			// Prefer interrupted query-turn resume (continues execute from checkpoint).
			if turn, err := session.FindInterrupted(h.Config.SlmDir()); err == nil && turn != nil && id == "" {
				ctx, cancel := signalContext()
				defer cancel()
				h.Orchestrator.OnEvent(func(e orchestrator.Event) {
					if cli.ShouldRender(e) {
						cli.PrintEvent(e)
					}
				})
				res, err := h.Resume(ctx, turn.ID)
				if res != nil {
					fmt.Println(cli.Bold(res.Summary))
				}
				return err
			}
			if id != "" {
				if turn, err := session.LoadTurn(h.Config.SlmDir(), id); err == nil && turn != nil && (turn.Interrupted || len(turn.Board.Tasks) > 0) {
					ctx, cancel := signalContext()
					defer cancel()
					h.Orchestrator.OnEvent(func(e orchestrator.Event) {
						cli.PrintEvent(e)
					})
					res, err := h.Resume(ctx, turn.ID)
					if res != nil {
						fmt.Println(cli.Bold(res.Summary))
					}
					return err
				}
			}
			// Legacy: restore completed session snapshot into live board.
			if id == "" {
				return fmt.Errorf("no interrupted run — pass a session/query id")
			}
			s, err := session.Load(h.Config.SlmDir(), id)
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
			if err := h.Orchestrator.Board().Replace(s.Board); err != nil {
				return err
			}
			fmt.Println(cli.Success("board restored from " + s.ID))
			fmt.Println(cli.Info("edit/promote tasks, then: slmcode run \"continue previous work\" — or /resume for interrupted turns"))
			return nil
		},
	})
	return cmd
}

func commitCmd() *cobra.Command {
	var msg string
	cmd := &cobra.Command{
		Use:     "commit",
		Short:   "Git add -A && commit (harness helper)",
		Example: `  slmcode commit -m "apply agent changes"`,
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
			// msg is the user's own --message flag value, passed as a discrete
			// argv element (no shell involved), not attacker-controlled input.
			c = exec.Command("git", "commit", "-m", msg) //nolint:gosec // msg is a local CLI flag value, argv-only (no shell)
			c.Dir = root
			out, err := c.CombinedOutput()
			fmt.Print(string(out))
			return err
		},
	}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "commit message")
	return cmd
}

func isGitRepo(root string) bool {
	c := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	c.Dir = root
	out, err := c.CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func truncateCLI(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

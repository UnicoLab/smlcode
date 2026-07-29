package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/piotrlaczkowski/slmcode/pkg/cli"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
	"github.com/piotrlaczkowski/slmcode/pkg/harness"
	"github.com/piotrlaczkowski/slmcode/pkg/orchestrator"
	"github.com/piotrlaczkowski/slmcode/pkg/server"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .slmcode/ memory, board.json, config (oMLX defaults)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			// Always run InitWorkspace (idempotent): empty scaffolds + skills + board.json.
			// Agents populate CONTEXT/PLAN/TASKS on the first real run — nothing is seeded.
			h := &harness.Harness{Config: ws.Config}
			if err := h.Init(); err != nil {
				return err
			}
			fmt.Println(cli.Success("workspace ready"))
			cli.KeyVal("path", ws.Config.SlmDir())
			cli.KeyVal("provider", ws.Config.Provider)
			cli.KeyVal("model", ws.Config.Model)
			cli.KeyVal("endpoint", ws.Config.Endpoint)
			fmt.Println()
			fmt.Println(cli.Info("next: slmcode run -v \"…\"  — agents fill context, plan, and tasks"))
			return nil
		},
	}
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [query...]",
		Short: "Full pipeline or single specialist (see --mode / --agent)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			_ = h.EnsureInitialized()
			query := strings.Join(args, " ")

			mode, _ := cmd.Flags().GetString("mode")
			agent, _ := cmd.Flags().GetString("agent")
			pinSkills, _ := cmd.Flags().GetStringSlice("skill")
			if mode != "" {
				h.Config.Mode = mode
			}
			if agent != "" {
				h.Config.Specialist = agent
				h.Config.Mode = config.ModeSpecialist
			}
			if len(pinSkills) > 0 {
				h.Config.PinnedSkills = append(h.Config.PinnedSkills, pinSkills...)
				for _, s := range pinSkills {
					query += " @skill:" + s
				}
			}

			ctx, cancel := signalContext()
			defer cancel()

			fmt.Print(cli.Banner())
			cli.KeyVal("provider", h.Config.Provider)
			cli.KeyVal("model", h.Config.Model)
			cli.KeyVal("backend", h.Config.Backend)
			cli.KeyVal("mode", h.Config.Mode)
			if h.Config.Mode == config.ModeSpecialist {
				cli.KeyVal("specialist", h.Config.Specialist)
			}
			cli.KeyVal("think", fmt.Sprintf("%d passes", h.Config.ThinkPasses))
			cli.KeyVal("parallel", fmt.Sprintf("%d", h.Config.MaxParallel))
			fmt.Println()
			fmt.Println(cli.Bold("Query: ") + query)
			fmt.Println()

			h.Orchestrator.OnEvent(func(e orchestrator.Event) {
				cli.PrintEvent(e)
			})

			res, err := h.Run(ctx, query)
			if err != nil {
				return err
			}
			fmt.Println()
			if res.Success {
				fmt.Println(cli.Success(res.Summary))
			} else {
				fmt.Println(cli.Warn(res.Summary))
			}
			cli.KeyVal("duration", res.Duration.Round(time.Millisecond).String())
			cli.KeyVal("failed", fmt.Sprintf("%d", res.FailedTasks))
			cli.KeyVal("board", h.Config.SlmDir()+"/board.json")
			if !res.Success {
				return fmt.Errorf("run finished with failures — inspect board / promote blocked tasks")
			}
			return nil
		},
	}
	cmd.Flags().String("mode", "", "full | specialist (overrides config)")
	cmd.Flags().String("agent", "", "run a single specialist (worker, explorer, …)")
	cmd.Flags().StringSlice("skill", nil, "pin/load skill by name (repeatable); also accepts @skill:name in query")
	return cmd
}

func studioCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch Studio UI + API (live kanban, context edit, SSE)",
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			_ = h.EnsureInitialized()
			addr := flagListen
			if addr == "" {
				addr = h.Config.Listen
			}
			uiFS, err := fs.Sub(uiEmbed, "ui")
			if err != nil {
				return err
			}
			fmt.Print(cli.Banner())
			fmt.Println(cli.Success("Studio listening"))
			cli.KeyVal("url", "http://"+addr)
			cli.KeyVal("root", h.Config.Root)
			cli.KeyVal("provider", h.Config.Provider+" / "+h.Config.Model)
			fmt.Println(cli.Dim("\n  Edit context & kanban while agents run. Ctrl+C to stop.\n"))
			return server.New(h, uiFS).ListenAndServe(addr)
		},
	}
	cmd.Flags().StringVar(&flagListen, "listen", "", "listen address (default from config)")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Snapshot of query, plan head, board counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			cli.Header("Status")
			cli.KeyVal("root", ws.Config.Root)
			cli.KeyVal("provider", ws.Config.Provider)
			cli.KeyVal("model", ws.Config.Model)
			cli.KeyVal("backend", ws.Config.Backend)
			b := ws.Board.Snapshot()
			by := b.ByColumn()
			fmt.Println()
			for _, col := range []string{"to_scope", "scoped", "ready_to_dev", "in_progress", "in_review", "done", "blocked"} {
				n := len(by[col])
				if n == 0 {
					continue
				}
				fmt.Printf("  %s  %s\n", cli.ColumnColor(fmt.Sprintf("%-14s", col)), cli.Bold(fmt.Sprintf("%d", n)))
			}
			fmt.Println()
			q, _ := ws.Store.Read("QUERY.md")
			fmt.Println(cli.Dim(q))
			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(cli.Accent("slmcode") + " " + cli.Bold(Version))
			fmt.Println(cli.Dim("SLM engine · GoLangGraph specialists · oMLX default"))
			if p, err := os.Executable(); err == nil {
				if real, err2 := filepath.EvalSymlinks(p); err2 == nil {
					p = real
				}
				fmt.Println(cli.Dim("binary: " + p))
			}
			if GitCommit != "" && GitCommit != "unknown" {
				fmt.Println(cli.Dim("commit: " + GitCommit))
			}
			if BuildTime != "" && BuildTime != "unknown" {
				fmt.Println(cli.Dim("built:  " + BuildTime))
			}
			if SourceRoot != "" {
				fmt.Println(cli.Dim("source: " + SourceRoot))
			}
			fmt.Println(cli.Dim("update: slmcode update"))
		},
	}
}

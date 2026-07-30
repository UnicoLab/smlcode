package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Aliases: []string{"ui", "repl"},
		Short:   "Premium interactive TUI (also the default when you run slmcode alone)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPremiumTUI()
		},
	}
}

func runPremiumTUI() error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	_ = ws.EnsureInitialized()

	st := loadDashboard(ws)
	if !cli.IsInteractive() {
		cli.PrintStaticDashboard(st)
		return nil
	}

	h, err := openHarness()
	if err != nil {
		return err
	}
	_ = h.EnsureInitialized()

	sess := cli.NewLiveSession()
	sess.SetState(loadDashboardFromHarness(h))

	var runMu sync.Mutex
	var cancelRun func()
	var runFn func(string) error

	runFn = func(query string) error {
		runMu.Lock()
		ctx, cancel := signalContext()
		cancelRun = cancel
		runMu.Unlock()
		defer cancel()

		h.Orchestrator.OnEvent(func(e orchestrator.Event) {
			sess.Observe(e)
		})
		res, err := h.Run(ctx, query)
		sess.SetState(loadDashboardFromHarness(h))
		if err != nil {
			return err
		}
		if res != nil && !res.Success {
			return fmt.Errorf("%s", res.Summary)
		}
		return nil
	}

	sess.OnRun(runFn)
	sess.OnStop(func() {
		runMu.Lock()
		defer runMu.Unlock()
		if cancelRun != nil {
			cancelRun()
		}
	})

	sess.OnSlash(func(line string) (bool, error) {
		parts := strings.Fields(line)
		cmdName := strings.ToLower(parts[0])
		arg := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		switch cmdName {
		case "/q", "/quit", "/exit":
			return true, nil
		case "/help", "/?":
			return false, nil
		case "/refresh", "/board", "/status":
			_ = h.Orchestrator.Board().Load()
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/stop":
			runMu.Lock()
			if cancelRun != nil {
				cancelRun()
			}
			runMu.Unlock()
			fmt.Println(cli.Warn("stop requested"))
			return false, nil
		case "/errors":
			path := filepath.Join(h.Config.SlmDir(), "errors", "errors.md")
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Println(cli.Dim("no errors.md yet"))
				return false, nil
			}
			fmt.Println(string(data))
			return false, nil
		case "/diff":
			return false, showDiff(h.Config.Root, "")
		case "/queries":
			list, err := session.ListQueries(h.Config.SlmDir())
			if err != nil || len(list) == 0 {
				fmt.Println(cli.Dim("no query turns yet"))
				return false, nil
			}
			for _, q := range list {
				fmt.Printf("  %s  %s\n", cli.Accent(q.ID), cli.Dim(cli.Clip(q.Query, 60)))
			}
			return false, nil
		case "/agents":
			custom, _ := agents.LoadCustomSpecs(append([]string{h.Config.AgentsDir()}, agents.GlobalAgentRoots()...)...)
			for _, a := range agents.PublicSpecsWithCustom(custom) {
				id, _ := a["id"].(string)
				title, _ := a["title"].(string)
				mark := "·"
				if c, _ := a["custom"].(bool); c {
					mark = "★"
				}
				fmt.Printf("  %s @%-14s %s\n", mark, id, title)
			}
			return false, nil
		case "/skills":
			list, _ := h.Orchestrator.Skills().List()
			for _, sk := range list {
				fmt.Printf("  • %s — %s\n", sk.Name, sk.Description)
			}
			return false, nil
		case "/studio":
			addr := h.Config.Listen
			if addr == "" {
				addr = "127.0.0.1:7421"
			}
			fmt.Println(cli.Info("Studio: slmcode studio → http://" + addr))
			return false, nil
		case "/model":
			if arg == "" {
				return false, fmt.Errorf("usage: /model <id>")
			}
			h.Config.Model = arg
			_ = h.Config.Save()
			fmt.Println(cli.Success("model = " + arg + " (restart TUI to rebuild agents)"))
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/provider":
			if arg == "" {
				return false, fmt.Errorf("usage: /provider <name>")
			}
			h.Config.Provider = arg
			_ = h.Config.Save()
			fmt.Println(cli.Success("provider = " + arg + " (restart TUI to rebuild)"))
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/run":
			if arg == "" {
				return false, fmt.Errorf("usage: /run <query>")
			}
			return false, runFn(arg)
		default:
			return false, fmt.Errorf("unknown %s — try ?", cmdName)
		}
	})

	fmt.Print(cli.Banner())
	return sess.RunInteractive()
}

func loadDashboard(ws *harness.Workspace) cli.DashboardState {
	_ = ws.Board.Load()
	b := ws.Board.Snapshot()
	st := cli.DashboardState{
		Root:       ws.Config.Root,
		Provider:   ws.Config.Provider,
		Model:      ws.Config.Model,
		Endpoint:   ws.Config.Endpoint,
		Backend:    ws.Config.Backend,
		Permission: ws.Config.Permission,
		Board:      &b,
	}
	if data, err := os.ReadFile(filepath.Join(ws.Config.SlmDir(), "errors", "errors.md")); err == nil {
		st.ErrorsHead = firstNonEmptyLine(string(data))
	}
	st.DiffHead = gitDirtySummary(ws.Config.Root)
	st.Queries = recentQueryLabels(ws.Config.SlmDir(), 4)
	return st
}

func loadDashboardFromHarness(h *harness.Harness) cli.DashboardState {
	_ = h.Orchestrator.Board().Load()
	b := h.Orchestrator.Board().Snapshot()
	st := cli.DashboardState{
		Root:       h.Config.Root,
		Provider:   h.Config.Provider,
		Model:      h.Config.Model,
		Endpoint:   h.Config.Endpoint,
		Backend:    h.Config.Backend,
		Permission: h.Config.Permission,
		Board:      &b,
	}
	if data, err := os.ReadFile(filepath.Join(h.Config.SlmDir(), "errors", "errors.md")); err == nil {
		st.ErrorsHead = firstNonEmptyLine(string(data))
	}
	st.DiffHead = gitDirtySummary(h.Config.Root)
	st.Queries = recentQueryLabels(h.Config.SlmDir(), 4)
	return st
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

func gitDirtySummary(root string) string {
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 4 {
		return fmt.Sprintf("%d files dirty", len(lines))
	}
	var names []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if len(l) > 3 {
			names = append(names, strings.TrimSpace(l[3:]))
		}
	}
	return strings.Join(names, ", ")
}

func recentQueryLabels(slmDir string, n int) []string {
	list, err := session.ListQueries(slmDir)
	if err != nil || len(list) == 0 {
		return nil
	}
	if len(list) > n {
		list = list[:n]
	}
	var out []string
	for _, q := range list {
		label := q.ID
		if q.Query != "" {
			label = cli.Clip(q.Query, 28)
		}
		out = append(out, label)
	}
	return out
}

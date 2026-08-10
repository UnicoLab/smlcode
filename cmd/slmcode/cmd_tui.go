package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/updatecheck"
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
	sess.SetCompact(h.Config.CompactMode)
	sess.OnBoardRefresh(func() *plan.Board {
		_ = h.Orchestrator.Board().Load()
		snap := h.Orchestrator.Board().Snapshot()
		return &snap
	})

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
		case "/clear":
			sess.ClearLive()
			fmt.Println(cli.Dim("live stream cleared — type a query to run"))
			return false, nil
		case "/plan":
			mode := strings.ToLower(strings.TrimSpace(arg))
			switch mode {
			case "", "toggle":
				if h.Config.PlanApprove == "ask" {
					h.Config.PlanApprove = "auto"
				} else {
					h.Config.PlanApprove = "ask"
				}
			case "auto", "off":
				h.Config.PlanApprove = "auto"
			case "ask", "on":
				h.Config.PlanApprove = "ask"
			default:
				return false, fmt.Errorf("usage: /plan [auto|ask]")
			}
			_ = h.Config.Save()
			cli.KeyVal("plan_approve", h.Config.PlanApprove)
			return false, nil
		case "/escalate":
			action := plan.NormalizeEscalateAction(strings.TrimSpace(arg))
			if strings.TrimSpace(arg) == "" {
				return false, fmt.Errorf("usage: /escalate re_scope|retry|mark_done|abort")
			}
			ans := plan.EscalateAnswer{
				Action:     action,
				AnsweredAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := hitl.WriteAnswers(h.Config.SlmDir(), "escalate", ans); err != nil {
				return false, err
			}
			fmt.Println(cli.Success("escalate → " + action))
			return false, nil
		case "/history":
			hist := sess.History()
			if hist == nil {
				fmt.Println(cli.Dim("no prompt history"))
				return false, nil
			}
			recent := hist.Recent(15)
			if len(recent) == 0 {
				fmt.Println(cli.Dim("no prompt history yet"))
				return false, nil
			}
			for i, q := range recent {
				fmt.Printf("  %2d  %s\n", i+1, cli.Dim(cli.Clip(q, 72)))
			}
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
			fmt.Println(cli.Warn("stop requested — board + ReAct history checkpointed; use /resume to continue"))
			return false, nil
		case "/resume":
			id := strings.TrimSpace(arg)
			runMu.Lock()
			ctx, cancel := signalContext()
			cancelRun = cancel
			runMu.Unlock()
			defer cancel()
			h.Orchestrator.OnEvent(func(e orchestrator.Event) {
				sess.Observe(e)
			})
			res, err := h.Resume(ctx, id)
			sess.SetState(loadDashboardFromHarness(h))
			if err != nil && (res == nil || !strings.Contains(strings.ToLower(err.Error()), "canceled")) {
				return false, err
			}
			if res != nil {
				if res.Success {
					fmt.Println(cli.Success("resumed — " + res.Summary))
				} else {
					fmt.Println(cli.Warn(res.Summary))
				}
			}
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
		case "/queries", "/sessions":
			list, err := session.ListQueries(h.Config.SlmDir())
			if err != nil || len(list) == 0 {
				fmt.Println(cli.Dim("no query turns yet"))
				return false, nil
			}
			if arg != "" {
				// Session picker: /sessions <id|N> shows plan/summary for that turn.
				var pick *session.Turn
				for i := range list {
					q := &list[i]
					if q.ID == arg || fmt.Sprintf("%d", i+1) == arg {
						pick = q
						break
					}
				}
				if pick == nil {
					return false, fmt.Errorf("unknown session %q — use /sessions to list", arg)
				}
				fmt.Println(cli.Bold("Session " + pick.ID))
				fmt.Println(cli.Dim(pick.Query))
				if pick.Summary != "" {
					fmt.Println(cli.Accent("summary"))
					fmt.Println(cli.Clip(pick.Summary, 800))
				}
				if pick.Board.Plan.Summary != "" {
					fmt.Println(cli.Accent("plan"))
					fmt.Println(cli.Clip(pick.Board.Plan.Summary, 600))
				}
				if planBytes, err := os.ReadFile(filepath.Join(session.TurnDir(h.Config.SlmDir(), pick.ID), "PLAN.md")); err == nil && len(planBytes) > 0 {
					fmt.Println(cli.Accent("PLAN.md"))
					fmt.Println(cli.Clip(string(planBytes), 800))
				}
				return false, nil
			}
			for i, q := range list {
				fmt.Printf("  %2d  %s  %s\n", i+1, cli.Accent(q.ID), cli.Dim(cli.Clip(q.Query, 60)))
			}
			fmt.Println(cli.Dim("  /sessions <n|id>  show plan + summary"))
			fmt.Println(cli.Dim("  /resume [n|id]    continue interrupted run (ReAct history when present)"))
			interrupted, _ := session.ListInterrupted(h.Config.SlmDir())
			if len(interrupted) > 0 {
				fmt.Println(cli.Warn(fmt.Sprintf("  %d interrupted — /resume to continue", len(interrupted))))
			}
			return false, nil
		case "/compact":
			if arg == "heuristic" || arg == "llm" || arg == "auto" {
				h.Config.ContextCompactEngine = arg
				_ = h.Config.Save()
				fmt.Println(cli.Success("context_compact_engine = " + arg))
				arg = "context"
			}
			if arg == "context" || arg == "ctx" {
				res, err := h.Orchestrator.CompactContextNow()
				if err != nil {
					return false, err
				}
				if res.Compacted {
					fmt.Println(cli.Success(fmt.Sprintf("CONTEXT compacted %d→%d bytes (engine=%s)",
						res.BeforeBytes, res.AfterBytes, h.Config.ContextCompactEngine)))
				} else {
					fmt.Println(cli.Dim(fmt.Sprintf("CONTEXT already lean (%d bytes)", res.BeforeBytes)))
				}
				return false, nil
			}
			on := !sess.Compact()
			if arg == "on" || arg == "1" || arg == "true" {
				on = true
			}
			if arg == "off" || arg == "0" || arg == "false" {
				on = false
			}
			sess.SetCompact(on)
			h.Config.CompactMode = on
			_ = h.Config.Save()
			if on {
				fmt.Println(cli.Success("compact stream on — /compact context to summarize CONTEXT.md"))
			} else {
				fmt.Println(cli.Success("compact stream off"))
			}
			return false, nil
		case "/rewind":
			mgr := &rewind.Manager{SlmDir: h.Config.SlmDir(), Root: h.Config.Root}
			if arg == "" || arg == "list" {
				list, err := mgr.List()
				if err != nil {
					return false, err
				}
				if len(list) == 0 {
					fmt.Println(cli.Dim("no wave snapshots yet"))
					return false, nil
				}
				for i, s := range list {
					if i >= 10 {
						break
					}
					fmt.Printf("  %s  wave=%d  files=%d  %s\n", s.ID, s.Wave, len(s.Files), s.CreatedAt)
				}
				fmt.Println(cli.Dim("usage: /rewind <snapshot-id>"))
				return false, nil
			}
			n, err := mgr.Restore(arg)
			if err != nil {
				return false, err
			}
			fmt.Println(cli.Success(fmt.Sprintf("restored %d files from %s", n, arg)))
			return false, nil
		case "/stats":
			head := sess.LatencyHead()
			usage := sess.UsageHead()
			if head == "" && usage == "" {
				fmt.Println(cli.Dim("no stats yet — run a query first"))
				return false, nil
			}
			if head != "" {
				fmt.Println(cli.Bold("Latency (last run)"))
				fmt.Println("  " + head)
			}
			if usage != "" {
				fmt.Println(cli.Bold("Tokens (last run)"))
				fmt.Println("  " + usage)
			}
			return false, nil
		case "/permission":
			if arg == "" {
				fmt.Printf("permission=%s  shell=%s\n", h.Config.Permission, h.Config.ShellPermission)
				fmt.Println(cli.Dim("usage: /permission auto|dry-run|review  or  /permission shell=allow|ask|deny"))
				return false, nil
			}
			if strings.HasPrefix(arg, "shell=") {
				h.Config.ShellPermission = strings.TrimPrefix(arg, "shell=")
			} else {
				h.Config.Permission = arg
			}
			_ = h.Config.Save()
			if err := h.RebuildOrchestrator(); err != nil {
				return false, err
			}
			h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
			fmt.Println(cli.Success(fmt.Sprintf("permission=%s shell=%s (rebuilt)", h.Config.Permission, h.Config.ShellPermission)))
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/agents", "/agent":
			return false, handleTUIAgentCmd(h, sess, line)
		case "/skills":
			list, _ := h.Orchestrator.Skills().List()
			for _, sk := range list {
				fmt.Printf("  • %s — %s\n", sk.Name, sk.Description)
			}
			return false, nil
		case "/feedback", "/fb":
			return false, handleFeedbackCmd(h, arg)
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
			h.Config.ApplyPatch(config.Patch{Model: &arg})
			_ = h.Config.Save()
			if err := h.RebuildOrchestrator(); err != nil {
				fmt.Println(cli.Warn("model = " + arg + " (saved; rebuild failed: " + err.Error() + ")"))
			} else {
				h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
				fmt.Println(cli.Success("model = " + arg + " (active_stack cleared; orchestrator rebuilt)"))
			}
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/models":
			cat := models.Find(context.Background(), h.Config, arg, 24)
			fmt.Println(cli.Bold(fmt.Sprintf("Models (%s) auth=%s", cat.Provider, cat.Auth.Source)))
			if cat.Error != "" {
				fmt.Println(cli.Warn(cat.Error))
			}
			for i, m := range cat.Matches {
				cost := ""
				if i < len(cat.Costs) && cat.Costs[i].Known {
					cost = fmt.Sprintf("  ~$%.2f/$%.2f /MTok", cat.Costs[i].PromptPerMTok, cat.Costs[i].CompletionPerMTok)
				}
				cur := ""
				if m.ID == cat.Current {
					cur = " *"
				}
				fmt.Printf("  %s%s%s\n", m.Selector, cur, cli.Dim(cost))
			}
			if len(cat.EnabledModels) > 0 {
				fmt.Println(cli.Dim("enabled_models: " + strings.Join(cat.EnabledModels, ", ")))
			}
			return false, nil
		case "/mcp":
			st := h.Orchestrator.MCPStatus()
			fmt.Println(cli.Bold("MCP — " + st.MetaTool))
			fmt.Println(cli.Dim(st.Pattern))
			if !st.Enabled {
				fmt.Println(cli.Dim("no mcp_servers configured"))
				return false, nil
			}
			for _, srv := range st.Servers {
				conn := "offline"
				if srv.Connected {
					conn = "connected"
				}
				fmt.Printf("  %s [%s] %s tools=%d\n", srv.Name, conn, srv.Transport, srv.ToolCount)
				if len(srv.Tools) > 0 {
					fmt.Println(cli.Dim("    " + strings.Join(srv.Tools, ", ")))
				}
			}
			return false, nil
		case "/schema":
			for _, f := range config.Schema() {
				enum := ""
				if len(f.Enum) > 0 {
					enum = " (" + strings.Join(f.Enum, "|") + ")"
				}
				fmt.Printf("  %-28s %-8s %s%s\n", f.Key, f.Type, f.Label, enum)
			}
			fmt.Println(cli.Dim("--- slash extras ---"))
			for _, line := range config.SlashHelp() {
				fmt.Println("  " + line)
			}
			return false, nil
		case "/auth":
			parts2 := strings.Fields(arg)
			if len(parts2) >= 2 && parts2[0] == "set" {
				key := strings.Join(parts2[1:], " ")
				if err := authstore.Set(h.Config.SlmDir(), h.Config.Provider, key); err != nil {
					return false, err
				}
				h.Config.APIKey = key
				_ = h.Config.Save()
				if err := h.RebuildOrchestrator(); err != nil {
					fmt.Println(cli.Warn("auth.json saved; rebuild failed: " + err.Error()))
				} else {
					h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
					fmt.Println(cli.Success("API key saved to .slmcode/auth.json for " + h.Config.Provider))
				}
				return false, nil
			}
			st := models.ResolveAuth(h.Config)
			fmt.Printf("  provider=%s configured=%v source=%s\n", st.Provider, st.Configured, st.Source)
			if st.Message != "" {
				fmt.Println(cli.Dim("  " + st.Message))
			}
			fmt.Println(cli.Dim("  usage: /auth set <api-key>"))
			return false, nil
		case "/provider":
			if arg == "" {
				return false, fmt.Errorf("usage: /provider <name>")
			}
			h.Config.ApplyPatch(config.Patch{Provider: &arg})
			_ = h.Config.Save()
			if err := h.RebuildOrchestrator(); err != nil {
				fmt.Println(cli.Warn("provider = " + arg + " (saved; rebuild failed: " + err.Error() + ")"))
			} else {
				h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
				fmt.Println(cli.Success("provider = " + arg + " (active_stack cleared; orchestrator rebuilt)"))
			}
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

	// Async update notice: fetch once after the TUI paints so a slow network
	// never blocks startup, and delay so it lands after the first dashboard.
	go func() {
		time.Sleep(800 * time.Millisecond)
		info := updatecheck.Check(Version)
		if info.UpdateAvailable {
			fmt.Println(cli.Warn("new version v" + info.Latest + " available — run: slmcode update"))
		}
	}()

	fmt.Print(cli.Banner())
	return sess.RunInteractive()
}

// handleFeedbackCmd steers running agents via live feedback injected into the
// next agent prompt. Shared by the premium TUI and the chat REPL.
func handleFeedbackCmd(h *harness.Harness, args string) error {
	args = strings.TrimSpace(args)
	if h == nil || h.Orchestrator == nil {
		fmt.Println(cli.Error("live feedback unavailable — no active orchestrator (start a run first)"))
		return nil
	}
	if args == "" {
		cur := h.Orchestrator.LiveFeedback()
		if cur == "" {
			fmt.Println(cli.Dim("no active live feedback — send e.g. /feedback focus on pkg/loop, add tests"))
		} else {
			fmt.Println(cli.Cyan("live feedback: " + cur))
			fmt.Println(cli.Dim("clear it with /feedback clear"))
		}
		return nil
	}
	if args == "clear" || args == "c" {
		h.Orchestrator.ClearLiveFeedback()
		fmt.Println(cli.Success("live feedback cleared"))
		return nil
	}
	h.Orchestrator.SetLiveFeedback(args)
	fmt.Println(cli.Success("live feedback set — injected into the next agent call"))
	fmt.Println(cli.Cyan(args))
	return nil
}

func handleTUIAgentCmd(h *harness.Harness, sess *cli.LiveSession, line string) error {
	cmd, err := cli.ParseAgentCommand(line)
	if err != nil {
		return err
	}
	custom, _ := cli.LoadProjectCustoms(h.Config.AgentsDir())
	switch cmd.Action {
	case "list":
		fmt.Print(cli.FormatAgentListWithGlobals(custom, h.Config.Provider, h.Config.Model))
		return nil
	case "help":
		fmt.Println(cli.Bold("Agent CRUD (Studio parity)"))
		fmt.Println("  " + cli.Cyan("/agents") + "                         list specialists")
		fmt.Println("  " + cli.Cyan("/agent show <id>") + "                show one agent")
		fmt.Println("  " + cli.Cyan("/agent new") + "                       interactive create / builtin override")
		fmt.Println("  " + cli.Cyan("/agent new id=… provider=… …") + "   non-interactive create")
		fmt.Println("  " + cli.Cyan("/agent edit <id>") + "                 interactive edit")
		fmt.Println("  " + cli.Cyan("/agent edit <id> model=…") + "         patch fields")
		fmt.Println("  " + cli.Cyan("/agent delete <id>") + "               delete custom / clear override")
		fmt.Println(cli.Dim("  Fields: title description provider model endpoint skills tools max_iter max_tokens temperature system_prompt"))
		fmt.Println(cli.Dim("  Empty model/provider inherits active stack — see also: slmcode stack apply --agents"))
		return nil
	case "show":
		a := agents.AgentDetail(cmd.ID, custom)
		if a == nil {
			return fmt.Errorf("agent %q not found", cmd.ID)
		}
		enriched := agents.EnrichPublicSpecs([]map[string]interface{}{a}, h.Config.Provider, h.Config.Model, h.Config.ActiveStack)
		fmt.Print(cli.FormatAgentShow(enriched[0]))
		return nil
	case "delete":
		if err := agents.DeleteCustom(h.Config.AgentsDir(), cmd.ID); err != nil {
			return err
		}
		if err := h.RebuildOrchestrator(); err != nil {
			return fmt.Errorf("deleted but rebuild failed: %w", err)
		}
		h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
		fmt.Println(cli.Success("deleted " + cmd.ID + " (orchestrator rebuilt)"))
		return nil
	case "new", "edit":
		var base agents.CustomSpec
		if cmd.Action == "edit" {
			path := filepath.Join(h.Config.AgentsDir(), cmd.ID+".yaml")
			if got, rerr := agents.ReadCustomFile(path); rerr == nil {
				base = got
			} else if got, rerr := agents.ReadCustomFile(filepath.Join(h.Config.AgentsDir(), cmd.ID+".yml")); rerr == nil {
				base = got
			} else {
				// Seed from full builtin detail (includes system prompt).
				if pub := agents.AgentDetail(cmd.ID, custom); pub != nil {
					base.ID = cmd.ID
					if t, _ := pub["title"].(string); t != "" {
						base.Title = t
					}
					if d, _ := pub["description"].(string); d != "" {
						base.Description = d
					}
					if sp, _ := pub["system_prompt"].(string); sp != "" {
						base.SystemPrompt = sp
					}
					if p, _ := pub["provider"].(string); p != "" {
						base.Provider = p
					}
					if m, _ := pub["model"].(string); m != "" {
						base.Model = m
					}
					if e, _ := pub["endpoint"].(string); e != "" {
						base.Endpoint = e
					}
				} else {
					return fmt.Errorf("agent %q not found — use /agent new", cmd.ID)
				}
			}
		}
		var spec agents.CustomSpec
		if len(cmd.Fields) > 0 {
			id := cmd.ID
			if id == "" {
				id = cmd.Fields["id"]
			}
			spec = cli.SpecFromFields(id, cmd.Fields, &base)
		} else if cli.IsInteractive() {
			seed := base
			if cmd.Action == "new" && seed.ID == "" {
				seed.ID = cmd.Fields["id"]
			}
			var ferr error
			spec, ferr = cli.PromptAgentForm(os.Stdin, os.Stdout, seed, cmd.Action == "new")
			if ferr != nil {
				return ferr
			}
		} else {
			return fmt.Errorf("non-interactive: provide fields, e.g. /agent new id=foo title=Foo provider=openai")
		}
		path, err := agents.WriteCustom(h.Config.AgentsDir(), spec)
		if err != nil {
			return err
		}
		if err := h.RebuildOrchestrator(); err != nil {
			return fmt.Errorf("saved %s but rebuild failed: %w", path, err)
		}
		h.Orchestrator.OnEvent(func(e orchestrator.Event) { sess.Observe(e) })
		kind := "created"
		if cmd.Action == "edit" || agents.BuiltinIDs()[spec.ID] {
			kind = "saved"
		}
		fmt.Println(cli.Success(kind + " @" + spec.ID + " → " + path + " (orchestrator rebuilt)"))
		return nil
	default:
		return fmt.Errorf("unknown /agent action")
	}
}

func loadDashboard(ws *harness.Workspace) cli.DashboardState {
	_ = ws.Board.Load()
	b := ws.Board.Snapshot()
	st := cli.DashboardState{
		Root:            ws.Config.Root,
		Provider:        ws.Config.Provider,
		Model:           ws.Config.Model,
		Endpoint:        ws.Config.Endpoint,
		Backend:         ws.Config.Backend,
		Permission:      ws.Config.Permission,
		ShellPermission: ws.Config.ShellPermission,
		Compact:         ws.Config.CompactMode,
		Board:           &b,
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
		Root:            h.Config.Root,
		Provider:        h.Config.Provider,
		Model:           h.Config.Model,
		Endpoint:        h.Config.Endpoint,
		Backend:         h.Config.Backend,
		Permission:      h.Config.Permission,
		ShellPermission: h.Config.ShellPermission,
		Compact:         h.Config.CompactMode,
		Board:           &b,
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

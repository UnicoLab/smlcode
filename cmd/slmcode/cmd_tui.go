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
	"github.com/UnicoLab/slmcode/pkg/blocks"
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
		Example: "  slmcode tui\n  slmcode            # same thing",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPremiumTUI()
		},
	}
}

// slashCatalog is the single source of truth for REPL command discovery: help,
// the `/` fuzzy picker and Tab completion all read it.
func slashCatalog() *cli.SlashRegistry {
	return cli.NewSlashRegistry([]cli.SlashCommand{
		{Name: "/run", Args: "<query>", Help: "run the full pipeline", Group: "run"},
		{Name: "/stop", Help: "cancel the in-flight run (board is checkpointed)", Group: "run", LiveOK: true},
		{Name: "/resume", Args: "[id]", Help: "continue an interrupted run", Group: "run"},
		{Name: "/feedback", Aliases: []string{"/fb"}, Args: "<text>", Help: "steer the running agents (/feedback clear)", Group: "run", LiveOK: true},
		{Name: "/escalate", Args: "re_scope|retry|mark_done|abort", Help: "answer a pending escalate gate", Group: "run", LiveOK: true},
		{Name: "/plan", Args: "[auto|ask]", Help: "plan-approval gate mode", Group: "run", LiveOK: true},

		{Name: "/diff", Args: "[path]", Help: "working-tree diff incl. new files", Group: "review", LiveOK: true},
		{Name: "/apply", Help: "review pending agent writes", Group: "review"},
		{Name: "/reject", Args: "<path>", Help: "discard a pending proposal", Group: "review"},
		{Name: "/rewind", Args: "[snapshot]", Help: "list / restore wave snapshots", Group: "review"},
		{Name: "/errors", Help: "tail .slmcode/errors/errors.md", Group: "review", LiveOK: true},

		{Name: "/board", Help: "refresh + redraw the dashboard", Group: "session", LiveOK: true},
		{Name: "/status", Help: "connection / settings glance", Group: "session", LiveOK: true},
		{Name: "/refresh", Help: "repaint the dashboard", Group: "session", LiveOK: true},
		{Name: "/clear", Help: "reset the live stream and banners", Group: "session"},
		{Name: "/history", Args: "[n]", Help: "recent prompts (n recalls one into the buffer)", Group: "session"},
		{Name: "/sessions", Aliases: []string{"/queries"}, Args: "[n|id]", Help: "prior query turns", Group: "session"},
		{Name: "/stats", Help: "last-run latency + tokens", Group: "session", LiveOK: true},

		{Name: "/model", Args: "<id>", Help: "switch model (persists, rebuilds agents)", Group: "config"},
		{Name: "/models", Args: "[query]", Help: "search models (auth-aware, with costs)", Group: "config"},
		{Name: "/provider", Args: "<name>", Help: "switch provider", Group: "config"},
		{Name: "/auth", Args: "[set <key>]", Help: "auth status · save a key to .slmcode/auth.json", Group: "config"},
		{Name: "/permission", Args: "auto|dry-run|review | shell=allow|ask|deny", Help: "permission modes", Group: "config"},
		{Name: "/compact", Args: "[on|off|context|llm|auto|heuristic]", Help: "stream + context compaction", Group: "config"},
		{Name: "/schema", Help: "patchable config fields", Group: "config"},

		{Name: "/agents", Help: "list specialists", Group: "inspect"},
		{Name: "/agent", Args: "show|new|edit|delete <id>", Help: "agent CRUD (Studio parity)", Group: "inspect"},
		{Name: "/skills", Help: "list skills", Group: "inspect"},
		{Name: "/blocks", Help: "list building blocks", Group: "inspect"},
		{Name: "/pack", Args: "<pack-id>", Help: "apply a language pack (/blocks lists all 13)", Group: "config"},
		{Name: "/mcp", Help: "MCP connection status", Group: "inspect"},
		{Name: "/doctor", Help: "re-probe the endpoint and print health", Group: "inspect", LiveOK: true},
		{Name: "/studio", Help: "print the Studio URL", Group: "inspect", LiveOK: true},
		{Name: "/help", Aliases: []string{"/?"}, Help: "this screen", Group: "inspect", LiveOK: true},
		{Name: "/q", Aliases: []string{"/quit", "/exit"}, Help: "quit", Group: "inspect", LiveOK: true},
	})
}

// probeCache is shared by the REPL so repeated pre-flights are cheap.
var probeCache = cli.NewProbeCache(30 * time.Second)

// preflight probes the configured endpoint and reports whether a run may start.
func preflight(cfg *config.Config) cli.ProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return cli.ProbeCached(ctx, probeCache, cfg.Provider, cfg.Endpoint, cfg.Model, cfg.APIKey, 2*time.Second)
}

func runPremiumTUI() error { return runInteractiveSession(false) }

// runInteractiveSession drives both `slmcode` (boxed dashboard) and
// `slmcode chat` (plain transcript). One loop, one command set, one gate
// implementation — the classic REPL is no longer a second, blocking code path.
func runInteractiveSession(plain bool) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	// Bare `slmcode` used to scaffold .slmcode/ in whatever directory it was
	// run from, before it even checked for a terminal. Ask first.
	if !workspaceInitialized(root) {
		if !cli.IsInteractive() {
			fmt.Println(cli.Warn("no .slmcode/ workspace here — run `slmcode init` first"))
			fmt.Println(cli.Dim("  root: " + root))
			return failf(3, "workspace not initialized in %s", root)
		}
		if !confirm(fmt.Sprintf("Initialize a slmcode workspace in %s?", root), true) {
			fmt.Println(cli.Dim("nothing created — run `slmcode init` when you are ready"))
			return nil
		}
	}

	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	_ = ws.EnsureInitialized()
	_ = ensureSlmGitignore(ws.Config.SlmDir())

	st := loadDashboard(ws)
	if !cli.IsInteractive() {
		cli.PrintStaticDashboard(st)
		return nil
	}

	h, err := openHarness()
	if err != nil {
		return err
	}
	defer closeHarness(h)
	_ = h.EnsureInitialized()

	sess := cli.NewLiveSession()
	sess.SetShowDashboard(!plain)
	sess.SetSlashRegistry(slashCatalog())
	sess.SetState(loadDashboardFromHarness(h))
	sess.SetCompact(h.Config.CompactMode)
	sess.OnBoardRefresh(func() *plan.Board {
		_ = h.Orchestrator.Board().Load()
		snap := h.Orchestrator.Board().Snapshot()
		return &snap
	})
	sess.SetProbe(preflight(h.Config))

	// HITL gates render inline instead of pointing at a REST endpoint.
	_ = registerGates(h, sess)
	h.Orchestrator.OnEvent(func(e orchestrator.Event) {
		if cli.ShouldRender(e) {
			sess.Observe(e)
		} else {
			sess.Activity().Observe(e)
		}
	})

	var runMu sync.Mutex
	var cancelRun func()

	rebindOrchestrator := func() {
		_ = registerGates(h, sess)
		h.Orchestrator.OnEvent(func(e orchestrator.Event) {
			if cli.ShouldRender(e) {
				sess.Observe(e)
			} else {
				sess.Activity().Observe(e)
			}
		})
	}

	runFn := func(query string) error {
		probe := preflight(h.Config)
		sess.SetProbe(probe)
		if probe.State == cli.ProbeDown {
			sess.Console().Write(probe.Block())
			return fmt.Errorf("model server unreachable — %s", probe.Cause)
		}
		runMu.Lock()
		ctx, cancel := signalContext()
		cancelRun = cancel
		runMu.Unlock()
		defer func() {
			cancel()
			runMu.Lock()
			cancelRun = nil
			runMu.Unlock()
		}()

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

	stopFn := func() {
		runMu.Lock()
		defer runMu.Unlock()
		if cancelRun != nil {
			cancelRun()
		}
	}

	sess.OnRun(runFn)
	sess.OnStop(stopFn)
	sess.OnSteer(func(text string) {
		if h.Orchestrator != nil {
			h.Orchestrator.SetLiveFeedback(text)
		}
	})

	slashFn := makeSlashHandler(h, sess, runFn, stopFn, &runMu, &cancelRun, rebindOrchestrator)
	sess.OnSlash(slashFn)
	sess.OnLiveSlash(slashFn) // every command is reachable mid-run

	// Async update notice — routed into the status line instead of being
	// printed into the middle of the freshly painted dashboard.
	go func() {
		time.Sleep(800 * time.Millisecond)
		info := updatecheck.Check(Version)
		if info.UpdateAvailable {
			sess.Console().Write(cli.Warn("new version v" + info.Latest + " available — run: slmcode update"))
		}
	}()

	fmt.Print(cli.Banner())
	if plain {
		fmt.Println(cli.Info("Interactive mode — type a task or ? for commands. Esc interrupts a run."))
		cli.KeyVal("model", h.Config.Model)
		cli.KeyVal("permission", h.Config.Permission)
		fmt.Println()
	}
	return sess.RunInteractive()
}

// workspaceInitialized reports whether .slmcode/ already exists.
func workspaceInitialized(root string) bool {
	st, err := os.Stat(filepath.Join(root, ".slmcode"))
	return err == nil && st.IsDir()
}

// makeSlashHandler builds the REPL command dispatcher. It is shared by the
// idle and the mid-run paths so steering commands are never queued.
func makeSlashHandler(
	h *harness.Harness,
	sess *cli.LiveSession,
	runFn func(string) error,
	stopFn func(),
	runMu *sync.Mutex,
	cancelRun *func(),
	rebind func(),
) func(string) (bool, error) {
	out := sess.Console()
	say := func(s string) { out.Write(s) }

	return func(line string) (bool, error) {
		parts := strings.Fields(line)
		if len(parts) == 0 {
			return false, nil
		}
		cmdName := strings.ToLower(parts[0])
		arg := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))

		reg := slashCatalog()
		if _, ok := reg.Lookup(cmdName); !ok {
			cands := reg.Find(cmdName)
			if len(cands) == 1 {
				cmdName = cands[0].Name
			} else if len(cands) > 1 {
				say(cli.Warn("did you mean:"))
				say(reg.RenderPicker(strings.TrimPrefix(cmdName, "/"), out.Width(), 8))
				return false, nil
			}
		}

		switch cmdName {
		case "/q", "/quit", "/exit":
			return true, nil
		case "/help", "/?":
			say(reg.RenderHelp(out.Width()))
			return false, nil
		case "/clear":
			sess.ClearLive()
			say(cli.Dim("live stream cleared — type a query to run"))
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
			say(cli.Success("plan_approve = " + h.Config.PlanApprove))
			return false, nil
		case "/escalate":
			action := plan.NormalizeEscalateAction(strings.TrimSpace(arg))
			if strings.TrimSpace(arg) == "" {
				return false, fmt.Errorf("usage: /escalate re_scope|retry|mark_done|abort")
			}
			var ask plan.EscalateAsk
			ok, err := hitl.ReadAsk(h.Config.SlmDir(), "escalate", &ask)
			if err != nil {
				return false, err
			}
			if !ok || strings.TrimSpace(ask.ID) == "" {
				return false, fmt.Errorf("no pending escalate ask")
			}
			ans := plan.EscalateAnswer{
				AskID:      ask.ID,
				Action:     action,
				AnsweredAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := hitl.WriteAnswersOnce(h.Config.SlmDir(), "escalate", ans); err != nil {
				return false, err
			}
			say(cli.Success("escalate → " + action))
			return false, nil
		case "/history":
			hist := sess.History()
			if hist == nil {
				say(cli.Dim("no prompt history"))
				return false, nil
			}
			recent := hist.Recent(20)
			if len(recent) == 0 {
				say(cli.Dim("no prompt history yet"))
				return false, nil
			}
			if n := strings.TrimSpace(arg); n != "" {
				var idx int
				if _, err := fmt.Sscanf(n, "%d", &idx); err == nil && idx >= 1 && idx <= len(recent) {
					say(cli.Info("recalled: " + recent[idx-1]))
					say(cli.Dim("  press ↑ to edit it, or paste it back"))
					return false, nil
				}
				return false, fmt.Errorf("usage: /history [1-%d]", len(recent))
			}
			var b strings.Builder
			for i, q := range recent {
				fmt.Fprintf(&b, "  %2d  %s\n", i+1, cli.Dim(cli.Clip(q, 72)))
			}
			b.WriteString(cli.Dim("  ↑/↓ browse · Ctrl-R search · /history <n> recall"))
			say(b.String())
			return false, nil
		case "/refresh", "/board", "/status":
			_ = h.Orchestrator.Board().Load()
			sess.SetState(loadDashboardFromHarness(h))
			sess.SetProbe(preflight(h.Config))
			cli.RenderDashboard(os.Stdout, sess.State())
			return false, nil
		case "/doctor":
			p := cli.ProbeEndpoint(context.Background(), h.Config.Provider, h.Config.Endpoint,
				h.Config.Model, h.Config.APIKey, 2*time.Second)
			probeCache.Put(h.Config.Endpoint+"|"+h.Config.Model, p)
			sess.SetProbe(p)
			if p.State == cli.ProbeOK {
				say(cli.Success(fmt.Sprintf("endpoint ok — %s (%d ms)", p.Endpoint, p.Latency.Milliseconds())))
			} else {
				say(p.Block())
			}
			return false, nil
		case "/stop":
			runMu.Lock()
			active := *cancelRun != nil
			runMu.Unlock()
			if !active && !sess.Activity().Running() {
				say(cli.Dim("nothing is running"))
				return false, nil
			}
			stopFn()
			say(cli.Warn("stop requested — board + ReAct history checkpointed; use /resume to continue"))
			return false, nil
		case "/resume":
			id := strings.TrimSpace(arg)
			runMu.Lock()
			ctx, cancel := signalContext()
			*cancelRun = cancel
			runMu.Unlock()
			defer cancel()
			res, err := h.Resume(ctx, id)
			sess.SetState(loadDashboardFromHarness(h))
			if err != nil && (res == nil || !strings.Contains(strings.ToLower(err.Error()), "canceled")) {
				return false, err
			}
			if res != nil {
				if res.Success {
					say(cli.Success("resumed — " + res.Summary))
				} else {
					say(cli.Warn(res.Summary))
				}
			}
			return false, nil
		case "/errors":
			path := filepath.Join(h.Config.SlmDir(), "errors", "errors.md")
			data, err := os.ReadFile(path)
			if err != nil {
				say(cli.Dim("no errors.md yet"))
				return false, nil
			}
			say(string(data))
			return false, nil
		case "/diff":
			return false, showDiff(h.Config.Root, strings.TrimSpace(arg))
		case "/apply":
			patches, err := loadPending(h.Config.SlmDir())
			if err != nil {
				return false, err
			}
			if len(patches) == 0 {
				say(cli.Dim("nothing pending"))
				return false, nil
			}
			say(cli.Info(fmt.Sprintf("%d pending change(s) — leave the TUI and run `slmcode apply` to review them", len(patches))))
			for _, p := range patches {
				say(cli.DiffStatLine(p.diff(h.Config.Root)))
			}
			return false, nil
		case "/reject":
			if strings.TrimSpace(arg) == "" {
				return false, fmt.Errorf("usage: /reject <path>")
			}
			patches, err := loadPending(h.Config.SlmDir())
			if err != nil {
				return false, err
			}
			hits := filterPatches(patches, strings.Fields(arg))
			if len(hits) == 0 {
				return false, fmt.Errorf("no pending proposal matches %q", arg)
			}
			for _, p := range hits {
				_ = dropPatch(h.Config.SlmDir(), p)
				say(cli.Warn("rejected " + p.Path))
			}
			return false, nil
		case "/queries", "/sessions":
			return false, tuiSessions(h, arg, say)
		case "/compact":
			return false, tuiCompact(h, sess, arg, say)
		case "/rewind":
			mgr := &rewind.Manager{SlmDir: h.Config.SlmDir(), Root: h.Config.Root}
			if arg == "" || arg == "list" {
				list, err := mgr.List()
				if err != nil {
					return false, err
				}
				if len(list) == 0 {
					say(cli.Dim("no wave snapshots yet"))
					return false, nil
				}
				var b strings.Builder
				for i, s := range list {
					if i >= 10 {
						break
					}
					fmt.Fprintf(&b, "  %s  wave=%d  files=%d  %s\n", s.ID, s.Wave, len(s.Files), s.CreatedAt)
				}
				b.WriteString(cli.Dim("usage: /rewind <snapshot-id>"))
				say(b.String())
				return false, nil
			}
			n, err := mgr.Restore(arg)
			if err != nil {
				return false, err
			}
			say(cli.Success(fmt.Sprintf("restored %d files from %s", n, arg)))
			return false, nil
		case "/stats":
			head := sess.LatencyHead()
			usage := sess.UsageHead()
			if head == "" && usage == "" {
				say(cli.Dim("no stats yet — run a query first"))
				return false, nil
			}
			var b strings.Builder
			if head != "" {
				b.WriteString(cli.Bold("Latency (last run)") + "\n  " + head + "\n")
			}
			if usage != "" {
				b.WriteString(cli.Bold("Tokens (last run)") + "\n  " + usage)
			}
			say(b.String())
			return false, nil
		case "/permission":
			if arg == "" {
				say(fmt.Sprintf("permission=%s  shell=%s", h.Config.Permission, h.Config.ShellPermission))
				say(cli.Dim("usage: /permission auto|dry-run|review  or  /permission shell=allow|ask|deny"))
				return false, nil
			}
			if strings.HasPrefix(arg, "shell=") {
				h.Config.ShellPermission = strings.TrimPrefix(arg, "shell=")
			} else {
				h.Config.Permission = arg
				h.Config.DryRun = arg == "dry-run"
			}
			_ = h.Config.Save()
			if err := quietRebuild(h); err != nil {
				return false, err
			}
			rebind()
			say(cli.Success(fmt.Sprintf("permission=%s shell=%s (rebuilt)", h.Config.Permission, h.Config.ShellPermission)))
			sess.SetState(loadDashboardFromHarness(h))
			return false, nil
		case "/agents", "/agent":
			return false, handleTUIAgentCmd(h, sess, rebind, line)
		case "/blocks":
			reg2, err := blocks.Load(h.Config.Root)
			if err != nil {
				return false, err
			}
			var b strings.Builder
			for _, e := range reg2.Catalog("") {
				fmt.Fprintf(&b, "  %s  %-24s  %s\n", cli.Accent(e.Kind), e.ID, cli.Dim(e.Name))
			}
			say(strings.TrimRight(b.String(), "\n"))
			return false, nil
		case "/pack":
			if arg == "" {
				return false, fmt.Errorf("usage: /pack <pack-id> — `/blocks` lists every pack (go, python, react, typescript, web, rust, java, kotlin, dotnet, ruby, php, swift, cpp)")
			}
			reg2, err := blocks.Load(h.Config.Root)
			if err != nil {
				return false, err
			}
			res, err := blocks.ApplyPack(h.Config, reg2, arg, blocks.ApplyOptions{MaterializeAgents: true})
			if err != nil {
				return false, err
			}
			_ = h.Config.Save()
			say(cli.Success(fmt.Sprintf("pack applied: %s (pipeline: %s, qa_gate: %s)", res.PackID, res.PipelineID, res.QAGateCommand)))
			return false, nil
		case "/skills":
			list, _ := h.Orchestrator.Skills().List()
			var b strings.Builder
			for _, sk := range list {
				fmt.Fprintf(&b, "  • %s — %s\n", sk.Name, sk.Description)
			}
			say(strings.TrimRight(b.String(), "\n"))
			return false, nil
		case "/feedback", "/fb":
			return false, handleFeedbackCmd(h, arg, say)
		case "/studio":
			addr := h.Config.Listen
			if addr == "" {
				addr = "127.0.0.1:7420"
			}
			say(cli.Info("Studio: slmcode studio → http://" + addr))
			return false, nil
		case "/model":
			if arg == "" {
				return false, fmt.Errorf("usage: /model <id>")
			}
			h.Config.ApplyPatch(config.Patch{Model: &arg})
			_ = h.Config.Save()
			if err := quietRebuild(h); err != nil {
				say(cli.Warn("model = " + arg + " (saved; rebuild failed: " + err.Error() + ")"))
			} else {
				rebind()
				say(cli.Success("model = " + arg + " (active_stack cleared; orchestrator rebuilt)"))
			}
			sess.SetState(loadDashboardFromHarness(h))
			sess.SetProbe(preflight(h.Config))
			return false, nil
		case "/models":
			cat := models.Find(context.Background(), h.Config, arg, 24)
			var b strings.Builder
			b.WriteString(cli.Bold(fmt.Sprintf("Models (%s) auth=%s", cat.Provider, cat.Auth.Source)) + "\n")
			if cat.Error != "" {
				b.WriteString(cli.Warn(cat.Error) + "\n")
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
				fmt.Fprintf(&b, "  %s%s%s\n", m.Selector, cur, cli.Dim(cost))
			}
			if len(cat.EnabledModels) > 0 {
				b.WriteString(cli.Dim("enabled_models: " + strings.Join(cat.EnabledModels, ", ")))
			}
			say(strings.TrimRight(b.String(), "\n"))
			return false, nil
		case "/mcp":
			st := h.Orchestrator.MCPStatus()
			var b strings.Builder
			b.WriteString(cli.Bold("MCP — "+st.MetaTool) + "\n" + cli.Dim(st.Pattern) + "\n")
			if !st.Enabled {
				b.WriteString(cli.Dim("no mcp_servers configured"))
				say(b.String())
				return false, nil
			}
			for _, srv := range st.Servers {
				conn := "offline"
				if srv.Connected {
					conn = "connected"
				}
				fmt.Fprintf(&b, "  %s [%s] %s tools=%d\n", srv.Name, conn, srv.Transport, srv.ToolCount)
				if len(srv.Tools) > 0 {
					b.WriteString(cli.Dim("    "+strings.Join(srv.Tools, ", ")) + "\n")
				}
			}
			say(strings.TrimRight(b.String(), "\n"))
			return false, nil
		case "/schema":
			var b strings.Builder
			for _, f := range config.Schema() {
				enum := ""
				if len(f.Enum) > 0 {
					enum = " (" + strings.Join(f.Enum, "|") + ")"
				}
				fmt.Fprintf(&b, "  %-28s %-8s %s%s\n", f.Key, f.Type, f.Label, enum)
			}
			b.WriteString(cli.Dim("--- slash extras ---") + "\n")
			for _, l := range config.SlashHelp() {
				b.WriteString("  " + l + "\n")
			}
			say(strings.TrimRight(b.String(), "\n"))
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
				if err := quietRebuild(h); err != nil {
					say(cli.Warn("auth.json saved; rebuild failed: " + err.Error()))
				} else {
					rebind()
					say(cli.Success("API key saved to .slmcode/auth.json for " + h.Config.Provider))
				}
				return false, nil
			}
			as := models.ResolveAuth(h.Config)
			say(fmt.Sprintf("  provider=%s configured=%v source=%s", as.Provider, as.Configured, as.Source))
			if as.Message != "" {
				say(cli.Dim("  " + as.Message))
			}
			say(cli.Dim("  usage: /auth set <api-key>"))
			return false, nil
		case "/provider":
			if arg == "" {
				return false, fmt.Errorf("usage: /provider <name>")
			}
			h.Config.ApplyPatch(config.Patch{Provider: &arg})
			_ = h.Config.Save()
			if err := quietRebuild(h); err != nil {
				say(cli.Warn("provider = " + arg + " (saved; rebuild failed: " + err.Error() + ")"))
			} else {
				rebind()
				say(cli.Success("provider = " + arg + " (active_stack cleared; orchestrator rebuilt)"))
			}
			sess.SetState(loadDashboardFromHarness(h))
			sess.SetProbe(preflight(h.Config))
			return false, nil
		case "/run":
			if arg == "" {
				return false, fmt.Errorf("usage: /run <query>")
			}
			return false, runFn(arg)
		default:
			return false, fmt.Errorf("unknown %s — press ? for the command list", cmdName)
		}
	}
}

func tuiSessions(h *harness.Harness, arg string, say func(string)) error {
	list, err := session.ListQueries(h.Config.SlmDir())
	if err != nil || len(list) == 0 {
		say(cli.Dim("no query turns yet"))
		return nil
	}
	if arg != "" {
		var pick *session.Turn
		for i := range list {
			q := &list[i]
			if q.ID == arg || fmt.Sprintf("%d", i+1) == arg {
				pick = q
				break
			}
		}
		if pick == nil {
			return fmt.Errorf("unknown session %q — use /sessions to list", arg)
		}
		var b strings.Builder
		b.WriteString(cli.Bold("Session "+pick.ID) + "\n" + cli.Dim(pick.Query) + "\n")
		if pick.Summary != "" {
			b.WriteString(cli.Accent("summary") + "\n" + cli.Clip(pick.Summary, 800) + "\n")
		}
		if pick.Board.Plan.Summary != "" {
			b.WriteString(cli.Accent("plan") + "\n" + cli.Clip(pick.Board.Plan.Summary, 600) + "\n")
		}
		if planBytes, err := os.ReadFile(filepath.Join(session.TurnDir(h.Config.SlmDir(), pick.ID), "PLAN.md")); err == nil && len(planBytes) > 0 {
			b.WriteString(cli.Accent("PLAN.md") + "\n" + cli.Clip(string(planBytes), 800))
		}
		say(strings.TrimRight(b.String(), "\n"))
		return nil
	}
	var b strings.Builder
	for i, q := range list {
		fmt.Fprintf(&b, "  %2d  %s  %s\n", i+1, cli.Accent(q.ID), cli.Dim(cli.Clip(q.Query, 60)))
	}
	b.WriteString(cli.Dim("  /sessions <n|id>  show plan + summary") + "\n")
	b.WriteString(cli.Dim("  /resume [n|id]    continue interrupted run"))
	interrupted, _ := session.ListInterrupted(h.Config.SlmDir())
	if len(interrupted) > 0 {
		b.WriteString("\n" + cli.Warn(fmt.Sprintf("  %d interrupted — /resume to continue", len(interrupted))))
	}
	say(b.String())
	return nil
}

func tuiCompact(h *harness.Harness, sess *cli.LiveSession, arg string, say func(string)) error {
	if arg == "heuristic" || arg == "llm" || arg == "auto" {
		h.Config.ContextCompactEngine = arg
		_ = h.Config.Save()
		say(cli.Success("context_compact_engine = " + arg))
		arg = "context"
	}
	if arg == "context" || arg == "ctx" {
		res, err := h.Orchestrator.CompactContextNow()
		if err != nil {
			return err
		}
		if res.Compacted {
			say(cli.Success(fmt.Sprintf("CONTEXT compacted %d→%d bytes (engine=%s)",
				res.BeforeBytes, res.AfterBytes, h.Config.ContextCompactEngine)))
		} else {
			say(cli.Dim(fmt.Sprintf("CONTEXT already lean (%d bytes)", res.BeforeBytes)))
		}
		return nil
	}
	on := !sess.Compact()
	switch arg {
	case "on", "1", "true":
		on = true
	case "off", "0", "false":
		on = false
	}
	sess.SetCompact(on)
	h.Config.CompactMode = on
	_ = h.Config.Save()
	if on {
		say(cli.Success("compact stream on — /compact context to summarize CONTEXT.md"))
	} else {
		say(cli.Success("compact stream off"))
	}
	return nil
}

// handleFeedbackCmd steers running agents via live feedback injected into the
// next agent prompt. Shared by the premium TUI and the chat REPL.
func handleFeedbackCmd(h *harness.Harness, args string, say func(string)) error {
	if say == nil {
		say = func(s string) { fmt.Println(s) }
	}
	args = strings.TrimSpace(args)
	if h == nil || h.Orchestrator == nil {
		say(cli.Error("live feedback unavailable — no active orchestrator (start a run first)"))
		return nil
	}
	if args == "" {
		cur := h.Orchestrator.LiveFeedback()
		if cur == "" {
			say(cli.Dim("no active live feedback — send e.g. /feedback focus on pkg/loop, add tests"))
		} else {
			say(cli.Cyan("live feedback: " + cur))
			say(cli.Dim("clear it with /feedback clear"))
		}
		return nil
	}
	if args == "clear" || args == "c" {
		h.Orchestrator.ClearLiveFeedback()
		say(cli.Success("live feedback cleared"))
		return nil
	}
	h.Orchestrator.SetLiveFeedback(args)
	say(cli.Success("live feedback set — injected into the next agent call"))
	say(cli.Cyan(args))
	return nil
}

func handleTUIAgentCmd(h *harness.Harness, sess *cli.LiveSession, rebind func(), line string) error {
	cmd, err := cli.ParseAgentCommand(line)
	if err != nil {
		return err
	}
	out := sess.Console()
	custom, _ := cli.LoadProjectCustoms(h.Config.AgentsDir())
	switch cmd.Action {
	case "list":
		out.Write(cli.FormatAgentListWithGlobals(custom, h.Config.Provider, h.Config.Model))
		return nil
	case "help":
		out.Write(cli.Bold("Agent CRUD (Studio parity)") + "\n" +
			"  " + cli.Cyan("/agents") + "                         list specialists\n" +
			"  " + cli.Cyan("/agent show <id>") + "                show one agent\n" +
			"  " + cli.Cyan("/agent new") + "                       interactive create / builtin override\n" +
			"  " + cli.Cyan("/agent new id=… provider=… …") + "   non-interactive create\n" +
			"  " + cli.Cyan("/agent edit <id>") + "                 interactive edit\n" +
			"  " + cli.Cyan("/agent edit <id> model=…") + "         patch fields\n" +
			"  " + cli.Cyan("/agent delete <id>") + "               delete custom / clear override\n" +
			cli.Dim("  Fields: title description provider model endpoint skills tools max_iter max_tokens temperature system_prompt"))
		return nil
	case "show":
		a := agents.AgentDetail(cmd.ID, custom)
		if a == nil {
			return fmt.Errorf("agent %q not found", cmd.ID)
		}
		enriched := agents.EnrichPublicSpecs([]map[string]interface{}{a}, h.Config.Provider, h.Config.Model, h.Config.ActiveStack)
		out.Write(cli.FormatAgentShow(enriched[0]))
		return nil
	case "delete":
		if err := agents.DeleteCustom(h.Config.AgentsDir(), cmd.ID); err != nil {
			return err
		}
		if err := quietRebuild(h); err != nil {
			return fmt.Errorf("deleted but rebuild failed: %w", err)
		}
		rebind()
		out.Write(cli.Success("deleted " + cmd.ID + " (orchestrator rebuilt)"))
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
		} else {
			return fmt.Errorf("provide fields inline, e.g. /agent %s id=foo title=Foo provider=openai", cmd.Action)
		}
		path, err := agents.WriteCustom(h.Config.AgentsDir(), spec)
		if err != nil {
			return err
		}
		if err := quietRebuild(h); err != nil {
			return fmt.Errorf("saved %s but rebuild failed: %w", path, err)
		}
		rebind()
		kind := "created"
		if cmd.Action == "edit" || agents.BuiltinIDs()[spec.ID] {
			kind = "saved"
		}
		out.Write(cli.Success(kind + " @" + spec.ID + " → " + path + " (orchestrator rebuilt)"))
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

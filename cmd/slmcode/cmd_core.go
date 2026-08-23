package main

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/server"
	"github.com/UnicoLab/slmcode/pkg/updatecheck"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "init",
		Short:   "Create .slmcode/ memory, board.json, config (provider/model overridable)",
		Example: "  slmcode init\n  slmcode init --provider ollama --model qwen2.5-coder:14b",
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
			// Keep secrets and scratch state out of git: `slmcode commit` runs
			// `git add -A`, and .slmcode/auth.json holds provider API keys.
			if err := ensureSlmGitignore(ws.Config.SlmDir()); err != nil {
				fmt.Println(cli.Warn("could not write .slmcode/.gitignore: " + err.Error()))
			}
			fmt.Println(cli.Success("workspace ready"))
			cli.KeyVal("path", ws.Config.SlmDir())
			cli.KeyVal("gitignore", ".slmcode/.gitignore (auth.json, pending/, sessions/, …)")
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
		Example: `  slmcode run "add JWT auth"
  slmcode run --agent explorer "where is the retry logic?"
  slmcode run --dynamic "refactor the parser"
  slmcode run --on-gate-timeout=approve "…"   # headless: approve the plan`,
		Args: cobra.MinimumNArgs(1),
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
			dynamic, _ := cmd.Flags().GetBool("dynamic")
			noDynamic, _ := cmd.Flags().GetBool("no-dynamic")
			if mode != "" {
				h.Config.Mode = mode
			}
			if dynamic {
				h.Config.DynamicPipeline = true
			} else if noDynamic {
				h.Config.DynamicPipeline = false
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

			// Pre-flight: refuse to start against a dead endpoint instead of
			// marching through every phase emitting per-agent failures.
			probe := cli.ProbeEndpoint(cmd.Context(), h.Config.Provider, h.Config.Endpoint,
				h.Config.Model, h.Config.APIKey, 2*time.Second)
			if probe.State == cli.ProbeDown {
				fmt.Print(probe.Block())
				return failf(4, "model server unreachable — %s", probe.Cause)
			}

			ctx, cancel := signalContext()
			defer cancel()

			// HITL gates answer from this terminal; with no TTY they follow
			// --on-gate-timeout (default: stop) instead of auto-approving.
			registerGates(h, nil)

			fmt.Print(cli.Banner())
			cli.KeyVal("provider", h.Config.Provider)
			cli.KeyVal("model", h.Config.Model)
			cli.KeyVal("backend", h.Config.Backend)
			cli.KeyVal("mode", h.Config.Mode)
			if h.Config.Mode == config.ModeSpecialist {
				cli.KeyVal("specialist", h.Config.Specialist)
			}
			if h.Config.DynamicPipeline {
				cli.KeyVal("dynamic", "composer assembles a task-specific pipeline")
			}
			cli.KeyVal("think", fmt.Sprintf("%d passes", h.Config.ThinkPasses))
			cli.KeyVal("parallel", fmt.Sprintf("%d", h.Config.MaxParallel))
			fmt.Println()
			fmt.Println(cli.Bold("Query: ") + query)
			fmt.Println()

			status := cli.NewStatusTracker()
			h.Orchestrator.OnEvent(func(e orchestrator.Event) {
				if cli.ShouldRender(e) {
					cli.PrintEventWithStatus(e, status)
				} else {
					status.Observe(e)
				}
			})

			res, err := h.Run(ctx, query)
			if err != nil {
				return err
			}
			fmt.Println()
			fmt.Println(status.Footer())
			fmt.Println()
			if res.Success {
				fmt.Println(cli.Success(res.Summary))
			} else {
				fmt.Println(cli.Warn(res.Summary))
			}
			cli.KeyVal("duration", res.Duration.Round(time.Millisecond).String())
			cli.KeyVal("failed", fmt.Sprintf("%d", res.FailedTasks))
			cli.KeyVal("board", h.Config.SlmDir()+"/board.json")
			cli.KeyVal("errors", h.Config.SlmDir()+"/errors/errors.md")
			if n := pendingCount(h.Config.SlmDir()); n > 0 {
				fmt.Println(cli.Warn(fmt.Sprintf("%d change(s) awaiting review — slmcode apply", n)))
			}
			if !res.Success {
				return failf(5, "run finished with failures — inspect board / promote escalated tasks")
			}
			return nil
		},
	}
	cmd.Flags().String("mode", "", "full | specialist (overrides config)")
	cmd.Flags().String("agent", "", "run a single specialist (worker, explorer, …)")
	cmd.Flags().Bool("dynamic", false, "run the composer specialist to assemble a task-specific pipeline (default: on)")
	cmd.Flags().Bool("no-dynamic", false, "disable the dynamic pipeline (use the static pipeline)")
	cmd.Flags().StringSlice("skill", nil, "pin/load skill by name (repeatable); also accepts @skill:name in query")
	return cmd
}

// portIsBound reports whether a TCP port is already in use.
//
// This used to shell out to lsof, which meant that on any machine without lsof
// installed the function returned false and conflict detection silently no-oped.
// A plain net.Listen is dependency-free and always correct.
func portIsBound(addr string) bool {
	host, port := resolveAddr(addr)
	if port == 0 {
		return false
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// resolveAddr resolves an address like "127.0.0.1:7420" or ":7420" and returns
// host and port separately for process lookup.
func resolveAddr(addr string) (host string, port int) {
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 7420
	}
	port, _ = strconv.Atoi(p)
	if h == "" {
		h = "127.0.0.1"
	}
	return h, port
}

// killExistingStudio kills the slmcode process listening on addr.
//
// Safety: the previous version killed any PID on the port whose *cmdline
// contained the substring* "slmcode" — so `vim /path/to/slmcode/foo.go`
// matched. This compares the executable basename exactly and never runs unless
// the user explicitly asked for it with --kill.
func killExistingStudio(addr string, force bool) bool {
	_, port := resolveAddr(addr)
	if port == 0 {
		return false
	}
	// port comes from the local --addr/--port flag, formatted as a plain int;
	// argv-only invocation, no shell involved.
	out, err := exec.Command("lsof", "-ti", "tcp:"+strconv.Itoa(port)).Output() //nolint:gosec // port is a local CLI flag value, argv-only (no shell)
	if err != nil {
		fmt.Println(cli.Warn("cannot identify the process on port " + strconv.Itoa(port) + " (lsof unavailable)"))
		fmt.Println(cli.Dim("  use --port-auto to pick a free port instead"))
		return false
	}

	pids := strings.Fields(string(out))
	killed := false
	for _, pidStr := range pids {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == os.Getpid() {
			continue
		}
		if !processIsSlmcode(pid, pidStr) {
			fmt.Println(cli.Warn(fmt.Sprintf("pid %d holds port %d but is not slmcode — refusing to kill it", pid, port)))
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		sig := syscall.SIGTERM
		if force {
			sig = syscall.SIGKILL
		}
		if err := proc.Signal(sig); err == nil {
			fmt.Println(cli.Warn(fmt.Sprintf("killed slmcode studio (pid %d) on port %d", pid, port)))
			killed = true
		}
	}

	if killed {
		time.Sleep(300 * time.Millisecond)
	}
	return killed
}

// processIsSlmcode verifies a PID's executable basename is exactly "slmcode".
func processIsSlmcode(pid int, pidStr string) bool {
	// Linux: /proc/<pid>/cmdline is NUL-separated; argv[0] is the executable.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		argv0 := string(data)
		if i := strings.IndexByte(argv0, 0); i >= 0 {
			argv0 = argv0[:i]
		}
		return filepath.Base(strings.TrimSpace(argv0)) == "slmcode"
	}
	// macOS/BSD: ps prints the command name.
	psOut, err := exec.Command("ps", "-p", pidStr, "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return filepath.Base(strings.TrimSpace(string(psOut))) == "slmcode"
}

// nextFreePort finds an available port starting from the given base address.
// It tries up to 10 increments.
func nextFreeAddr(addr string) string {
	host, port := resolveAddr(addr)
	for i := 0; i < 10; i++ {
		try := net.JoinHostPort(host, strconv.Itoa(port+i))
		if !portIsBound(try) {
			return try
		}
	}
	return addr // fallback to original
}

func studioCmd() *cobra.Command {
	var noKill bool
	var forceKill bool
	var portAuto bool
	var noPortAuto bool

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch Studio UI + API (live kanban, context edit, SSE)",
		Long: `Launch the Studio web UI and API server.

If the configured port is busy the server moves to the next free one and says
so. Killing whatever holds the port is never automatic — pass --kill, which
only ever signals a process whose executable is exactly "slmcode".`,
		Example: `  slmcode studio                    # default port (7420), auto-picks a free one if busy
  slmcode studio --listen :9000     # custom port
  slmcode studio --no-port-auto     # fail instead of moving to a free port
  slmcode studio --kill             # terminate an existing slmcode on that port first`,
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
			if noPortAuto {
				portAuto = false
			}

			if portIsBound(addr) {
				switch {
				case forceKill && !noKill:
					killExistingStudio(addr, true)
					if portIsBound(addr) {
						return failf(1, "port %s is still in use after --kill", addr)
					}
				case portAuto:
					newAddr := nextFreeAddr(addr)
					if newAddr == addr {
						return failf(1, "port %s is in use and no free port was found nearby", addr)
					}
					fmt.Println(cli.Warn(fmt.Sprintf("port %s is in use → using %s instead", addr, newAddr)))
					fmt.Println(cli.Dim("  pass --kill to terminate the existing slmcode, or --no-port-auto to fail instead"))
					addr = newAddr
				default:
					fmt.Println(cli.Warn(fmt.Sprintf("port %s is in use.", addr)))
					fmt.Println(cli.Dim("  --kill terminates an existing slmcode there · omit --no-port-auto to move to a free port"))
					return failf(1, "port %s is in use", addr)
				}
			}

			uiFS, err := fs.Sub(uiEmbed, "ui")
			if err != nil {
				return err
			}
			url := "http://" + addr
			fmt.Print(cli.Banner())
			fmt.Println(cli.Success("Studio listening"))
			fmt.Printf("  url             \033]8;;%s\033\\%s\033]8;;\033\\\n", url, url)
			cli.KeyVal("root", h.Config.Root)
			cli.KeyVal("provider", h.Config.Provider+" / "+h.Config.Model)
			go openBrowser(url)
			fmt.Println(cli.Dim("\n  Opening browser… Ctrl+C to stop.\n"))
			return server.New(h, uiFS).ListenAndServe(addr)
		},
	}
	cmd.Flags().StringVar(&flagListen, "listen", "", "listen address (default from config)")
	cmd.Flags().BoolVar(&noKill, "no-kill", false, "never signal another process (default behavior)")
	cmd.Flags().BoolVar(&forceKill, "kill", false, "terminate an existing slmcode studio holding the port")
	cmd.Flags().BoolVar(&portAuto, "port-auto", true, "move to the next free port when the target is busy")
	cmd.Flags().BoolVar(&noPortAuto, "no-port-auto", false, "fail instead of moving to a free port")
	return cmd
}

func statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Snapshot of query, dynamic pipeline, plan gate, diagnostics, and board counts",
		Example: "  slmcode status\n  slmcode status --json | jq .board",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			_ = ws.Board.Load()
			b := ws.Board.Snapshot()
			by := b.ByColumn()
			q, _ := ws.Store.Read("QUERY.md")

			if asJSON {
				counts := map[string]int{}
				for _, col := range plan.Columns() {
					counts[col] = len(by[col])
				}
				probe := cli.ProbeEndpoint(cmd.Context(), ws.Config.Provider, ws.Config.Endpoint,
					ws.Config.Model, ws.Config.APIKey, 2*time.Second)
				return emitJSON(map[string]any{
					"root":     ws.Config.Root,
					"provider": ws.Config.Provider,
					"model":    ws.Config.Model,
					"endpoint": ws.Config.Endpoint,
					"backend":  ws.Config.Backend,
					"query":    strings.TrimSpace(q),
					"board": map[string]any{
						"total":   len(b.Tasks),
						"columns": counts,
						"plan":    b.Plan.Summary,
					},
					"connection": map[string]any{
						"state":      string(probe.State),
						"latency_ms": probe.Latency.Milliseconds(),
						"status":     probe.Status,
						"cause":      probe.Cause,
						"remedy":     probe.Remedy,
					},
					"pending": pendingCount(ws.Config.SlmDir()),
				})
			}

			cli.Header("Status")
			cli.KeyVal("root", ws.Config.Root)
			cli.KeyVal("provider", ws.Config.Provider)
			cli.KeyVal("model", ws.Config.Model)
			cli.KeyVal("backend", ws.Config.Backend)
			fmt.Print(formatPipelineStatus(ws.Config))
			if comp := formatLatestCompositionStatus(ws.Config); comp != "" {
				fmt.Print(comp)
			}
			fmt.Println()
			for _, col := range []string{"to_scope", "scoped", "ready_to_dev", "in_progress", "in_review", "done", "blocked"} {
				n := len(by[col])
				if n == 0 {
					continue
				}
				fmt.Printf("  %s  %s\n", cli.ColumnColor(cli.PadWidth(col, 14)), cli.Bold(fmt.Sprintf("%d", n)))
			}
			if n := pendingCount(ws.Config.SlmDir()); n > 0 {
				fmt.Println()
				fmt.Println(cli.Warn(fmt.Sprintf("%d change(s) awaiting review — slmcode apply", n)))
			}
			fmt.Println()
			fmt.Println(cli.Dim(strings.TrimSpace(q)))
			if diag := formatLatestRunDiagnostics(ws.Config.SlmDir()); diag != "" {
				fmt.Print(diag)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

// pendingCount counts review-mode proposals waiting in .slmcode/pending.
func pendingCount(slmDir string) int {
	p, _ := loadPending(slmDir)
	return len(p)
}

func versionCmd() *cobra.Command {
	var check bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Print version (pass --check to query GitHub for a newer release)",
		Example: "  slmcode version\n  slmcode version --check\n  slmcode version --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			binary := ""
			if p, err := os.Executable(); err == nil {
				if real, err2 := filepath.EvalSymlinks(p); err2 == nil {
					p = real
				}
				binary = p
			}
			// The update check used to run on every `slmcode version`, blocking
			// for up to the full HTTP timeout whenever GitHub was unreachable.
			// It is now opt-in.
			var info updatecheck.Info
			if check {
				info = updatecheck.Check(Version)
			}

			if asJSON {
				return emitJSON(map[string]any{
					"version":          Version,
					"commit":           GitCommit,
					"built":            BuildTime,
					"binary":           binary,
					"source":           SourceRoot,
					"latest":           info.Latest,
					"update_available": info.UpdateAvailable,
					"check_error":      info.Error,
				})
			}

			fmt.Println(cli.Accent("slmcode") + " " + cli.Bold(Version))
			fmt.Println(cli.Dim("SLM engine · GoLangGraph specialists · any OpenAI-compatible provider"))
			if binary != "" {
				fmt.Println(cli.Dim("binary: " + binary))
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
			if check {
				switch {
				case info.UpdateAvailable:
					fmt.Println(cli.Warn("new version v" + info.Latest + " available — run: slmcode update"))
				case info.Error != "":
					fmt.Println(cli.Dim("update check unavailable: " + info.Error))
				default:
					fmt.Println(cli.Success("up to date"))
				}
			} else {
				fmt.Println(cli.Dim("update: slmcode update   ·   check: slmcode version --check"))
			}
			fmt.Println(cli.Accent("https://unicolab.ai") + cli.Dim("  —  ") + cli.Bold(cli.Magenta("AI")) + " " + cli.Dim("&") + " " + cli.Bold(cli.Blue("Innovation")) + "  " + cli.Magenta("♥"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "query GitHub for a newer release")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

package main

import (
	"context"
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

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/server"
	"github.com/UnicoLab/slmcode/pkg/updatecheck"
)

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create .slmcode/ memory, board.json and a minimal config",
		Long: `Scaffold a workspace.

The config written here is MINIMAL: only what init detected (provider, model,
endpoint when it is not the provider default, and the language pack). Every
other knob follows the built-in defaults and your user-level config, so a
later release's better default reaches this project without editing a file.
Run ` + "`slmcode config show --all`" + ` to see the full effective surface.`,
		Example: "  slmcode init\n  slmcode init --provider ollama --model qwen2.5-coder:14b",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			// A brand-new workspace has no previous config.yaml/pipeline.yaml to
			// back up. Remember that so the ".bak of a file that never existed"
			// left behind by pack application can be cleaned up below.
			fresh := freshWorkspaceFiles(ws.Config.SlmDir())

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

			// Write intent, not a snapshot of every default.
			detected := []string{"provider", "model"}
			if ws.Config.Endpoint != config.DefaultEndpointFor(ws.Config.Provider) {
				detected = append(detected, "endpoint")
			}
			// Detection lives in pkg/blocks, next to the pack definitions: it is
			// deterministic, precedence-ranked, proves a language from file
			// CONTENT (Detect.Contains) and skips nested sub-projects. The CLI
			// used to keep a third, weaker marker list here and run it AFTER
			// InitWorkspace, overwriting the right answer — a Kotlin repo got
			// active_pack: java next to ./gradlew test, a TypeScript repo got
			// active_pack: web next to npm test.
			if pack := blocks.DetectPack(ws.Config.Root, ws.Config.Root); pack != "" {
				ws.Config.ActivePack = pack
				detected = append(detected, "active_pack")
			}
			if err := ws.Config.SaveInitial(detected...); err != nil {
				return err
			}
			// After the LAST write of this init, not before: pack application
			// and SaveInitial each rewrite config.yaml/pipeline.yaml.
			dropPhantomBackups(fresh)

			fmt.Println(cli.Success("workspace ready"))
			cli.KeyVal("path", ws.Config.SlmDir())
			cli.KeyVal("gitignore", fmt.Sprintf(".slmcode/.gitignore — %d rules (auth.json, sessions/, memory/, metrics/, …)",
				len(config.SlmIgnoreEntries)))
			cli.KeyVal("provider", ws.Config.Provider)
			cli.KeyVal("model", ws.Config.Model)
			cli.KeyVal("endpoint", ws.Config.Endpoint)
			if ws.Config.ActivePack != "" {
				cli.KeyVal("pack", ws.Config.ActivePack+" (detected)")
			}
			if p := config.UserConfigPath(); p != "" {
				cli.KeyVal("user config", p+" (inherited)")
			}
			cli.KeyVal("config", fmt.Sprintf("%s — %d key(s), the rest inherited",
				ws.Config.ConfigPath(), len(ws.Config.SavedKeys())))
			fmt.Println()
			fmt.Println(cli.Dim("  slmcode config show --all      every key and its effective value"))
			// The next step depends on whether there is a model to talk to.
			// Sending a new user straight to `slmcode run` when nothing is
			// listening earns them an exit-4 refusal on their first command.
			probe := cli.ProbeEndpoint(cmd.Context(), ws.Config.Provider, ws.Config.Endpoint,
				ws.Config.Model, ws.Config.APIKey, 1500*time.Millisecond)
			if probe.State == cli.ProbeDown {
				fmt.Println(cli.Warn("no model server answered at " + ws.Config.Endpoint))
				if probe.Remedy != "" {
					fmt.Println(cli.Dim("  " + probe.Remedy))
				}
				fmt.Println(cli.Info("next: slmcode doctor  — check the connection, then `slmcode run -v \"…\"`"))
				return nil
			}
			fmt.Println(cli.Info("next: slmcode run -v \"…\"  — agents fill context, plan, and tasks"))
			return nil
		},
	}
	return cmd
}

// initBackupCandidates are the workspace files that pack application rewrites
// during `slmcode init`, each of which grows a ".bak" sibling on the second
// write.
var initBackupCandidates = []string{"config.yaml", "pipeline.yaml"}

// freshWorkspaceFiles returns the candidates that do NOT exist yet.
//
// On a brand-new workspace init writes config.yaml and pipeline.yaml, then
// applies the detected language pack, which rewrites both — and the atomic
// writer dutifully saves a backup of a file that is seconds old and that the
// user has never seen. The result was a first-run workspace containing
// config.yaml.bak and pipeline.yaml.bak: two files that look like evidence of
// a botched upgrade. Backups of files that predate this init are kept.
func freshWorkspaceFiles(slmDir string) []string {
	if slmDir == "" {
		return nil
	}
	var fresh []string
	for _, name := range initBackupCandidates {
		if _, err := os.Stat(filepath.Join(slmDir, name)); os.IsNotExist(err) {
			fresh = append(fresh, filepath.Join(slmDir, name))
		}
	}
	return fresh
}

// dropPhantomBackups removes the .bak siblings of files this init created.
func dropPhantomBackups(fresh []string) {
	for _, path := range fresh {
		_ = os.Remove(path + ".bak")
	}
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [query...]",
		Short: "Full pipeline or single specialist (see --mode / --agent)",
		Example: `  slmcode run "add JWT auth"
  slmcode run --agent explorer "where is the retry logic?"
  slmcode run --dynamic "refactor the parser"
  slmcode run "…"                             # no TTY: gates auto-approve, logged
  slmcode run --on-gate-timeout=stop "…"      # no TTY: refuse up front instead`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			// Calibrate before serving. Studio is a full run surface — it starts
			// runs, and it is where a user most often SWITCHES MODEL, which is
			// exactly the moment a stale or missing profile matters. Leaving it
			// to `run` meant a model picked in Studio was governed by defaults
			// written for a different one.
			//
			// Cached per (model, endpoint), so this is a no-op on every launch
			// after the first for a given pair.
			autoCalibrate(cmd.Context(), h)
			defer closeHarness(h)
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

			// Pre-flight #1, and it costs nothing: resolve every human-in-the-
			// loop gate BEFORE the first model call. A headless run whose gate
			// policy would stop it refuses here, at t=0, instead of spending
			// its whole budget planning and discarding the result at a gate
			// nobody can answer.
			if err := prepareHeadlessGates(cmd, h); err != nil {
				return err
			}

			// Pre-flight #2: refuse to start against a dead endpoint instead of
			// marching through every phase emitting per-agent failures.
			probe := cli.ProbeEndpoint(cmd.Context(), h.Config.Provider, h.Config.Endpoint,
				h.Config.Model, h.Config.APIKey, 2*time.Second)
			if probe.State == cli.ProbeDown {
				fmt.Print(probe.Block())
				return failf(4, "model server unreachable — %s", probe.Cause)
			}

			// Pre-flight #3: measure what this endpoint can actually do. The
			// probe above already proved it is up, so a failure here is a
			// calibration problem and is reported as one — never as an outage.
			// Bounded, cached per (model, endpoint), and it only fills in
			// values the user has not set.
			autoCalibrate(cmd.Context(), h)

			ctx, cancel := signalContext()
			defer cancel()

			// HITL gates answer from this terminal (registerGates installs a
			// plain terminal prompt when there is no dashboard). With no TTY
			// the policy resolved by prepareHeadlessGates above applies, and
			// the engine has already announced what each gate will do.
			gates := registerGates(h, nil)

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
			// Why it is what it is, exactly once — only when the endpoint-aware
			// default lowered it and calibration did not already explain it.
			printMaxParallelNotice(h.Config)
			fmt.Println()
			fmt.Println(cli.Bold("Query: ") + query)
			fmt.Println()

			status := cli.NewStatusTracker()
			// Feed the footer real board progress. Without this its counters
			// (finished agent CALLS) were labeled done/fail and read as tasks.
			status.SetTaskSource(boardProgress(h.Config.SlmDir()))
			h.Orchestrator.OnEvent(func(e orchestrator.Event) {
				if cli.ShouldRender(e) {
					cli.PrintEventWithStatus(e, status)
				} else {
					status.Observe(e)
				}
			})

			// What the tree looks like BEFORE the engine runs, so the closing
			// summary can say what this run changed rather than what the
			// working tree happens to contain.
			before := fingerprintDirty(h.Config.Root)

			res, err := h.Run(ctx, query)
			if err != nil {
				return runFailure(ctx, err, gates, outcomeOptions{
					root:      h.Config.Root,
					slmDir:    h.Config.SlmDir(),
					before:    before,
					board:     boardSnapshot(h.Config.SlmDir()),
					overrides: gates.overrides,
				})
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
			tally := tallyBoard(res.Board)
			if n := len(gates.overrides); n > tally.forced {
				tally.forced = n
			}
			printTaskTally(tally)
			cli.KeyVal("board", h.Config.SlmDir()+"/board.json")
			// Only when it holds something: a path printed on every run,
			// successful ones included, teaches the reader to ignore it.
			if p := errorsLogPath(h.Config.SlmDir()); p != "" {
				cli.KeyVal("errors", p)
			}
			printRunOutcome(outcomeOptions{
				root:      h.Config.Root,
				slmDir:    h.Config.SlmDir(),
				before:    before,
				board:     res.Board,
				success:   res.Success,
				overrides: gates.overrides,
			})
			if !res.Success {
				fmt.Println()
				if gates.interrupted {
					fmt.Println(cli.Dim("  interrupted at a gate — `slmcode session resume` picks the board back up"))
					return failf(130, "interrupted")
				}
				if gates.blocked() {
					fmt.Println(cli.Dim("  " + strings.ReplaceAll(gates.hint(), "\n", "\n  ")))
					printRetainedRunHint(h.Config.SlmDir())
					return failf(6, "stopped at a human-in-the-loop gate")
				}
				return failf(5, "run finished with failures — see `slmcode board` and `slmcode apply`")
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
	psOut, err := exec.Command("ps", "-p", pidStr, "-o", "comm=").Output() //nolint:gosec // pidStr is a numeric PID string, argv-only (no shell)
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
	var devCORS bool
	var noAuth bool

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch Studio UI + API (live kanban, context edit, SSE)",
		Long: `Launch the Studio web UI and API server.

If the configured port is busy the server moves to the next free one and says
so. Killing whatever holds the port is never automatic — pass --kill, which
only ever signals a process whose executable is exactly "slmcode".

Studio is a local agent with file-read, config-write, API-key-write and
run-start capability. It refuses non-loopback hosts and cross-origin requests,
and mints a random session token per launch — the printed URL carries it as
?t=<token>. Ctrl-C shuts it down gracefully, unwinding any in-flight run.`,
		Example: `  slmcode studio                    # default port (7420), auto-picks a free one if busy
  slmcode studio --listen :9000     # custom port
  slmcode studio --no-port-auto     # fail instead of moving to a free port
  slmcode studio --kill             # terminate an existing slmcode on that port first
  slmcode studio --dev-cors         # allow the Vite dev server (npm run dev in web/)
  slmcode studio --no-auth          # drop the session token (loopback only, still)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			defer closeHarness(h)
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

			// Studio can read files, write config, store API keys and start
			// runs. It is loopback-only and emits no permissive CORS headers,
			// and by default it also mints a random per-launch token that the
			// printed URL carries as ?t=…; presenting it once mints an
			// HttpOnly SameSite=Strict cookie and EVERY later request — the
			// HTML shell included — is checked against it.
			//
			// Be precise about what that token buys: it bounds other ORIGINS
			// and a local listener that is not this user. It does NOT bound a
			// process running as this user, because the token is printed to
			// this terminal's stdout and lives in this process's memory.
			// --no-auth drops it; --dev-cors lets the Vite dev server talk.
			opts := server.DefaultOptions()
			if noAuth {
				opts.NoAuth = true
				opts.GenerateToken = false
			}
			if devCORS {
				opts.DevCORS = true
			}
			srv := server.NewWithOptions(h, uiFS, opts)
			url := srv.URL(addr)

			fmt.Print(cli.Banner())
			fmt.Println(cli.Success("Studio listening"))
			// OSC-8 hyperlink only when ANSI is on. Piped to a file or a log,
			// the escape wrapper turned the one thing the user has to copy —
			// the tokenised URL — into unusable bytes.
			if cli.ColorEnabled() {
				fmt.Printf("  url             \033]8;;%s\033\\%s\033]8;;\033\\\n", url, url)
			} else {
				cli.KeyVal("url", url)
			}
			cli.KeyVal("root", h.Config.Root)
			cli.KeyVal("provider", h.Config.Provider+" / "+h.Config.Model)
			if srv.AuthEnabled() {
				cli.KeyVal("auth", "session token required (the URL above carries it)")
			} else {
				fmt.Println(cli.Warn("auth disabled — any local process can drive this agent"))
			}
			if devCORS {
				fmt.Println(cli.Warn("dev CORS enabled for " + strings.Join(server.DevOrigins, ", ")))
			}
			if studioUIIsPlaceholder(uiFS) {
				fmt.Println(cli.Warn("the Studio UI is not built into this binary — the page will say so"))
				fmt.Println(cli.Dim("  fix: make bootstrap   (or `make ui-react` if Node is already installed)"))
				fmt.Println(cli.Dim("  the API and the CLI work regardless; only the React SPA is missing"))
			}
			go openBrowser(url)
			fmt.Println(cli.Dim("\n  Opening browser… Ctrl+C to stop.\n"))

			ctx, cancel := signalContext()
			defer cancel()
			errCh := make(chan error, 1)
			go func() { errCh <- srv.ListenAndServe(addr) }()
			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				// Graceful stop: unwind an in-flight run and close every SSE
				// stream instead of truncating responses mid-write.
				fmt.Println(cli.Dim("  stopping Studio…"))
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutCancel()
				if err := srv.Shutdown(shutCtx); err != nil {
					return err
				}
				<-errCh
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&flagListen, "listen", "", "listen address (default from config)")
	cmd.Flags().BoolVar(&noKill, "no-kill", false, "never signal another process (default behavior)")
	cmd.Flags().BoolVar(&forceKill, "kill", false, "terminate an existing slmcode studio holding the port")
	cmd.Flags().BoolVar(&portAuto, "port-auto", true, "move to the next free port when the target is busy")
	cmd.Flags().BoolVar(&noPortAuto, "no-port-auto", false, "fail instead of moving to a free port")
	cmd.Flags().BoolVar(&devCORS, "dev-cors", false, "allow the Vite dev server origins (npm run dev in web/)")
	cmd.Flags().BoolVar(&noAuth, "no-auth", false, "disable the session token (loopback enforcement stays)")
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
			noteUninitialized(ws.Config.Root)
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

// runFailure turns the engine's error into the message a user can act on.
//
// Two failures used to surface as raw Go error text with no next step: an
// interrupt ("context canceled") and a gate nobody could answer ("plan not
// approved"). Both now carry the documented exit code and say what to do.
func runFailure(ctx context.Context, err error, gates *gateAudit, opt outcomeOptions) error {
	if err == nil {
		return nil
	}
	// A run that died still usually touched the tree, and "did my files
	// change?" is a MORE urgent question after a failure than after a success.
	opt.failure = err
	printRunOutcome(opt)
	// One definition of "this was a cancellation", shared with the engine:
	// errors.Is(context.Canceled) plus the exact provider phrase in either
	// spelling. The CLI used to re-implement it inline and got the answer from
	// substring matching alone.
	canceled := loop.IsContextCancelErr(err)
	switch {
	case gates != nil && gates.interrupted:
		fmt.Println()
		fmt.Println(cli.Dim("  the board was checkpointed — pick the run back up with"))
		fmt.Println(cli.Dim("    slmcode session resume"))
		return failf(130, "interrupted at the gate — nothing was lost")
	case canceled && ctx.Err() == nil:
		// Nobody interrupted this run: our own signal context is still live, so
		// the cancellation came from inside the engine — a speculative racer's
		// loser, a slot timeout — and surfaced as if the user had pressed
		// Ctrl-C. Say what actually happened instead of claiming an interrupt.
		fmt.Println()
		fmt.Println(cli.Dim("  the run was canceled internally — no interrupt was sent from this terminal."))
		fmt.Println(cli.Dim("  the board was checkpointed; `slmcode session resume` picks it up."))
		fmt.Println(cli.Dim("  re-run with --vv to see which agent call was canceled."))
		return failf(1, "run canceled inside the engine (not by you) — %s", cli.Clip(err.Error(), 160))
	case canceled:
		fmt.Println()
		fmt.Println(cli.Dim("  the board and the ReAct history were checkpointed — pick the run back up with"))
		fmt.Println(cli.Dim("    slmcode session resume"))
		return failf(130, "interrupted — nothing was lost")
	case gates.blocked():
		fmt.Println()
		fmt.Println(cli.Dim("  " + strings.ReplaceAll(gates.hint(), "\n", "\n  ")))
		printRetainedRunHint(opt.slmDir)
		// Keep the engine's own reason: it names the gate and, for the plan
		// gate, the run id whose board is still on disk. Replacing it with a
		// bare "stopped at a gate" is how a stop came to look like a discard.
		return failf(6, "stopped at a human-in-the-loop gate — %s", cli.Clip(err.Error(), 200))
	}
	// A gate that stopped the run without tripping the audit (an explicit
	// [n]o at the terminal, a rejected plan from Studio) still produced a
	// board. Say what it kept.
	if strings.Contains(strings.ToLower(err.Error()), "plan not approved") {
		printRetainedRunHint(opt.slmDir)
	}
	return err
}

// boardProgress returns a probe of board.json for the run footer: tasks in the
// done column, and the total the board holds.
func boardProgress(slmDir string) func() (int, int) {
	store := plan.NewLiveStore(slmDir)
	return func() (int, int) {
		if err := store.Load(); err != nil {
			return 0, 0
		}
		b := store.Snapshot()
		done := 0
		for _, t := range b.Tasks {
			if t.Column == plan.ColDone {
				done++
			}
		}
		return done, len(b.Tasks)
	}
}

// studioUIIsPlaceholder reports that no built Studio SPA is embedded in this
// binary, so the server will answer navigations with the built-in placeholder
// page instead.
//
// `go build` alone embeds only cmd/slmcode/ui/.gitkeep — the Vite output is
// gitignored build product, not source — so a from-source binary has no SPA
// until `make bootstrap` runs. The CLI used to announce "Studio listening" and
// open a browser without a word about it, so the first thing a from-source user
// saw was a placeholder with no explanation in the terminal they were looking
// at.
//
// The predicate is server.UIIsBuilt so the CLI's startup warning and the page
// the server actually serves can never disagree; it used to grep a checked-in
// index.html for a magic string, which forced that placeholder to be a tracked
// file that `make ui-react` then overwrote.
func studioUIIsPlaceholder(uiFS fs.FS) bool {
	return !server.UIIsBuilt(uiFS)
}

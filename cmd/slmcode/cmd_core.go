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
	"github.com/UnicoLab/slmcode/pkg/server"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .slmcode/ memory, board.json, config (provider/model overridable)",
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

			status := cli.NewStatusTracker()
			h.Orchestrator.OnEvent(func(e orchestrator.Event) {
				cli.PrintEventWithStatus(e, status)
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
			if !res.Success {
				return fmt.Errorf("run finished with failures — inspect board / promote escalated tasks")
			}
			return nil
		},
	}
	cmd.Flags().String("mode", "", "full | specialist (overrides config)")
	cmd.Flags().String("agent", "", "run a single specialist (worker, explorer, …)")
	cmd.Flags().StringSlice("skill", nil, "pin/load skill by name (repeatable); also accepts @skill:name in query")
	return cmd
}

// portIsBound checks whether a TCP port is already in use by any process.
func portIsBound(addr string) bool {
	_, port := resolveAddr(addr)
	if port == 0 {
		return false
	}
	// lsof is the most reliable cross-platform way to check port binding.
	out, err := exec.Command("lsof", "-ti", "tcp:"+strconv.Itoa(port)).Output()
	if err != nil {
		return false // lsof exits non-zero when nothing is bound
	}
	return len(strings.TrimSpace(string(out))) > 0
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

// killExistingStudio finds and kills any slmcode process listening on the given
// address. Returns true if one was found and killed.
func killExistingStudio(addr string) bool {
	_, port := resolveAddr(addr)
	if port == 0 {
		return false
	}

	// Use lsof to find the PID bound to this TCP port.
	out, err := exec.Command("lsof", "-ti", "tcp:"+strconv.Itoa(port)).Output()
	if err != nil {
		return false // lsof returns non-zero if no match
	}

	pids := strings.Fields(string(out))
	killed := false
	for _, pidStr := range pids {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == os.Getpid() {
			continue
		}

		// Verify this is a slmcode process before killing.
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			// macOS: use ps to check command name.
			psOut, psErr := exec.Command("ps", "-p", pidStr, "-o", "comm=").Output()
			if psErr != nil || !strings.Contains(string(psOut), "slmcode") {
				continue
			}
		} else if !strings.Contains(string(cmdline), "slmcode") {
			continue
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGTERM); err == nil {
			fmt.Println(cli.Warn(fmt.Sprintf("Killed existing slmcode studio (pid %d) on port %d", pid, port)))
			killed = true
		}
	}

	if killed {
		// Brief wait for the port to free up.
		time.Sleep(300 * time.Millisecond)
	}
	return killed
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
	var killExisting bool
	var portAuto bool

	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Launch Studio UI + API (live kanban, context edit, SSE)",
		Long: `Launch the Studio web UI and API server.

By default, auto-kills any existing slmcode instance on the same port.
Use --no-kill to skip cleanup, or --port-auto to switch to the next free port.

Examples:
  slmcode studio                    # default port (7420), auto-kill existing
  slmcode studio --listen :9000     # custom port
  slmcode studio --port-auto        # auto-switch to next free port if busy
  slmcode studio --no-kill          # fail if port is in use (classic behaviour)`,
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

			// ── Port conflict resolution ──
			if portIsBound(addr) {
				if portAuto {
					// Auto-switch to next free port.
					newAddr := nextFreeAddr(addr)
					if newAddr != addr {
						fmt.Println(cli.Warn(fmt.Sprintf("Port %s in use → auto-switching to %s", addr, newAddr)))
						addr = newAddr
					}
				} else if !killExisting {
					// Default: kill the existing instance.
					killExistingStudio(addr)
					if portIsBound(addr) {
						// Still bound — maybe a non-slmcode process.
						fmt.Println(cli.Warn(fmt.Sprintf("Port %s is in use by another process.", addr)))
						fmt.Println(cli.Dim("  Use --port-auto to auto-switch, or --no-kill to see the original error."))
						return fmt.Errorf("port %s is in use and could not be freed — try --port-auto", addr)
					}
				}
				// If --no-kill (killExisting=true but we skipped above) — let ListenAndServe fail naturally.
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
			if portAuto {
				fmt.Println(cli.Dim("  (--port-auto enabled — will auto-switch on next conflict)"))
			}
			fmt.Println(cli.Dim("\n  Edit context & kanban while agents run. Ctrl+C to stop.\n"))
			return server.New(h, uiFS).ListenAndServe(addr)
		},
	}
	cmd.Flags().StringVar(&flagListen, "listen", "", "listen address (default from config)")
	cmd.Flags().BoolVar(&killExisting, "no-kill", false, "do NOT auto-kill existing studio on the same port")
	cmd.Flags().BoolVar(&portAuto, "port-auto", false, "auto-switch to next free port if the target is in use")
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
			fmt.Println(cli.Dim("SLM engine · GoLangGraph specialists · any OpenAI-compatible provider"))
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
			fmt.Println(cli.Accent("https://unicolab.ai") + cli.Dim(" — AI & Innovation"))
		},
	}
}

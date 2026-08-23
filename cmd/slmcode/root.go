package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/server"
)

//go:embed all:ui
var uiEmbed embed.FS

var (
	flagRoot        string
	flagModel       string
	flagProvider    string
	flagEndpoint    string
	flagAPIKey      string
	flagBackend     string
	flagVerbose     bool
	flagVeryVerbose bool
	flagLogLevel    string
	flagColor       string
	flagDryRun      bool
	flagMaxParallel int
	flagMaxRetries  int
	flagThink       int
	flagListen      string
	flagNoBanner    bool
	flagGateTimeout string
)

func main() {
	// Keep CLI UX clean — GoLangGraph registries are chatty at Info.
	// NOTE: this only tames the *standard* logrus logger; the dependency also
	// builds private loggers with logrus.New(), which is why noisy construction
	// is additionally wrapped in cli.QuietStderr (see openHarnessQuiet).
	logrus.SetLevel(logrus.WarnLevel)
	server.Version = Version

	root := &cobra.Command{
		Use:   "slmcode",
		Short: "SLM-first coding harness (any OpenAI-compat LLM · atomic tasks · live kanban)",
		Long: cli.Banner() + `
` + cli.Dim(`Designed for local SLMs: scoped context packs, markdown memory, multi-pass
thinking, parallel specialists, and a live kanban you can edit while agents run.

Default (no subcommand): premium interactive TUI — connection, board, live events,
agents, queries, errors, diffs, and keyboard UX. Docs sometimes spell it "smlcode";
the binary is slmcode.

Point at any OpenAI-compatible endpoint (oMLX, Ollama, LM Studio, cloud OpenAI, …):
  slmcode run --provider openai --model gpt-4o-mini --endpoint https://api.openai.com/v1 "…"
  slmcode run --provider ollama --model qwen2.5-coder:14b "…"
  SLMCODE_PROVIDER=lmstudio SLMCODE_MODEL=… slmcode run "…"

Non-interactive: --json (status/doctor/readiness/board/version/apply/config/blocks),
--color=never, --log-level, --on-gate-timeout=stop (never auto-approves a plan),
and deterministic exit codes: 2 usage · 3 no workspace · 4 provider unreachable
· 5 failing tasks · 130 interrupted.`),
		Example: `  slmcode                          # premium TUI (default)
  slmcode init
  slmcode run "add JWT auth"
  slmcode apply                    # review agent changes hunk by hunk
  slmcode diff
  slmcode board
  slmcode status --json
  slmcode studio
  slmcode doctor`,
		SilenceUsage: true,
		// Cobra printed "Error: …" and main printed "✖ …" for the same failure.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `slmcode` → premium TUI (Studio-parity dashboard + REPL).
			return runPremiumTUI()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			mode, err := cli.ParseColorMode(flagColor)
			if err != nil {
				return err
			}
			cli.SetColorMode(mode)

			lvl := flagLogLevel
			if lvl == "" {
				switch {
				case flagVeryVerbose:
					lvl = "debug"
				case flagVerbose:
					lvl = "info"
				}
			}
			parsed, ok := cli.ParseLogLevel(lvl)
			if !ok {
				return fmt.Errorf("invalid --log-level %q (want error|warn|info|debug)", flagLogLevel)
			}
			cli.SetLogLevel(parsed)
			return nil
		},
	}

	root.PersistentFlags().StringVar(&flagRoot, "root", "", "project root (default: cwd)")
	root.PersistentFlags().StringVar(&flagModel, "model", "", "model id (any id your provider serves)")
	root.PersistentFlags().StringVar(&flagProvider, "provider", "", "omlx|ollama|openai|lmstudio|openrouter|vllm|… (any OpenAI-compat name)")
	root.PersistentFlags().StringVar(&flagEndpoint, "endpoint", "", "API base URL (e.g. http://127.0.0.1:1234/v1)")
	root.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (or SLMCODE_API_KEY / OPENAI_API_KEY)")
	root.PersistentFlags().StringVar(&flagBackend, "backend", "", "slmcode|claude-code")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output (same as --log-level=info)")
	root.PersistentFlags().BoolVar(&flagVeryVerbose, "vv", false, "very verbose output (same as --log-level=debug)")
	root.PersistentFlags().StringVar(&flagLogLevel, "log-level", "", "error|warn|info|debug — what the CLI renders")
	root.PersistentFlags().StringVar(&flagColor, "color", "auto", "auto|always|never — ANSI color policy")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "do not write code files")
	root.PersistentFlags().IntVar(&flagMaxParallel, "parallel", 0, "max parallel workers")
	root.PersistentFlags().IntVar(&flagMaxRetries, "retries", 0, "review/correct retries")
	root.PersistentFlags().IntVar(&flagThink, "think-passes", 0, "multi-pass think loops")
	root.PersistentFlags().BoolVar(&flagNoBanner, "no-banner", false, "hide ASCII banner on help")
	root.PersistentFlags().StringVar(&flagGateTimeout, "on-gate-timeout", "stop",
		"approve|reject|stop — what a HITL gate does with no TTY attached")

	groupRun := &cobra.Group{ID: "run", Title: "Run & steer:"}
	groupReview := &cobra.Group{ID: "review", Title: "Review changes:"}
	groupConfig := &cobra.Group{ID: "config", Title: "Configure:"}
	groupInspect := &cobra.Group{ID: "inspect", Title: "Inspect:"}
	root.AddGroup(groupRun, groupReview, groupConfig, groupInspect)

	inGroup := func(id string, cmds ...*cobra.Command) []*cobra.Command {
		for _, c := range cmds {
			c.GroupID = id
		}
		return cmds
	}

	var all []*cobra.Command
	all = append(all, inGroup("run", tuiCmd(), initCmd(), runCmd(), chatCmd(), studioCmd(), watchCmd())...)
	all = append(all, inGroup("review", applyCmd(), rejectCmd(), diffCmd(), commitCmd())...)
	all = append(all, inGroup("config", configCmd(), stackCmd(), agentCmd(), blockCmd(), skillsCmd(), updateCmd())...)
	all = append(all, inGroup("inspect", statusCmd(), boardCmd(), composeCmd(), readinessCmd(), taskCmd(),
		contextCmd(), docsCmd(), planCmd(), sessionCmd(), doctorCmd(), evalCmd(), versionCmd())...)
	all = append(all, completionCmd())
	root.AddCommand(all...)

	defer cli.RestoreAllRaw()
	if err := root.Execute(); err != nil {
		cli.RestoreAllRaw()
		fmt.Fprintln(os.Stderr, cli.Error(err.Error()))
		os.Exit(exitCodeFor(err))
	}
}

// exitCodeFor maps an error onto a deterministic exit code so scripts can
// branch on the outcome:
//
//	0  success
//	1  generic failure
//	2  usage / invalid argument
//	3  not initialized / missing workspace
//	4  provider unreachable
//	5  run finished with failing tasks
//	6  a HITL gate was not answered (non-interactive)
//	130 interrupted
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	if ec, ok := err.(exitCoder); ok {
		return ec.ExitCode()
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled"), strings.Contains(msg, "interrupted"):
		return 130
	case strings.Contains(msg, "unknown flag"), strings.Contains(msg, "invalid argument"),
		strings.Contains(msg, "accepts "), strings.Contains(msg, "required flag"):
		return 2
	}
	return 1
}

type exitCoder interface{ ExitCode() int }

// codedError carries a deterministic exit code out of a command.
type codedError struct {
	err  error
	code int
}

func (c codedError) Error() string { return c.err.Error() }
func (c codedError) Unwrap() error { return c.err }
func (c codedError) ExitCode() int { return c.code }

func failf(code int, format string, a ...any) error {
	return codedError{err: fmt.Errorf(format, a...), code: code}
}

func projectRoot() (string, error) {
	root := flagRoot
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(root)
}

// openWorkspace is the fast path (board/docs/skills) — no LLM spin-up.
func openWorkspace() (*harness.Workspace, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}
	ws, err := harness.OpenWorkspace(root)
	if err != nil {
		return nil, err
	}
	applyFlags(ws.Config)
	return ws, nil
}

// openHarness starts the full SLM engine (run/studio/doctor LLM checks).
//
// Orchestrator construction is the noisy step: the GoLangGraph tool/LLM/agent
// registries create private logrus loggers that dump ~20 INFO lines to stderr
// on every build (and every rebuild triggered by /model, /provider, …). Wrap it
// so only warnings and errors survive.
func openHarness() (*harness.Harness, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}
	var h *harness.Harness
	var orch *orchestrator.Orchestrator
	var innerErr error
	cli.QuietStderr(func() {
		h, innerErr = harness.New(root)
		if innerErr != nil {
			return
		}
		applyFlags(h.Config)
		orch, innerErr = orchestrator.New(h.Config)
	}, emitQuietLine)
	if innerErr != nil {
		return nil, innerErr
	}
	h.Orchestrator = orch
	return h, nil
}

// emitQuietLine re-surfaces the warn/error lines captured from the dependency.
func emitQuietLine(level, line string) {
	switch level {
	case "error":
		fmt.Fprintln(os.Stderr, cli.Error(cli.Clip(line, 300)))
	case "warning":
		if cli.CurrentLogLevel() >= cli.LogWarn {
			fmt.Fprintln(os.Stderr, cli.Warn(cli.Clip(line, 300)))
		}
	}
}

// quietRebuild wraps an orchestrator rebuild in the same stderr filter.
func quietRebuild(h *harness.Harness) error {
	var err error
	cli.QuietStderr(func() { err = h.RebuildOrchestrator() }, emitQuietLine)
	return err
}

func applyFlags(c *config.Config) {
	// Endpoint provenance: --provider must not clobber an endpoint the user
	// pinned via flag, env, or an explicit non-default config value.
	// User layer first (lowest precedence above defaults), so a project value,
	// an env var or a flag still wins.
	if _, unknown := applyUserConfigLayer(c, c.SlmDir()); len(unknown) > 0 && cli.CurrentLogLevel() >= cli.LogWarn {
		fmt.Fprintln(os.Stderr, cli.Warn("user config: ignoring unknown keys "+strings.Join(unknown, ", ")))
	}

	fileEndpoint := strings.TrimSpace(c.Endpoint)
	fileProvider := c.Provider
	endpointPinned := flagEndpoint != "" ||
		strings.TrimSpace(os.Getenv("SLMCODE_ENDPOINT")) != "" ||
		(fileEndpoint != "" && fileEndpoint != config.DefaultEndpointFor(fileProvider))

	c.ApplyEnv()
	providerChanged := false
	if flagProvider != "" {
		next := config.NormalizeProvider(flagProvider)
		if next != config.NormalizeProvider(c.Provider) {
			providerChanged = true
		}
		c.Provider = next
	}
	if flagModel != "" {
		c.Model = flagModel
	}
	if flagEndpoint != "" {
		c.Endpoint = flagEndpoint
	} else if providerChanged && !endpointPinned {
		c.Endpoint = config.DefaultEndpointFor(c.Provider)
	}
	if flagAPIKey != "" {
		c.APIKey = flagAPIKey
	}
	if flagBackend != "" {
		c.Backend = flagBackend
	}
	if flagVerbose || flagVeryVerbose {
		c.Verbose = true
	}
	if flagDryRun {
		c.DryRun = true
	}
	if flagMaxParallel > 0 {
		c.MaxParallel = flagMaxParallel
	}
	if flagMaxRetries > 0 {
		c.MaxRetries = flagMaxRetries
	}
	if flagThink > 0 {
		c.ThinkPasses = flagThink
	}
	c.ResolveAPIKey()
}

// signalContext returns a context cancelled by the first SIGINT/SIGTERM, and
// hard-exits on the second.
//
// The previous implementation read exactly one signal and then let its
// goroutine die while signal.Notify kept Go's default SIGINT handling disabled
// forever — so a second Ctrl-C was swallowed and the process could not be
// killed from its own terminal. It also leaked a goroutine and a registration
// per call. The returned cancel func now deregisters.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	stop := make(chan struct{})

	go func() {
		defer signal.Stop(ch)
		select {
		case <-stop:
			return
		case <-ch:
		}
		cli.RestoreAllRaw()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, cli.Warn("interrupted — board preserved in .slmcode/board.json; press Ctrl-C again to force quit"))
		cancel()
		select {
		case <-stop:
			return
		case <-ch:
			cli.RestoreAllRaw()
			fmt.Fprintln(os.Stderr, cli.Error("force quit"))
			os.Exit(130)
		}
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() { close(stop) })
		cancel()
	}
}

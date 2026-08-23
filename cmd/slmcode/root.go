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

	// Self-improvement / budget overrides. These mirror config keys of the
	// same name; a flag is the top of the precedence chain.
	flagNoExplore          bool
	flagEvolve             bool
	flagNoEvolve           bool
	flagMaxTaskCalls       int
	flagArchitectEditor    bool
	flagStructuredDecoding string
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
		Example: `  slmcode init                     # start here: scaffolds .slmcode/ in this project
  slmcode doctor                   # provider, model, endpoint, workspace
  slmcode run "add JWT auth"       # full pipeline; pauses at the plan gate for one keystroke
  slmcode                          # premium TUI (the default with no subcommand)
  slmcode apply                    # review agent changes file by file
  slmcode diff
  slmcode board
  slmcode status --json
  slmcode studio                   # web UI + SSE API`,
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
	root.PersistentFlags().BoolVar(&flagNoExplore, "no-explore", false,
		"greedy bandit, no exploration — reproducible runs (config: deterministic)")
	root.PersistentFlags().BoolVar(&flagEvolve, "evolve", false,
		"force the self-improvement engine on (memory, repair rules, bandit)")
	root.PersistentFlags().BoolVar(&flagNoEvolve, "no-evolve", false,
		"disable the self-improvement engine for this run")
	root.PersistentFlags().IntVar(&flagMaxTaskCalls, "max-task-calls", 0,
		"per-task LLM call budget (config: max_task_calls)")
	root.PersistentFlags().BoolVar(&flagArchitectEditor, "architect-editor", false,
		"enable the describer→editor role pair (config: architect_editor)")
	root.PersistentFlags().StringVar(&flagStructuredDecoding, "structured-decoding", "",
		"auto|off — constrained decoding policy (config: structured_decoding)")

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

	// A parent command given an unrecognized subcommand printed its help and
	// exited 0: `slmcode memory nosuchthing` looked like a success. Make every
	// group command reject unknown arguments.
	var all []*cobra.Command
	all = append(all, inGroup("run", tuiCmd(), initCmd(), runCmd(), chatCmd(), studioCmd(), watchCmd())...)
	all = append(all, inGroup("review", applyCmd(), rejectCmd(), diffCmd(), commitCmd())...)
	all = append(all, inGroup("config", configCmd(), authCmd(), stackCmd(), agentCmd(), blockCmd(), skillsCmd(), updateCmd())...)
	all = append(all, inGroup("inspect", statusCmd(), boardCmd(), composeCmd(), readinessCmd(), taskCmd(),
		contextCmd(), docsCmd(), planCmd(), sessionCmd(), doctorCmd(), evalCmd(),
		memoryCmd(), evolveCmd(), metricsCmd(), versionCmd())...)
	all = append(all, completionCmd())
	for _, c := range all {
		rejectUnknownSubcommands(c)
	}
	root.AddCommand(all...)

	defer cli.RestoreAllRaw()
	// Drop the dependency's per-agent Info records for the whole command, not
	// just for engine construction: GoLangGraph builds a private logrus logger
	// (bound to whatever os.Stderr is at the time) for every agent it creates,
	// so without this a run's transcript is interleaved with ~40 "Executing
	// node" lines and the TUI's boxes are shredded. --log-level=debug and
	// SLMCODE_NO_QUIET=1 turn the filter off and show every line.
	var execErr error
	cli.FilterStderr(func() { execErr = root.Execute() })
	if execErr != nil {
		cli.RestoreAllRaw()
		fmt.Fprintln(os.Stderr, cli.Error(execErr.Error()))
		os.Exit(exitCodeFor(execErr))
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
	// Every shape cobra uses to say "you typed the command wrong" maps to 2.
	// "unknown command" and "requires at least N arg(s)" used to return 1, so a
	// script could not tell a typo from a real failure.
	case strings.Contains(msg, "unknown flag"), strings.Contains(msg, "invalid argument"),
		strings.Contains(msg, "accepts "), strings.Contains(msg, "required flag"),
		strings.Contains(msg, "unknown command"), strings.Contains(msg, "unknown shorthand"),
		strings.Contains(msg, "requires at least"), strings.Contains(msg, "requires exactly"),
		strings.Contains(msg, "arg(s), received"), strings.Contains(msg, "invalid value"),
		strings.Contains(msg, "flag needs an argument"):
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
	// SetOrchestrator, not a bare assignment: harness.New already built one, and
	// dropping that pointer strands its stdio MCP subprocesses and evolve store
	// for the lifetime of the process.
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		fmt.Fprintln(os.Stderr, cli.Warn("previous orchestrator did not close cleanly: "+cerr.Error()))
	}
	return h, nil
}

// closeHarness reaps the harness's engine (MCP subprocesses, evolve store) on
// command exit. Every openHarness caller defers it — a CLI that leaves stdio
// MCP children behind on exit orphans them to init.
func closeHarness(h *harness.Harness) {
	if h == nil {
		return
	}
	if err := h.Close(); err != nil && cli.CurrentLogLevel() >= cli.LogWarn {
		fmt.Fprintln(os.Stderr, cli.Warn("harness shutdown: "+cli.Clip(err.Error(), 200)))
	}
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
	// The defaults → user file → project file → env chain is resolved by
	// config.Load; this function only adds the top layer, the flags.
	// Endpoint provenance: --provider must not clobber an endpoint the user
	// pinned via flag, env, or an explicit non-default config value.
	for _, w := range c.Provenance().Warnings {
		if cli.CurrentLogLevel() >= cli.LogWarn {
			fmt.Fprintln(os.Stderr, cli.Warn(w))
		}
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
	if flagNoExplore {
		c.Deterministic = true
	}
	switch {
	case flagNoEvolve:
		c.Evolve = false
	case flagEvolve:
		c.Evolve = true
	}
	if flagMaxTaskCalls > 0 {
		c.MaxTaskCalls = flagMaxTaskCalls
	}
	if flagArchitectEditor {
		c.ArchitectEditor = true
	}
	if v := strings.TrimSpace(flagStructuredDecoding); v != "" {
		c.StructuredDecoding = config.NormalizeStructuredDecoding(v)
	}
	markFlagOrigins(c)
	c.ResolveAPIKey()
}

// signalContext returns a context canceled by the first SIGINT/SIGTERM, and
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
		// cli.Stderr(), not os.Stderr: while FilterStderr is active os.Stderr
		// is a pipe drained by a goroutine, and os.Exit below would kill the
		// process before the drain ran — the force-quit line would vanish.
		_, _ = fmt.Fprintln(cli.Stderr())
		_, _ = fmt.Fprintln(cli.Stderr(), cli.Warn("interrupted — board preserved in .slmcode/board.json; press Ctrl-C again to force quit"))
		cancel()
		select {
		case <-stop:
			return
		case <-ch:
			cli.RestoreAllRaw()
			_, _ = fmt.Fprintln(cli.Stderr(), cli.Error("force quit"))
			os.Exit(130)
		}
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() { close(stop) })
		cancel()
	}
}

// rejectUnknownSubcommands makes a group command fail on an argument it does
// not recognize instead of printing help and exiting 0.
//
// It only applies to commands that have subcommands and no RunE of their own —
// `slmcode config` is a group, `slmcode run "…"` takes a free-text argument
// and must keep it.
func rejectUnknownSubcommands(c *cobra.Command) {
	for _, sub := range c.Commands() {
		rejectUnknownSubcommands(sub)
	}
	if !c.HasSubCommands() || c.RunE != nil || c.Run != nil || c.Args != nil {
		return
	}
	c.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return fmt.Errorf("unknown command %q for %q — try `%s --help`",
			args[0], cmd.CommandPath(), cmd.CommandPath())
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	}
}

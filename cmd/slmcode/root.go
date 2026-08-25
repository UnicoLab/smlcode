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
	"github.com/UnicoLab/slmcode/pkg/loop"
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

var rootLongBody = `
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
--color=never, --log-level, and deterministic exit codes: 2 usage · 3 no workspace
· 4 provider unreachable · 5 failing tasks · 6 unanswerable gate · 130 interrupted.

With no TTY on stdin+stdout the plan/clarify/continue/escalate gates answer
themselves with "yes" and log the decision — nobody is there to answer, and you
asked for work to be done. --on-gate-timeout=stop|reject overrides that, and
then the run refuses AT THE DOOR rather than planning for minutes first. The
shell-permission gate is a safety gate: it never auto-approves.`)

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
		Long:  cli.Banner() + rootLongBody,
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
			// One switch, every render site: `studio`, the TUI and `version`
			// all call cli.Banner(). The flag used to be parsed into a variable
			// that nothing ever read, so `--no-banner` was documented, accepted
			// and inert. The help path is handled separately below, because
			// cobra prints help before PersistentPreRunE runs.
			cli.SetBannerEnabled(!flagNoBanner)
			return nil
		},
	}

	// `slmcode --version` is what people type first; without this it was
	// "unknown flag: --version" and exit 2. Cobra adds the flag (not the -v
	// shorthand, which --verbose already owns) and prints this template. The
	// `version` subcommand stays the detailed one.
	root.Version = Version
	root.SetVersionTemplate("slmcode {{.Version}}\n" +
		cli.Dim("  commit / build time / update check: slmcode version\n"))

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
	root.PersistentFlags().BoolVar(&flagNoBanner, "no-banner", false,
		"hide the ASCII banner (help, studio, TUI, version)")
	root.PersistentFlags().StringVar(&flagGateTimeout, "on-gate-timeout", "stop",
		"approve|reject|stop — what a HITL gate does with no TTY attached "+
			"(unset + no TTY: convenience gates auto-approve and say so; "+
			"an explicit stop|reject refuses the run up front)")
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
	all = append(all, inGroup("config", configCmd(), authCmd(), stackCmd(), agentCmd(), blockCmd(), skillsCmd(), hooksCmd(), updateCmd())...)
	all = append(all, inGroup("inspect", statusCmd(), boardCmd(), composeCmd(), readinessCmd(), taskCmd(),
		contextCmd(), docsCmd(), planCmd(), sessionCmd(), doctorCmd(), evalCmd(),
		memoryCmd(), evolveCmd(), calibrateCmd(), graphCmd(), autoresearchCmd(), metricsCmd(), versionCmd())...)
	all = append(all, completionCmd())
	for _, c := range all {
		rejectUnknownSubcommands(c)
	}
	root.AddCommand(all...)

	// Cobra resolves --help right after ParseFlags and BEFORE PersistentPreRunE,
	// so the banner has to be stripped here rather than in the pre-run hook.
	// root.Long was built with the banner already concatenated, hence the swap
	// rather than a call to cli.SetBannerEnabled.
	baseHelp := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		if flagNoBanner {
			cli.SetBannerEnabled(false)
			root.Long = strings.TrimLeft(rootLongBody, "\n")
		}
		baseHelp(c, args)
	})

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
	// 130 is decided by ONE definition, shared with the engine:
	// loop.IsContextCancelErr is errors.Is(context.Canceled) plus the exact
	// provider phrase. This used to be an inline substring test that also
	// matched the bare word "interrupted", so a provider replying
	// "upstream request interrupted" exited 130 on a run nobody had touched
	// and every wrapper script read it as a Ctrl-C. Commands that know
	// whether their own run context was canceled classify it themselves and
	// return a coded error, which the branch above honors first — see
	// runFailure in cmd_core.go.
	msg := strings.ToLower(err.Error())
	switch {
	case loop.IsContextCancelErr(err):
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
		// SetMaxParallel, not a field write: --parallel is an explicit choice
		// and must survive both the endpoint-aware default and calibration.
		c.SetMaxParallel(flagMaxParallel)
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
	if !c.HasSubCommands() {
		return
	}
	// An explicit Args policy is the author's decision and is left alone — a
	// parent that deliberately takes free text (or a fixed arity) has already
	// said so.
	//
	// The `c.RunE != nil` bail this used to carry defeated the whole point:
	// every group whose bare form does something useful — `skills`, `blocks`,
	// `hooks`, `stack`, `agent`, `docs`, `context` — was skipped, so
	// `slmcode blocks nosuchthing` printed a block listing and exited 0. Half
	// the groups rejected a typo and half congratulated you on it.
	if c.Args != nil {
		return
	}
	c.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		// Cobra resolves a real subcommand before the parent ever runs, so
		// anything still here is a name this command does not have.
		return fmt.Errorf("unknown command %q for %q — try `%s --help`",
			args[0], cmd.CommandPath(), cmd.CommandPath())
	}
	// Only supply a default action when the group has none; a group whose bare
	// form lists something keeps doing that.
	if c.RunE == nil && c.Run == nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		}
	}
}

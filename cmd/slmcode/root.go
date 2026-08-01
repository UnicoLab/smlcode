package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
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
	flagDryRun      bool
	flagMaxParallel int
	flagMaxRetries  int
	flagThink       int
	flagListen      string
	flagNoBanner    bool
)

func main() {
	// Keep CLI UX clean — GoLangGraph registries are chatty at Info.
	logrus.SetLevel(logrus.WarnLevel)

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

Examples:
  slmcode                 # premium TUI (default)
  slmcode tui             # same
  slmcode init
  slmcode run "add JWT auth"
  slmcode board
  slmcode studio
  slmcode doctor
  slmcode chat            # classic REPL`),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `slmcode` → premium TUI (Studio-parity dashboard + REPL).
			return runPremiumTUI()
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if flagNoBanner || cmd.Name() == "completion" || cmd.Name() == "help" {
				return
			}
		},
	}

	root.PersistentFlags().StringVar(&flagRoot, "root", "", "project root (default: cwd)")
	root.PersistentFlags().StringVar(&flagModel, "model", "", "model id (any id your provider serves)")
	root.PersistentFlags().StringVar(&flagProvider, "provider", "", "omlx|ollama|openai|lmstudio|openrouter|vllm|… (any OpenAI-compat name)")
	root.PersistentFlags().StringVar(&flagEndpoint, "endpoint", "", "API base URL (e.g. http://127.0.0.1:1234/v1)")
	root.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (or SLMCODE_API_KEY / OPENAI_API_KEY)")
	root.PersistentFlags().StringVar(&flagBackend, "backend", "", "slmcode|claude-code")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose agent logs")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "do not write code files")
	root.PersistentFlags().IntVar(&flagMaxParallel, "parallel", 0, "max parallel workers")
	root.PersistentFlags().IntVar(&flagMaxRetries, "retries", 0, "review/correct retries")
	root.PersistentFlags().IntVar(&flagThink, "think-passes", 0, "multi-pass think loops")
	root.PersistentFlags().BoolVar(&flagNoBanner, "no-banner", false, "hide ASCII banner on help")

	root.AddCommand(
		tuiCmd(),
		initCmd(),
		runCmd(),
		chatCmd(),
		studioCmd(),
		statusCmd(),
		boardCmd(),
		taskCmd(),
		contextCmd(),
		docsCmd(),
		planCmd(),
		skillsCmd(),
		sessionCmd(),
		diffCmd(),
		commitCmd(),
		applyCmd(),
		configCmd(),
		doctorCmd(),
		watchCmd(),
		evalCmd(),
		versionCmd(),
		updateCmd(),
		completionCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, cli.Error(err.Error()))
		os.Exit(1)
	}
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
func openHarness() (*harness.Harness, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}
	h, err := harness.New(root)
	if err != nil {
		return nil, err
	}
	applyFlags(h.Config)
	orch, err := orchestrator.New(h.Config)
	if err != nil {
		return nil, err
	}
	h.Orchestrator = orch
	return h, nil
}

func applyFlags(c *config.Config) {
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
	} else if providerChanged {
		c.Endpoint = config.DefaultEndpointFor(c.Provider)
	}
	if flagAPIKey != "" {
		c.APIKey = flagAPIKey
	}
	if flagBackend != "" {
		c.Backend = flagBackend
	}
	if flagVerbose {
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

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		fmt.Println()
		fmt.Println(cli.Warn("interrupted — board state preserved in .slmcode/board.json"))
		cancel()
	}()
	return ctx, cancel
}

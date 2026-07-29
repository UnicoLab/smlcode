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

	"github.com/piotrlaczkowski/slmcode/pkg/cli"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
	"github.com/piotrlaczkowski/slmcode/pkg/harness"
	"github.com/piotrlaczkowski/slmcode/pkg/orchestrator"
)

//go:embed all:ui
var uiEmbed embed.FS

var (
	flagRoot        string
	flagModel       string
	flagProvider    string
	flagEndpoint    string
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
		Short: "SLM-first coding harness (oMLX · atomic tasks · live kanban)",
		Long: cli.Banner() + `
` + cli.Dim(`Designed for local SLMs: scoped context packs, markdown memory, multi-pass
thinking, parallel specialists, and a live kanban you can edit while agents run.

Examples:
  slmcode init
  slmcode run "add JWT auth"
  slmcode board
  slmcode task add "Write tests" --column ready_to_dev --role tester
  slmcode task move T3 in_review
  slmcode context edit
  slmcode studio
  slmcode doctor`),
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if flagNoBanner || cmd.Name() == "completion" || cmd.Name() == "help" {
				return
			}
		},
	}

	root.PersistentFlags().StringVar(&flagRoot, "root", "", "project root (default: cwd)")
	root.PersistentFlags().StringVar(&flagModel, "model", "", "model id")
	root.PersistentFlags().StringVar(&flagProvider, "provider", "", "omlx|ollama|openai")
	root.PersistentFlags().StringVar(&flagEndpoint, "endpoint", "", "API endpoint")
	root.PersistentFlags().StringVar(&flagBackend, "backend", "", "slmcode|claude-code")
	root.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose agent logs")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "do not write code files")
	root.PersistentFlags().IntVar(&flagMaxParallel, "parallel", 0, "max parallel workers")
	root.PersistentFlags().IntVar(&flagMaxRetries, "retries", 0, "review/correct retries")
	root.PersistentFlags().IntVar(&flagThink, "think-passes", 0, "multi-pass think loops")
	root.PersistentFlags().BoolVar(&flagNoBanner, "no-banner", false, "hide ASCII banner on help")

	root.AddCommand(
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
	if flagModel != "" {
		c.Model = flagModel
	}
	if flagProvider != "" {
		c.Provider = flagProvider
	}
	if flagEndpoint != "" {
		c.Endpoint = flagEndpoint
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

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// `slmcode memory` — inspect and clear what the harness remembers.
//
// pkg/memory keeps four layers: working (this run), episodic (past runs,
// project-scoped), semantic (facts about the project) and procedural (what
// works for a MODEL, user-scoped and therefore shared across projects).
// Without a command to look at them, the only way to see why the harness is
// behaving a certain way was to read JSONL by hand, and the only way to reset
// it was `rm -rf`.

// openMemory opens the store for the current workspace.
func openMemory(readOnly bool) (*memory.Store, string, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, "", err
	}
	home, _ := os.UserHomeDir()
	store, err := memory.OpenWith(root, home, memory.Options{ReadOnly: readOnly})
	if err != nil {
		return nil, root, err
	}
	return store, root, nil
}

func memoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Inspect and clear working / episodic / semantic / procedural memory",
		Example: `  slmcode memory show
  slmcode memory episodes 10
  slmcode memory facts --json
  slmcode memory forget episodic`,
	}
	cmd.AddCommand(memoryShowCmd(), memoryEpisodesCmd(), memoryFactsCmd(), memoryForgetCmd())
	return cmd
}

func memoryShowCmd() *cobra.Command {
	var asJSON bool
	var role string
	var budget int
	c := &cobra.Command{
		Use:   "show",
		Short: "Render the memory block the harness injects into a prompt",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			store, root, err := openMemory(true)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			if budget <= 0 {
				ws, wsErr := openWorkspace()
				if wsErr == nil {
					budget = ws.Config.MemoryTokens
				}
			}
			block := store.RenderForPrompt(role, budget)

			if asJSON {
				return emitJSON(map[string]any{
					"root":     root,
					"dir":      store.Dir(),
					"user_dir": store.UserDir(),
					"role":     role,
					"budget":   budget,
					"counts": map[string]int{
						"episodes":   store.Episodes().Count(),
						"facts":      store.Semantic().Count(),
						"procedures": store.Procedural().Count(),
					},
					"block":    block,
					"warnings": store.Warnings(),
				})
			}

			cli.Header("Memory")
			fmt.Println(cli.Dim("  What the harness carries into the next run's prompt: episodes (what happened),"))
			fmt.Println(cli.Dim("  facts (what is true about this repo), procedures (what worked). Trimmed to the"))
			fmt.Println(cli.Dim("  token budget below and injected per role. `slmcode memory clear` resets it."))
			fmt.Println()
			cli.KeyVal("project", store.Dir())
			cli.KeyVal("user", store.UserDir())
			cli.KeyVal("role", orDash(role))
			cli.KeyVal("budget", fmt.Sprintf("%d tokens (config: memory_tokens)", budget))
			cli.KeyVal("episodes", strconv.Itoa(store.Episodes().Count()))
			cli.KeyVal("facts", strconv.Itoa(store.Semantic().Count()))
			cli.KeyVal("procedures", strconv.Itoa(store.Procedural().Count()))
			for _, w := range store.Warnings() {
				fmt.Println(cli.Warn(w))
			}
			fmt.Println()
			if strings.TrimSpace(block) == "" {
				fmt.Println(cli.Dim("  (nothing remembered yet — run slmcode run once)"))
				return nil
			}
			// The stored fact text can already carry its own "- " (facts are
			// distilled out of markdown lists), and pkg/memory's renderer adds
			// one of its own, so the block arrives with "- - The project is …".
			// Normalizing here leaves the stored fact byte-identical.
			fmt.Println(cli.NormalizeBullets(block))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().StringVar(&role, "role", "worker", "render the block as this role sees it")
	c.Flags().IntVar(&budget, "budget", 0, "token budget (default: config memory_tokens)")
	return c
}

func memoryEpisodesCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "episodes [n]",
		Short: "List the most recent runs the harness remembers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			n := 10
			if len(args) == 1 {
				parsed, err := strconv.Atoi(args[0])
				if err != nil || parsed <= 0 {
					return failf(2, "episodes: %q is not a positive count", args[0])
				}
				n = parsed
			}
			store, _, err := openMemory(true)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			eps := store.Episodes().Recent(n)

			if asJSON {
				return emitJSON(map[string]any{
					"total":    store.Episodes().Count(),
					"episodes": eps,
				})
			}
			cli.Header(fmt.Sprintf("Episodes (%d of %d)", len(eps), store.Episodes().Count()))
			if len(eps) == 0 {
				fmt.Println(cli.Dim("  (none yet)"))
				return nil
			}
			for i := len(eps) - 1; i >= 0; i-- {
				e := eps[i]
				passed, total := e.GatesPassed()
				mark := cli.Green("✔")
				if !e.Success {
					mark = cli.Red("✖")
				}
				fmt.Printf("  %s %s  %s\n", mark,
					cli.Dim(e.At.Local().Format("2006-01-02 15:04")),
					cli.Bold(cli.Clip(firstLine(e.Query), 70)))
				detail := fmt.Sprintf("edits %d/%d · tools %s · gates %d/%d · calls %d",
					e.EditsApplied, e.EditsAttempted, strings.Join(e.ToolsUsed, ","), passed, total, e.LLMCalls)
				fmt.Println("      " + cli.Dim(detail))
				if e.Summary != "" {
					fmt.Println("      " + cli.Dim(cli.Clip(firstLine(e.Summary), 90)))
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func memoryFactsCmd() *cobra.Command {
	var asJSON bool
	var kind string
	c := &cobra.Command{
		Use:   "facts",
		Short: "List semantic facts learned about this project",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			store, _, err := openMemory(true)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			facts := store.Semantic().All()
			if kind != "" {
				var kept []memory.Fact
				for _, f := range facts {
					if strings.EqualFold(string(f.Kind), kind) {
						kept = append(kept, f)
					}
				}
				facts = kept
			}
			sort.SliceStable(facts, func(i, j int) bool { return facts[i].Confidence > facts[j].Confidence })

			if asJSON {
				return emitJSON(map[string]any{"total": len(facts), "facts": facts})
			}
			cli.Header(fmt.Sprintf("Facts (%d)", len(facts)))
			if len(facts) == 0 {
				fmt.Println(cli.Dim("  (none yet — facts are distilled from runs)"))
				return nil
			}
			for _, f := range facts {
				pin := " "
				if f.Pinned {
					pin = cli.Yellow("★")
				}
				fmt.Printf("  %s %s %s  %s\n", pin,
					cli.Accent(cli.PadWidth(string(f.Kind), 11)),
					cli.Dim(fmt.Sprintf("%3.0f%%", f.Confidence*100)),
					// The stored text may open with its own list marker, which
					// collides with this table's own layout.
					cli.Clip(cli.TrimBulletMarker(f.Text), 90))
				fmt.Println("      " + cli.Dim(fmt.Sprintf("%s · seen %d · last %s",
					f.Subject, f.Support, f.LastSeen.Local().Format("2006-01-02"))))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().StringVar(&kind, "kind", "", "command|gotcha|layout|convention|dependency|file")
	return c
}

// memoryScopes maps the CLI's scope words onto memory.Scope.
var memoryScopes = map[string]memory.Scope{
	"working":    memory.ScopeWorking,
	"episodic":   memory.ScopeEpisodic,
	"episodes":   memory.ScopeEpisodic,
	"semantic":   memory.ScopeSemantic,
	"facts":      memory.ScopeSemantic,
	"procedural": memory.ScopeProcedural,
	"project":    memory.ScopeProject,
	"all":        memory.ScopeAll,
}

func memoryScopeNames() []string {
	out := make([]string, 0, len(memoryScopes))
	for k := range memoryScopes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func memoryForgetCmd() *cobra.Command {
	var asJSON bool
	var yes bool
	c := &cobra.Command{
		Use:   "forget [working|episodic|semantic|procedural|project|all]",
		Short: "Erase a memory layer, on disk and in process",
		Long: `Erase a memory layer.

  working      this run's scratch state
  episodic     the log of past runs (project)
  semantic     learned facts about this project
  procedural   what works for a model (user-scoped, shared across projects)
  project      episodic + semantic
  all          every layer, project and user

Nothing else depends on memory existing: forgetting is always safe, and the
harness relearns from the next run.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			word := strings.ToLower(strings.TrimSpace(args[0]))
			scope, ok := memoryScopes[word]
			if !ok {
				return failf(2, "memory forget: invalid scope %q — allowed: %s",
					args[0], strings.Join(memoryScopeNames(), ", "))
			}
			if (scope == memory.ScopeAll || scope == memory.ScopeProcedural) && !yes && !asJSON {
				if !confirm(fmt.Sprintf("Forget %s memory (this also clears user-scoped state)?", word), false) {
					return failf(2, "canceled")
				}
			}
			store, root, err := openMemory(false)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			if err := store.Forget(scope); err != nil {
				return err
			}
			if asJSON {
				return emitJSON(map[string]any{"forgot": string(scope), "root": root})
			}
			fmt.Println(cli.Success("forgot " + string(scope) + " memory"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return c
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// agoString renders a coarse "3d ago" for list views.
func agoString(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

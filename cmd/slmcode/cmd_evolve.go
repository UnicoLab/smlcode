package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// `slmcode evolve` — look at what the self-improvement engine has learned.
//
// The engine keeps three stores: repair rules (what fixed a class of failure),
// a contextual bandit policy (which option wins for this model and language),
// and regression checks (what must keep working). All three were write-only
// from a user's point of view; these subcommands make them readable, which is
// the whole point of an engine that claims to improve.

func openEvolve(readOnly bool) (*evolve.Engine, string, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, "", err
	}
	home, _ := os.UserHomeDir()
	eng, err := evolve.OpenWith(root, home, evolve.EngineOptions{ReadOnly: readOnly})
	if err != nil && eng == nil {
		return nil, root, err
	}
	return eng, root, nil
}

func evolveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolve",
		Short: "Inspect learned repair rules, the decision policy and regression checks",
		Example: `  slmcode evolve rules
  slmcode evolve why edit_format
  slmcode evolve regressions
  slmcode evolve reset --yes`,
	}
	cmd.AddCommand(evolveRulesCmd(), evolveWhyCmd(), evolveRegressionsCmd(), evolveResetCmd())
	return cmd
}

func evolveRulesCmd() *cobra.Command {
	var asJSON bool
	var all bool
	c := &cobra.Command{
		Use:   "rules",
		Short: "List repair rules with confidence and hit counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			eng, _, err := openEvolve(true)
			if err != nil {
				return err
			}
			defer func() { _ = eng.Close() }()
			rules := eng.Rules().All()
			sort.SliceStable(rules, func(i, j int) bool {
				if rules[i].Samples() != rules[j].Samples() {
					return rules[i].Samples() > rules[j].Samples()
				}
				return rules[i].Confidence() > rules[j].Confidence()
			})
			var shown []evolve.Rule
			for _, r := range rules {
				if !all && (r.Retired || (r.Seeded && r.Samples() == 0)) {
					continue
				}
				shown = append(shown, r)
			}

			if asJSON {
				return emitJSON(map[string]any{
					"total":    len(rules),
					"shown":    len(shown),
					"rules":    shown,
					"warnings": eng.Warnings(),
				})
			}
			cli.Header(fmt.Sprintf("Repair rules (%d of %d)", len(shown), len(rules)))
			fmt.Println(cli.Dim("  When a tool call fails, the harness fingerprints the failure and looks here"))
			fmt.Println(cli.Dim("  first. A rule that matches fixes the call with NO model round-trip; each hit or"))
			fmt.Println(cli.Dim("  miss moves its confidence, and a rule applies once confidence clears the bar."))
			fmt.Println()
			if len(shown) == 0 {
				fmt.Println(cli.Dim("  (nothing learned yet — pass --all to see the seeded rules)"))
				return nil
			}
			for _, r := range shown {
				origin := cli.Green("learned")
				if r.Seeded {
					origin = cli.Dim("seeded")
				}
				if r.Retired {
					origin = cli.Red("retired")
				}
				fmt.Printf("  %s %s  %s  %s\n",
					cli.Accent(cli.PadWidth(cli.Clip(r.ID, 18), 18)),
					cli.PadWidth(string(r.Trigger.Class), 18),
					cli.Dim(fmt.Sprintf("%3.0f%% · %d✔/%d✖", r.Confidence()*100, r.Successes, r.Failures)),
					origin)
				fmt.Println("      " + cli.Dim(cli.Clip(firstLine(r.Repair.Guidance), 92)))
				if r.LastUsed.IsZero() {
					continue
				}
				fmt.Println("      " + cli.Dim("last used "+agoString(r.LastUsed)))
			}
			fmt.Println()
			fmt.Println(cli.Dim("  a rule applies once its confidence clears the apply threshold"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&all, "all", false, "include seeded rules with no evidence and retired rules")
	return c
}

// evolveDecisions lists the decisions the bandit is allowed to explain.
var evolveDecisions = []evolve.Decision{
	evolve.DecEditFormat, evolve.DecRoleModel, evolve.DecThinkPasses,
	evolve.DecExplorePhase, evolve.DecRetryLadder, evolve.DecReviewStrictness,
}

func evolveDecisionNames() []string {
	out := make([]string, 0, len(evolveDecisions))
	for _, d := range evolveDecisions {
		out = append(out, string(d))
	}
	return out
}

func evolveWhyCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "why [decision]",
		Short: "Explain a learned decision — the posterior table behind the choice",
		Long: "Decisions: " + strings.Join(evolveDecisionNames(), ", ") +
			"\n\nWith no evidence the harness says so explicitly rather than inventing a reason.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			want := strings.ToLower(strings.TrimSpace(args[0]))
			var decision evolve.Decision
			for _, d := range evolveDecisions {
				if string(d) == want {
					decision = d
					break
				}
			}
			if decision == "" {
				return failf(2, "evolve why: unknown decision %q — allowed: %s",
					args[0], strings.Join(evolveDecisionNames(), ", "))
			}
			eng, _, err := openEvolve(true)
			if err != nil {
				return err
			}
			defer func() { _ = eng.Close() }()
			explanation := eng.Why(decision)

			if asJSON {
				var stats []evolve.KeyStats
				for _, ks := range eng.Bandit().Snapshot() {
					if ks.Key.Decision == decision {
						stats = append(stats, ks)
					}
				}
				return emitJSON(map[string]any{
					"decision":    string(decision),
					"explanation": explanation,
					"keys":        stats,
				})
			}
			cli.Header("Why: " + string(decision))
			fmt.Println("  " + strings.ReplaceAll(explanation, "\n", "\n  "))
			fmt.Println()
			shownAny := false
			for _, ks := range eng.Bandit().Snapshot() {
				if ks.Key.Decision != decision {
					continue
				}
				shownAny = true
				fmt.Println("  " + cli.Bold(ks.Key.String()) + cli.Dim(fmt.Sprintf("  %d pulls", ks.Pulls)))
				best, bestMean := "", -1.0
				for _, arm := range ks.Arms {
					if arm.Mean() > bestMean {
						best, bestMean = arm.Name, arm.Mean()
					}
				}
				for _, arm := range ks.Arms {
					marker := "  "
					if arm.Name == best {
						marker = cli.Green("→ ")
					}
					fmt.Printf("    %s%s  %s\n", marker,
						cli.Accent(cli.PadWidth(arm.Name, 20)),
						cli.Dim(fmt.Sprintf("mean %.3f ± %.3f", arm.Mean(), arm.StdDev())))
				}
			}
			if shownAny {
				// The posterior table is meaningless without this sentence.
				fmt.Println()
				fmt.Println(cli.Dim("  Each row is one option the harness can pick for this decision, scored by how"))
				fmt.Println(cli.Dim("  well past runs went with it (mean, ± spread). → marks the current leader; a"))
				fmt.Println(cli.Dim("  wide ± means the harness is still exploring. Key = decision|model|language."))
				fmt.Println(cli.Dim("  Pin the outcome instead with `slmcode config set <key> <value>`, or make runs"))
				fmt.Println(cli.Dim("  reproducible with --no-explore."))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func evolveRegressionsCmd() *cobra.Command {
	var asJSON bool
	var run bool
	c := &cobra.Command{
		Use:   "regressions",
		Short: "List stored regression checks and their status",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			eng, root, err := openEvolve(!run)
			if err != nil {
				return err
			}
			defer func() { _ = eng.Close() }()
			checks := eng.Regressions().Checks()
			var results []evolve.Result
			if run {
				results = eng.Regressions().RunOffline(root)
			}

			if asJSON {
				payload := map[string]any{"total": len(checks), "checks": checks}
				if run {
					payload["results"] = results
				}
				return emitJSON(payload)
			}
			cli.Header(fmt.Sprintf("Regression checks (%d)", len(checks)))
			if len(checks) == 0 {
				fmt.Println(cli.Dim("  (none — checks are recorded when a fixed failure is worth re-testing)"))
				return nil
			}
			for _, ch := range checks {
				status := cli.Dim("never run")
				if ch.Runs > 0 {
					if ch.LastOK {
						status = cli.Green("passing")
					} else {
						status = cli.Red("failing")
					}
				}
				fmt.Printf("  %s %s  %s\n",
					cli.Accent(cli.PadWidth(cli.Clip(ch.ID, 18), 18)),
					cli.PadWidth(string(ch.Kind), 12), status)
				fmt.Println("      " + cli.Dim(cli.Clip(ch.Description, 92)))
				if ch.Runs > 0 {
					fmt.Println("      " + cli.Dim(fmt.Sprintf("%d runs · %d fails · last %s",
						ch.Runs, ch.Fails, agoString(ch.LastRun))))
				}
			}
			if run {
				fmt.Println()
				for _, r := range results {
					mark := cli.Green("✔")
					if !r.OK {
						mark = cli.Red("✖")
					}
					fmt.Printf("  %s %s %s\n", mark, cli.PadWidth(cli.Clip(r.Check.ID, 18), 18), cli.Dim(r.Detail))
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&run, "run", false, "replay the offline checks now")
	return c
}

func evolveResetCmd() *cobra.Command {
	var asJSON bool
	var yes bool
	c := &cobra.Command{
		Use:   "reset",
		Short: "Erase learned rules, the decision policy, regression checks and memory",
		Long: `Erase everything the self-improvement engine learned.

This clears repair rules, the bandit policy, regression checks and every memory
layer, project and user. The harness starts from its shipped seeds again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			if !yes && !asJSON {
				if !confirm("Erase every learned rule, policy, regression check and memory?", false) {
					return failf(2, "canceled")
				}
			}
			if !yes && asJSON {
				return failf(2, "evolve reset --json requires --yes")
			}
			eng, root, err := openEvolve(false)
			if err != nil {
				return err
			}
			var errs []string
			if err := eng.Forget(memory.ScopeAll); err != nil {
				errs = append(errs, err.Error())
			}
			if err := eng.Rules().Forget(); err != nil {
				errs = append(errs, err.Error())
			}
			if err := eng.Bandit().Forget(); err != nil {
				errs = append(errs, err.Error())
			}
			if err := eng.Regressions().Forget(); err != nil {
				errs = append(errs, err.Error())
			}
			_ = eng.Close()
			if asJSON {
				return emitJSON(map[string]any{"reset": true, "root": root, "errors": errs})
			}
			for _, e := range errs {
				fmt.Println(cli.Warn(e))
			}
			if len(errs) > 0 {
				return failf(1, "reset finished with %d problem(s)", len(errs))
			}
			fmt.Println(cli.Success("evolve state cleared — rules, policy, regressions and memory"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return c
}

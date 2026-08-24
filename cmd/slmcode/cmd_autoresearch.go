package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/autoresearch"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/eval"
)

// `slmcode autoresearch` — point the harness's self-improvement machinery at
// the harness itself.
//
// Everything else under `slmcode evolve` learns from runs that happened anyway.
// This command is different in kind: it deliberately CHANGES your agent prompts
// and config knobs, measures the result against the fixed eval suite, and keeps
// only what survives. That is a useful thing to be able to ask for and a
// terrible default, so the command is built to be dull unless invited:
//
//   - with no flags it DRY RUNS — proposes, prints, applies nothing;
//   - applying takes `--apply` AND `autoresearch: true` in the project config;
//   - every applied run leaves a snapshot, and `--restore` puts it all back.
//
// The two-key rule (flag plus config) is deliberate. A flag alone is one typo
// away from rewriting a repository's prompts; a config key alone is one shared
// stack preset away from doing it to a team.

func autoresearchCmd() *cobra.Command {
	var (
		maxExperiments int
		budget         time.Duration
		seed           int64
		deterministic  bool
		dryRun         bool
		showSurface    bool
		restore        bool
		apply          bool
		yes            bool
		maxTokens      int
		realQueries    bool
		asJSON         bool
	)
	cmd := &cobra.Command{
		Use:   "autoresearch",
		Short: "Tune this project's agent prompts and safe config knobs against the eval suite",
		Long: `Run a ratchet loop over the harness's own settings.

    snapshot → propose ONE change → apply → evaluate → keep if better,
    else restore → record → repeat

A change is retained only when the primary metric (task pass rate) improves AND
no guarded metric regresses: tokens per task, wall seconds per task, tool error
rate and edit-format apply rate. That guard is what stops the loop buying a
better pass rate with three times the tokens.

SAFETY — this command mutates files, so it will not do so by accident:

  * with no flags it is a DRY RUN: it proposes, prints, and applies nothing;
  * applying needs BOTH --apply and ` + "`autoresearch: true`" + ` in .slmcode/config.yaml;
  * only whitelisted knobs are reachable. Provider credentials, permissions,
    shell policy, hooks, MCP servers and filesystem roots are not on the
    surface and cannot be reached by an experiment;
  * every applied run snapshots the files it can write first. --restore undoes
    the whole run, including one that was killed outright.

Examples:
  slmcode autoresearch --surface                 # what is mutable, and in what range
  slmcode autoresearch                           # dry run: what it would try
  slmcode autoresearch --apply --max-experiments 6
  slmcode autoresearch --apply --budget 20m --seed 7
  slmcode autoresearch --restore                 # undo the last applied run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			root, err := projectRoot()
			if err != nil {
				return err
			}

			if restore {
				return runAutoresearchRestore(root, asJSON)
			}

			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			surface, err := autoresearch.Reflect(autoresearch.Options{Root: root})
			if err != nil {
				return err
			}
			if showSurface {
				return renderSurface(surface, asJSON)
			}

			// The opt-in gate. Note the asymmetry on purpose: refusing to apply
			// degrades to a dry run rather than failing, so a user who forgot
			// the config key still gets the useful half of the command.
			live := apply
			var gateNote string
			switch {
			case dryRun:
				live = false
				gateNote = "dry run requested"
			case !apply:
				live = false
				gateNote = "no --apply: proposing only, nothing will be written"
			case !ws.Config.Autoresearch:
				live = false
				gateNote = "autoresearch is not enabled for this project — " +
					"run `slmcode config set autoresearch true` to allow --apply"
			}
			if live && !yes && !asJSON {
				if !confirm(fmt.Sprintf(
					"Apply up to %d experiment(s) to this project's agent prompts and config knobs?",
					maxExperiments), false) {
					return failf(2, "canceled")
				}
			}
			if live && !yes && asJSON {
				return failf(2, "autoresearch --apply --json requires --yes")
			}

			cases := eval.DefaultCases()
			if realQueries {
				cases = eval.RealQueryCases()
			}
			evaluator := autoresearch.NewEvalEvaluator(cases, ws.Config)

			var proposer autoresearch.Proposer = autoresearch.NewDeterministicProposer(seed)
			if !deterministic && !ws.Config.Deterministic {
				// Optional, and optional means optional: with no rewriter wired
				// the LLM proposer IS the deterministic one. Prompt rewriting is
				// opted into separately (see docs/autoresearch.md) precisely so
				// a default run cannot depend on a small model behaving.
				proposer = autoresearch.NewLLMProposer(nil, proposer)
			}

			opts := autoresearch.RatchetOptions{
				Surface:   surface,
				Evaluator: evaluator,
				Proposer:  proposer,
				Budget: autoresearch.Budget{
					MaxExperiments: maxExperiments,
					MaxWallClock:   budget,
					MaxTokens:      maxTokens,
				},
				Seed:        seed,
				DryRun:      !live,
				Journal:     autoresearch.OpenJournal(root),
				SnapshotDir: autoresearch.SnapshotDir(root),
			}
			if !asJSON {
				opts.OnTrial = printTrial
			}
			r, err := autoresearch.New(opts)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), budget+10*time.Minute)
			defer cancel()

			if !asJSON {
				fmt.Println(cli.Accent("autoresearch") + " " + cli.Dim(fmt.Sprintf(
					"%d knob(s) · %d case(s) · seed %d · %s",
					surface.Len(), len(cases), seed, modeLabel(live))))
				if gateNote != "" {
					fmt.Println(cli.Warn(gateNote))
				}
				fmt.Println()
			}
			res, err := r.Run(ctx)
			if err != nil {
				return err
			}
			return renderAutoresearchResult(res, surface, asJSON)
		},
	}
	cmd.Flags().IntVar(&maxExperiments, "max-experiments", autoresearch.DefaultBudget().MaxExperiments,
		"cap on experiments in one run")
	cmd.Flags().DurationVar(&budget, "budget", autoresearch.DefaultBudget().MaxWallClock,
		"wall-clock budget for the whole run")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", autoresearch.DefaultBudget().MaxTokens,
		"token budget for the whole run")
	cmd.Flags().Int64Var(&seed, "seed", 1, "seed for the experiment sequence — the same seed replays the same run")
	cmd.Flags().BoolVar(&deterministic, "deterministic", false,
		"deterministic proposer only, never the optional prompt-rewriting model")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"propose and print, apply nothing (this is also what happens without --apply)")
	cmd.Flags().BoolVar(&showSurface, "surface", false, "list the mutable knobs and their domains, then exit")
	cmd.Flags().BoolVar(&restore, "restore", false, "restore the files from the last run's snapshot and exit")
	cmd.Flags().BoolVar(&apply, "apply", false,
		"actually write the changes (also needs `autoresearch: true` in config)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&realQueries, "real", false, "score against the real-user query suite instead of the default cases")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func modeLabel(live bool) string {
	if live {
		return "applying"
	}
	return "dry run"
}

// renderSurface answers "what can this thing touch", which is the first
// question anybody sensible asks before running it.
func renderSurface(s *autoresearch.Surface, asJSON bool) error {
	knobs := s.Knobs()
	if asJSON {
		return emitJSON(map[string]any{
			"root":      s.Root(),
			"knobs":     knobs,
			"files":     s.Files(),
			"guards":    autoresearch.GuardNames(autoresearch.DefaultGuards()),
			"immutable": autoresearch.SecuritySensitiveKeys(),
			"warnings":  s.Warnings(),
		})
	}
	cli.Header(fmt.Sprintf("Mutable surface (%d knob(s))", len(knobs)))
	fmt.Println(cli.Dim("  These are the ONLY things an experiment can change. The list is an"))
	fmt.Println(cli.Dim("  allow-list: anything not named here is immutable, including every key"))
	fmt.Println(cli.Dim("  that touches credentials, permissions, shell policy, hooks or paths."))
	fmt.Println()
	if len(knobs) == 0 {
		fmt.Println(cli.Dim("  (nothing to tune — no .slmcode/agents/*.yaml and no config.yaml)"))
		return nil
	}
	for _, k := range knobs {
		origin := cli.Dim("default")
		if k.InFile {
			origin = cli.Green("in file")
		}
		fmt.Printf("  %s %s  %s\n",
			cli.Accent(cli.PadWidth(cli.Clip(k.ID, 38), 38)),
			cli.PadWidth(cli.Clip(k.Domain.String(), 26), 26),
			origin)
		fmt.Println("      " + cli.Dim("now: "+cli.Clip(firstLine(k.Value), 88)))
	}
	fmt.Println()
	cli.KeyVal("files", strings.Join(s.Files(), ", "))
	cli.KeyVal("guards", autoresearch.GuardNames(autoresearch.DefaultGuards()))
	for _, w := range s.Warnings() {
		fmt.Println(cli.Warn(w))
	}
	return nil
}

func printTrial(t autoresearch.Trial) {
	mark := cli.Red("revert")
	if t.Kept {
		mark = cli.Green("keep  ")
	}
	if t.DryRun {
		mark = cli.Dim("dry   ")
	}
	fmt.Printf("  %s %s %s\n", mark,
		cli.PadWidth(cli.Clip(t.KnobID, 34), 34),
		cli.Dim(cli.Clip(firstLine(t.Before)+" → "+firstLine(t.After), 46)))
	fmt.Println("         " + cli.Dim(cli.Clip(t.Reason, 100)))
}

func renderAutoresearchResult(res autoresearch.Result, s *autoresearch.Surface, asJSON bool) error {
	if asJSON {
		return emitJSON(map[string]any{
			"result":   res,
			"surface":  s.Knobs(),
			"warnings": append(res.Warnings, s.Warnings()...),
		})
	}
	fmt.Println()
	cli.Header("Autoresearch")
	cli.KeyVal("experiments", fmt.Sprintf("%d (kept %d, reverted %d, guard vetoes %d)",
		res.Experiments, len(res.Kept), res.Reverted(), res.GuardVetoes()))
	cli.KeyVal("baseline", res.Baseline.Render())
	cli.KeyVal("best", res.Best.Render())
	cli.KeyVal("tokens", fmt.Sprintf("%d", res.TokensUsed))
	// The stop reason is printed unconditionally and never softened: a spent
	// budget and a converged search look identical in a score table.
	cli.KeyVal("stopped", res.StopDetail)
	fmt.Println()
	switch {
	case res.DryRun:
		fmt.Println(cli.Warn("dry run — nothing was written. Add --apply (and `autoresearch: true`) to run it for real."))
	case res.Improved():
		fmt.Println(cli.Success(fmt.Sprintf("%d change(s) retained — see %s", len(res.Kept), autoresearch.BestPath(s.Root()))))
		fmt.Println(cli.Dim("  undo everything this run kept: slmcode autoresearch --restore"))
	default:
		fmt.Println(cli.Warn("nothing was retained — the harness is unchanged"))
	}
	for _, w := range sortedWarnings(res.Warnings, s.Warnings()) {
		fmt.Fprintln(os.Stderr, cli.Warn(w))
	}
	return nil
}

func runAutoresearchRestore(root string, asJSON bool) error {
	paths, err := autoresearch.RestoreLast(root)
	if err != nil {
		if os.IsNotExist(err) {
			return failf(2, "autoresearch: no snapshot to restore in %s", autoresearch.SnapshotDir(root))
		}
		return err
	}
	if asJSON {
		return emitJSON(map[string]any{"restored": paths})
	}
	cli.Header(fmt.Sprintf("Restored %d file(s)", len(paths)))
	for _, p := range paths {
		fmt.Println("  " + cli.Dim(p))
	}
	fmt.Println()
	fmt.Println(cli.Success("the surface is back to its state before the last applied run"))
	return nil
}

// sortedWarnings merges and dedupes warning lists for display.
func sortedWarnings(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, w := range l {
			if w == "" || seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

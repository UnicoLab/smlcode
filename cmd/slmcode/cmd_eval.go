package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/eval"
)

// `slmcode eval` answers two different questions, and only one of them needs a
// model.
//
//   - "did the model finish the task" — the live cases. Right question, wrong
//     instrument for harness work: it needs a running endpoint, takes minutes,
//     and its variance swamps the effect of any single harness change.
//   - "did the harness get better" — --offline. Each embedded fixture is a
//     recorded trajectory (what a small model really emitted, which tool calls
//     failed, which arguments finally worked), replayed with and without the
//     repair store. One variable, no network, no flakiness.
//
// Either way the run now RECORDS its metrics.Metrics into the project's log
// (rep.RecordMetrics) so a later run can --compare against it. Without that
// the harness could only ever report pass/fail, which is exactly the number
// that cannot show an improvement.

func evalCmd() *cobra.Command {
	var outPath string
	var caseID string
	var realQueries bool
	var offline bool
	var comparePath string
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the eval harness (live cases, or --offline fixture replay)",
		Long: `Runs canned coding cases against the configured provider/model and writes
a JSON report, recording each case's harness metrics for later comparison.

--offline replays three embedded trajectories instead: no model, no network,
deterministic. That is the mode that proves a harness change helped.

Examples:
  slmcode eval
  slmcode eval --offline
  slmcode eval --real
  slmcode eval --case langgraph-class-template --real
  slmcode eval --case py-hello --out .slmcode/eval-report.json
  slmcode eval --compare .slmcode/eval-baseline.json
  RUN_E2E=1 go test ./test/e2e -run TestLiveRealQueryLangGraph`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if offline {
				return runOfflineEval(outPath)
			}
			h, err := openHarness()
			if err != nil {
				return err
			}
			defer closeHarness(h)
			cases := eval.DefaultCases()
			if realQueries {
				cases = eval.RealQueryCases()
			}
			if caseID != "" {
				pool := cases
				if !realQueries {
					// Allow selecting a real-query id even without --real.
					pool = append(append([]eval.Case{}, eval.DefaultCases()...), eval.RealQueryCases()...)
				}
				var filtered []eval.Case
				for _, c := range pool {
					if c.ID == caseID {
						filtered = append(filtered, c)
					}
				}
				if len(filtered) == 0 {
					return fmt.Errorf("unknown case %q", caseID)
				}
				cases = filtered
			}
			fmt.Println(cli.Accent("eval") + " " + cli.Dim(fmt.Sprintf("%d case(s) · %s/%s",
				len(cases), h.Config.Provider, h.Config.Model)))
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
			defer cancel()
			rep := eval.RunAll(ctx, cases, h.Config)
			// Record BEFORE anything below can fail: an unwritten metrics line
			// is a comparison a future run cannot make.
			if err := rep.RecordMetrics(h.Config.Root); err != nil {
				fmt.Fprintln(os.Stderr, cli.Warn("could not record eval metrics: "+err.Error()))
			}
			if outPath == "" {
				outPath = filepath.Join(h.Config.SlmDir(), "eval-report.json")
			}
			if err := eval.WriteReport(outPath, rep); err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
			cli.KeyVal("report", outPath)
			cli.KeyVal("metrics", eval.MetricsPath(h.Config.Root))
			cli.KeyVal("passed", fmt.Sprintf("%d", rep.Passed))
			cli.KeyVal("failed", fmt.Sprintf("%d", rep.Failed))
			fmt.Println()
			fmt.Println(rep.Summary().Render())

			if comparePath != "" {
				baseline, berr := readEvalReport(comparePath)
				if berr != nil {
					return fmt.Errorf("read baseline %s: %w", comparePath, berr)
				}
				fmt.Println()
				fmt.Println(cli.Bold("Compared to " + comparePath))
				fmt.Println(rep.CompareTo(baseline).Render())
			}
			if rep.Failed > 0 {
				return fmt.Errorf("%d eval case(s) failed", rep.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "report JSON path (default .slmcode/eval-report.json)")
	cmd.Flags().StringVar(&caseID, "case", "", "run a single case id")
	cmd.Flags().BoolVar(&realQueries, "real", false, "run real-user query suite (LangGraph template, FastAPI, CLI)")
	cmd.Flags().BoolVar(&offline, "offline", false,
		"replay the embedded fixture trajectories (no model, no network) and report the A/B")
	cmd.Flags().StringVar(&comparePath, "compare", "",
		"path to a previous eval report to compare this run against")
	return cmd
}

// runOfflineEval replays the embedded trajectories and prints the A/B table.
// It never opens a harness: the whole point is that it needs no endpoint.
func runOfflineEval(outPath string) error {
	rep, err := eval.RunOffline(eval.OfflineOptions{})
	if err != nil {
		return err
	}
	fmt.Println(cli.Accent("eval --offline") + " " +
		cli.Dim(fmt.Sprintf("%d fixture trajector(ies) · no model called", len(rep.Cases))))
	fmt.Println()
	fmt.Println(rep.Render())
	if rep.Improved() {
		fmt.Println(cli.Success("the repair store beat the baseline arm"))
	} else {
		fmt.Println(cli.Warn("no improvement over the baseline arm"))
	}
	if outPath != "" {
		b, merr := json.MarshalIndent(rep, "", "  ")
		if merr != nil {
			return merr
		}
		if werr := os.WriteFile(outPath, b, 0o600); werr != nil {
			return werr
		}
		cli.KeyVal("report", outPath)
	}
	// An offline run is a measurement, not a gate: "no improvement" is a real
	// and reportable answer, so it does not fail the command.
	return nil
}

// readEvalReport loads a previously written report for --compare.
func readEvalReport(path string) (eval.Report, error) {
	var rep eval.Report
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied report path
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

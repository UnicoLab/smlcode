package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
)

// `slmcode metrics` — is the harness actually getting better?
//
// pkg/eval/metrics has recorded a row per run for a while, and knows how to
// compare two windows, but nothing surfaced it. Without this a user has no way
// to tell whether memory, repair rules and the bandit are earning their keep.

func metricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show the latest run's metrics and compare against earlier runs",
		Example: `  slmcode metrics show
  slmcode metrics compare 10
  slmcode metrics show --json`,
	}
	cmd.AddCommand(metricsShowCmd(), metricsCompareCmd())
	return cmd
}

// loadMetrics reads the run log for the current project.
func loadMetrics() ([]metrics.Metrics, string, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, "", err
	}
	path := metrics.Path(root)
	runs, err := metrics.Load(path)
	if err != nil {
		return nil, path, err
	}
	return runs, path, nil
}

func metricsShowCmd() *cobra.Command {
	var asJSON bool
	var last int
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the latest run's metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			runs, path, err := loadMetrics()
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				if asJSON {
					return emitJSON(map[string]any{"path": path, "runs": 0})
				}
				cli.Header("Metrics")
				fmt.Println(cli.Dim("  (no runs recorded yet — " + path + ")"))
				fmt.Println(cli.Dim("  metrics are appended after each `slmcode run`"))
				return nil
			}
			if last <= 0 {
				last = 1
			}
			if last > len(runs) {
				last = len(runs)
			}
			window := runs[len(runs)-last:]
			latest := runs[len(runs)-1]
			summary := metrics.Aggregate(window)

			if asJSON {
				return emitJSON(map[string]any{
					"path":    path,
					"runs":    len(runs),
					"latest":  latest,
					"window":  last,
					"summary": summary,
				})
			}
			cli.Header("Metrics")
			cli.KeyVal("log", path)
			cli.KeyVal("runs", strconv.Itoa(len(runs)))
			cli.KeyVal("latest", latest.At.Local().Format("2006-01-02 15:04")+"  "+latest.RunID)
			cli.KeyVal("model", strings.TrimSpace(latest.Provider+" / "+latest.Model))
			fmt.Println()
			row := func(label, value string) {
				fmt.Printf("  %s  %s\n", cli.Dim(cli.PadWidth(label, 22)), value)
			}
			row("tasks passed", fmt.Sprintf("%d/%d  (%s)", latest.TasksPassed, latest.Tasks, pct(latest.TaskPassRate())))
			row("edit apply rate", fmt.Sprintf("%d/%d  (%s)  format=%s",
				latest.EditsApplied, latest.EditsAttempted, pct(latest.EditApplyRate()), orDash(latest.EditFormat)))
			// First-attempt compliance is the diagnostic number for a small
			// model: an edit the harness had to repair still lands, so the
			// apply rate alone cannot tell a compliant model from one the
			// repair ladder is carrying.
			row("first-attempt applies", fmt.Sprintf("%d/%d  (%s)",
				latest.EditsFirstAttempt, latest.EditsAttempted, pct(latest.FirstAttemptApplyRate())))
			row("tool errors", fmt.Sprintf("%d/%d  (%s)", latest.ToolErrors, latest.ToolCalls, pct(latest.ToolErrorRate())))
			row("redundant calls", fmt.Sprintf("%d  (%s)", latest.RedundantCalls, pct(latest.RedundantCallRate())))
			row("repair hit rate", fmt.Sprintf("%d/%d  (%s)", latest.RepairHits, latest.Failures, pct(latest.RepairHitRate())))
			row("resolved from memory", fmt.Sprintf("%d  (%s)", latest.ResolvedFromMemory, pct(latest.MemoryResolutionRate())))
			row("gate pass rate", pct(latest.GatePassRate()))
			row("llm calls / task", fmt.Sprintf("%.1f", latest.LLMCallsPerTask()))
			row("tokens / task", fmt.Sprintf("%.0f", latest.TokensPerTask()))
			row("wall seconds / task", fmt.Sprintf("%.1f", latest.WallSecondsPerTask()))
			if last > 1 {
				fmt.Println()
				fmt.Println(cli.Bold(fmt.Sprintf("  Aggregate over the last %d runs", last)))
				fmt.Println("  " + strings.ReplaceAll(strings.TrimRight(summary.Render(), "\n"), "\n", "\n  "))
			}
			if reading := metricsReading(latest); len(reading) > 0 {
				fmt.Println()
				fmt.Println(cli.Bold("  What this says"))
				for _, line := range reading {
					fmt.Println("  " + line)
				}
			}
			fmt.Println()
			fmt.Println(cli.Dim("  slmcode metrics compare 10   older half vs newer half"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().IntVar(&last, "last", 1, "aggregate over the last N runs as well")
	return c
}

func metricsCompareCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "compare [n]",
		Short: "Compare the newest N runs against the N before them",
		Long: `Split the run log into a baseline window and a current window of the same
size, and report every metric that moved. This is the only honest answer to
"is the harness improving?" — a single run is noise.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			n := 5
			if len(args) == 1 {
				parsed, err := strconv.Atoi(args[0])
				if err != nil || parsed <= 0 {
					return failf(2, "compare: %q is not a positive window size", args[0])
				}
				n = parsed
			}
			runs, path, err := loadMetrics()
			if err != nil {
				return err
			}
			if len(runs) < 2 {
				if asJSON {
					return emitJSON(map[string]any{"path": path, "runs": len(runs), "comparable": false})
				}
				cli.Header("Metrics compare")
				fmt.Println(cli.Dim(fmt.Sprintf("  need at least 2 runs, have %d — %s", len(runs), path)))
				return nil
			}
			if n*2 > len(runs) {
				n = len(runs) / 2
			}
			baseline := runs[len(runs)-2*n : len(runs)-n]
			current := runs[len(runs)-n:]
			cmp := metrics.Compare(baseline, current)

			if asJSON {
				return emitJSON(map[string]any{
					"path":       path,
					"runs":       len(runs),
					"window":     n,
					"comparable": true,
					"improved":   cmp.Improved(),
					"comparison": cmp,
				})
			}
			cli.Header(fmt.Sprintf("Metrics compare (%d vs %d runs)", n, n))
			fmt.Println("  " + strings.ReplaceAll(strings.TrimRight(cmp.Render(), "\n"), "\n", "\n  "))
			fmt.Println()
			if cmp.Improved() {
				fmt.Println(cli.Success("the harness improved over this window"))
			} else {
				fmt.Println(cli.Warn("no clear improvement over this window"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

// pct renders a metrics rate, which is -1 when the denominator was zero.
//
// Every rate on metrics.Metrics uses that convention, and printing it as
// "%.0f%%" turned "no gates ran" into a confident "-100%" — the one number on
// the screen that cannot happen.
func pct(rate float64) string {
	if rate < 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", rate*100)
}

// metricsReading turns the number dump into sentences.
//
// `slmcode metrics show` printed twelve ratios and stopped. Every one of them
// exists to answer a question ("is the model's edit format working?", "is the
// repair ladder carrying it?"), and none of them said so — which made the
// self-improvement engine's shop window unreadable to anyone who had not read
// the source. Each line below states what the number means AND the command
// that acts on it.
func metricsReading(m metrics.Metrics) []string {
	var out []string
	switch {
	case m.Tasks > 0 && m.TasksPassed == 0:
		out = append(out, cli.Warn("no task passed — the board holds why")+
			cli.Dim("  ·  slmcode board  ·  slmcode apply"))
	case m.Tasks > 0 && m.TasksPassed < m.Tasks:
		out = append(out, cli.Warn(fmt.Sprintf("%d of %d tasks did not pass",
			m.Tasks-m.TasksPassed, m.Tasks))+cli.Dim("  ·  slmcode board"))
	}
	if m.EditsAttempted > 0 {
		switch rate := m.EditApplyRate(); {
		case rate < 0.6:
			out = append(out, cli.Warn(fmt.Sprintf(
				"only %s of this model's edits landed — the edit format is a poor fit", pct(rate)))+
				cli.Dim("  ·  slmcode evolve why edit_format"))
		case m.FirstAttemptApplyRate() < rate-0.25:
			out = append(out, cli.Dim(fmt.Sprintf(
				"the repair ladder is carrying this model: %s of edits land, but only %s land first try",
				pct(rate), pct(m.FirstAttemptApplyRate()))))
		default:
			out = append(out, cli.Dim(fmt.Sprintf(
				"edits are landing (%s, %s first try) — this model fits the %s format",
				pct(rate), pct(m.FirstAttemptApplyRate()), orDash(m.EditFormat))))
		}
	}
	if m.ToolCalls > 0 && m.ToolErrorRate() > 0.25 {
		out = append(out, cli.Warn(fmt.Sprintf(
			"%s of tool calls errored — usually a context/prompt fit problem", pct(m.ToolErrorRate())))+
			cli.Dim("  ·  slmcode readiness"))
	}
	if m.RedundantCallRate() > 0.2 {
		out = append(out, cli.Warn(fmt.Sprintf(
			"%s of calls repeated an earlier one — the model is looping", pct(m.RedundantCallRate())))+
			cli.Dim("  ·  lower max_task_calls, or raise think_passes"))
	}
	if m.Failures > 0 {
		if m.RepairHits == 0 {
			out = append(out, cli.Dim(fmt.Sprintf(
				"%d failure(s), none matched a learned repair rule — they cost a full model round-trip",
				m.Failures))+cli.Dim("  ·  slmcode evolve rules"))
		} else {
			out = append(out, cli.Dim(fmt.Sprintf(
				"%d of %d failures were repaired from stored rules (no model call)",
				m.RepairHits, m.Failures)))
		}
	}
	return out
}

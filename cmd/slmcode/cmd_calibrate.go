package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/calibrate"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// `slmcode calibrate` — measure what this endpoint can actually do.
//
// The point of showing the whole table rather than just the chosen number is
// that "max_parallel 2" on its own is indistinguishable from a hardcoded
// guess, which is precisely what this replaces. With the levels printed, the
// choice is checkable: you can see that four-way ran at 37% of ideal and
// decide for yourself whether the floor was set sensibly.

func openCalibrationStore() *calibrate.Store {
	home, _ := os.UserHomeDir()
	return calibrate.Open(calibrate.UserDir(home))
}

func calibrateCmd() *cobra.Command {
	var (
		asJSON bool
		force  bool
		show   bool
		model  string
	)
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Measure the endpoint: concurrency knee, latency, throughput, context window",
		Long: `Measure what the configured (model, endpoint) pair can actually do.

A single local model server shares one GPU across concurrent calls, so past its
knee extra workers only add queueing — and because role timeouts are
wall-clock, that is a cause of role timeouts rather than a tuning preference. A
hosted API scales horizontally and has no such knee. Neither fact is knowable
from the provider name, so slmcode measures it: a handful of tiny completions
at 1, 2, 4 (and 8 only if 4 still scales), plus whatever the server reports
about the model.

The result is cached per (model, endpoint) under ~/.slmcode/memory and reused
until it ages out or the server reports a different context window. Values you
have set yourself are never overridden.`,
		Example: `  slmcode calibrate
  slmcode calibrate --force
  slmcode calibrate --show --json
  slmcode calibrate --model Qwen3.5-9B-MLX-4bit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			cfg := ws.Config
			if m := strings.TrimSpace(model); m != "" {
				cfg.Model = m
			}
			store := openCalibrationStore()
			defer func() { _ = store.Close() }()

			if show {
				return showCalibration(cfg, store, asJSON)
			}
			return runCalibration(cmd.Context(), cfg, store, force, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&force, "force", false, "re-measure even when a current profile exists")
	cmd.Flags().BoolVar(&show, "show", false, "print stored profiles without probing")
	cmd.Flags().StringVar(&model, "model", "", "calibrate this model instead of the configured one")
	return cmd
}

// showCalibration prints what is stored, touching nothing.
func showCalibration(cfg *config.Config, store *calibrate.Store, asJSON bool) error {
	all := store.All()
	active, current := store.Lookup(cfg.Model, cfg.Endpoint)
	if asJSON {
		return emitJSON(map[string]any{
			"path":     store.Path(),
			"count":    len(all),
			"profiles": all,
			"active": map[string]any{
				"model":    cfg.Model,
				"endpoint": calibrate.EndpointIdentity(cfg.Endpoint),
				"current":  current,
				"profile":  active,
			},
			"warnings": store.Warnings(),
		})
	}
	cli.Header("Calibration")
	cli.KeyVal("store", orDash(store.Path()))
	cli.KeyVal("profiles", fmt.Sprintf("%d", len(all)))
	for _, w := range store.Warnings() {
		fmt.Println(cli.Warn(w))
	}
	if len(all) == 0 {
		fmt.Println(cli.Dim("  nothing measured yet — run `slmcode calibrate`"))
		return nil
	}
	fmt.Println()
	for _, p := range all {
		marker := ""
		if p.ID == active.ID {
			marker = cli.Green("  ← active")
			if !current {
				marker = cli.Yellow("  ← active, stale")
			}
		}
		fmt.Println(cli.Bold(p.Key.String()) + marker)
		printProfileBody(p)
		fmt.Println()
	}
	return nil
}

// runCalibration measures (or reuses) the active pair and reports it.
func runCalibration(ctx context.Context, cfg *config.Config, store *calibrate.Store, force, asJSON bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return failf(2, "no model configured — set one with `slmcode config set model <id>`")
	}
	if !asJSON {
		cli.Header("Calibrate")
		cli.KeyVal("provider", cfg.Provider)
		cli.KeyVal("model", cfg.Model)
		cli.KeyVal("endpoint", calibrate.EndpointIdentity(cfg.Endpoint))
		fmt.Println(cli.Dim(fmt.Sprintf(
			"  probing concurrency 1, 2, 4 (8 only if 4 still scales) — bounded at %s",
			calibrate.DefaultBudget)))
		fmt.Println()
	}

	// Asking for a calibration is asking for a calibration: this path does not
	// consult `calibrate: off` or SLMCODE_NO_CALIBRATE, which exist to keep the
	// RUN path inert rather than to refuse an explicit command.
	start := time.Now()
	opts := calibrate.AutoOptions{Store: store, Force: force}
	opts.Options.OnProgress = func(pr calibrate.Progress) {
		fmt.Println(cli.Dim("  · " + pr.String()))
	}
	out := calibrate.EnsureCalibrated(ctx, cfg, opts)
	// The full evidence report: every measurement, everything it changed, and
	// how to disagree with any of it. `slmcode calibrate` is the command whose
	// entire job is to explain these numbers, so it prints the long form.
	fmt.Println()
	fmt.Print(out.Report().Render())
	if out.Profile.MaxParallel == 0 {
		// Nothing was measured and nothing was cached. Report the reason when
		// there is one rather than rendering an all-zero profile.
		reason := out.Warning
		if reason == "" {
			reason = fmt.Sprintf("no usable endpoint for %s (endpoint %q)",
				cfg.Model, calibrate.EndpointIdentity(cfg.Endpoint))
		}
		if asJSON {
			if err := emitJSON(map[string]any{"ok": false, "warning": reason}); err != nil {
				return err
			}
		}
		return failf(4, "%s", reason)
	}

	if asJSON {
		return emitJSON(map[string]any{
			"ok":       true,
			"measured": out.Measured,
			"cached":   out.Cached,
			"elapsed":  time.Since(start).Round(time.Millisecond).String(),
			"profile":  out.Profile,
			"applied":  appliedJSON(out.Applied),
			"warning":  out.Warning,
			"store":    store.Path(),
		})
	}

	printProfileBody(out.Profile)
	fmt.Println()
	switch {
	case out.Measured:
		fmt.Println(cli.Success(fmt.Sprintf("measured in %s", time.Since(start).Round(time.Millisecond))))
	case out.Cached:
		fmt.Println(cli.Info("reused the stored profile — `--force` re-measures"))
	}
	for _, a := range out.Applied {
		fmt.Println(cli.Dim("  applies: " + a.String()))
	}
	if cfg.MaxParallelExplicit() {
		fmt.Println(cli.Dim(fmt.Sprintf(
			"  max_parallel=%d is set explicitly and is left alone", cfg.MaxParallel)))
	}
	if out.Warning != "" {
		fmt.Println(cli.Warn(out.Warning))
	}
	return nil
}

// printProfileBody renders one profile: the numbers, then the evidence.
func printProfileBody(p calibrate.Profile) {
	cli.KeyVal("max_parallel", fmt.Sprintf("%d  (efficiency floor %.0f%%)", p.MaxParallel, p.FloorUsed*100))
	if p.P95Ms > 0 {
		cli.KeyVal("latency", fmt.Sprintf("p50 %s  p95 %s  (%d solo sample(s), %d-token completion)",
			ms(p.P50Ms), ms(p.P95Ms), p.SoloSamples, p.CompletionTokens))
	}
	if p.TokensPerSec > 0 {
		cli.KeyVal("throughput", fmt.Sprintf("%.1f tokens/sec", p.TokensPerSec))
	}
	if p.QueueInflation > 1 {
		cli.KeyVal("queueing", fmt.Sprintf("%.2fx per-request latency at max_parallel=%d",
			p.QueueInflation, p.MaxParallel))
	}
	if p.ContextLimit > 0 {
		cli.KeyVal("context", fmt.Sprintf("%d tokens (%s)", p.ContextLimit, orDash(p.ContextSource)))
	}
	if !p.MeasuredAt.IsZero() {
		cli.KeyVal("measured", p.MeasuredAt.Local().Format(time.RFC3339))
	}
	if p.Partial {
		fmt.Println(cli.Warn("partial: the probe ran out of budget, so the knee may be an underestimate"))
	}
	if len(p.Levels) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(cli.Dim("  concurrency   wall     per-request   throughput   efficiency"))
	for _, l := range p.Levels {
		mark := "  "
		if l.Concurrency == p.MaxParallel {
			mark = cli.Green("← ")
		}
		fmt.Printf("  %s%-11d %-8s %-13s %-12s %s\n",
			mark, l.Concurrency, ms(l.WallMs), ms(l.PerRequestMs),
			fmt.Sprintf("%.2fx", l.Throughput),
			efficiencyCell(l.Efficiency, p.FloorUsed))
	}
}

// efficiencyCell colors a level by whether it cleared the floor, so the
// selection is legible at a glance.
func efficiencyCell(eff, floor float64) string {
	txt := fmt.Sprintf("%.0f%%", eff*100)
	if floor > 0 && eff < floor {
		return cli.Yellow(txt)
	}
	return cli.Green(txt)
}

func appliedJSON(applied []calibrate.Applied) []map[string]string {
	out := make([]map[string]string, 0, len(applied))
	for _, a := range applied {
		out = append(out, map[string]string{
			"key": a.Key, "from": a.From, "to": a.To, "why": a.Why,
		})
	}
	return out
}

func ms(v int64) string {
	if v <= 0 {
		return "—"
	}
	return (time.Duration(v) * time.Millisecond).Round(time.Millisecond).String()
}

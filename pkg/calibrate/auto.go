package calibrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Automatic calibration.
//
// The contract a startup probe has to meet, in order of importance:
//
//  1. it never blocks a run. Every path is time-capped, and every failure
//     falls through to the static defaults with exactly one warning line.
//  2. it never runs twice for the same pair in one process, and not once per
//     wave.
//  3. it says what it did. A harness that silently halves your parallelism is
//     indistinguishable from a bug.
//  4. it is inert unless a caller asks. Nothing here runs from a constructor,
//     so `make check` cannot reach a model even by accident.

// MetadataCheckTimeout bounds the cheap freshness check on a cached profile.
// It is a single GET against a server that is about to be used anyway.
const MetadataCheckTimeout = 2 * time.Second

// Outcome is what one EnsureCalibrated call did.
type Outcome struct {
	// Profile is the profile in force, measured or cached. Zero when none.
	Profile Profile
	// Measured is true when this call ran a probe.
	Measured bool
	// Cached is true when a stored profile was reused.
	Cached bool
	// Applied lists the config values calibration changed.
	Applied []Applied
	// Notice is the single human line to print, or "".
	Notice string
	// Warning is the single failure line to print, or "".
	Warning string
	// BudgetsBefore / BudgetsAfter bracket the model profile across Apply, so a
	// report can show the derived token budgets as a diff. Captured here rather
	// than recomputed later: after Apply the "before" no longer exists anywhere.
	BudgetsBefore, BudgetsAfter config.ModelProfile
}

// Report renders this outcome as the full evidence report.
func (o Outcome) Report() Report {
	return NewReport(o.Profile, o.Applied, o.BudgetsBefore, o.BudgetsAfter)
}

// AutoOptions configures EnsureCalibrated.
type AutoOptions struct {
	// Store is where profiles live. Nil disables caching (always re-probes).
	Store *Store
	// Prober overrides the HTTP prober (tests inject a fake here; when nil an
	// HTTP prober is built from cfg).
	Prober Prober
	// Force re-probes even when a current profile exists.
	Force bool
	// Options are the calibration knobs.
	Options Options
	// TTL overrides DefaultTTL for the freshness check.
	TTL time.Duration
	// Now is injectable for tests.
	Now func() time.Time
	// SkipMetadataCheck disables the cheap "did the context window change?"
	// validity probe on a cached profile.
	SkipMetadataCheck bool
}

// once guards against re-probing the same pair inside one process. A run, a
// resume and a rebuild all pass through the same startup path.
var (
	onceMu sync.Mutex
	onceBy = map[string]bool{}
)

// ResetOnce clears the per-process guard (tests).
func ResetOnce() {
	onceMu.Lock()
	defer onceMu.Unlock()
	onceBy = map[string]bool{}
}

func claimOnce(id string) bool {
	onceMu.Lock()
	defer onceMu.Unlock()
	if onceBy[id] {
		return false
	}
	onceBy[id] = true
	return true
}

// EnsureCalibrated makes sure the configured (model, endpoint) pair has a
// current profile, and applies it to cfg.
//
// It probes only when the pair is unseen, its profile predates this
// calibrator, its profile has aged out, the server now reports a different
// context window, or Force is set. Otherwise the cached profile is applied and
// nothing touches the network beyond one bounded metadata GET.
//
// It returns an Outcome rather than an error: a calibration that could not run
// is not a run failure, it is a fallback with a warning.
func EnsureCalibrated(ctx context.Context, cfg *config.Config, opt AutoOptions) Outcome {
	if cfg == nil {
		return Outcome{}
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	ttl := opt.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	key := Key{Model: cfg.Model, Endpoint: cfg.Endpoint, Provider: cfg.Provider}.Normalize()
	if key.Model == "" || key.Endpoint == "" {
		return Outcome{}
	}

	prober := opt.Prober
	if prober == nil {
		prober = NewHTTPProber(cfg.Provider, cfg.Endpoint, cfg.Model, cfg.APIKey, probeCallTimeout(opt.Options))
	}

	// ── cached path ──
	var cached Profile
	var haveCurrent bool
	if opt.Store != nil && !opt.Force {
		p, current := opt.Store.LookupWithTTL(key.Model, key.Endpoint, ttl)
		cached, haveCurrent = p, current
		if haveCurrent && !opt.SkipMetadataCheck && staleByMetadata(ctx, prober, p) {
			haveCurrent = false
			cached.Note = "context window changed since it was measured"
		}
	}
	if haveCurrent {
		budgetsBefore := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
		out := Outcome{Profile: cached, Cached: true, Applied: Apply(cfg, cached)}
		out.BudgetsBefore = budgetsBefore
		out.BudgetsAfter = config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
		if len(out.Applied) > 0 {
			out.Notice = fmt.Sprintf("calibration (cached, %s ago) for %s: %s",
				roundAge(cached.Age(now())), key.String(), describeApplied(out.Applied))
		}
		return out
	}

	// ── measure ──
	// The per-process guard is claimed only on the path that actually probes,
	// so a cached hit stays free and repeatable.
	if !opt.Force && !claimOnce(key.ID()) {
		return Outcome{}
	}
	prof, err := Calibrate(ctx, prober, key, opt.Options)
	if err != nil {
		return Outcome{Warning: fmt.Sprintf(
			"calibration skipped for %s (%s) — using defaults: max_parallel=%d",
			key.String(), shortErr(err), cfg.MaxParallel)}
	}
	// Read the pin BEFORE applying: Apply marks max_parallel explicit when it
	// installs the measured knee, so asking afterwards would report every
	// calibrated run as "keeping your max_parallel".
	pinned := cfg.MaxParallelExplicit()
	pinnedValue := cfg.MaxParallel

	var saveWarning string
	if opt.Store != nil {
		opt.Store.Put(prof)
		if ferr := opt.Store.Flush(); ferr != nil {
			// Losing the cache costs one re-probe next run; it is not a run
			// failure and must not read like one.
			saveWarning = "calibration not saved: " + ferr.Error()
		}
	}
	// Bracket Apply: after it runs, the pre-derivation profile exists nowhere
	// else, and the report's whole value is showing the change.
	budgetsBefore := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	applied := Apply(cfg, prof)
	return Outcome{
		Profile:       prof,
		Measured:      true,
		Applied:       applied,
		Notice:        calibratedNotice(prof, pinned, pinnedValue),
		Warning:       saveWarning,
		BudgetsBefore: budgetsBefore,
		BudgetsAfter:  config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model),
	}
}

// calibratedNotice is the line a user sees after a fresh measurement.
// pinned/pinnedValue describe max_parallel as it was BEFORE the profile was
// applied, so the line can say when a user's own setting is being respected.
func calibratedNotice(p Profile, pinned bool, pinnedValue int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "calibrated %s — %s", p.Key.String(), p.Summary())
	if pinned {
		fmt.Fprintf(&b, "; keeping your max_parallel=%d", pinnedValue)
	}
	b.WriteString(" — override with max_parallel/task_timeout, or `calibrate: off`")
	return b.String()
}

func describeApplied(applied []Applied) string {
	parts := make([]string, 0, len(applied))
	for _, a := range applied {
		parts = append(parts, a.Key+"="+a.To)
	}
	return strings.Join(parts, " ")
}

// staleByMetadata asks the server whether the context window still matches.
// Any failure means "not stale": an offline /v1/models must never trigger a
// re-probe storm, and the cached numbers are still the best evidence we have.
func staleByMetadata(ctx context.Context, p Prober, prof Profile) bool {
	if prof.ContextLimit <= 0 {
		return false
	}
	mctx, cancel := context.WithTimeout(ctx, MetadataCheckTimeout)
	defer cancel()
	md, err := p.Metadata(mctx)
	if err != nil {
		return false
	}
	return prof.StaleAgainst(md)
}

// probeCallTimeout bounds ONE call. Half the whole budget: a single call that
// eats the entire budget leaves nothing to measure with.
func probeCallTimeout(o Options) time.Duration {
	b := o.Budget
	if b <= 0 {
		b = DefaultBudget
	}
	if half := b / 2; half > 0 {
		return half
	}
	return DefaultBudget / 2
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 160 {
		return msg[:157] + "..."
	}
	return msg
}

func roundAge(d time.Duration) time.Duration {
	switch {
	case d >= time.Hour:
		return d.Round(time.Hour)
	case d >= time.Minute:
		return d.Round(time.Minute)
	default:
		return d.Round(time.Second)
	}
}

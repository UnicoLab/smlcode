// Package calibrate measures what a concrete (model, endpoint) pair can
// actually do, instead of guessing it from a provider name.
//
// Four numbers matter to the harness and none are knowable from a string like
// "omlx":
//
//   - the CONCURRENCY KNEE — how many role calls this server runs at once
//     before extra workers only add queueing. A hosted API scales
//     horizontally; a single local model server shares one GPU and one KV
//     cache, and past its knee the extra throughput is a rounding error while
//     per-request latency roughly doubles. Role timeouts are wall-clock
//     (pkg/orchestrator/roletimeout.go), so being past the knee is a direct
//     cause of role timeouts, not a tuning preference.
//   - a LATENCY BASELINE — p50/p95 of a solo call, which is what lets the role
//     timeout store start from reality instead of "0/3 samples, use the whole
//     ceiling".
//   - THROUGHPUT — measured tokens/sec, which is what per-call deadlines are
//     actually derived from (see backends.EstimateTimeout).
//   - the CONTEXT WINDOW — which the server almost always just tells us.
//
// # Division of labor with the learning layers
//
// slmcode already learns. This package must SEED those layers, never compete
// with them:
//
//   - calibration measures what is knowable UP FRONT, cheaply, without doing
//     real work: knee, latency, throughput, context window. All four are
//     properties of the server, observable in seconds.
//   - pkg/evolve's contextual bandit learns what is only knowable from
//     OUTCOMES — edit format, think passes, review strictness, role model.
//     A synthetic 16-token probe says nothing about whether a second think
//     pass improves a patch, so this package deliberately seeds NO bandit
//     posterior. Its warm starts (evolve.DefaultPriors) stay the authority.
//   - pkg/memory/latency.go tracks real per-role p95. Calibration seeds it via
//     SeedRoleLatency and is then swamped by real observations, which is the
//     intended decay.
//
// # Testability
//
// The SELECTION is a pure function of the timing table (SelectKnee) and all
// I/O goes through Prober, so unit tests inject a table and `make check` never
// contacts a model.
package calibrate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// CalibratorVersion is the generation of the probe that produced a Profile.
//
// Bump it whenever calibration starts measuring something NEW, so a later
// slmcode can tell an incomplete old profile from a complete current one and
// re-probe rather than silently missing a field.
const CalibratorVersion = 1

// Probe shape and bounds.
const (
	// DefaultEfficiencyFloor is the fraction of IDEAL linear scaling a
	// concurrency level must still deliver to be worth using.
	//
	// 0.60 is a threshold, not a fitted constant: it sits in the empty band
	// between what a partially-parallel local server delivers at its knee
	// (~68% measured on both a 9B and a 27B) and what it delivers past it
	// (~37-39%). A server that genuinely scales stays near 1.0 at every level
	// and keeps climbing — the floor SELECTS a knee, it does not encode one.
	DefaultEfficiencyFloor = 0.60

	// DefaultMaxTokens is the completion size of a calibration call: large
	// enough that decode dominates connect+prefill (and above the 8-token
	// floor backends.Throughput.Observe requires), small enough that four of
	// them on a 27B cost seconds rather than minutes.
	DefaultMaxTokens = 16

	// DefaultSoloSamples is how many solo calls establish the baseline. Three
	// gives a usable p50/p95 without turning the probe into a benchmark.
	DefaultSoloSamples = 3

	// DefaultBudget is the hard wall-clock cap on a whole calibration.
	// Exceeding it stops the probe and keeps whatever levels completed; a
	// calibration that can hang is worse than no calibration.
	DefaultBudget = 60 * time.Second

	// MaxConcurrencyLevel caps what any calibration will try.
	MaxConcurrencyLevel = 8
)

// DefaultLevels are the concurrency levels probed, in order. 8 is only reached
// when 4 is still above the efficiency floor — see Calibrate.
var DefaultLevels = []int{1, 2, 4, 8}

// ErrUnreachable is returned when the endpoint never answered. Callers fall
// back to their static defaults and emit exactly one warning.
var ErrUnreachable = errors.New("calibrate: endpoint did not answer")

// Sample is one completed calibration call.
type Sample struct {
	Elapsed time.Duration
	// CompletionTokens is what the server reported in `usage`. 0 when it
	// reported nothing, in which case throughput is left unmeasured rather
	// than invented from the requested max_tokens.
	CompletionTokens int
}

// Level is one concurrency level's measurement.
//
// Efficiency is wall(1)/wall(n): the fraction of ideal linear scaling actually
// delivered. It reproduces the hand-run numbers exactly — 0.58/0.85 = 68% at
// two-way, 0.58/1.49 = 39% at four-way — and needs no token accounting, which
// is what makes it comparable across models and servers.
type Level struct {
	Concurrency int `json:"concurrency"`
	// WallMs is how long all Concurrency calls took, together.
	WallMs int64 `json:"wall_ms"`
	// PerRequestMs is the mean of the individual call durations.
	PerRequestMs int64 `json:"per_request_ms"`
	// Throughput is the multiple of SOLO throughput this level delivered
	// (ideal would equal Concurrency).
	Throughput float64 `json:"throughput"`
	// Efficiency is Throughput/Concurrency, i.e. the fraction of ideal.
	Efficiency float64 `json:"efficiency"`
}

// Metadata is what the server reports about a model, when it reports anything.
type Metadata struct {
	// ContextLimit is the usable context window in tokens (0 = not reported).
	ContextLimit int `json:"context_limit,omitempty"`
	// Source names where ContextLimit came from, for the human output.
	Source string `json:"source,omitempty"`
}

// Prober issues the raw calls a calibration needs.
//
// It exists so the policy above can be tested without a model: unit tests
// inject a table-driven fake, and `make check` therefore never contacts an
// endpoint. The HTTP implementation is NewHTTPProber.
type Prober interface {
	// Complete issues ONE tiny completion. It must be safe to call
	// concurrently — the concurrency levels depend on it.
	Complete(ctx context.Context) (Sample, error)
	// Metadata reports what the server says about the model. A prober that
	// cannot ask returns a zero Metadata and no error.
	Metadata(ctx context.Context) (Metadata, error)
}

// Options configures one calibration.
// Progress is one calibration stage, reported as it happens.
//
// Calibration measures a live endpoint and is hard-capped at Budget, but on a
// cold local model most of that can go into loading weights for the FIRST call
// — a 42GB model is minutes of silence before anything is measurable. Without
// staged progress the harness looks hung at exactly the moment it is doing the
// thing that makes every later timeout correct.
type Progress struct {
	// Stage is a short human phrase: "reading model metadata", "warming up",
	// "latency baseline", "concurrency 4".
	Stage string
	// Step and Total describe position, when known (Total 0 = indeterminate).
	Step, Total int
	// Detail is an optional measured value to show alongside, e.g. "312ms".
	Detail string
}

// String renders a progress line for a terminal.
func (p Progress) String() string {
	out := p.Stage
	if p.Total > 0 {
		out = fmt.Sprintf("%s (%d/%d)", out, p.Step, p.Total)
	}
	if p.Detail != "" {
		out += " — " + p.Detail
	}
	return out
}

type Options struct {
	// OnProgress, when set, is called as each stage begins and completes. It
	// must not block: calibration is on the startup path.
	OnProgress func(Progress)

	// Levels are the concurrency levels to try, ascending, starting at 1.
	// Empty uses DefaultLevels.
	Levels []int
	// SoloSamples is how many solo calls form the latency baseline.
	SoloSamples int
	// EfficiencyFloor overrides DefaultEfficiencyFloor.
	EfficiencyFloor float64
	// Budget is the hard cap on the whole calibration.
	Budget time.Duration
	// Now is injectable for deterministic tests. Defaults to time.Now.
	Now func() time.Time
}

// note reports a stage, safely when no observer is installed.
func (o Options) note(stage string, step, total int, detail string) {
	if o.OnProgress == nil {
		return
	}
	o.OnProgress(Progress{Stage: stage, Step: step, Total: total, Detail: detail})
}

func (o Options) withDefaults() Options {
	if len(o.Levels) == 0 {
		o.Levels = append([]int(nil), DefaultLevels...)
	}
	if o.SoloSamples <= 0 {
		o.SoloSamples = DefaultSoloSamples
	}
	if o.EfficiencyFloor <= 0 {
		o.EfficiencyFloor = DefaultEfficiencyFloor
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Calibrate measures one (model, endpoint) pair.
//
// Sequence: one throwaway warm-up call (so model-load time is never counted as
// latency), then the solo baseline, then each concurrency level in turn. A
// level is only attempted when the previous one cleared the efficiency floor —
// there is no reason to spend four seconds proving that 8 is worse than 4 on a
// server that already fell off at 4.
//
// It never reports a number it did not measure. On total failure it returns
// ErrUnreachable and the caller falls back to the static defaults.
func Calibrate(ctx context.Context, p Prober, key Key, opt Options) (Profile, error) {
	if p == nil {
		return Profile{}, ErrUnreachable
	}
	opt = opt.withDefaults()
	start := opt.Now()
	deadline := start.Add(opt.Budget)
	remaining := func() time.Duration { return deadline.Sub(opt.Now()) }

	// Warm-up. Its duration is deliberately discarded: on a cold local server
	// it is dominated by loading weights, which is not what a role call pays.
	opt.note("warming up the model", 0, 0, "first call loads weights; this is the slow one")
	if _, err := p.Complete(ctx); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	key = key.Normalize()
	prof := Profile{
		ID:          key.ID(),
		Version:     CalibratorVersion,
		Key:         key,
		Model:       key.Model,
		Endpoint:    key.Endpoint,
		Provider:    key.Provider,
		MeasuredAt:  start.UTC(),
		FloorUsed:   opt.EfficiencyFloor,
		MaxParallel: 1,
	}

	// Solo baseline: SoloSamples sequential calls.
	solo := make([]Sample, 0, opt.SoloSamples)
	for i := 0; i < opt.SoloSamples; i++ {
		if i > 0 && remaining() <= 0 {
			prof.Partial = true
			break
		}
		opt.note("latency baseline", i+1, opt.SoloSamples, "")
		s, err := p.Complete(ctx)
		if err != nil {
			if len(solo) == 0 {
				return Profile{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
			}
			prof.Partial = true
			break
		}
		solo = append(solo, s)
	}
	if len(solo) == 0 {
		return Profile{}, ErrUnreachable
	}
	elapsed := make([]time.Duration, len(solo))
	for i, s := range solo {
		elapsed[i] = s.Elapsed
	}
	prof.P50Ms = quantileMs(elapsed, 0.5)
	prof.P95Ms = quantileMs(elapsed, 0.95)
	prof.SoloSamples = len(solo)
	prof.CompletionTokens, prof.TokensPerSec = throughputOf(solo)

	base := meanMs(elapsed)
	if base <= 0 {
		base = 1
	}

	for _, n := range opt.Levels {
		if n <= 1 {
			// Level 1 is the baseline itself: perfect by construction.
			prof.Levels = append(prof.Levels, Level{
				Concurrency: 1, WallMs: base, PerRequestMs: base,
				Throughput: 1, Efficiency: 1,
			})
			continue
		}
		if n > MaxConcurrencyLevel {
			break
		}
		// Only climb while the previous level is still worth it, and only when
		// budget remains for a level that costs roughly one solo call.
		if !lastLevelClears(prof.Levels, opt.EfficiencyFloor) {
			break
		}
		if remaining() < time.Duration(base)*time.Millisecond {
			prof.Partial = true
			break
		}
		opt.note(fmt.Sprintf("concurrency %d", n), 0, 0, "")
		lvl, err := measureLevel(ctx, p, n, base)
		if err != nil {
			prof.Partial = true
			break
		}
		prof.Levels = append(prof.Levels, lvl)
	}

	prof.MaxParallel = SelectKnee(prof.Levels, opt.EfficiencyFloor)
	prof.QueueInflation = inflationAt(prof.Levels, prof.MaxParallel)
	opt.note("reading the model's context window", 0, 0, "")
	if md, err := p.Metadata(ctx); err == nil && md.ContextLimit > 0 {
		prof.ContextLimit = md.ContextLimit
		prof.ContextSource = md.Source
	}
	prof.DurationMs = opt.Now().Sub(start).Milliseconds()
	return prof, nil
}

// measureLevel times n identical calls issued at once.
func measureLevel(ctx context.Context, p Prober, n int, baseMs int64) (Level, error) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		each []time.Duration
		errs int
	)
	t0 := time.Now()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s, err := p.Complete(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				return
			}
			each = append(each, s.Elapsed)
		}()
	}
	wg.Wait()
	wall := time.Since(t0)

	// A level with ANY failure is not evidence: the wall time then measures a
	// fast rejection, not the server's concurrency behavior.
	if errs > 0 || len(each) != n {
		return Level{}, fmt.Errorf("calibrate: %d of %d calls failed at concurrency %d", errs, n, n)
	}
	wallMs := wall.Milliseconds()
	if wallMs <= 0 {
		wallMs = 1
	}
	// throughput = n × solo_wall / level_wall; efficiency = that over n,
	// which reduces to solo_wall / level_wall.
	eff := float64(baseMs) / float64(wallMs)
	return Level{
		Concurrency:  n,
		WallMs:       wallMs,
		PerRequestMs: meanMs(each),
		Throughput:   round4(eff * float64(n)),
		Efficiency:   round4(eff),
	}, nil
}

// lastLevelClears reports whether the highest measured level so far is still
// above the floor — the "only if still climbing" escalation rule.
func lastLevelClears(levels []Level, floor float64) bool {
	if len(levels) == 0 {
		return true
	}
	return levels[len(levels)-1].Efficiency >= floor
}

// SelectKnee picks the concurrency to use from a measured table.
//
// It is the whole policy, as a pure function: the highest level that still
// delivers at least `floor` of ideal scaling, with every lower level also
// clearing it. The first level below the floor stops the walk — a scaling
// curve that has bent does not un-bend, and treating a later higher reading as
// real would chase noise.
//
// This hardcodes NO concurrency value. Fed the measured local table
// (1.00, 0.68, 0.39) it answers 2; fed a server that genuinely scales
// (1.00, 0.95, 0.92, 0.85) it answers 8.
func SelectKnee(levels []Level, floor float64) int {
	if floor <= 0 {
		floor = DefaultEfficiencyFloor
	}
	sorted := append([]Level(nil), levels...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Concurrency < sorted[j].Concurrency
	})
	best := 1
	for _, l := range sorted {
		if l.Concurrency <= 1 {
			continue
		}
		if l.Efficiency < floor {
			break
		}
		best = l.Concurrency
	}
	return best
}

// inflationAt is how much longer ONE request takes at concurrency n than it
// takes alone. It is the honest cost of running at the knee, and what a
// wall-clock timeout budget has to absorb.
func inflationAt(levels []Level, n int) float64 {
	var solo, at int64
	for _, l := range levels {
		if l.Concurrency == 1 {
			solo = l.PerRequestMs
		}
		if l.Concurrency == n {
			at = l.PerRequestMs
		}
	}
	if solo <= 0 || at <= 0 || n <= 1 {
		return 1
	}
	r := float64(at) / float64(solo)
	if r < 1 {
		return 1
	}
	return round4(r)
}

// throughputOf derives decode rate from the solo samples. Servers that report
// no `usage` leave it at zero rather than having it invented from the
// requested max_tokens — a made-up rate would be used with confidence by
// backends.EstimateTimeout.
func throughputOf(samples []Sample) (tokens int, tps float64) {
	var totalTokens int
	var totalSec float64
	for _, s := range samples {
		if s.CompletionTokens <= 0 || s.Elapsed <= 0 {
			continue
		}
		totalTokens += s.CompletionTokens
		totalSec += s.Elapsed.Seconds()
	}
	if totalTokens == 0 || totalSec <= 0 {
		return 0, 0
	}
	return totalTokens / len(samples), round4(float64(totalTokens) / totalSec)
}

// quantileMs is the nearest-rank quantile of a duration set, in milliseconds.
// Nearest rank (not interpolation) is exact integer arithmetic on the recorded
// values, matching pkg/memory's latency quantiles so the two agree.
func quantileMs(in []time.Duration, q float64) int64 {
	n := len(in)
	if n == 0 {
		return 0
	}
	if q < 0 || math.IsNaN(q) {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	ms := make([]int64, n)
	for i, d := range in {
		ms[i] = maxInt64(d.Milliseconds(), 1)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	idx := int(math.Ceil(q*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return ms[idx]
}

func meanMs(in []time.Duration) int64 {
	if len(in) == 0 {
		return 0
	}
	var total int64
	for _, d := range in {
		total += maxInt64(d.Milliseconds(), 1)
	}
	return total / int64(len(in))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// round4 keeps the stored ratios stable across runs and platforms so a Profile
// round-trips through JSON byte-identically.
func round4(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*10000) / 10000
}

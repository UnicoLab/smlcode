package calibrate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func localCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Model = "Qwen3.8-27B-4bit"
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Normalize()
	return cfg
}

func measured() Profile {
	return Profile{
		Version:     CalibratorVersion,
		Key:         Key{Model: "Qwen3.8-27B-4bit", Endpoint: "http://127.0.0.1:8000/v1"}.Normalize(),
		Model:       "Qwen3.8-27B-4bit",
		MaxParallel: 4,
		Levels: []Level{
			{Concurrency: 1, PerRequestMs: 1660, Efficiency: 1},
			{Concurrency: 2, PerRequestMs: 2400, Efficiency: 0.95},
			{Concurrency: 4, PerRequestMs: 3000, Efficiency: 0.90},
			{Concurrency: 8, PerRequestMs: 9000, Efficiency: 0.20},
		},
		FloorUsed:        DefaultEfficiencyFloor,
		QueueInflation:   1.8,
		P50Ms:            1660,
		P95Ms:            1700,
		SoloSamples:      3,
		CompletionTokens: 16,
		TokensPerSec:     9.6,
		ContextLimit:     262144,
		ContextSource:    "GET /v1/models max_model_len",
		MeasuredAt:       time.Now(),
	}
}

func TestApplyUsesTheMeasuredKneeWhenNothingIsSet(t *testing.T) {
	cfg := localCfg(t)
	before := cfg.MaxParallel
	applied := Apply(cfg, measured())
	if cfg.MaxParallel != 4 {
		t.Fatalf("max_parallel = %d, want the measured 4 (was %d)", cfg.MaxParallel, before)
	}
	if !hasKey(applied, "max_parallel") {
		t.Fatalf("the change must be reported: %+v", applied)
	}
}

// TestExplicitMaxParallelAlwaysWinsOverCalibration is the override guarantee:
// a number a human wrote down is never re-derived, never re-measured away.
func TestExplicitMaxParallelAlwaysWinsOverCalibration(t *testing.T) {
	for _, want := range []int{1, 2, 4, 6} {
		cfg := localCfg(t)
		if err := cfg.Set("max_parallel", want); err != nil {
			t.Fatal(err)
		}
		cfg.Normalize()
		applied := Apply(cfg, measured())
		if cfg.MaxParallel != want {
			t.Fatalf("explicit max_parallel=%d was overridden to %d", want, cfg.MaxParallel)
		}
		if hasKey(applied, "max_parallel") {
			t.Fatalf("an explicit value must not be reported as applied: %+v", applied)
		}
	}
}

func TestApplyInstallsTheServerReportedContextWindow(t *testing.T) {
	cfg := localCfg(t)
	before := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	applied := Apply(cfg, measured())
	prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	if prof.ContextLimit != 262144 {
		t.Fatalf("context limit = %d, want the server-reported 262144", prof.ContextLimit)
	}
	if !hasKey(applied, "context_limit") {
		t.Fatalf("the change must be reported: %+v", applied)
	}
	// Installing an exact-model key short-circuits the family/size-bucket
	// refinement, so everything else the resolver used to produce must SURVIVE.
	//
	// "Survive" now means preserved-or-RAISED, not preserved-exactly: applying a
	// measured window also derives the budgets that depend on it (see
	// derive.go), because leaving them static is what gave a 262K model the same
	// 260-token skill budget as a 4K one. The invariant that matters is
	// unchanged — no field may be LOST or shrunk.
	for _, f := range []struct {
		name        string
		got, wasMin int
	}{
		{"max_tokens", prof.MaxTokens, before.MaxTokens},
		{"max_turns", prof.MaxTurns, before.MaxTurns},
		{"thinking_budget_tokens", prof.ThinkingBudgetTokens, before.ThinkingBudgetTokens},
		{"skill_token_budget", prof.SkillTokenBudget, before.SkillTokenBudget},
		{"knowledge_token_budget", prof.KnowledgeTokenBudget, before.KnowledgeTokenBudget},
	} {
		if f.got < f.wasMin {
			t.Fatalf("%s was lost or shrunk: %d → %d\n got %+v\nwas %+v",
				f.name, f.wasMin, f.got, prof, before)
		}
	}
	if prof.Temperature != before.Temperature {
		t.Fatalf("temperature was changed by a context measurement: %v → %v",
			before.Temperature, prof.Temperature)
	}
}

func TestExplicitModelProfilesWinOverTheReportedWindow(t *testing.T) {
	cfg := localCfg(t)
	if err := cfg.Set("model_profiles", map[string]config.ModelProfile{
		"default": {ContextLimit: 8192, MaxTokens: 2048},
	}); err != nil {
		t.Fatal(err)
	}
	cfg.Provenance().Mark("model_profiles", config.LayerProject, "")
	cfg.Normalize()
	Apply(cfg, measured())
	if got := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model).ContextLimit; got != 8192 {
		t.Fatalf("context limit = %d, want the user's 8192", got)
	}
}

func TestApplyOnlyRaisesTaskTimeoutAndOnlyWhenUnset(t *testing.T) {
	// Unset: a slow model gets a wider ceiling.
	cfg := localCfg(t)
	before := cfg.TaskTimeout
	Apply(cfg, measured())
	if cfg.TaskTimeout <= before {
		t.Fatalf("task_timeout = %s, want a raise from %s for a 9.6 tok/s model", cfg.TaskTimeout, before)
	}

	// Explicit: untouched, even when the measurement says it is too small.
	pinned := localCfg(t)
	if err := pinned.Set("task_timeout", "90s"); err != nil {
		t.Fatal(err)
	}
	pinned.Provenance().Mark("task_timeout", config.LayerProject, "")
	pinned.Normalize()
	Apply(pinned, measured())
	if pinned.TaskTimeout != 90*time.Second {
		t.Fatalf("explicit task_timeout was changed to %s", pinned.TaskTimeout)
	}

	// A fast model must never LOWER an unset ceiling.
	fast := localCfg(t)
	quick := measured()
	quick.TokensPerSec = 500
	quick.P95Ms = 100
	quick.QueueInflation = 1
	baseline := fast.TaskTimeout
	Apply(fast, quick)
	if fast.TaskTimeout < baseline {
		t.Fatalf("task_timeout lowered to %s from %s — a 16-token probe must never shrink a ceiling",
			fast.TaskTimeout, baseline)
	}
}

// TestAutoTaskTimeoutIsCapped: a very slow model's honest recommendation can be
// most of an hour. task_timeout is a spend limit too, so the automatic raise
// stops at a documented ceiling and the rest is advice.
func TestAutoTaskTimeoutIsCapped(t *testing.T) {
	cfg := localCfg(t)
	crawling := measured()
	crawling.TokensPerSec = 1.5 // ~45 minutes of pure decode for 4096 tokens
	// The recommendation must be computed against the max_tokens that will
	// actually be IN FORCE, which is the derived one: applyContext runs before
	// applyTaskTimeout precisely so the timeout reflects the budget the run will
	// use rather than the static default it replaced.
	prof := DeriveProfile(
		config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model), crawling.ContextLimit)
	uncapped, ok := crawling.RecommendedTaskTimeout(prof.MaxTokens)
	if !ok || uncapped <= MaxAutoTaskTimeout {
		t.Fatalf("test setup: recommendation %s must exceed the %s cap", uncapped, MaxAutoTaskTimeout)
	}
	applied := Apply(cfg, crawling)
	if cfg.TaskTimeout != MaxAutoTaskTimeout {
		t.Fatalf("task_timeout = %s, want the %s automatic cap", cfg.TaskTimeout, MaxAutoTaskTimeout)
	}
	var why string
	for _, a := range applied {
		if a.Key == "task_timeout" {
			why = a.Why
		}
	}
	if !strings.Contains(why, "capped") || !strings.Contains(why, uncapped.String()) {
		t.Fatalf("the capped raise must still report the real recommendation: %q", why)
	}
}

func TestApplyIsANoOpForAnEmptyProfile(t *testing.T) {
	cfg := localCfg(t)
	before := *cfg
	if applied := Apply(cfg, Profile{}); applied != nil {
		t.Fatalf("an unmeasured profile must change nothing, got %+v", applied)
	}
	if cfg.MaxParallel != before.MaxParallel || cfg.TaskTimeout != before.TaskTimeout {
		t.Fatal("an unmeasured profile mutated the config")
	}
}

// ── seeding ────────────────────────────────────────────────────────────────

type fakeLatency struct {
	samples map[string]int
	records map[string][]time.Duration
}

func newFakeLatency() *fakeLatency {
	return &fakeLatency{samples: map[string]int{}, records: map[string][]time.Duration{}}
}

func (f *fakeLatency) Samples(role, family string) int { return f.samples[role+"|"+family] }

func (f *fakeLatency) Record(role, family string, d time.Duration) {
	k := role + "|" + family
	f.records[k] = append(f.records[k], d)
	f.samples[k]++
}

func TestSeedRoleLatencyFillsEmptySeriesOnly(t *testing.T) {
	st := newFakeLatency()
	st.samples["worker|qwen3.8"] = 7 // real evidence already exists

	seeded := SeedRoleLatency(st, "qwen3.8", measured(), []RoleSeed{
		{Role: "worker", MaxTokens: 4096},
		{Role: "reviewer", MaxTokens: 4096},
		{Role: "planner", MaxTokens: 4096},
	}, 3, 0)

	if len(seeded) != 2 || seeded[0] != "planner" || seeded[1] != "reviewer" {
		t.Fatalf("seeded = %v, want the two empty roles in a stable order", seeded)
	}
	if got := len(st.records["worker|qwen3.8"]); got != 0 {
		t.Fatalf("real evidence was displaced: %d records written to worker", got)
	}
	if got := len(st.records["reviewer|qwen3.8"]); got != 3 {
		t.Fatalf("reviewer got %d samples, want exactly the 3 minimum", got)
	}
	for _, d := range st.records["reviewer|qwen3.8"] {
		if d <= 0 {
			t.Fatal("a seeded duration must be positive")
		}
	}
}

// TestSeedRoleLatencyIsCappedAtTheCeiling: a slow model's seed must degrade to
// exactly today's cold-start behavior (the whole budget), never to a claim
// the harness would refuse to grant.
func TestSeedRoleLatencyIsCappedAtTheCeiling(t *testing.T) {
	st := newFakeLatency()
	ceiling := 90 * time.Second
	slow := measured() // 9.6 tok/s: 4096 tokens is many minutes
	SeedRoleLatency(st, "qwen3.8", slow, []RoleSeed{{Role: "worker", MaxTokens: 4096}}, 3, ceiling)
	for _, d := range st.records["worker|qwen3.8"] {
		if d != ceiling {
			t.Fatalf("seed %s exceeds the %s ceiling", d, ceiling)
		}
	}

	// A model fast enough that a role provably fits inside the ceiling keeps
	// the real estimate — that is the case where seeding actually helps.
	fastStore := newFakeLatency()
	fast := measured()
	fast.TokensPerSec = 400
	fast.P95Ms = 200
	fast.QueueInflation = 1
	SeedRoleLatency(fastStore, "gpt-4o", fast, []RoleSeed{{Role: "worker", MaxTokens: 4096}}, 3, 10*time.Minute)
	for _, d := range fastStore.records["worker|gpt-4o"] {
		if d >= 10*time.Minute || d <= 0 {
			t.Fatalf("fast model seed = %s, want a real sub-ceiling estimate", d)
		}
	}
}

// TestSeedTurnAllowanceBiasesHigh: a role phase is many calls, and an
// under-estimated budget is the failure this change exists to remove.
func TestSeedTurnAllowanceBiasesHigh(t *testing.T) {
	p := measured()
	seed, ok := p.RoleLatencySeed(4096)
	if !ok {
		t.Fatal("a measured rate must yield a seed")
	}
	perCall := time.Duration((4096/p.TokensPerSec*p.QueueInflation + float64(p.P95Ms)/1000) * float64(time.Second))
	if seed < perCall*SeedTurnAllowance-time.Second || seed > perCall*SeedTurnAllowance+time.Second {
		t.Fatalf("seed %s is not %d× the single-call estimate %s", seed, SeedTurnAllowance, perCall)
	}
	if seed <= perCall {
		t.Fatal("the seed must exceed a single call — a role phase is many calls")
	}
}

func TestSeedRoleLatencyDoesNothingWithoutThroughput(t *testing.T) {
	st := newFakeLatency()
	p := measured()
	p.TokensPerSec = 0
	if got := SeedRoleLatency(st, "qwen3.8", p, []RoleSeed{{Role: "worker", MaxTokens: 4096}}, 3, 0); got != nil {
		t.Fatalf("seeded %v without a measured rate — a fabricated seed is worse than none", got)
	}
	if len(st.records) != 0 {
		t.Fatal("nothing may be written without evidence")
	}
}

func TestSeedRoleLatencyIsNilSafe(t *testing.T) {
	if got := SeedRoleLatency(nil, "f", measured(), []RoleSeed{{Role: "w", MaxTokens: 10}}, 3, 0); got != nil {
		t.Fatal("a nil store must be a silent no-op — evolve may be off")
	}
	if got := SeedRoleLatency(newFakeLatency(), "", measured(), []RoleSeed{{Role: "w", MaxTokens: 10}}, 3, 0); got != nil {
		t.Fatal("an empty family must not be seeded")
	}
}

type fakeThroughput struct {
	model  string
	tokens int
	took   time.Duration
	calls  int
}

func (f *fakeThroughput) Observe(model string, tokens int, elapsed time.Duration) {
	f.model, f.tokens, f.took, f.calls = model, tokens, elapsed, f.calls+1
}

func TestSeedThroughputHandsOverTheMeasuredRate(t *testing.T) {
	rec := &fakeThroughput{}
	if !SeedThroughput(rec, measured()) {
		t.Fatal("a measured rate must be handed over")
	}
	if rec.model != "Qwen3.8-27B-4bit" || rec.tokens != 16 || rec.took <= 0 {
		t.Fatalf("observed %q %d tokens in %s", rec.model, rec.tokens, rec.took)
	}
	// The rate reconstructed from what we passed must match what we measured.
	got := float64(rec.tokens) / rec.took.Seconds()
	if got < 9.5 || got > 9.7 {
		t.Fatalf("reconstructed rate %.2f tok/s, want ~9.6", got)
	}

	rec2 := &fakeThroughput{}
	p := measured()
	p.TokensPerSec = 0
	if SeedThroughput(rec2, p) || rec2.calls != 0 {
		t.Fatal("an unmeasured rate must not be handed over")
	}
}

// ── the auto path ──────────────────────────────────────────────────────────

func TestEnsureCalibratedFallsBackWithOneWarningAndNeverBlocks(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	before := cfg.MaxParallel
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{
		Store:  Open(t.TempDir()),
		Prober: &fakeProber{solo: time.Millisecond, failFrom: 1},
	})
	if out.Measured || out.Profile.MaxParallel != 0 {
		t.Fatal("a failed probe must not report a measurement")
	}
	if out.Warning == "" {
		t.Fatal("a failed probe must warn exactly once")
	}
	if !strings.Contains(out.Warning, "using defaults") {
		t.Fatalf("the warning must name the fallback: %q", out.Warning)
	}
	if cfg.MaxParallel != before {
		t.Fatalf("a failed probe changed max_parallel to %d", cfg.MaxParallel)
	}
}

func TestEnsureCalibratedProbesOncePerProcess(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	// A store that cannot cache, so only the once-guard can stop a re-probe.
	p := &fakeProber{solo: time.Millisecond, tokens: 16}
	first := EnsureCalibrated(context.Background(), cfg, AutoOptions{Prober: p})
	if !first.Measured {
		t.Fatal("the first call must measure")
	}
	callsAfterFirst := p.calls
	second := EnsureCalibrated(context.Background(), cfg, AutoOptions{Prober: p})
	if second.Measured {
		t.Fatal("the second call must not re-probe the same pair in one process")
	}
	if p.calls != callsAfterFirst {
		t.Fatalf("the prober was called %d more times", p.calls-callsAfterFirst)
	}
}

func TestEnsureCalibratedReusesAStoredProfileWithoutProbing(t *testing.T) {
	ResetOnce()
	dir := t.TempDir()
	cfg := localCfg(t)
	store := Open(dir)
	store.Put(measured())

	p := &fakeProber{solo: time.Millisecond, tokens: 16, meta: Metadata{ContextLimit: 262144}}
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Store: store, Prober: p})
	if out.Measured {
		t.Fatal("a current stored profile must not be re-measured")
	}
	if !out.Cached {
		t.Fatal("the cached profile must be reported as such")
	}
	if cfg.MaxParallel != 4 {
		t.Fatalf("the cached knee was not applied: max_parallel=%d", cfg.MaxParallel)
	}
	// Only the cheap metadata check may touch the network.
	if p.calls != 0 {
		t.Fatalf("a cache hit issued %d completions", p.calls)
	}
}

func TestEnsureCalibratedRePorbesWhenTheContextWindowChanges(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	store := Open(t.TempDir())
	store.Put(measured())

	p := &fakeProber{solo: time.Millisecond, tokens: 16, meta: Metadata{ContextLimit: 32768}}
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Store: store, Prober: p})
	if !out.Measured {
		t.Fatal("a changed context window must trigger a re-measurement")
	}
	if p.calls == 0 {
		t.Fatal("a re-measurement must issue completions")
	}
}

func TestEnsureCalibratedIgnoresAnOfflineMetadataCheck(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	store := Open(t.TempDir())
	store.Put(measured())

	p := &fakeProber{solo: time.Millisecond, tokens: 16, metaErr: errors.New("connection refused")}
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Store: store, Prober: p})
	if out.Measured {
		t.Fatal("a failed metadata check must not trigger a re-probe storm")
	}
	if !out.Cached {
		t.Fatal("the cached profile is still the best evidence available")
	}
}

func TestEnsureCalibratedForceAlwaysReMeasures(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	store := Open(t.TempDir())
	store.Put(measured())
	p := &fakeProber{solo: time.Millisecond, tokens: 16, meta: Metadata{ContextLimit: 262144}}

	for i := 0; i < 2; i++ {
		out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Store: store, Prober: p, Force: true})
		if !out.Measured {
			t.Fatalf("--force must re-measure every time (attempt %d)", i+1)
		}
	}
}

func TestEnsureCalibratedNoticeNamesTheChoiceAndTheOverride(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	p := &fakeProber{
		solo:     40 * time.Millisecond,
		perLevel: map[int]time.Duration{2: 50 * time.Millisecond, 4: 110 * time.Millisecond},
		tokens:   16,
		meta:     Metadata{ContextLimit: 262144, Source: "GET /v1/models max_model_len"},
	}
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Prober: p, Store: Open(t.TempDir())})
	if !out.Measured {
		t.Fatalf("expected a measurement, warning=%q", out.Warning)
	}
	for _, want := range []string{"calibrated", "concurrency knee", "efficiency", "ctx 262144", "override"} {
		if !strings.Contains(out.Notice, want) {
			t.Fatalf("notice %q is missing %q", out.Notice, want)
		}
	}
	// Calibration set max_parallel itself here, so it must not claim to be
	// respecting a setting the user never made.
	if strings.Contains(out.Notice, "keeping your") {
		t.Fatalf("notice claims a user pin that does not exist: %q", out.Notice)
	}
}

// TestCalibratedNoticeSaysWhenAPinIsRespected: the reassurance a user with an
// explicit max_parallel needs — calibration ran, and left their number alone.
func TestCalibratedNoticeSaysWhenAPinIsRespected(t *testing.T) {
	ResetOnce()
	cfg := localCfg(t)
	if err := cfg.Set("max_parallel", 6); err != nil {
		t.Fatal(err)
	}
	cfg.Normalize()
	p := &fakeProber{
		solo:     40 * time.Millisecond,
		perLevel: map[int]time.Duration{2: 50 * time.Millisecond, 4: 110 * time.Millisecond},
		tokens:   16,
	}
	out := EnsureCalibrated(context.Background(), cfg, AutoOptions{Prober: p, Store: Open(t.TempDir())})
	if !out.Measured {
		t.Fatalf("expected a measurement, warning=%q", out.Warning)
	}
	if !strings.Contains(out.Notice, "keeping your max_parallel=6") {
		t.Fatalf("notice must say the pin was respected: %q", out.Notice)
	}
	if cfg.MaxParallel != 6 {
		t.Fatalf("max_parallel = %d, want the pinned 6", cfg.MaxParallel)
	}
}

func hasKey(applied []Applied, key string) bool {
	for _, a := range applied {
		if a.Key == key {
			return true
		}
	}
	return false
}

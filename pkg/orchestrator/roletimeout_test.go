package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// everyRole is the set roleTimeout is asked about in production.
var everyRole = []string{
	plan.RoleWorker, "deep", plan.RoleCorrector, plan.RoleExplorer, "docs",
	plan.RoleTester, plan.RolePlaceholder, plan.RolePlanner, "splitter",
	plan.RoleReviewer, "coordinator", "architect", plan.RoleContext, "memory",
	"composer", "go-tester", "escalate",
}

// newTimeoutFixture is an Orchestrator with real (temp-dir) latency memory and
// no LLM. The timeout policy is deterministic and must need zero model calls.
func newTimeoutFixture(t *testing.T, model string, taskTimeout time.Duration) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Root = dir
	cfg.Model = model
	cfg.Evolve = true
	cfg.Normalize()
	cfg.TaskTimeout = taskTimeout // after Normalize, which floors a zero value

	eng, err := evolve.OpenWith(cfg.Root, filepath.Join(dir, "userhome"),
		evolve.EngineOptions{Deterministic: true})
	if err != nil {
		t.Fatalf("open evolve engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	return &Orchestrator{
		cfg:     cfg,
		store:   contextstore.New(cfg.SlmDir()),
		evolve:  eng,
		onEvent: func(Event) {},
	}
}

func (o *Orchestrator) seedLatency(t *testing.T, role string, n int, d time.Duration) {
	t.Helper()
	st := o.latencyStore()
	if st == nil {
		t.Fatal("no latency store")
	}
	for i := 0; i < n; i++ {
		st.Record(memory.LatencyKey{Role: role, ModelFamily: o.modelFamily()}, d)
	}
}

// TestSlowModelIsNotStarvedAtColdStart is the exact regression found live: with
// task_timeout 5m on a 27B oMLX model, `context` was handed full/4 = 75s and
// failed on every single run while `explorer` — comparable work, same workspace
// — was handed the full 300s and finished in 128s.
//
// With no recorded latency the harness has no basis to be stingy, so NO role
// may get less than the full budget.
func TestSlowModelIsNotStarvedAtColdStart(t *testing.T) {
	const full = 5 * time.Minute
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", full)

	for _, role := range everyRole {
		if got := o.roleTimeout(role); got != full {
			t.Errorf("cold start: roleTimeout(%q) = %v, want the full budget %v", role, got, full)
		}
	}

	// The specific pair from the bug report, spelled out.
	ctxTO := o.roleTimeout(plan.RoleContext)
	if ctxTO < 128*time.Second {
		t.Fatalf("context budget %v is below the 128s a comparable role really took", ctxTO)
	}
	if ctxTO != o.roleTimeout(plan.RoleExplorer) {
		t.Fatalf("context (%v) and explorer (%v) must start from the same budget",
			ctxTO, o.roleTimeout(plan.RoleExplorer))
	}
}

// Below MinLatencySamples there is no evidence, so the generous default holds.
func TestColdStartHoldsUntilEnoughSamples(t *testing.T) {
	const full = 5 * time.Minute
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", full)

	for n := 1; n < memory.MinLatencySamples; n++ {
		o.seedLatency(t, plan.RoleContext, 1, 4*time.Second)
		if got := o.roleTimeout(plan.RoleContext); got != full {
			t.Fatalf("with %d sample(s) roleTimeout = %v, want the full budget %v", n, got, full)
		}
	}
	o.seedLatency(t, plan.RoleContext, 1, 4*time.Second)
	if got := o.roleTimeout(plan.RoleContext); got >= full {
		t.Fatalf("with %d samples the budget should tighten, got %v", memory.MinLatencySamples, got)
	}
}

// p95 — not a mean — drives the budget: nine fast samples plus one slow one
// must still buy a budget that covers the slow one.
func TestP95DrivesTimeoutNotMean(t *testing.T) {
	const full = 12 * time.Minute
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", full)

	o.seedLatency(t, plan.RoleContext, 9, 10*time.Second)
	o.seedLatency(t, plan.RoleContext, 1, 200*time.Second)

	got := o.roleTimeout(plan.RoleContext)
	mean := (9*10 + 200) * time.Second / 10 // 29s — what a mean policy would see
	if got <= mean {
		t.Fatalf("roleTimeout = %v, a mean-based budget (%v) would starve the tail", got, mean)
	}
	if got < 200*time.Second {
		t.Fatalf("roleTimeout = %v, must cover the observed 200s tail", got)
	}
	if want := 300 * time.Second; got != want { // 200s × 3/2
		t.Fatalf("roleTimeout = %v, want p95×3/2 = %v", got, want)
	}
}

// The user's task_timeout is a hard ceiling — measurement never overrides it.
func TestTaskTimeoutIsNeverExceeded(t *testing.T) {
	const full = 90 * time.Second
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", full)

	for _, role := range everyRole {
		o.seedLatency(t, role, 6, 30*time.Minute) // absurdly slow evidence
		if got := o.roleTimeout(role); got > full {
			t.Errorf("roleTimeout(%q) = %v exceeds the user's task_timeout %v", role, got, full)
		}
	}
	// A floor larger than the ceiling must also be clamped, not honored.
	tiny := newTimeoutFixture(t, "tinyllama-1.1b", 20*time.Second)
	tiny.seedLatency(t, plan.RoleWorker, 5, time.Second)
	if got := tiny.roleTimeout(plan.RoleWorker); got != 20*time.Second {
		t.Fatalf("roleTimeout = %v, want the 20s ceiling (below the class floor)", got)
	}
}

// A very fast model must not drive budgets to something absurd: the per-class
// floors hold, and they stay ordered heavy > planning > light.
func TestRoleClassFloorsHoldForFastModel(t *testing.T) {
	o := newTimeoutFixture(t, "qwen2.5-coder:1.5b", 12*time.Minute)

	cases := []struct {
		role string
		want time.Duration
	}{
		{plan.RoleWorker, roleFloorHeavy},
		{plan.RoleExplorer, roleFloorHeavy},
		{plan.RolePlanner, roleFloorPlanning},
		{"splitter", roleFloorPlanning},
		{plan.RoleContext, roleFloorLight},
		{plan.RoleReviewer, roleFloorLight},
		{"composer", roleFloorDefault},
	}
	for _, tc := range cases {
		o.seedLatency(t, tc.role, 8, 900*time.Millisecond) // p95×3/2 = 1.35s
		if got := o.roleTimeout(tc.role); got != tc.want {
			t.Errorf("roleTimeout(%q) = %v, want the class floor %v", tc.role, got, tc.want)
		}
	}
	if roleFloorHeavy <= roleFloorPlanning || roleFloorPlanning <= roleFloorLight {
		t.Fatalf("class floors out of order: heavy=%v planning=%v light=%v",
			roleFloorHeavy, roleFloorPlanning, roleFloorLight)
	}
}

// Measured evidence — not a hardcoded fraction — is what makes a light role
// cheaper than a heavy one. The ordering is an OUTCOME of the data.
func TestMeasuredOrderingEmergesFromEvidence(t *testing.T) {
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 12*time.Minute)
	o.seedLatency(t, plan.RoleContext, 5, 20*time.Second)
	o.seedLatency(t, plan.RolePlanner, 5, 100*time.Second)
	o.seedLatency(t, plan.RoleWorker, 5, 240*time.Second)

	ctxTO := o.roleTimeout(plan.RoleContext)
	planTO := o.roleTimeout(plan.RolePlanner)
	workerTO := o.roleTimeout(plan.RoleWorker)
	if ctxTO >= planTO || planTO >= workerTO {
		t.Fatalf("measured budgets out of order: context=%v planner=%v worker=%v", ctxTO, planTO, workerTO)
	}
	// And the slow role really does get more than the old full/4 fraction.
	if workerTO <= 12*time.Minute/4 {
		t.Fatalf("worker budget %v is no better than the old fraction policy", workerTO)
	}
}

// Same recorded data → same timeout. No wall clock, no map iteration.
func TestRoleTimeoutIsDeterministic(t *testing.T) {
	samples := []time.Duration{
		31 * time.Second, 12 * time.Second, 97 * time.Second,
		44 * time.Second, 8 * time.Second, 61 * time.Second,
	}
	var first time.Duration
	for run := 0; run < 25; run++ {
		o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 10*time.Minute)
		for _, d := range samples {
			o.seedLatency(t, plan.RoleContext, 1, d)
		}
		got := o.roleTimeout(plan.RoleContext)
		if run == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d gave %v, run 0 gave %v", run, got, first)
		}
	}

	// The pure policy is exercised directly too — no store, no orchestrator.
	for i := 0; i < 50; i++ {
		if got := roleTimeoutFrom(plan.RoleContext, 10*time.Minute, 97*time.Second, 6); got != first {
			t.Fatalf("roleTimeoutFrom drifted: %v vs %v", got, first)
		}
	}
}

func TestRoleTimeoutFromPurePolicy(t *testing.T) {
	const ceiling = 5 * time.Minute
	cases := []struct {
		name    string
		role    string
		p95     time.Duration
		samples int
		want    time.Duration
	}{
		{"no samples", plan.RoleContext, 0, 0, ceiling},
		{"too few samples", plan.RoleContext, 4 * time.Second, memory.MinLatencySamples - 1, ceiling},
		{"zero p95 with samples", plan.RoleContext, 0, 9, ceiling},
		{"measured mid-range", plan.RoleContext, 100 * time.Second, 9, 150 * time.Second},
		{"below the light floor", plan.RoleContext, 5 * time.Second, 9, roleFloorLight},
		{"below the heavy floor", plan.RoleWorker, 5 * time.Second, 9, roleFloorHeavy},
		{"p95 above the ceiling", plan.RoleWorker, 30 * time.Minute, 9, ceiling},
		{"safety factor would exceed the ceiling", plan.RoleWorker, 4 * time.Minute, 9, ceiling},
		{"zero ceiling falls back to the default", plan.RoleWorker, 0, 0, config.DefaultTaskTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ceiling
			if tc.name == "zero ceiling falls back to the default" {
				c = 0
			}
			if got := roleTimeoutFrom(tc.role, c, tc.p95, tc.samples); got != tc.want {
				t.Errorf("roleTimeoutFrom(%q, %v, %v, %d) = %v, want %v",
					tc.role, c, tc.p95, tc.samples, got, tc.want)
			}
		})
	}
}

// A missing evolve engine (--no-evolve) must not change the answer.
func TestRoleTimeoutWithoutEvolveEngine(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.TaskTimeout = 7 * time.Minute
	o := &Orchestrator{cfg: cfg}
	if o.latencyStore() != nil {
		t.Fatal("expected no latency store without an engine")
	}
	for _, role := range everyRole {
		if got := o.roleTimeout(role); got != 7*time.Minute {
			t.Errorf("roleTimeout(%q) = %v, want %v", role, got, 7*time.Minute)
		}
	}
	// Recording must be a no-op, not a panic.
	o.recordRoleLatency(plan.RoleContext, time.Second, true)
}

// ---------------------------------------------------------------------------
// Evidence rules
// ---------------------------------------------------------------------------

func TestOnlyRealEvidenceIsPersisted(t *testing.T) {
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 5*time.Minute)
	st := o.latencyStore()
	key := memory.LatencyKey{Role: plan.RoleContext, ModelFamily: o.modelFamily()}

	o.recordRoleLatency(plan.RoleContext, 30*time.Second, true) // success
	o.recordRoleLatency(plan.RoleContext, 2*time.Second, false) // provider error
	r, _ := st.Get(key)
	if r.Count() != 1 || r.Max() != 30*time.Second {
		t.Fatalf("kept %d sample(s) (max %v), want only the successful 30s one", r.Count(), r.Max())
	}
	// The in-run tally still sees both — that report is about wall time spent.
	if ms := o.snapshotLatency()[plan.RoleContext]; ms != 32000 {
		t.Errorf("in-run latency tally = %dms, want 32000", ms)
	}
}

// A starved budget must widen on its own: recording the censored timeout is
// what stops "3 fast samples then a cold model" from failing forever.
func TestTimeoutObservationWidensTheBudget(t *testing.T) {
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 10*time.Minute)
	o.seedLatency(t, plan.RoleContext, 4, 10*time.Second)

	before := o.roleTimeout(plan.RoleContext)
	if before != roleFloorLight {
		t.Fatalf("expected the light floor %v, got %v", roleFloorLight, before)
	}
	// The model is cold and blows the budget; the timeout is recorded.
	o.recordRoleLatency(plan.RoleContext, before, true)
	after := o.roleTimeout(plan.RoleContext)
	if after <= before {
		t.Fatalf("budget did not widen after a timeout: %v → %v", before, after)
	}
}

func TestIsTimeoutError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.DeadlineExceeded, true},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), true},
		{errors.New("node execution failed: context deadline exceeded"), true},
		{errors.New("Post \"http://127.0.0.1:8000/v1/chat/completions\": i/o timeout"), true},
		{errors.New("request timed out"), true},
		{errors.New("connection refused"), false},
		{errors.New("400 Bad Request: unknown field guided_json"), false},
	}
	for _, tc := range cases {
		if got := isTimeoutError(tc.err); got != tc.want {
			t.Errorf("isTimeoutError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// The failure has to be legible
// ---------------------------------------------------------------------------

// Today the user sees only "node execution failed: context deadline exceeded",
// which tells them nothing. The replacement must name the role, the budget it
// blew, what has been measured for this model family, and what to do about it.
func TestTimeoutWarningNamesRoleBudgetAndRemedy(t *testing.T) {
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 5*time.Minute)
	o.seedLatency(t, plan.RoleContext, 9, 4*time.Minute)

	var events []Event
	o.onEvent = func(e Event) { events = append(events, e) }

	base := errors.New("node execution failed: context deadline exceeded")
	err := o.explainRoleTimeout(plan.RoleContext, "T1", 5*time.Minute, 5*time.Minute, base)
	if !errors.Is(err, base) {
		t.Fatal("the original cause must stay wrapped")
	}
	msg := err.Error()
	t.Logf("what the user now sees:\n%s", msg)

	for _, want := range []string{
		plan.RoleContext,        // which role
		"timed out",             // what happened
		"5m0s",                  // the budget it blew
		"p95",                   // what has been measured
		"qwen3.8",               // for which model family
		"9 samples",             // over how much evidence
		"task_timeout",          // the concrete remedy
		"faster model",          // the alternative remedy
		"context deadline exce", // the original cause survives
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message is missing %q:\n%s", want, msg)
		}
	}

	var warned bool
	for _, e := range events {
		if e.Level == stream.LevelWarn && strings.Contains(e.Message, "task_timeout") {
			warned = true
			if e.Phase != plan.RoleContext {
				t.Errorf("warning phase = %q, want the role", e.Phase)
			}
		}
	}
	if !warned {
		t.Error("no warning event was emitted for the timeout")
	}
}

// With no measurement yet the advice must say so instead of inventing a p95.
func TestTimeoutWarningWithoutMeasurement(t *testing.T) {
	o := newTimeoutFixture(t, "Qwen3.8-27B-4bit", 5*time.Minute)
	o.onEvent = func(Event) {}
	msg := o.roleTimeoutAdvice(plan.RoleExplorer, 5*time.Minute, 5*time.Minute)
	for _, want := range []string{"explorer", "no latency measured yet", "qwen3.8", "task_timeout", "faster model"} {
		if !strings.Contains(msg, want) {
			t.Errorf("advice is missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "p95 is") {
		t.Errorf("advice invented a p95 with no samples:\n%s", msg)
	}
}

// When the ceiling is what failed, the suggested task_timeout must actually be
// bigger than the one that just failed.
func TestSuggestedTaskTimeoutAlwaysExceedsTheCeiling(t *testing.T) {
	cases := []struct{ ceiling, elapsed, p95 time.Duration }{
		{5 * time.Minute, 5 * time.Minute, 4 * time.Minute},
		{time.Minute, time.Minute, 0},
		{12 * time.Minute, 12 * time.Minute, 11 * time.Minute},
		{30 * time.Second, 30 * time.Second, 29 * time.Second},
	}
	for _, tc := range cases {
		got := suggestedTaskTimeout(tc.ceiling, tc.elapsed, tc.p95)
		if got <= tc.ceiling {
			t.Errorf("suggestedTaskTimeout(%v, %v, %v) = %v, must exceed the ceiling",
				tc.ceiling, tc.elapsed, tc.p95, got)
		}
		if got%(30*time.Second) != 0 {
			t.Errorf("suggestion %v is not a readable 30s step", got)
		}
	}
}

// A measured (sub-ceiling) budget that fails gets the honest remedy: it widens
// itself, and raising task_timeout is the fallback.
func TestAdviceDistinguishesMeasuredBudgetFromCeiling(t *testing.T) {
	measured := roleTimeoutAdviceText(plan.RoleContext, "qwen3.8",
		90*time.Second, 91*time.Second, 5*time.Minute, 60*time.Second, 9)
	if !strings.Contains(measured, "widens it on the next run") {
		t.Errorf("sub-ceiling advice should say the budget self-corrects:\n%s", measured)
	}
	atCeiling := roleTimeoutAdviceText(plan.RoleContext, "qwen3.8",
		5*time.Minute, 5*time.Minute, 5*time.Minute, 4*time.Minute, 9)
	if !strings.Contains(atCeiling, "raise task_timeout") {
		t.Errorf("at-ceiling advice should say to raise task_timeout:\n%s", atCeiling)
	}
	// Both must name the role and stay a single readable line.
	for _, m := range []string{measured, atCeiling} {
		if !strings.HasPrefix(m, `role "`+plan.RoleContext+`" timed out`) {
			t.Errorf("advice does not lead with the role:\n%s", m)
		}
		if strings.Contains(m, "\n") {
			t.Errorf("advice must be one line:\n%s", m)
		}
	}
}

func TestRoleTimeoutAdviceHandlesUnknownModel(t *testing.T) {
	msg := roleTimeoutAdviceText("", "", time.Minute, time.Minute, time.Minute, 0, 0)
	if !strings.Contains(msg, `role "agent" timed out`) {
		t.Errorf("empty role should fall back to a generic name:\n%s", msg)
	}
	if !strings.Contains(msg, "this model") {
		t.Errorf("empty family should read naturally:\n%s", msg)
	}
}

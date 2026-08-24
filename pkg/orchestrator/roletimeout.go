package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Role timeout policy — measured, not fractional.
//
//	timeout(role) = clamp(p95(role, family) × safety, floor(class), ceiling)
//
//	ceiling = the user's task_timeout. A hard cap: the harness never spends
//	          longer on one role call than the user asked for.
//	floor   = a per-role-class minimum, so a fast model cannot drive a budget
//	          down to something absurd and start failing on its own variance.
//	safety  = 3/2. Integer arithmetic, so the result is exactly reproducible.
//
// What this replaces, and why. Role budgets used to be fixed fractions of the
// user's task_timeout: full/4 for the light roles, full/2 for planning, full
// for the heavy ones. A fraction of a user-chosen number encodes an assumption
// about how fast the model is, and that assumption does not survive the 1.2B →
// 30B range the harness supports. Measured live against Qwen3.8-27B-4bit on
// oMLX with task_timeout: 5m, the `context` role got 75s and failed on EVERY
// run with "context deadline exceeded", while `explorer` — same workspace,
// comparable work — was given the full 300s and finished in 128s. The run then
// continued with stale context instead of hard-failing, so the user got worse
// results and no signal why.
//
// A budget has to come from what the model actually needs. The one thing the
// fraction policy did buy — a stuck planner not stalling the whole task_timeout
// — is bought back properly here: once three observations exist, a planner that
// normally answers in 40s gets ~60s, not 12 minutes.
//
// Cold start: with no evidence the harness has no basis to be stingy, so every
// role gets the full budget. Being slow once is far cheaper than failing every
// run.
//
// Determinism: roleTimeoutFrom is a pure function of (role, ceiling, p95,
// samples). No wall clock, no map iteration, no floating point in the result.
const (
	// roleTimeoutSafetyNum/Den is the headroom over the measured p95.
	roleTimeoutSafetyNum = 3
	roleTimeoutSafetyDen = 2

	// Per-class floors. A measured budget is never taken below these.
	roleFloorHeavy    = 120 * time.Second // implementation, exploration, tests: tool-heavy, long
	roleFloorPlanning = 90 * time.Second  // structured JSON planning
	roleFloorLight    = 60 * time.Second  // short structured outputs
	roleFloorDefault  = 90 * time.Second
)

// Role classes for the floor lookup.
const (
	roleClassHeavy    = "heavy"
	roleClassPlanning = "planning"
	roleClassLight    = "light"
	roleClassDefault  = "default"
)

// roleClassOf buckets a role by the shape of the work it does. The buckets are
// the ones the old fraction policy used, so the floors stay recognizable.
func roleClassOf(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case plan.RoleWorker, "deep", plan.RoleCorrector, plan.RoleExplorer, "docs",
		plan.RoleTester, plan.RolePlaceholder:
		return roleClassHeavy
	case plan.RolePlanner, "splitter":
		return roleClassPlanning
	case plan.RoleReviewer, "coordinator", "architect", plan.RoleContext, "memory":
		return roleClassLight
	default:
		return roleClassDefault
	}
}

// roleTimeoutFloor is the smallest budget a role class may be given.
func roleTimeoutFloor(role string) time.Duration {
	switch roleClassOf(role) {
	case roleClassHeavy:
		return roleFloorHeavy
	case roleClassPlanning:
		return roleFloorPlanning
	case roleClassLight:
		return roleFloorLight
	default:
		return roleFloorDefault
	}
}

// roleTimeoutFrom is the whole policy, as a pure function.
//
// ceiling is the user's task_timeout, p95/samples come from latency memory for
// this role on this model family. Fewer than memory.MinLatencySamples
// observations counts as no evidence.
func roleTimeoutFrom(role string, ceiling, p95 time.Duration, samples int) time.Duration {
	if ceiling <= 0 {
		ceiling = config.DefaultTaskTimeout
	}
	// Cold start: no basis to be stingy. The full budget, for every role.
	//
	// backends.Probe (pkg/backends/capabilities.go) is the only latency-ish
	// signal available before the first role call, and it is deliberately not
	// used to seed this estimate. It issues max_tokens=1 requests, so what it
	// measures is connect + model load + prefill of a two-token prompt, while a
	// role call is a multi-turn tool-using generation of hundreds of tokens: on
	// the 27B oMLX build that produced this bug the probe answers in about a
	// second and the explorer role takes 128s — two orders of magnitude apart,
	// and not proportional. It also records no elapsed time today and is
	// memoised for a week, so capturing one would mean adding a real generation
	// to the startup path. A seed wrong by 100× is worse than no seed, because
	// it would be used with confidence.
	if samples < memory.MinLatencySamples || p95 <= 0 {
		return ceiling
	}
	if p95 >= ceiling {
		return ceiling
	}
	want := p95 / roleTimeoutSafetyDen * roleTimeoutSafetyNum
	floor := roleTimeoutFloor(role)
	if floor > ceiling {
		floor = ceiling
	}
	if want < floor {
		want = floor
	}
	if want > ceiling {
		want = ceiling
	}
	return want
}

// taskTimeoutCeiling is the user's task_timeout — the hard cap on every role.
func (o *Orchestrator) taskTimeoutCeiling() time.Duration {
	if o == nil || o.cfg == nil || o.cfg.TaskTimeout <= 0 {
		return config.DefaultTaskTimeout
	}
	return o.cfg.TaskTimeout
}

// modelFamily folds the configured model id to the family latency generalizes
// across ("Qwen3.8-27B-4bit" → "qwen3.8").
func (o *Orchestrator) modelFamily() string {
	if o == nil || o.cfg == nil {
		return ""
	}
	return memory.ModelFamily(o.cfg.Model)
}

// latencyStore returns cross-project latency memory, or nil when the evolve
// engine is off (--no-evolve). Every caller is nil-safe.
func (o *Orchestrator) latencyStore() *memory.Latencies {
	if o == nil || o.evolve == nil {
		return nil
	}
	mem := o.evolve.Memory()
	if mem == nil {
		return nil
	}
	return mem.Latency()
}

// observedRoleLatency returns the measured p95 for this role on the configured
// model family, and how many samples back it.
func (o *Orchestrator) observedRoleLatency(role string) (time.Duration, int) {
	st := o.latencyStore()
	if st == nil {
		return 0, 0
	}
	return st.P95(role, o.modelFamily())
}

// recordRoleLatency folds one role observation into the in-run tally and,
// when it is real evidence, into cross-project latency memory.
//
// What counts as evidence:
//
//   - a successful call: exactly how long this role needs. Recorded.
//   - a timed-out call: a censored lower bound — the role needed AT LEAST the
//     budget. Recorded, because that is what lets an under-measured budget
//     widen on the next run instead of failing forever.
//   - any other failure: a provider error that returned in two seconds says
//     nothing about how long the role needs, and recording it would drag the
//     quantile down and starve the next run. Not recorded.
func (o *Orchestrator) recordRoleLatency(role string, d time.Duration, evidence bool) {
	o.recordLatency(role, d)
	if !evidence || d <= 0 {
		return
	}
	st := o.latencyStore()
	if st == nil {
		return
	}
	st.Record(memory.LatencyKey{Role: role, ModelFamily: o.modelFamily()}, d)
}

// isTimeoutError reports whether err is a deadline/timeout failure. The string
// checks matter: the graph executor wraps the cause ("node execution failed:
// context deadline exceeded"), so errors.Is alone misses it.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"context deadline exceeded", "deadline exceeded", "timed out", "i/o timeout", "timeout"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// explainRoleTimeout turns a bare "context deadline exceeded" into something a
// user can act on: it emits the advice as a warning and wraps it into the
// returned error, so every surface that prints the error (the phase warning in
// Run, the loop's failure log, the studio stream) carries the remedy too.
func (o *Orchestrator) explainRoleTimeout(role, taskID string, budget, elapsed time.Duration, err error) error {
	advice := o.roleTimeoutAdvice(role, budget, elapsed)
	if advice == "" {
		return err
	}
	o.emitWarn(role, advice, taskID)
	if err == nil {
		return errors.New(advice)
	}
	return fmt.Errorf("%w — %s", err, advice)
}

// roleTimeoutAdvice binds the live numbers to the message.
func (o *Orchestrator) roleTimeoutAdvice(role string, budget, elapsed time.Duration) string {
	p95, samples := o.observedRoleLatency(role)
	return roleTimeoutAdviceText(role, o.modelFamily(), budget, elapsed,
		o.taskTimeoutCeiling(), p95, samples)
}

// roleTimeoutAdviceText renders the timeout advice. Pure, so the wording is
// testable: it must always name the role, the budget it blew, what has actually
// been measured for this model family, and a concrete remedy.
func roleTimeoutAdviceText(role, family string, budget, elapsed, ceiling, p95 time.Duration, samples int) string {
	if strings.TrimSpace(role) == "" {
		role = "agent"
	}
	if strings.TrimSpace(family) == "" {
		family = "this model"
	} else {
		family = "model family " + family
	}
	// The role is quoted because one of them is literally called "context",
	// and `context timed out` reads as Go's context package.
	var b strings.Builder
	fmt.Fprintf(&b, "role %q timed out after %s (budget %s of the %s task_timeout ceiling)",
		role, roundSec(elapsed), roundSec(budget), roundSec(ceiling))

	if samples >= memory.MinLatencySamples && p95 > 0 {
		fmt.Fprintf(&b, "; measured p95 for %s is %s over %d samples",
			family, roundSec(p95), samples)
	} else {
		fmt.Fprintf(&b, "; no latency measured yet for %s (%d/%d samples)",
			family, samples, memory.MinLatencySamples)
	}

	if budget >= ceiling {
		// The user's own setting is the binding constraint — say so, and say
		// what to set it to.
		fmt.Fprintf(&b, ". Remedy: raise task_timeout in .slmcode/config.yaml from %s to at least %s, or switch to a faster model",
			roundSec(ceiling), roundSec(suggestedTaskTimeout(ceiling, elapsed, p95)))
	} else {
		// The measured estimate was too low. Recording this timeout widens it,
		// so the honest first remedy is "it self-corrects".
		fmt.Fprintf(&b, ". Remedy: the budget is measured and this timeout widens it on the next run; if it keeps failing, raise task_timeout in .slmcode/config.yaml (now %s) or switch to a faster model",
			roundSec(ceiling))
	}
	return b.String()
}

// suggestedTaskTimeout is the value to tell the user to set: 1.5× the worst
// thing we have seen, rounded up to a readable 30s step, and always strictly
// more than the ceiling that just failed.
func suggestedTaskTimeout(ceiling, elapsed, p95 time.Duration) time.Duration {
	base := ceiling
	if elapsed > base {
		base = elapsed
	}
	if p95 > base {
		base = p95
	}
	want := roundUpTo(base/roleTimeoutSafetyDen*roleTimeoutSafetyNum, 30*time.Second)
	if want <= ceiling {
		want = roundUpTo(ceiling+ceiling/2, 30*time.Second)
	}
	return want
}

func roundUpTo(d, unit time.Duration) time.Duration {
	if unit <= 0 || d <= 0 {
		return d
	}
	if r := d % unit; r != 0 {
		d += unit - r
	}
	return d
}

// roundSec keeps durations readable in user-facing text.
func roundSec(d time.Duration) time.Duration { return d.Round(time.Second) }

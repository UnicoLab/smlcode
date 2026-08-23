package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// DefaultMaxTaskCalls is the per-task LLM call budget when MaxTaskCalls is 0.
//
// The worst case before this existed was ~16 calls for ONE task on defaults:
// worker (1) + recoverIncompleteFinalize (0-2) + self-critique (0-4, because
// `passes` was escalated to min(max(MaxRetries,3),4) whenever smoke/static/
// acceptance failed) + reviewAndCorrect (up to 5 reviewer + 4 corrector). At
// 30-60s per call on a local 30B that is 10-20 minutes for a single task.
//
// The value is DERIVED from MaxRetries rather than picked: the budget exists to
// stop runaway ladders, not to quietly overrule the retry setting the operator
// chose. One task's honest floor on the shipped defaults is
//
//	worker (1) + self-critique (1) + MaxRetries × (review + correct)
//
// which at the default MaxRetries=4 is 1 + 1 + 8 = 10. The old default of 6
// bought exactly TWO correction rounds no matter what MaxRetries said, so a
// legitimately hard task escalated to a human with half its retries unspent and
// nothing in the log connecting the two numbers. Keep this in step with
// config.Config.MaxRetries: MaxTaskCallsFor is the relationship.
const DefaultMaxTaskCalls = 10

// MaxTaskCallsFor returns the per-task budget that lets maxRetries correction
// rounds actually happen: worker + self-critique + maxRetries × (review +
// correct). Callers that let an operator raise max_retries should raise
// max_task_calls with it, or the budget silently caps the retries instead.
func MaxTaskCallsFor(maxRetries int) int {
	if maxRetries < 0 {
		maxRetries = 0
	}
	n := 2 + 2*maxRetries
	if n < DefaultMaxTaskCalls {
		return DefaultMaxTaskCalls
	}
	return n
}

// callBudget tracks LLM calls per task. Waves run tasks in parallel, so every
// method is safe for concurrent use.
type callBudget struct {
	mu   sync.Mutex
	max  int
	used map[string]int
	// requests counts REAL LLM round-trips, which is not the same number as
	// `used`. One speculative review costs a single budget unit but fans out to
	// the reviewer AND reviewer-strict whenever max_parallel >= 3 (the default),
	// so a "10-call budget" can be ~13 requests at a server that runs inference
	// serially. The budget bounds the correction LADDER; this counter is what
	// makes the wall-clock cost of that ladder visible instead of surprising.
	requests map[string]int
}

func newCallBudget(max int) *callBudget {
	if max <= 0 {
		max = DefaultMaxTaskCalls
	}
	return &callBudget{max: max, used: map[string]int{}, requests: map[string]int{}}
}

// note records n real LLM round-trips against a task without spending budget.
func (b *callBudget) note(taskID string, n int) {
	if b == nil || taskID == "" || n <= 0 {
		return
	}
	b.mu.Lock()
	if b.requests == nil {
		b.requests = map[string]int{}
	}
	b.requests[taskID] += n
	b.mu.Unlock()
}

// sentRequests reports how many real LLM round-trips a task has issued.
func (b *callBudget) sentRequests(taskID string) int {
	if b == nil || taskID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requests[taskID]
}

// reset clears a task's spend — called once when the task starts executing.
func (b *callBudget) reset(taskID string) {
	if b == nil || taskID == "" {
		return
	}
	b.mu.Lock()
	delete(b.used, taskID)
	delete(b.requests, taskID)
	b.mu.Unlock()
}

// take spends one call, reporting whether it was within budget.
func (b *callBudget) take(taskID string) bool {
	if b == nil || taskID == "" {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used[taskID] >= b.max {
		return false
	}
	b.used[taskID]++
	if b.requests == nil {
		b.requests = map[string]int{}
	}
	b.requests[taskID]++
	return true
}

// remaining reports how many calls a task may still make.
func (b *callBudget) remaining(taskID string) int {
	if b == nil || taskID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := b.max - b.used[taskID]; n > 0 {
		return n
	}
	return 0
}

// spent reports how many calls a task has made.
func (b *callBudget) spent(taskID string) int {
	if b == nil || taskID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used[taskID]
}

// budget lazily builds the runner's call budget.
func (r *Runner) budget() *callBudget {
	r.budgetOnce.Do(func() {
		r.callBudget = newCallBudget(r.MaxTaskCalls)
	})
	return r.callBudget
}

// startTask resets the per-task tool CallTracker bucket and call budget, and
// returns a ctx tagged with the task id so the tool layer's loop guard keeps a
// per-task history instead of one shared "" bucket.
func (r *Runner) startTask(ctx context.Context, taskID string) context.Context {
	if r == nil || taskID == "" {
		return ctx
	}
	r.budget().reset(taskID)
	if r.OnTaskStart != nil {
		r.OnTaskStart(taskID)
	}
	return workspace.WithTaskID(ctx, taskID)
}

// taskCtx tags ctx with the task id without resetting anything.
func (r *Runner) taskCtx(ctx context.Context, taskID string) context.Context {
	if r == nil || taskID == "" {
		return ctx
	}
	return workspace.WithTaskID(ctx, taskID)
}

// spend consumes one LLM call for a task. When the budget is exhausted it logs
// and emits an intervention event so the operator sees an escalation rather
// than silent looping, and returns false.
func (r *Runner) spend(taskID, what string) bool {
	if r == nil || taskID == "" {
		return true
	}
	if r.budget().take(taskID) {
		return true
	}
	// Report BOTH numbers. `used` is budget units; `llm_requests` is the real
	// round-trip count, which is higher whenever a speculative review fanned
	// out — and the round-trips, not the units, are what the operator waited on.
	r.logf("%s call budget exhausted (%d/%d units, %d LLM requests) — refusing %s; escalating instead of looping",
		taskID, r.budget().spent(taskID), r.budget().max, r.budget().sentRequests(taskID), what)
	r.fireIntervention(taskID, "call_budget",
		fmt.Sprintf("%s hit its %d-call budget — escalating instead of another %s round-trip",
			taskID, r.budget().max, what),
		fmt.Sprintf("max_task_calls=%d used=%d llm_requests=%d blocked=%s",
			r.budget().max, r.budget().spent(taskID), r.budget().sentRequests(taskID), what))
	return false
}

// noteExtraRequests records LLM round-trips a task issued beyond the budget
// units it spent — the extra speculative slots of a review race.
func (r *Runner) noteExtraRequests(taskID string, n int) {
	if r == nil {
		return
	}
	r.budget().note(taskID, n)
}

// budgetExhausted reports whether a task has no calls left.
func (r *Runner) budgetExhausted(taskID string) bool {
	return r != nil && taskID != "" && r.budget().remaining(taskID) == 0
}

// execOne runs a single subagent request under the task's ctx tag, after
// spending one unit of the task's call budget. It returns ok=false when the
// budget refused the call — callers must escalate, never retry.
func (r *Runner) execOne(ctx context.Context, taskID, what string, req ggagent.SubAgentRequest) (ggagent.SubAgentResult, bool) {
	if !r.spend(taskID, what) {
		return ggagent.SubAgentResult{}, false
	}
	if r.Executor == nil {
		return ggagent.SubAgentResult{Error: fmt.Errorf("nil executor")}, true
	}
	defer r.streamTokens(req.AgentID, taskID)()
	res, err := r.Executor.ExecuteSubAgents(r.taskCtx(ctx, taskID),
		[]ggagent.SubAgentRequest{req}, r.Shared)
	if len(res) == 0 {
		return ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: taskID, Error: err}, true
	}
	out := res[0]
	if out.Error == nil && err != nil && outputString(out) == "" {
		out.Error = err
	}
	r.noteUsage(out, req.Input, outputString(out))
	return out, true
}

// resolveRole maps a slot role id through ResolveRole (identity when nil).
func (r *Runner) resolveRole(role string) string {
	if r == nil || r.ResolveRole == nil {
		return role
	}
	if mapped := strings.TrimSpace(r.ResolveRole(role)); mapped != "" {
		return mapped
	}
	return role
}

// resolveBuiltinSlot resolves a role the LOOP ITSELF hardcodes (the speculative
// review race's "reviewer-strict") and asserts loudly when the result is not a
// registered agent. "reviewer-strict" was unregistered for as long as
// speculate.go referenced it, so that whole path silently returned
// "subagent 'reviewer-strict' not found" and never once ran.
//
// Only built-in slot names go through here: pipeline/block-defined roles such
// as go-worker are legitimately absent from agents.BuiltinIDs.
func (r *Runner) resolveBuiltinSlot(role string) (string, bool) {
	out := r.resolveRole(role)
	if out == "" {
		return "", false
	}
	if agents.IsKnownRole(out) {
		return out, true
	}
	r.logf("WARNING: slot role %q resolves to %q which is not a registered agent — skipping that speculative path", role, out)
	r.fireLevel(stream.KindDebug, "harness", "",
		fmt.Sprintf("unknown agent role %q (from slot %q)", out, role), "", "", stream.LevelWarn)
	return out, false
}

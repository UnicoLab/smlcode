package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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

// agentCtx is taskCtx plus the role that is about to run, so the workspace
// write guard can state THAT role's contract when it refuses a write. Without
// it an explorer's blocked ws_edit reads as an edit-syntax problem and the
// model spends its whole budget rewording the call.
func (r *Runner) agentCtx(ctx context.Context, taskID, role string) context.Context {
	return workspace.WithRole(r.taskCtx(ctx, taskID), role)
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

// budgetExhausted reports whether a task has no calls left.
func (r *Runner) budgetExhausted(taskID string) bool {
	return r != nil && taskID != "" && r.budget().remaining(taskID) == 0
}

// roleBudget is the measured budget for one request of role, ignoring the
// run's remaining runway: min(Timeout, RoleTimeout(base role)). It is for a
// site with no ctx to hand; requestTimeout is the ctx-aware version every
// dispatch should use.
func (r *Runner) roleBudget(role string) time.Duration {
	if r == nil {
		return 0
	}
	budget := r.Timeout
	if r.RoleTimeout == nil {
		return budget
	}
	base, _ := agents.BaseRoleID(role)
	if measured := r.RoleTimeout(nil, base); measured > 0 && (budget <= 0 || measured < budget) {
		return measured
	}
	return budget
}

// requestTimeout is the budget for one request of role right now:
// callTimeout (the flat Timeout clamped to the runway) capped by the measured
// per-role budget. The measured value can only TIGHTEN the flat one — the
// orchestrator's policy already floors it per role class and ceilings it at
// task_timeout, so nothing here can hand out more than Timeout.
func (r *Runner) requestTimeout(ctx context.Context, role string) time.Duration {
	if r == nil {
		return 0
	}
	budget := r.callTimeout(ctx)
	if r.RoleTimeout == nil {
		return budget
	}
	base, _ := agents.BaseRoleID(role)
	if measured := r.RoleTimeout(ctx, base); measured > 0 && (budget <= 0 || measured < budget) {
		return measured
	}
	return budget
}

// applyRoleTimeout tightens req.Timeout to the measured budget for its role.
// A request that arrived with no timeout gets the measured one; one that
// arrived with a larger budget is cut down; a smaller one is left alone.
func (r *Runner) applyRoleTimeout(ctx context.Context, req *ggagent.SubAgentRequest) {
	if r == nil || req == nil {
		return
	}
	want := r.requestTimeout(ctx, req.AgentID)
	if want <= 0 {
		return
	}
	if req.Timeout <= 0 || want < req.Timeout {
		req.Timeout = want
	}
}

// noteRoleLatency reports one request's duration to OnRoleLatency under the
// BASE role (an escalation rung is the same role on a bigger model, and the
// budget is looked up under the base too). Evidence follows the orchestrator's
// rule: a success measures the role, a timeout is a censored lower bound worth
// keeping, any other failure says nothing about how long the role needs.
func (r *Runner) noteRoleLatency(role string, d time.Duration, err error, res ggagent.SubAgentResult) {
	if r == nil || r.OnRoleLatency == nil || d <= 0 {
		return
	}
	base, _ := agents.BaseRoleID(role)
	failed := err != nil || res.Error != nil
	r.OnRoleLatency(base, d, !failed || isTimeoutResult(err, res))
}

// execOne runs a single subagent request under the task's ctx tag, after
// spending one unit of the task's call budget. It returns ok=false when the
// budget refused the call — callers must escalate, never retry.
func (r *Runner) execOne(ctx context.Context, taskID, what string, req ggagent.SubAgentRequest) (ggagent.SubAgentResult, bool) {
	// Out of runway: refuse the call rather than spend the finish reserve on it.
	//
	// The wave loop already stops admitting NEW waves at this point, and that is
	// not sufficient — a worker that stalls right up to the reserve boundary is
	// still followed, inside the same wave, by a review and possibly a
	// correction. Each of those got a floor-sized budget and together they ate
	// the reserve the stall had just spared. execOne is the one choke point
	// every loop-side dispatch passes through, so the rule belongs here.
	//
	// ok=false is the same answer the call budget gives, and callers already
	// handle it correctly: escalate, never retry.
	if r.runwaySpent(ctx) {
		return ggagent.SubAgentResult{}, false
	}
	if !r.spend(taskID, what) {
		return ggagent.SubAgentResult{}, false
	}
	if r.Executor == nil {
		return ggagent.SubAgentResult{Error: fmt.Errorf("nil executor")}, true
	}
	defer r.streamTokens(req.AgentID, taskID)()
	// The measured per-role budget, not the flat task ceiling: reviewers and
	// correctors own most of a run's wall clock and used to be the only roles
	// that never contributed a latency sample nor received a measured budget.
	r.applyRoleTimeout(ctx, &req)
	start := time.Now()
	res, err := r.Executor.ExecuteSubAgents(r.agentCtx(ctx, taskID, req.AgentID),
		[]ggagent.SubAgentRequest{req}, r.Shared)
	elapsed := time.Since(start)
	if len(res) == 0 {
		r.noteRoleLatency(req.AgentID, elapsed, err, ggagent.SubAgentResult{Error: err})
		return ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: taskID, Error: err}, true
	}
	out := res[0]
	r.noteRoleLatency(req.AgentID, elapsed, err, out)
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

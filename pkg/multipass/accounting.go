package multipass

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// Pass names reported through OnCall.
const (
	PassDraft    = "draft"
	PassCritique = "critique"
	PassRefine   = "refine"
)

// CallInfo describes one LLM round-trip inside a multi-pass run. Runner.Execute
// issues up to 1 + 2×Passes of them for the two slowest roles in the harness
// (planner and splitter), so the orchestrator needs to see each one to budget.
type CallInfo struct {
	// Role is the specialist id when the runner created the agent itself
	// (ExecuteRole); empty when the caller supplied the agent.
	Role string
	// Pass is draft, critique, or refine.
	Pass string
	// Index is the 1-based refine iteration (0 for the draft).
	Index int
	// InputChars / OutputChars approximate the cost of the call without
	// requiring token accounting from the provider.
	InputChars  int
	OutputChars int
	Elapsed     time.Duration
	Err         error
}

// Usage is the aggregate of every call in one Execute.
type Usage struct {
	Role        string
	Calls       int
	InputChars  int
	OutputChars int
	Elapsed     time.Duration
	// EarlyExit reports that the draft already looked like complete structured
	// JSON, so the critique/refine passes were skipped.
	EarlyExit bool
	// TimedOut reports that a pass or the whole budget ran out; the best answer
	// so far is still returned.
	TimedOut bool
}

// AgentFactory creates a specialist agent for a role id.
type AgentFactory func(role string) (agent.Agent, error)

// SetPassTimeout bounds each individual pass. Zero means "no per-pass bound".
func (r *Runner) SetPassTimeout(d time.Duration) *Runner {
	r.PassTimeout = d
	return r
}

// SetBudget bounds the whole multi-pass run across all passes. When the budget
// runs out mid-run the best answer so far is returned instead of an error.
func (r *Runner) SetBudget(d time.Duration) *Runner {
	r.Budget = d
	return r
}

// SetFactory enables ExecuteRole and agent reuse across runs.
func (r *Runner) SetFactory(f AgentFactory) *Runner {
	r.Factory = f
	return r
}

// ExecuteRole runs the multi-pass cycle for a role, reusing the agent it built
// for that role on an earlier call. Building an agent means re-resolving the
// provider, tools and profile caps, which is pure overhead when the same role
// is invoked repeatedly.
//
// The agent's conversation is cleared before each run, so reuse never leaks the
// previous task's messages into this one.
func (r *Runner) ExecuteRole(ctx context.Context, role, input string) (string, error) {
	a, err := r.agentFor(role)
	if err != nil {
		return "", err
	}
	return r.execute(ctx, role, a, input)
}

func (r *Runner) agentFor(role string) (agent.Agent, error) {
	if r.Factory == nil {
		return nil, fmt.Errorf("multipass: no agent factory configured (call SetFactory)")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = map[string]agent.Agent{}
	}
	if a, ok := r.agents[role]; ok && a != nil && !a.IsRunning() {
		a.ClearConversation()
		return a, nil
	}
	a, err := r.Factory(role)
	if err != nil {
		return nil, err
	}
	r.agents[role] = a
	return a, nil
}

// ResetAgents drops every cached agent (call when the provider or model roster
// changes under the runner).
func (r *Runner) ResetAgents() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = nil
}

// call runs one pass under the per-pass timeout and reports it to OnCall.
func (r *Runner) call(ctx context.Context, acc *Usage, a agent.Agent, info CallInfo, input string) (string, error) {
	if err := ctx.Err(); err != nil {
		acc.TimedOut = true
		return "", err
	}
	cctx := ctx
	var cancel context.CancelFunc
	if r.PassTimeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, r.PassTimeout)
		defer cancel()
	}
	start := time.Now()
	exec, err := a.Execute(cctx, input)
	out := ""
	if exec != nil {
		out = asString(exec.Output)
	}
	info.InputChars = len(input)
	info.OutputChars = len(out)
	info.Elapsed = time.Since(start)
	info.Err = err

	acc.Calls++
	acc.InputChars += info.InputChars
	acc.OutputChars += info.OutputChars
	acc.Elapsed += info.Elapsed
	if err != nil && (cctx.Err() != nil || ctx.Err() != nil) {
		acc.TimedOut = true
	}
	if r.OnCall != nil {
		r.OnCall(info)
	}
	return out, err
}

func (r *Runner) reportUsage(u Usage) {
	if r.OnUsage != nil {
		r.OnUsage(u)
	}
}

// budgetCtx applies the whole-run budget on top of the caller's context.
func (r *Runner) budgetCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.Budget <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, r.Budget)
}

// mu / agents live here so think.go stays focused on the pass logic.
type runnerState struct {
	mu     sync.Mutex
	agents map[string]agent.Agent
}

package loop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// budgetTimeoutExec records the timeout each request carries and answers from a
// script, so the clamp execOne applies is observable at the executor.
type budgetTimeoutExec struct {
	mu   sync.Mutex
	reqs []ggagent.SubAgentRequest
	err  error
}

func (e *budgetTimeoutExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, reqs...)
	e.mu.Unlock()
	time.Sleep(time.Millisecond)
	if e.err != nil {
		return []ggagent.SubAgentResult{{AgentID: reqs[0].AgentID, Error: e.err}}, e.err
	}
	return []ggagent.SubAgentResult{{AgentID: reqs[0].AgentID, TaskID: reqs[0].TaskID, Output: "ok"}}, nil
}

func (e *budgetTimeoutExec) last() ggagent.SubAgentRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reqs[len(e.reqs)-1]
}

type latencyNote struct {
	role     string
	d        time.Duration
	evidence bool
}

// execOne dispatches on min(callTimeout, RoleTimeout(base role)) and reports
// every request's duration under the base role with the orchestrator's
// evidence rule.
func TestExecOneUsesTheMeasuredRoleBudget(t *testing.T) {
	exec := &budgetTimeoutExec{}
	var mu sync.Mutex
	var notes []latencyNote
	r := &Runner{
		Timeout:  8 * time.Minute,
		Executor: exec,
		RoleTimeout: func(_ context.Context, role string) time.Duration {
			if role == plan.RoleReviewer {
				return time.Minute
			}
			return 0 // no measurement: keep the flat budget
		},
		OnRoleLatency: func(role string, d time.Duration, evidence bool) {
			mu.Lock()
			notes = append(notes, latencyNote{role, d, evidence})
			mu.Unlock()
		},
	}
	ctx := context.Background()

	// A measured role is tightened to its budget.
	if _, ok := r.execOne(ctx, "T1", "review", ggagent.SubAgentRequest{
		AgentID: plan.RoleReviewer, Timeout: r.callTimeout(ctx)}); !ok {
		t.Fatal("execOne refused the call")
	}
	if got := exec.last().Timeout; got != time.Minute {
		t.Fatalf("reviewer dispatched with %v, want the measured 1m", got)
	}

	// An unmeasured role keeps the flat budget; an escalation rung is looked
	// up and recorded under its BASE role.
	escalated := agents.EscalatedRoleID(plan.RoleWorker, 2)
	if _, ok := r.execOne(ctx, "T1", "worker", ggagent.SubAgentRequest{
		AgentID: escalated, Timeout: r.callTimeout(ctx)}); !ok {
		t.Fatal("execOne refused the call")
	}
	if got := exec.last().Timeout; got != 8*time.Minute {
		t.Fatalf("unmeasured worker dispatched with %v, want the flat 8m", got)
	}

	// A request that arrived tighter than the measurement stays tight.
	if _, ok := r.execOne(ctx, "T1", "review", ggagent.SubAgentRequest{
		AgentID: plan.RoleReviewer, Timeout: 10 * time.Second}); !ok {
		t.Fatal("execOne refused the call")
	}
	if got := exec.last().Timeout; got != 10*time.Second {
		t.Fatalf("a tighter caller budget was widened to %v", got)
	}

	// The ctx-less variant used by reviewSlots agrees.
	if got := r.roleBudget(plan.RoleReviewer); got != time.Minute {
		t.Fatalf("roleBudget(reviewer) = %v, want 1m", got)
	}
	if got := r.roleBudget(escalated); got != 8*time.Minute {
		t.Fatalf("roleBudget(escalated worker) = %v, want 8m", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 3 {
		t.Fatalf("recorded %d latency samples for 3 requests: %+v", len(notes), notes)
	}
	if notes[1].role != plan.RoleWorker {
		t.Fatalf("escalated request recorded under %q, want the base role %q", notes[1].role, plan.RoleWorker)
	}
	for _, n := range notes {
		if !n.evidence || n.d <= 0 {
			t.Fatalf("a successful call is evidence with a positive duration: %+v", n)
		}
	}
}

// A provider error that returns in two seconds says nothing about how long the
// role needs: recorded as non-evidence, exactly as the phase roles do.
func TestExecOneDoesNotCountAProviderErrorAsLatencyEvidence(t *testing.T) {
	exec := &budgetTimeoutExec{err: errors.New("chat failed: 502")}
	var got []latencyNote
	r := &Runner{
		Timeout:  time.Minute,
		Executor: exec,
		OnRoleLatency: func(role string, d time.Duration, evidence bool) {
			got = append(got, latencyNote{role, d, evidence})
		},
	}
	r.execOne(context.Background(), "T1", "worker", ggagent.SubAgentRequest{AgentID: plan.RoleWorker})
	if len(got) != 1 || got[0].evidence {
		t.Fatalf("a provider error was recorded as evidence: %+v", got)
	}
	// Without the hooks nothing changes: the request keeps what it came with.
	bare := &Runner{Timeout: time.Minute, Executor: &budgetTimeoutExec{}}
	bare.execOne(context.Background(), "T1", "worker", ggagent.SubAgentRequest{AgentID: plan.RoleWorker, Timeout: 30 * time.Second})
	if got := bare.Executor.(*budgetTimeoutExec).last().Timeout; got != 30*time.Second {
		t.Fatalf("with no RoleTimeout the request timeout changed to %v", got)
	}
}

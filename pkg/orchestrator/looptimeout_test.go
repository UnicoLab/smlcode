package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// budgetExec records the timeout every request arrives with, writes the
// worker's file so the loop's evidence gates let the task finish, and takes a
// measurable moment per call so the latency it produces is a real sample.
type budgetExec struct {
	root string
	mu   sync.Mutex
	reqs []ggagent.SubAgentRequest
}

func (e *budgetExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, reqs...)
	e.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		reply := `{"approved":true,"score":90,"summary":"looks good","issues":[]}`
		if !strings.Contains(req.AgentID, "review") {
			name := strings.ToLower(req.TaskID) + ".go"
			_ = os.WriteFile(filepath.Join(e.root, name), []byte("package main\n"), 0o600)
			reply = "Observation: ws_edit edited " + name + " (1 replacement(s))\n" +
				`{"status":"done","summary":"done","files_changed":["` + name + `"]}`
		}
		out = append(out, ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: reply})
	}
	return out, nil
}

func (e *budgetExec) timeoutsFor(role string) []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []time.Duration
	for _, r := range e.reqs {
		if r.AgentID == role {
			out = append(out, r.Timeout)
		}
	}
	return out
}

// The execute loop's workers, reviewers and correctors own most of a run's
// wall clock, and they used to run on the flat task_timeout while contributing
// no latency samples at all. buildRunner now hands the loop the same measured
// policy the phase roles use, and the loop reports back.
func TestLoopRolesUseMeasuredBudgetsAndRecordLatency(t *testing.T) {
	const ceiling = 12 * time.Minute
	o := newTimeoutFixture(t, "qwen2.5-coder:14b", ceiling)
	o.cfg.PostWorkerSmoke = false
	o.cfg.RequireSmoke = false
	o.cfg.StaticQuality = false
	o.cfg.ClaimsGate = false
	o.cfg.WorkerCritique = false
	o.cfg.MaxRetries = 0
	o.cfg.SetMaxParallel(1)
	o.boardStore = plan.NewLiveStore(o.cfg.SlmDir())
	o.shared = ggagent.NewSharedState()
	exec := &budgetExec{root: o.cfg.Root}
	o.executor = exec
	o.buildPackers(nil, 32768)

	// Three reviewer observations at 20s: p95 × 1.5 = 30s, floored to the
	// light-role floor of 60s. The worker has no samples and keeps the ceiling.
	o.seedLatency(t, plan.RoleReviewer, 3, 20*time.Second)

	r := o.buildRunner("implement the thing", "run-budget", "")
	r.IdleWait = time.Millisecond
	r.Log = func(string, ...interface{}) {}
	if r.RoleTimeout == nil || r.OnRoleLatency == nil {
		t.Fatal("buildRunner did not wire RoleTimeout / OnRoleLatency")
	}
	ctx := context.Background()
	if got := r.RoleTimeout(ctx, plan.RoleReviewer); got != roleFloorLight {
		t.Fatalf("reviewer budget = %v, want the %v floor rather than the %v task_timeout", got, roleFloorLight, ceiling)
	}
	if got := r.RoleTimeout(ctx, plan.RoleWorker); got != ceiling {
		t.Fatalf("cold worker budget = %v, want the full %v", got, ceiling)
	}

	st := o.latencyStore()
	if _, before := st.P95(plan.RoleWorker, o.modelFamily()); before != 0 {
		t.Fatalf("worker already has %d samples before the wave", before)
	}

	board := &plan.Board{QueryID: "run-budget", Query: "implement the thing", Tasks: []plan.Task{{
		ID: "T1", Title: "build t1", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "write t1.go", Acceptance: "file written", Files: []string{"t1.go"},
	}}}
	if err := o.boardStore.Replace(*board); err != nil {
		t.Fatal(err)
	}
	if err := r.RunBoard(ctx, board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	if _, after := st.P95(plan.RoleWorker, o.modelFamily()); after < 1 {
		t.Fatalf("the wave recorded %d worker samples — the loop is still not feeding latency memory", after)
	}
	// Every reviewer request the wave issued was dispatched on the measured
	// budget, never on the task_timeout.
	for _, to := range exec.timeoutsFor(plan.RoleReviewer) {
		if to != roleFloorLight {
			t.Fatalf("a reviewer request went out with timeout %v, want the measured %v", to, roleFloorLight)
		}
	}
	for _, to := range exec.timeoutsFor(plan.RoleWorker) {
		if to != ceiling {
			t.Fatalf("a cold worker request went out with timeout %v, want the %v ceiling", to, ceiling)
		}
	}
}

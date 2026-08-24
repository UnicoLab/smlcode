package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// ladderExec drives one full review→correct ladder: the reviewer rejects every
// round with a distinct, quotable reason, and the corrector answers with a
// finalize whose `summary` states the approach it took. That is exactly the
// shape the attempt store has to capture — hypothesis on one side, the reason
// it was refused on the other.
type ladderExec struct {
	mu sync.Mutex
	// reasons is the reviewer's issue for round i (the last one repeats).
	reasons []string
	// approaches is the corrector's stated approach for round i.
	approaches []string

	reviews    int
	correctors int
	// prompts records every corrector prompt, in order.
	prompts []string
}

func (e *ladderExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		switch {
		case strings.Contains(req.AgentID, plan.RoleReviewer):
			reason := pick(e.reasons, e.reviews)
			e.reviews++
			payload, _ := json.Marshal(map[string]any{
				"approved": false,
				"score":    12,
				"issues":   []string{reason},
				"summary":  "rejected: " + reason,
			})
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: string(payload)}
		case strings.Contains(req.AgentID, plan.RoleCorrector):
			e.prompts = append(e.prompts, req.Input)
			approach := pick(e.approaches, e.correctors)
			e.correctors++
			payload, _ := json.Marshal(map[string]any{
				"status":        "done",
				"summary":       approach,
				"files_changed": []string{"a.go"},
			})
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: string(payload)}
		default:
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
				Output: `{"status":"done","summary":"worker pass","files_changed":["a.go"]}`}
		}
	}
	return out, nil
}

func (e *ladderExec) correctorPrompts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.prompts...)
}

func pick(list []string, i int) string {
	if len(list) == 0 {
		return "unspecified"
	}
	if i >= len(list) {
		return list[len(list)-1]
	}
	return list[i]
}

// ladderRunner wires a Runner whose review path always reaches the reviewer
// LLM: no disk evidence, no smoke, no static gate, single-threaded review.
func ladderRunner(t *testing.T, exec SubAgentRunner, maxRetries int) *Runner {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	r := NewRunner(exec, ggagent.NewSharedState())
	r.Root = root
	r.TurnID = "run-lineage"
	r.MaxRetries = maxRetries
	r.MaxParallel = 1
	r.ReviewParallel = false
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.Timeout = time.Minute
	r.Log = func(string, ...interface{}) {}
	return r
}

func ladderTask() plan.Task {
	return plan.Task{
		ID: "T1", Title: "fix a.go", Role: plan.RoleWorker,
		Description: "make a.go correct", Acceptance: "a.go compiles",
		Files: []string{"a.go"}, Column: plan.ColInReview,
		Output: `{"status":"done","summary":"first pass: renamed the helper","files_changed":["a.go"]}`,
	}
}

func openStoredAttempts(t *testing.T, root string) *plan.Attempts {
	t.Helper()
	s, err := plan.OpenAttempts(root)
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	return s
}

// TestRejectedApproachesReachTheCorrectorPrompt is THE regression guard for
// attempt lineage.
//
// The whole stage exists because a small model will happily re-propose an
// approach the reviewer already refused: the harness overwrote Task.Output on
// every corrector pass, kept the ledger in a map that died with the process,
// and sent the next prompt six truncated prose lines that named a number rather
// than an approach. If this test fails, the corrector has stopped being told
// what was already tried and why it was refused, and the loop is free to churn
// on the same idea until it escalates.
func TestRejectedApproachesReachTheCorrectorPrompt(t *testing.T) {
	exec := &ladderExec{
		reasons: []string{
			"stub/placeholder code detected — replace with real implementation",
			"old_str not found in a.go — the anchor is stale",
			"deterministic smoke failed",
		},
		approaches: []string{
			"regenerate a.go wholesale from the description",
			"patch a.go with a one-line anchor",
		},
	}
	r := ladderRunner(t, exec, 2)
	board := &plan.Board{Tasks: []plan.Task{ladderTask()}}
	if err := r.reviewAndCorrect(context.Background(), board, ladderTask(), nil); err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}

	prompts := exec.correctorPrompts()
	if len(prompts) < 2 {
		t.Fatalf("expected at least 2 corrector rounds, got %d", len(prompts))
	}
	last := prompts[len(prompts)-1]

	if !strings.Contains(last, "Approaches already tried at THIS task and REJECTED") {
		t.Fatalf("corrector prompt has no rejected-approach block:\n%s", last)
	}
	// The approach the FIRST attempt took, stated in its own finalize summary…
	if !strings.Contains(last, "first pass: renamed the helper") {
		t.Fatalf("corrector was not told the first approach:\n%s", last)
	}
	// …and the reason that approach was refused.
	if !strings.Contains(last, "stub/placeholder code detected") {
		t.Fatalf("corrector was not told WHY the first approach was refused:\n%s", last)
	}
	// The second round's approach and its own distinct reason too.
	if !strings.Contains(last, "regenerate a.go wholesale") {
		t.Fatalf("corrector was not told the second approach:\n%s", last)
	}
	if !strings.Contains(last, "old_str not found in a.go") {
		t.Fatalf("corrector was not told why the second approach was refused:\n%s", last)
	}
	// The first corrector prompt could only know about attempt 1.
	if strings.Contains(prompts[0], "old_str not found in a.go") {
		t.Fatalf("first corrector prompt leaked a later rejection:\n%s", prompts[0])
	}
}

func TestAttemptParentPointersAcrossCorrectorRounds(t *testing.T) {
	exec := &ladderExec{
		reasons: []string{
			"old_str not found in a.go", // classifies as edit_not_found
			"reason two",                // classifies as nothing
			"reason three",
		},
		approaches: []string{"approach one", "approach two"},
	}
	r := ladderRunner(t, exec, 2)
	board := &plan.Board{Tasks: []plan.Task{ladderTask()}}
	if err := r.reviewAndCorrect(context.Background(), board, ladderTask(), nil); err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}

	chain := openStoredAttempts(t, r.Root).Lineage("T1")
	if len(chain) != 3 {
		t.Fatalf("persisted %d attempts, want 3 (MaxRetries=2): %+v", len(chain), chain)
	}
	if chain[0].ParentID != "" {
		t.Fatalf("first attempt has a parent: %q", chain[0].ParentID)
	}
	for i, a := range chain {
		if a.N != i+1 {
			t.Fatalf("attempt %d numbered %d", i, a.N)
		}
		if a.RunID != "run-lineage" {
			t.Fatalf("attempt %s has run id %q", a.ID, a.RunID)
		}
		if i > 0 && a.ParentID != chain[i-1].ID {
			t.Fatalf("attempt %d parent = %q, want %q", a.N, a.ParentID, chain[i-1].ID)
		}
	}
	// Each attempt kept ITS OWN output — the corrector overwrite used to destroy
	// the intermediate one.
	if !strings.Contains(chain[0].Output, "first pass: renamed the helper") {
		t.Fatalf("attempt 1 output was overwritten: %q", chain[0].Output)
	}
	if !strings.Contains(chain[1].Output, "approach one") {
		t.Fatalf("attempt 2 output = %q", chain[1].Output)
	}
	if chain[0].Hypothesis != "first pass: renamed the helper" {
		t.Fatalf("attempt 1 hypothesis = %q", chain[0].Hypothesis)
	}
	if chain[1].Hypothesis != "approach one" {
		t.Fatalf("attempt 2 hypothesis = %q", chain[1].Hypothesis)
	}
	if len(chain[0].Issues) == 0 || chain[0].Issues[0] != "old_str not found in a.go" {
		t.Fatalf("attempt 1 issues = %v", chain[0].Issues)
	}
	// The evolve fingerprint taxonomy is reused rather than reinvented…
	if chain[0].FailureClass != string(evolve.ClassEditNotFound) {
		t.Fatalf("attempt 1 failure class = %q, want %q",
			chain[0].FailureClass, evolve.ClassEditNotFound)
	}
	// …and a failure it cannot place records no class at all, rather than the
	// word "unknown".
	if chain[1].FailureClass != "" {
		t.Fatalf("attempt 2 failure class = %q, want empty", chain[1].FailureClass)
	}
}

func TestAttemptPersistedWhenTaskEscalates(t *testing.T) {
	exec := &ladderExec{reasons: []string{"cannot verify the change"}}
	r := ladderRunner(t, exec, 0) // one review, no correction rounds → escalate
	board := &plan.Board{Tasks: []plan.Task{ladderTask()}}
	if err := r.reviewAndCorrect(context.Background(), board, ladderTask(), nil); err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}

	got, ok := board.Get("T1")
	if !ok {
		t.Fatal("task missing from board")
	}
	if got.Column != plan.ColToScope {
		t.Fatalf("task column = %s, want %s (escalated)", got.Column, plan.ColToScope)
	}

	chain := openStoredAttempts(t, r.Root).Lineage("T1")
	if len(chain) != 1 {
		t.Fatalf("persisted %d attempts, want 1: %+v", len(chain), chain)
	}
	a := chain[0]
	if a.Verdict != plan.AttemptEscalated {
		t.Fatalf("verdict = %q, want %q", a.Verdict, plan.AttemptEscalated)
	}
	if !a.Rejected() {
		t.Fatalf("escalated attempt does not read as rejected: %+v", a)
	}
	if a.Reason() != "cannot verify the change" {
		t.Fatalf("reason = %q", a.Reason())
	}
	if len(a.FilesTouched) == 0 {
		t.Fatalf("escalated attempt recorded no files: %+v", a)
	}
}

func TestAttemptGraphEdgesAreTraversable(t *testing.T) {
	exec := &ladderExec{
		reasons:    []string{"reason one", "reason two"},
		approaches: []string{"approach one"},
	}
	r := ladderRunner(t, exec, 1)
	board := &plan.Board{Tasks: []plan.Task{ladderTask()}}
	if err := r.reviewAndCorrect(context.Background(), board, ladderTask(), nil); err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}

	g, err := graph.Open(r.Root)
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	defer func() { _ = g.Close() }()

	task := graph.TaskNode("run-lineage", "T1")
	first := graph.AttemptNode("run-lineage", "T1", 1)
	second := graph.AttemptNode("run-lineage", "T1", 2)

	produced := g.Neighbors(task, graph.Outgoing, graph.Produced)
	if len(produced) != 2 {
		t.Fatalf("task produced %v, want both attempts", produced)
	}
	if !g.Has(task, first, graph.Produced) {
		t.Fatalf("missing task -produced-> attempt 1")
	}
	if !g.Has(first, second, graph.ParentOf) {
		t.Fatalf("missing attempt 1 -parent_of-> attempt 2")
	}
	if !g.Has(first, graph.FileNode("a.go"), graph.Touched) {
		t.Fatalf("missing attempt 1 -touched-> file a.go")
	}
	// And the edges are walkable in the other direction too.
	back := g.Neighbors(second, graph.Incoming, graph.ParentOf)
	if len(back) != 1 || back[0] != first {
		t.Fatalf("attempt 2 parents = %v, want [%s]", back, first)
	}
}

func TestAttemptSectionRespectsPromptByteBudget(t *testing.T) {
	r := ladderRunner(t, &ladderExec{}, 0)
	store := r.attemptStore()
	if store == nil {
		t.Fatal("attempt store unavailable")
	}
	parent := ""
	for i := 1; i <= 40; i++ {
		a, err := store.Append(plan.Attempt{
			RunID: r.attemptRunID(), TaskID: "T1", N: i, ParentID: parent,
			Verdict:    plan.AttemptRejected,
			Hypothesis: strings.Repeat("a very long hypothesis ", 40) + fmt.Sprint(i),
			Issues:     []string{strings.Repeat("a very long reason ", 40) + fmt.Sprint(i)},
			At:         time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		parent = a.ID
	}

	section := r.rejectedApproachSection(plan.Task{ID: "T1"})
	if section == "" {
		t.Fatal("no rejected-approach section rendered")
	}
	if len(section) > maxAttemptSectionBytes {
		t.Fatalf("section is %d bytes, budget is %d", len(section), maxAttemptSectionBytes)
	}
	// The whole prompt block, prose ledger included, stays bounded.
	full := r.attemptLogSection(plan.Task{
		ID: "T1", GateRetries: 2,
		AttemptLog: []string{"attempt 1 failed because X", "attempt 2 failed because Y"},
	})
	if len(full) > maxAttemptSectionBytes+800 {
		t.Fatalf("combined attempt block is %d bytes", len(full))
	}
	if !strings.Contains(full, "attempt 1 failed because X") {
		t.Fatalf("the prose AttemptLog ledger was dropped:\n%s", full)
	}
	if !strings.Contains(full, "reopened 2 time(s)") {
		t.Fatalf("the prose ledger lost its gate-retry count:\n%s", full)
	}
}

func TestAttemptLogSectionEmptyWithoutHistory(t *testing.T) {
	r := ladderRunner(t, &ladderExec{}, 0)
	if got := r.attemptLogSection(plan.Task{ID: "T1"}); got != "" {
		t.Fatalf("section rendered with no history: %q", got)
	}
	// A runner with no root at all must not panic or fabricate a section.
	bare := &Runner{Log: func(string, ...interface{}) {}}
	if got := bare.attemptLogSection(plan.Task{ID: "T1"}); got != "" {
		t.Fatalf("rootless runner rendered %q", got)
	}
}

func TestGateSignalsAreStableAndNamed(t *testing.T) {
	g := gateState{staticFail: true, smokeFail: true, diskWrite: true, scopeWhy: "outside focus"}
	got := g.signals()
	want := []string{"static_failed", "smoke_failed", "disk_write_evidence", "scope_violation"}
	if len(got) != len(want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signals = %v, want %v", got, want)
		}
	}
	if len(gateState{}.signals()) != 0 {
		t.Fatalf("a clean gate state produced signals: %v", gateState{}.signals())
	}
}

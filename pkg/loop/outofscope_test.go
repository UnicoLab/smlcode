package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// The chain the live 9B run broke on, end to end: an explorer scoped to
// stats_test.go tries to write stats.go, the focus guard refuses, and the
// refusal must reach the evolve engine as a classified failure with a shipped
// repair — otherwise the harness rediscovers this every run while the model
// burns its budget rewording the edit.
func TestFocusRefusalReachesEvolveWithAShippedRepair(t *testing.T) {
	g := workspace.NewFocusGuard()
	g.SetWave([][]string{{"stats_test.go"}})

	rules, err := evolve.OpenRules(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenRules: %v", err)
	}

	for _, role := range []string{"explorer", "worker"} {
		t.Run(role, func(t *testing.T) {
			refusal := g.Check(workspace.WithRole(context.Background(), role), "stats.go")
			if refusal == nil {
				t.Fatalf("%s writing stats.go must be refused", role)
			}
			// The tool layer reports the refusal as a hard error, so it always
			// counts as a failure regardless of the refusal-marker list.
			if msg := workspace.ToolResultFailure("", refusal); msg == "" {
				t.Fatal("the refusal would never be seen as a failure")
			}
			sig := evolve.Signal{Tool: "ws_edit", Message: refusal.Error(), Language: "go", Role: role}
			if got := evolve.Classify(sig); got != evolve.ClassOutOfScopeWrite {
				t.Fatalf("classified as %q, want %q:\n%s", got, evolve.ClassOutOfScopeWrite, refusal)
			}
			s, ok := rules.Lookup(sig)
			if !ok {
				t.Fatalf("no shipped repair for:\n%s", refusal)
			}
			if !s.Apply || s.Confidence < evolve.MinApplyConfidence {
				t.Fatalf("repair not applicable (%.2f < %.2f)", s.Confidence, evolve.MinApplyConfidence)
			}
			if s.Rule.Repair.Retry {
				t.Error("the repair must not re-issue the refused write")
			}
		})
	}
}

// scopeOK's review-side rejection carries the same contract as the tool-side
// refusal: it names the role's limit and the next action instead of only the
// list of offending paths.
func TestScopeRejectionIsRoleAware(t *testing.T) {
	r := &Runner{}
	explorer := plan.Task{ID: "T1", Role: plan.RoleExplorer, Files: []string{"stats_test.go"},
		Output: `{"status":"done","files_changed":["stats.go"],"summary":"implemented it"}`}
	why := r.scopeOK(explorer)
	if why == "" {
		t.Fatal("an explorer claiming an out-of-scope edit must be rejected")
	}
	if !strings.Contains(why, "does not edit files at all") {
		t.Errorf("read-only contract missing from the review rejection:\n%s", why)
	}

	worker := plan.Task{ID: "T2", Role: plan.RoleWorker, Files: []string{"pkg/stats/stats.go"},
		Output: `{"status":"done","files_changed":["main.go"],"summary":"oops"}`}
	why = r.scopeOK(worker)
	if !strings.Contains(why, "out-of-scope files_changed: main.go") {
		t.Errorf("offending path list lost:\n%s", why)
	}
	if !strings.Contains(why, "planner can rescope") {
		t.Errorf("no next action for an editing role:\n%s", why)
	}
}

// A worker's own tool context must still carry its role, so its refusal names
// the focus files rather than claiming it may not edit at all.
func TestAgentCtxCarriesRoleAndTaskID(t *testing.T) {
	r := &Runner{}
	ctx := r.agentCtx(context.Background(), "T3", "go-worker")
	if got := workspace.TaskIDFrom(ctx); got != "T3" {
		t.Errorf("task id = %q", got)
	}
	if got := workspace.RoleFrom(ctx); got != "go-worker" {
		t.Errorf("role = %q", got)
	}
	if workspace.IsReadOnlyRole(workspace.RoleFrom(ctx)) {
		t.Error("go-worker must not be treated as read-only")
	}
}

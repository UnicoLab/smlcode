package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The corrective rewrite scopes a fix by mining file paths out of the failure
// strings it is handed. A verdict carrying the literal "qa_gate red" names no
// file, so the rewrite fell back to "whatever finished most recently" — and
// measured on a live run, aimed three correction rounds at pkg/tasks while every
// compiler error was in cmd/server/main.go, which none of them could touch.
func TestQAFailureLinesCarryTheFailingFiles(t *testing.T) {
	o := &Orchestrator{lastQAFailure: `# sweep-agency/cmd/server
cmd/server/main.go:45:21: task.Title undefined (type tasks.Task has no field or method Title)
cmd/server/main.go:46:3: unknown field Description in struct literal of type TaskResponse
FAIL	sweep-agency/cmd/server [build failed]`}

	lines := o.qaFailureLines(6)
	if len(lines) == 0 {
		t.Fatal("no failure lines")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "cmd/server/main.go") {
		t.Fatalf("the failing file is not named:\n%s", joined)
	}
	// And the whole point: the rewrite can now resolve it to a real target.
	board := plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"pkg/tasks/store.go"}},
	}}
	targets := resolveFailureTargets(board, lines, "qa_gate command still failing")
	found := false
	for _, f := range targets.files {
		if strings.Contains(f, "cmd/server/main.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("the failing file did not become a corrective target: %+v", targets)
	}
}

// With nothing recorded the caller still needs a failure to report, or the
// verdict claims a rejection with no reason attached.
func TestQAFailureLinesFallBack(t *testing.T) {
	for _, o := range []*Orchestrator{{}, {lastQAFailure: "   \n  \n"}} {
		lines := o.qaFailureLines(6)
		if len(lines) != 1 || lines[0] != "qa_gate red" {
			t.Errorf("got %v, want the generic marker", lines)
		}
	}
	if lines := (*Orchestrator)(nil).qaFailureLines(6); len(lines) != 1 {
		t.Errorf("nil orchestrator: got %v", lines)
	}
}

// A whole test suite's output must not land in the verdict wholesale.
func TestQAFailureLinesAreBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("pkg/x/file.go:1:1: some diagnostic\n")
	}
	o := &Orchestrator{lastQAFailure: b.String()}
	if lines := o.qaFailureLines(6); len(lines) > 6 {
		t.Fatalf("got %d lines, want at most 6", len(lines))
	}
}

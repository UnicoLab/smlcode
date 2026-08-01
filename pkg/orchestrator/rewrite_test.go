package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestRewriteBoardFromTesterReopensAndAdds(t *testing.T) {
	board := &plan.Board{
		QueryID: "run-1",
		Query:   "implement agent.py",
		Plan:    plan.Plan{Summary: "build agent", Steps: []string{"implement agent.py"}},
		Tasks: []plan.Task{
			{ID: "T1", Title: "Implement agent.py", Role: plan.RoleWorker, Column: plan.ColDone,
				Files: []string{"agent.py"}},
			{ID: "T2", Title: "Verify agent", Role: plan.RoleTester, Column: plan.ColDone},
		},
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	out := rewriteBoardFromTester(board, board.Query,
		[]string{"agent.py is still a placeholder"}, "does not work")
	if len(out.Plan.Risks) == 0 {
		t.Fatal("expected plan risk from tester")
	}
	var reopenTester, reopenImpl, hasFix bool
	for _, task := range out.Tasks {
		task.Normalize()
		switch task.ID {
		case "T2":
			if task.Column == plan.ColReadyToDev {
				reopenTester = true
			}
		case "T1":
			if task.Column == plan.ColReadyToDev {
				reopenImpl = true
			}
		}
		if strings.Contains(strings.ToLower(task.Title), "fix tester") {
			hasFix = true
			if task.Column != plan.ColReadyToDev {
				t.Fatal("fix task should be ready")
			}
		}
	}
	if !reopenTester {
		t.Fatal("tester task should reopen")
	}
	if !reopenImpl {
		t.Fatal("implementer mentioning agent.py should reopen")
	}
	if !hasFix && !hasOpenCorrective(out) {
		t.Fatal("expected corrective open work")
	}
}

func TestRewriteBoardVagueFailureNarrowReopen(t *testing.T) {
	board := &plan.Board{
		QueryID: "run-v",
		Query:   "build stuff",
		Plan:    plan.Plan{Summary: "build"},
		Tasks: []plan.Task{
			{ID: "T1", Title: "Unrelated docs", Role: plan.RoleWorker, Column: plan.ColDone,
				Files: []string{"README.md"}},
			{ID: "T2", Title: "Implement core", Role: plan.RoleWorker, Column: plan.ColDone,
				Files: []string{"core.py"}},
			{ID: "T3", Title: "Blocked explorer", Role: plan.RoleWorker, Column: plan.ColBlocked,
				Files: []string{"other.py"}, Error: "timeout"},
			{ID: "T4", Title: "Verify", Role: plan.RoleTester, Column: plan.ColDone},
		},
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	out := rewriteBoardFromTester(board, board.Query,
		[]string{"does not work"}, "unclear")

	byID := map[string]plan.Task{}
	for _, task := range out.Tasks {
		task.Normalize()
		byID[task.ID] = task
	}
	if byID["T4"].Column != plan.ColReadyToDev {
		t.Fatal("tester should reopen")
	}
	// Vague: must NOT reopen unrelated done README or whole-board blocked other.py.
	if byID["T1"].Column != plan.ColDone {
		t.Fatalf("vague failure must not reopen unrelated T1, got %s", byID["T1"].Column)
	}
	if byID["T3"].Column != plan.ColBlocked {
		t.Fatalf("vague failure must not reopen unrelated blocked T3, got %s", byID["T3"].Column)
	}
	// Primary focus implementer (newest with files among done) should reopen.
	if byID["T2"].Column != plan.ColReadyToDev {
		t.Fatalf("expected narrow reopen of primary focus T2, got %s", byID["T2"].Column)
	}
	// Corrective task files should be narrow (core.py), not whole board.
	for _, task := range out.Tasks {
		if strings.Contains(strings.ToLower(task.Title), "fix tester") {
			joined := strings.Join(task.Files, ",")
			if strings.Contains(joined, "other.py") || strings.Contains(joined, "README.md") {
				t.Fatalf("corrective focus too broad: %v", task.Files)
			}
			if !strings.Contains(joined, "core.py") {
				t.Fatalf("corrective should focus core.py, got %v", task.Files)
			}
		}
	}
}

func TestRewriteBoardSpecificTaskID(t *testing.T) {
	board := &plan.Board{
		QueryID: "run-id",
		Query:   "q",
		Tasks: []plan.Task{
			{ID: "T1", Title: "A", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"a.go"}},
			{ID: "T2", Title: "B", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"b.go"}},
			{ID: "T3", Title: "Verify", Role: plan.RoleTester, Column: plan.ColDone},
		},
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	out := rewriteBoardFromTester(board, board.Query,
		[]string{"T2: b.go missing TestB"}, "failed on T2")
	byID := map[string]plan.Task{}
	for _, task := range out.Tasks {
		task.Normalize()
		byID[task.ID] = task
	}
	if byID["T2"].Column != plan.ColReadyToDev {
		t.Fatal("T2 should reopen from task id citation")
	}
	if byID["T1"].Column != plan.ColDone {
		t.Fatal("T1 should stay done when not cited")
	}
}

func TestParseTesterDrivesRewriteFlag(t *testing.T) {
	if !plan.TesterFailed(`{"passed":false,"failures":["broken"]}`) {
		t.Fatal("expected failed")
	}
	if plan.TesterFailed("Observation: go test ./... -short\nok\n" +
		`{"passed":true,"commands":["go test ./... -short"],"summary":"ok"}`) {
		t.Fatal("expected pass with shell evidence")
	}
	if !plan.TesterFailed(`{"passed":true,"commands":["go test ./... -short"],"summary":"ok"}`) {
		t.Fatal("fabricated commands[] without Observation must fail")
	}
	if !plan.TesterFailed("") {
		t.Fatal("empty finalize must fail (no silent skip)")
	}
	if !plan.TesterFailed(`{}`) {
		t.Fatal("empty object must fail")
	}
}

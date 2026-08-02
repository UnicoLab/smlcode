package plan

import "testing"

func TestNormalizeEscalateAction(t *testing.T) {
	if NormalizeEscalateAction("retry") != EscalateActionRetry {
		t.Fatal("retry")
	}
	if NormalizeEscalateAction("continue") != EscalateActionRetry {
		t.Fatal("continue→retry")
	}
	if NormalizeEscalateAction("mark_done") != EscalateActionMarkDone {
		t.Fatal("mark_done")
	}
	if NormalizeEscalateAction("abort") != EscalateActionAbort {
		t.Fatal("abort")
	}
	if NormalizeEscalateAction("") != EscalateActionReScope {
		t.Fatal("default re_scope")
	}
}

func TestApplyEscalateAction(t *testing.T) {
	board := &Board{Tasks: []Task{{
		ID: "T4", Title: "impl", Role: RoleWorker, Column: ColToScope,
		Error: "needs human",
	}}}
	ApplyEscalateAction(board, "T4", EscalateActionRetry, "")
	if board.Tasks[0].Column != ColReadyToDev {
		t.Fatalf("retry col=%s", board.Tasks[0].Column)
	}
	ApplyEscalateAction(board, "T4", EscalateActionMarkDone, "ok")
	if board.Tasks[0].Column != ColDone {
		t.Fatalf("done col=%s", board.Tasks[0].Column)
	}
	board.Tasks[0].Column = ColToScope
	ApplyEscalateAction(board, "T4", EscalateActionAbort, "")
	if board.Tasks[0].Column != ColBlocked {
		t.Fatalf("abort col=%s", board.Tasks[0].Column)
	}
}

func TestBuildEscalateAsk(t *testing.T) {
	ask := BuildEscalateAsk(Task{ID: "T4", Title: "wire graph", Role: RoleWorker, Files: []string{"main.py"}}, "stub code", 30)
	if ask.Kind != "escalate" || ask.TaskID != "T4" || ask.TimeoutS != 30 {
		t.Fatalf("%+v", ask)
	}
	if len(ask.Options) != 4 {
		t.Fatalf("options=%v", ask.Options)
	}
}

package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func TestIsCancelErr(t *testing.T) {
	if !isCancelErr(context.Canceled) {
		t.Fatal("context.Canceled")
	}
	if !isCancelErr(errors.New("context canceled")) {
		t.Fatal("string cancel")
	}
	if isCancelErr(errors.New("boom")) {
		t.Fatal("not cancel")
	}
}

func TestRenameDiskOK(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "greet.go"), []byte("package greet\n\nfunc Greet() string { return \"hello\" }\n"), 0o644)
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Rename Hello to Greet", Files: []string{"greet.go"},
	}}}
	if !renameDiskOK(root, "In greet.go rename Hello to Greet", board) {
		t.Fatal("expected disk OK")
	}
}

func TestPromoteRenameTasksDone(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Column: plan.ColToScope, Error: "escalated"},
		{ID: "T2", Role: plan.RoleTester, Column: plan.ColReadyToDev},
	}}
	promoteRenameTasksDone(board)
	if board.Tasks[0].Column != plan.ColDone || board.Tasks[1].Column != plan.ColDone {
		t.Fatalf("%+v", board.Tasks)
	}
	if board.FailedCount() != 0 || !board.AllDone() {
		t.Fatalf("failed=%d allDone=%v", board.FailedCount(), board.AllDone())
	}
}

func TestCheckpointInterruptPersists(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.QAGate = false
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := session.BeginTurn(cfg.SlmDir(), "run-stop", "do something")
	if err != nil {
		t.Fatal(err)
	}
	o.currentTurn = turn
	board := &plan.Board{
		QueryID: turn.ID, Query: turn.Query,
		Plan: plan.Plan{Summary: "work"},
		Tasks: []plan.Task{{
			ID: "T1", Title: "edit", Column: plan.ColInProgress, Role: plan.RoleWorker,
			Files: []string{"a.go"},
		}},
	}
	res, err := o.checkpointInterrupt(board, session.PhaseExecute, context.Canceled)
	if !isCancelErr(err) {
		t.Fatalf("err=%v", err)
	}
	if res == nil || res.Success {
		t.Fatalf("res=%+v", res)
	}
	loaded, err := session.LoadTurn(cfg.SlmDir(), turn.ID)
	if err != nil || !loaded.Interrupted {
		t.Fatalf("interrupted turn: %v %+v", err, loaded)
	}
	if loaded.Board.Tasks[0].Column != plan.ColReadyToDev {
		t.Fatalf("column=%s", loaded.Board.Tasks[0].Column)
	}
}

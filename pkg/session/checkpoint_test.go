package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestMarkInterruptedAndResumeNormalize(t *testing.T) {
	slm := t.TempDir()
	turn, err := BeginTurn(slm, "run-int", "rename Hello to Greet")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: turn.ID,
		Query:   turn.Query,
		Plan:    plan.Plan{Summary: "rename", Steps: []string{"edit greet.go"}},
		Tasks: []plan.Task{{
			ID: "T1", Title: "Rename Hello to Greet", Role: plan.RoleWorker,
			Column: plan.ColInProgress, Files: []string{"greet.go"},
			Error: "context canceled",
		}},
	}
	if err := MarkInterrupted(slm, turn, board, PhaseExecute); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTurn(slm, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Interrupted || loaded.Phase != PhaseExecute {
		t.Fatalf("%+v", loaded)
	}
	if loaded.Board.Tasks[0].Column != plan.ColReadyToDev {
		t.Fatalf("expected ready after interrupt normalize, got %s", loaded.Board.Tasks[0].Column)
	}
	if _, err := os.Stat(filepath.Join(TurnDir(slm, turn.ID), "INTERRUPTED.md")); err != nil {
		t.Fatal("missing INTERRUPTED.md")
	}
	if _, err := os.Stat(filepath.Join(slm, "checkpoint.json")); err != nil {
		t.Fatal("missing checkpoint.json")
	}

	found, err := FindInterrupted(slm)
	if err != nil || found == nil || found.ID != turn.ID {
		t.Fatalf("FindInterrupted: %v %+v", err, found)
	}

	if err := ClearInterrupted(slm, loaded); err != nil {
		t.Fatal(err)
	}
	loaded2, _ := LoadTurn(slm, turn.ID)
	if loaded2.Interrupted {
		t.Fatal("expected cleared")
	}
}

func TestResolveResumeTurnLatest(t *testing.T) {
	slm := t.TempDir()
	turn, _ := BeginTurn(slm, "run-a", "do work")
	board := plan.Board{
		QueryID: turn.ID, Query: turn.Query,
		Tasks: []plan.Task{{ID: "T1", Title: "x", Column: plan.ColInProgress, Role: plan.RoleWorker}},
	}
	_ = MarkInterrupted(slm, turn, board, PhaseExecute)
	got, err := ResolveResumeTurn(slm, "")
	if err != nil || got == nil || got.ID != "run-a" {
		t.Fatalf("%v %+v", err, got)
	}
}

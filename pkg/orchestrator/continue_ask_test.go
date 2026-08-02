package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

func TestNeedsContinueAsk(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Role: plan.RoleWorker, Column: plan.ColToScope,
		Error: "review rejected after max retries — needs human input",
		Notes: "ESCALATED",
	}}}
	if !needsContinueAsk(board, false, false, nil) {
		t.Fatal("escalated board should need continue ask")
	}
	if !needsContinueAsk(board, false, false, []quality.PreciseGap{{Path: "a.py", Reason: "stub"}}) {
		t.Fatal("gaps should need continue ask")
	}
	clean := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Role: plan.RoleWorker, Column: plan.ColDone, Output: `{"status":"done"}`,
	}}}
	if needsContinueAsk(clean, false, false, nil) {
		t.Fatal("clean done board should not need continue")
	}
}

func TestRunContinueAskAuto(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.ContinueAsk = plan.ContinueAskAuto
	cfg.PlaceholderPass = false
	cfg.AutoApprove = false
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{
		Tasks: []plan.Task{{
			ID: "T1", Role: plan.RoleWorker, Column: plan.ColToScope,
			Error: "review rejected after max retries — needs human input",
			Notes: "ESCALATED",
			Files: []string{"main.py"},
		}},
	}
	o.persistBoard(board)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	again, out := o.runContinueAsk(ctx, "setup template", board, nil,
		"escalated", nil, true, false)
	if !again {
		t.Fatal("auto mode should continue once")
	}
	if out == nil {
		t.Fatal("expected board")
	}
	// Reopened to ready
	found := false
	for _, tsk := range out.Tasks {
		if tsk.ID == "T1" && tsk.Column == plan.ColReadyToDev {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected T1 reopened ready, got %+v", out.Tasks)
	}
}

func TestNormalizeContinueAskConfig(t *testing.T) {
	if config.NormalizeContinueAsk("") != "ask" {
		t.Fatal("default ask")
	}
	if config.NormalizeContinueAsk("auto") != "auto" {
		t.Fatal("auto")
	}
	if config.NormalizeContinueAsk("off") != "off" {
		t.Fatal("off")
	}
}

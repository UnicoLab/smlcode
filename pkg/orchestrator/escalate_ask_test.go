package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestRunEscalateAskOff(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.EscalateAsk = "off"
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T4", Role: plan.RoleWorker, Column: plan.ColToScope,
		Error: "needs human",
	}}}
	o.persistBoard(board)
	ans := o.runEscalateAsk(context.Background(), board, board.Tasks[0], "detail")
	if ans.Action != plan.EscalateActionReScope {
		t.Fatalf("action=%s", ans.Action)
	}
	if board.Tasks[0].Column != plan.ColToScope {
		t.Fatalf("col=%s", board.Tasks[0].Column)
	}
}

func TestRunEscalateAskAutoRetry(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.EscalateAsk = "auto"
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T4", Role: plan.RoleWorker, Column: plan.ColToScope,
		Error: "needs human",
	}}}
	o.persistBoard(board)
	ans := o.runEscalateAsk(context.Background(), board, board.Tasks[0], "detail")
	if ans.Action != plan.EscalateActionRetry {
		t.Fatalf("action=%s", ans.Action)
	}
	if board.Tasks[0].Column != plan.ColReadyToDev {
		t.Fatalf("col=%s", board.Tasks[0].Column)
	}
}

func TestRunEscalateAskTimeoutSLMOrHeuristic(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.EscalateAsk = "ask"
	cfg.EscalateAskTimeout = 80 * time.Millisecond
	cfg.DryRun = true // no live LLM — heuristic / dry agent path
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T4", Role: plan.RoleWorker, Column: plan.ColToScope,
		Error: "static quality failed — Placeholder stub",
	}}}
	o.persistBoard(board)
	start := time.Now()
	ans := o.runEscalateAsk(context.Background(), board, board.Tasks[0], "Placeholder implementation")
	if time.Since(start) > 8*time.Second {
		t.Fatal("timeout+decide took too long")
	}
	// Timeout must not hardcode re_scope — SLM or heuristic picks (retry for stubs).
	if ans.Action != plan.EscalateActionRetry && ans.Action != plan.EscalateActionReScope &&
		ans.Action != plan.EscalateActionAbort && ans.Action != plan.EscalateActionMarkDone {
		t.Fatalf("unexpected action=%s", ans.Action)
	}
	if board.Tasks[0].Column == plan.ColToScope && ans.Action == plan.EscalateActionRetry {
		t.Fatal("retry should reopen ready_to_dev")
	}
}

func TestNormalizeEscalateAskConfig(t *testing.T) {
	if config.NormalizeEscalateAsk("") != "ask" {
		t.Fatal("default ask")
	}
	if config.NormalizeEscalateAsk("auto") != "auto" {
		t.Fatal("auto")
	}
	if config.NormalizeEscalateAsk("off") != "off" {
		t.Fatal("off")
	}
}

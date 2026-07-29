package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestWorkspaceInitAndBoardCRUD(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".slmcode", "PROJECT.md")); err != nil {
		t.Fatal(err)
	}

	ws, err := harness.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	err = ws.Board.Update(func(b *plan.Board) error {
		t1 := plan.Task{ID: "T1", Title: "Scope API", Role: plan.RoleExplorer}
		t1.MoveTo(plan.ColToScope)
		t1.AddChecklist("Read handlers")
		b.Tasks = append(b.Tasks, t1)

		t2 := plan.Task{ID: "T2", Title: "Implement", Role: plan.RoleWorker, DependsOn: []string{"T1"}}
		t2.MoveTo(plan.ColReadyToDev)
		b.Tasks = append(b.Tasks, t2)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = ws.Board.Update(func(b *plan.Board) error {
		t1, ok := b.Get("T1")
		if !ok {
			return os.ErrNotExist
		}
		t1.MoveTo(plan.ColReadyToDev)
		b.UpdateTask(t1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := ws.Board.Snapshot()
	ready := snap.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "T1" {
		t.Fatalf("ready=%+v want only T1 (T2 blocked by deps)", ready)
	}
	if _, err := os.Stat(ws.Board.Path()); err != nil {
		t.Fatal("board.json missing")
	}
}

func TestContextPackerBudget(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	store := contextstore.New(cfg.SlmDir())
	_ = store.Write(contextstore.DocContext, "# Working Context\n\nfocus: auth\n")
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	packer := contextstore.NewPacker(store, root, 16)
	tp, err := packer.Build("worker", "fix auth",
		[]string{contextstore.DocContext, contextstore.DocProject},
		[]string{"main.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tp.BudgetUsed <= 0 {
		t.Fatal("expected budget used")
	}
	if _, ok := tp.Files["main.go"]; !ok {
		t.Fatal("expected file pack")
	}
	rendered := tp.Render()
	if rendered == "" {
		t.Fatal("empty render")
	}
}

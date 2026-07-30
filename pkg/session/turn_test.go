package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestBeginTurnRewritesLiveBoard(t *testing.T) {
	slm := t.TempDir()
	// Stale global board from a previous query
	stale := plan.Board{
		QueryID: "old-run",
		Query:   "old work",
		Plan:    plan.Plan{Summary: "old plan", Steps: []string{"old step"}},
		Tasks:   []plan.Task{{ID: "T1", Title: "old task", Column: plan.ColDone}},
	}
	data, _ := jsonIndent(stale)
	_ = os.WriteFile(filepath.Join(slm, "board.json"), data, 0o644)
	_ = os.WriteFile(filepath.Join(slm, "PLAN.md"), []byte("# Plan\n\nold"), 0o644)
	_ = os.WriteFile(filepath.Join(slm, "TASKS.md"), []byte("# Tasks\n\nold"), 0o644)

	turn, err := BeginTurn(slm, "run-new", "brand new query")
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID != "run-new" || turn.Query != "brand new query" {
		t.Fatalf("%+v", turn)
	}
	if len(turn.Board.Tasks) != 0 {
		t.Fatalf("expected empty tasks, got %d", len(turn.Board.Tasks))
	}
	livePlan, _ := os.ReadFile(filepath.Join(slm, "PLAN.md"))
	if strings.Contains(string(livePlan), "old plan") {
		t.Fatal("live PLAN.md still has old plan")
	}
	liveTasks, _ := os.ReadFile(filepath.Join(slm, "TASKS.md"))
	if strings.Contains(string(liveTasks), "old task") {
		t.Fatal("live TASKS.md still has old tasks")
	}
	qdir := TurnDir(slm, "run-new")
	if _, err := os.Stat(filepath.Join(qdir, "meta.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSaveTurnAndSummaryEnrichment(t *testing.T) {
	slm := t.TempDir()
	turn, err := BeginTurn(slm, "run-1", "create hello.go")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: turn.ID,
		Query:   turn.Query,
		Plan:    plan.Plan{Summary: "Add hello", Steps: []string{"write hello.go"}},
		Tasks: []plan.Task{{
			ID: "T1", Title: "Write hello.go", Column: plan.ColDone,
			Files: []string{"hello.go"}, Role: plan.RoleWorker,
		}},
	}
	if err := SaveTurnBoard(slm, turn, board); err != nil {
		t.Fatal(err)
	}
	path, err := WriteTurnSummary(slm, turn, board, "- Prefer tiny files for SLMs")
	if err != nil || path == "" {
		t.Fatal(err, path)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "create hello.go") || !strings.Contains(string(body), "hello.go") {
		t.Fatalf("summary missing content:\n%s", body)
	}
	idx := RecentSummaries(slm, 3)
	if !strings.Contains(idx, "create hello.go") && !strings.Contains(idx, "Add hello") {
		t.Fatalf("recent summaries empty/missing:\n%s", idx)
	}

	// Second turn must clear live board but keep prior summaries
	turn2, err := BeginTurn(slm, "run-2", "add tests")
	if err != nil {
		t.Fatal(err)
	}
	if len(turn2.Board.Tasks) != 0 {
		t.Fatal("turn2 must start with empty tasks")
	}
	liveBoard, _ := os.ReadFile(filepath.Join(slm, "board.json"))
	if strings.Contains(string(liveBoard), "Write hello.go") {
		t.Fatal("live board still has turn1 tasks")
	}
	// Prior turn dir preserved
	if _, err := os.Stat(filepath.Join(TurnDir(slm, "run-1"), "summary.md")); err != nil {
		t.Fatal("turn1 summary should remain", err)
	}
	if !strings.Contains(RecentSummaries(slm, 5), "hello") {
		t.Fatal("prior summary should enrich later turns")
	}
}

func jsonIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

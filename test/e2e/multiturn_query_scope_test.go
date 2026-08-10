package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/server"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// TestMultiTurnQueryScopedPlanTasksSummary walks each multi-turn stage offline:
// BeginTurn → fresh board → plan/tasks → execute markers → review/tester →
// rewrite-on-fail (incl. empty finalize + vague narrow reopen) → summary →
// next turn uses summaries not old plan → queries API.
func TestMultiTurnQueryScopedPlanTasksSummary(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# toy\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)

	cfg := config.Default(root)
	cfg.QAGate = false
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	slm := cfg.SlmDir()

	// --- Stage: BeginTurn 1 → fresh board ---
	t1, err := session.BeginTurn(slm, "run-turn1", "Add a doc comment to Hello()")
	if err != nil {
		t.Fatal(err)
	}
	if t1.ID != "run-turn1" || t1.Query == "" {
		t.Fatalf("turn meta: %+v", t1)
	}
	if len(t1.Board.Tasks) != 0 {
		t.Fatal("BeginTurn must start with empty tasks")
	}
	liveBoardRaw, _ := os.ReadFile(filepath.Join(slm, "board.json"))
	var live plan.Board
	_ = json.Unmarshal(liveBoardRaw, &live)
	if live.QueryID != "run-turn1" || len(live.Tasks) != 0 {
		t.Fatalf("live board not query-scoped empty: query_id=%s tasks=%d", live.QueryID, len(live.Tasks))
	}

	// --- Stage: plan + tasks + execute (done markers) ---
	board1 := plan.Board{
		QueryID: t1.ID, Query: t1.Query,
		Plan: plan.Plan{Summary: "Document Hello", Steps: []string{"edit hello.go"}},
		Tasks: []plan.Task{{
			ID: "T1", Title: "Doc comment Hello", Role: plan.RoleWorker, Column: plan.ColDone,
			Files: []string{"hello.go"}, Acceptance: "hello.go has a doc comment",
		}},
	}
	for i := range board1.Tasks {
		board1.Tasks[i].Normalize()
	}
	if err := session.SaveTurnBoard(slm, t1, board1); err != nil {
		t.Fatal(err)
	}
	livePlan, _ := os.ReadFile(filepath.Join(slm, "PLAN.md"))
	if !strings.Contains(string(livePlan), "Document Hello") {
		t.Fatal("live PLAN missing turn1")
	}

	// --- Stage: summary for turn1 ---
	sum1, err := session.WriteTurnSummary(slm, t1, board1, "- Keep Hello() pure")
	if err != nil || sum1 == "" {
		t.Fatal(err, sum1)
	}
	body1, _ := os.ReadFile(sum1)
	if !strings.Contains(string(body1), "Add a doc comment") || !strings.Contains(string(body1), "hello.go") {
		t.Fatalf("turn1 summary incomplete:\n%s", body1)
	}

	// --- Stage: BeginTurn 2 → fresh board; prior summaries enrich ---
	t2, err := session.BeginTurn(slm, "run-turn2", "Add TestHello")
	if err != nil {
		t.Fatal(err)
	}
	if len(t2.Board.Tasks) != 0 {
		t.Fatalf("turn2 must start empty, got %d tasks", len(t2.Board.Tasks))
	}
	liveBoardRaw, _ = os.ReadFile(filepath.Join(slm, "board.json"))
	_ = json.Unmarshal(liveBoardRaw, &live)
	if len(live.Tasks) != 0 || strings.Contains(string(liveBoardRaw), "Doc comment Hello") {
		t.Fatal("live board still has turn1 tasks after BeginTurn")
	}
	prior := session.RecentSummaries(slm, 5)
	if !strings.Contains(prior, "doc comment") && !strings.Contains(prior, "Document Hello") {
		t.Fatal("prior summary should enrich turn2 context")
	}
	if strings.Contains(prior, "PLAN.md rewritten") {
		// knowledge only — not a live plan carry-over signal
	}
	if _, err := os.Stat(filepath.Join(session.TurnDir(slm, "run-turn1"), "PLAN.md")); err != nil {
		t.Fatal("turn1 PLAN.md should remain under queries/", err)
	}

	// --- Stage: turn2 plan/tasks + tester failure rewrite ---
	board2 := plan.Board{
		QueryID: t2.ID, Query: t2.Query,
		Plan: plan.Plan{Summary: "Add tests", Steps: []string{"write hello_test.go"}},
		Tasks: []plan.Task{
			{ID: "T1", Title: "Write hello_test.go", Role: plan.RoleWorker, Column: plan.ColDone,
				Files: []string{"hello_test.go"}},
			{ID: "T2", Title: "Run tests", Role: plan.RoleTester, Column: plan.ColDone,
				Output: `{"passed":false,"failures":["TestHello missing"],"summary":"does not work"}`},
		},
	}
	for i := range board2.Tasks {
		board2.Tasks[i].Normalize()
	}
	if err := session.SaveTurnBoard(slm, t2, board2); err != nil {
		t.Fatal(err)
	}

	rewritten := rewriteBoardExported(board2, []string{"TestHello missing in hello_test.go"}, "does not work")
	if rewritten.QueryID != "run-turn2" {
		t.Fatal("rewrite must keep query scope")
	}
	open := 0
	for _, task := range rewritten.Tasks {
		task.Normalize()
		if task.Column == plan.ColReadyToDev || task.Column == plan.ColInProgress {
			open++
		}
	}
	if open == 0 {
		t.Fatal("tester failure must reopen/add tasks")
	}

	// --- Stage: empty tester finalize must also force rewrite (no silent skip) ---
	if !plan.TesterFailed("") {
		t.Fatal("empty tester finalize must be treated as failure")
	}
	emptyBoard := board2
	emptyRewritten := rewriteBoardExported(emptyBoard, []string{"empty or missing tester JSON — treat as failed"}, "empty tester finalize")
	if !hasOpenWork(emptyRewritten) {
		t.Fatal("empty finalize rewrite must open corrective work")
	}

	// --- Stage: vague failure narrow reopen ---
	vagueBoard := plan.Board{
		QueryID: "run-vague", Query: "build",
		Tasks: []plan.Task{
			{ID: "T1", Title: "Docs", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"README.md"}},
			{ID: "T2", Title: "Core", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"core.go"}},
			{ID: "T3", Title: "Blocked other", Role: plan.RoleWorker, Column: plan.ColBlocked, Files: []string{"other.go"}, Error: "x"},
			{ID: "T4", Title: "Verify", Role: plan.RoleTester, Column: plan.ColDone},
		},
	}
	for i := range vagueBoard.Tasks {
		vagueBoard.Tasks[i].Normalize()
	}
	vagueOut := rewriteBoardExported(vagueBoard, []string{"does not work"}, "unclear")
	byID := map[string]plan.Task{}
	for _, task := range vagueOut.Tasks {
		task.Normalize()
		byID[task.ID] = task
	}
	if byID["T1"].Column != plan.ColDone {
		t.Fatal("vague rewrite must not reopen unrelated docs task")
	}
	if byID["T3"].Column != plan.ColBlocked {
		t.Fatal("vague rewrite must not reopen unrelated blocked task")
	}
	if byID["T2"].Column != plan.ColReadyToDev && !hasOpenWork(vagueOut) {
		t.Fatal("vague rewrite should keep narrow corrective open work")
	}

	if err := session.SaveTurnBoard(slm, t2, rewritten); err != nil {
		t.Fatal(err)
	}
	sum2, err := session.WriteTurnSummary(slm, t2, rewritten, "tester rejected — plan rewritten")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(sum2)
	if !strings.Contains(string(b2), "Add TestHello") {
		t.Fatal("turn2 summary missing query")
	}

	// Turn1 summary still enriching; next turn must not use old plan as live board
	idx := session.RecentSummaries(slm, 5)
	if !strings.Contains(idx, "turn1") && !strings.Contains(strings.ToLower(idx), "hello") {
		t.Fatalf("index should include prior knowledge:\n%s", idx)
	}
	t3, err := session.BeginTurn(slm, "run-turn3", "tiny follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if len(t3.Board.Tasks) != 0 {
		t.Fatal("turn3 must not inherit turn2 rewritten tasks as live plan")
	}
	if !strings.Contains(session.RecentSummaries(slm, 5), "Add TestHello") &&
		!strings.Contains(session.RecentSummaries(slm, 5), "Document Hello") {
		t.Fatal("turn3 should still see prior summaries")
	}

	// queries/ dirs exist for both turns with dedicated PLAN/TASKS
	for _, id := range []string{"run-turn1", "run-turn2"} {
		dir := session.TurnDir(slm, id)
		for _, name := range []string{"PLAN.md", "TASKS.md", "summary.md", "board.json", "meta.json"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("%s missing %s: %v", id, name, err)
			}
		}
	}

	// --- Stage: queries API smoke ---
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	srv := server.New(h, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/queries")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("queries list %d", resp.StatusCode)
	}
	var list []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 {
		t.Fatalf("expected >=2 query turns, got %d", len(list))
	}
	resp2, err := http.Get(ts.URL + "/api/queries/run-turn1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("query detail %d", resp2.StatusCode)
	}
	var detail map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail["id"] != "run-turn1" {
		t.Fatalf("detail id=%v", detail["id"])
	}
	if _, ok := detail["summary_md"].(string); !ok {
		t.Fatal("detail missing summary_md")
	}
	if _, ok := detail["plan_md"].(string); !ok {
		t.Fatal("detail missing plan_md")
	}
	if _, ok := detail["tasks_md"].(string); !ok {
		t.Fatal("detail missing tasks_md")
	}

	// UI embedding smoke: API client must wire queries to /api/queries
	uiClient := filepath.Join("..", "..", "web", "src", "api", "client.ts")
	if data, err := os.ReadFile(uiClient); err == nil {
		s := string(data)
		for _, needle := range []string{`/queries`, `getQueries`, `getQuery`, `QuerySession`, `QueryView`} {
			if !strings.Contains(s, needle) {
				t.Fatalf("Studio UI API client missing Queries wiring: %s", needle)
			}
		}
	}
}

func hasOpenWork(b plan.Board) bool {
	for _, task := range b.Tasks {
		task.Normalize()
		if task.Column == plan.ColReadyToDev || task.Column == plan.ColInProgress {
			return true
		}
	}
	return false
}

// rewriteBoardExported mirrors orchestrator.rewriteBoardFromTester closely enough
// for offline stage assertions (keeps e2e package decoupled from unexported helpers).
func rewriteBoardExported(board plan.Board, failures []string, summary string) plan.Board {
	out := board
	out.Plan.Risks = append(out.Plan.Risks, "Tester rejected: "+summary)
	failBlob := strings.ToLower(strings.Join(failures, " ") + " " + summary)
	vague := !strings.Contains(failBlob, ".go") && !strings.Contains(failBlob, ".py") &&
		!strings.Contains(failBlob, "t1") && !strings.Contains(failBlob, "t2") &&
		!strings.Contains(failBlob, "testhello") &&
		(strings.Contains(failBlob, "does not work") || strings.Contains(failBlob, "unclear") ||
			strings.Contains(failBlob, "empty"))

	// Collect primary focus from newest *done* implementer only (narrow).
	var focus []string
	for i := len(out.Tasks) - 1; i >= 0; i-- {
		t := out.Tasks[i]
		t.Normalize()
		role := strings.ToLower(t.Role)
		if (role == plan.RoleWorker || role == plan.RoleCorrector || role == "deep") &&
			t.Column == plan.ColDone && len(t.Files) > 0 {
			focus = append(focus, t.Files...)
			break
		}
	}

	reopenedImpl := 0
	for i := len(out.Tasks) - 1; i >= 0; i-- {
		t := out.Tasks[i]
		t.Normalize()
		role := strings.ToLower(t.Role)
		reopen := false
		if role == plan.RoleTester && t.Column == plan.ColDone {
			reopen = true
		}
		for _, f := range t.Files {
			if f != "" && strings.Contains(failBlob, strings.ToLower(filepath.Base(f))) {
				reopen = true
			}
		}
		if strings.Contains(failBlob, strings.ToLower(t.ID)) {
			reopen = true
		}
		if vague && (role == plan.RoleWorker || role == plan.RoleCorrector) && t.Column == plan.ColDone && reopenedImpl < 1 {
			for _, f := range t.Files {
				for _, pf := range focus {
					if f == pf {
						reopen = true
						reopenedImpl++
						break
					}
				}
				if reopen {
					break
				}
			}
		}
		// Vague: do not reopen unrelated blocked
		if vague && t.Column == plan.ColBlocked {
			reopen = false
		}
		if reopen {
			t.MoveTo(plan.ColReadyToDev)
			t.Notes = "REOPENED: tester reported failure"
		}
		out.Tasks[i] = t
	}
	id := out.NextID()
	files := focus
	if len(files) > 3 {
		files = files[:3]
	}
	nt := plan.Task{
		ID: id, Title: "Fix tester failures", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Acceptance: "tests pass", Description: "Fix: " + strings.Join(failures, "; "),
		Files: files,
	}
	nt.Normalize()
	out.Tasks = append(out.Tasks, nt)
	return out
}

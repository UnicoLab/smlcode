package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func apiCall(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	s.Handler().ServeHTTP(rec, newAPIRequest(method, path, rd))
	return rec
}

// taskUpdateEvents returns every task_update event in the ring, oldest first.
func taskUpdateEvents(s *Server) []orchestrator.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []orchestrator.Event
	for _, se := range s.events {
		if se.Event.Kind == "task_update" {
			out = append(out, se.Event)
		}
	}
	return out
}

// B1: every board task change is published as a task_update event carrying
// the full task under data.task, exactly as GET /api/tasks renders it.
func TestBoardChangesEmitTaskUpdateEvents(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()

	if rec := apiCall(t, s, http.MethodPost, "/api/tasks", `{"id":"T1","title":"main","column":"ready_to_dev"}`); rec.Code != 200 {
		t.Fatalf("add task: %d %s", rec.Code, rec.Body.String())
	}
	if rec := apiCall(t, s, http.MethodPatch, "/api/tasks/T1", `{"column":"in_progress"}`); rec.Code != 200 {
		t.Fatalf("patch task: %d %s", rec.Code, rec.Body.String())
	}
	evs := taskUpdateEvents(s)
	if len(evs) == 0 {
		t.Fatal("no task_update event emitted for a board change")
	}
	ev := evs[len(evs)-1]
	if ev.Phase != "board" || ev.TaskID != "T1" || ev.Message != "T1 -> in_progress" {
		t.Fatalf("event shape: phase=%q task=%q msg=%q", ev.Phase, ev.TaskID, ev.Message)
	}
	// Serialize exactly as SSE / /api/runs/latest do and compare with GET /api/tasks.
	raw, _ := json.Marshal(ev.Data)
	var data struct {
		Task plan.Task `json:"task"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || data.Task.ID != "T1" || data.Task.Column != "in_progress" || data.Task.Status != plan.StatusRunning {
		t.Fatalf("data.task: %s (err=%v)", raw, err)
	}
	var board plan.Board
	_ = json.Unmarshal(apiCall(t, s, http.MethodGet, "/api/tasks", "").Body.Bytes(), &board)
	if len(board.Tasks) != 1 || board.Tasks[0].Column != data.Task.Column || board.Tasks[0].Title != data.Task.Title {
		t.Fatalf("event task differs from GET /api/tasks: %+v vs %+v", data.Task, board.Tasks)
	}
	// Replayed like any other event.
	if !strings.Contains(apiCall(t, s, http.MethodGet, "/api/runs/latest", "").Body.String(), `"kind": "task_update"`) {
		t.Fatal("task_update not in the replay buffer")
	}
	// The phase follows the run once one is known.
	s.emit(orchestrator.Event{Phase: "execute", Kind: "phase", Message: "wave 1", Time: time.Now()})
	apiCall(t, s, http.MethodPatch, "/api/tasks/T1", `{"column":"in_review"}`)
	evs = taskUpdateEvents(s)
	if got := evs[len(evs)-1].Phase; got != "execute" {
		t.Fatalf("phase not taken from the run: %q", got)
	}
	// A removed task is reported with an empty column ("-> removed").
	apiCall(t, s, http.MethodDelete, "/api/tasks/T1", "")
	evs = taskUpdateEvents(s)
	if last := evs[len(evs)-1]; last.Message != "T1 -> removed" {
		t.Fatalf("removal event: %q", last.Message)
	}
}

// B2: POST /api/tasks/{id}/retry.
func TestRetryTaskEndpoint(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()

	// No board loaded → 409.
	if rec := apiCall(t, s, http.MethodPost, "/api/tasks/T1/retry", ""); rec.Code != http.StatusConflict {
		t.Fatalf("no board: %d %s", rec.Code, rec.Body.String())
	}
	board := plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "main", Column: plan.ColBlocked, Retries: 3, GateRetries: 1,
		Error: "tests failed", AttemptLog: []string{"attempt 1 failed because X"},
		Criteria: []plan.Criterion{{ID: "c1", Text: "builds"}},
	}}}
	raw, _ := json.Marshal(board)
	if rec := apiCall(t, s, http.MethodPut, "/api/tasks", string(raw)); rec.Code != 200 {
		t.Fatalf("put board: %d %s", rec.Code, rec.Body.String())
	}
	// Unknown id → 404.
	if rec := apiCall(t, s, http.MethodPost, "/api/tasks/T9/retry", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body.String())
	}
	rec := apiCall(t, s, http.MethodPost, "/api/tasks/T1/retry", "")
	if rec.Code != 200 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	var got plan.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Column != plan.ColReadyToDev || got.Status != plan.StatusReady || got.Retries != 0 || got.Error != "" {
		t.Fatalf("task after retry: %+v", got)
	}
	if len(got.AttemptLog) != 2 || got.AttemptLog[1] != "retried from Studio" {
		t.Fatalf("attempt_log: %v", got.AttemptLog)
	}
	if got.GateRetries != 1 || len(got.Criteria) != 1 {
		t.Fatalf("unrelated fields disturbed: %+v", got)
	}
	// Persisted, and exposed by GET /api/tasks with the fields Studio needs.
	body := apiCall(t, s, http.MethodGet, "/api/tasks", "").Body.String()
	for _, key := range []string{`"attempt_log"`, `"gate_retries"`, `"criteria"`, `"retried from Studio"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("GET /api/tasks lacks %s: %s", key, body)
		}
	}
	// And published as a task_update.
	evs := taskUpdateEvents(s)
	if len(evs) == 0 || evs[len(evs)-1].Message != "T1 -> ready_to_dev" {
		t.Fatalf("retry did not emit task_update: %+v", evs)
	}
}

// B3: GET /api/queries carries per-run figures computed from stored state.
func TestListQueriesCarriesRunFigures(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	slm := h.Config.SlmDir()
	turn, err := session.BeginTurn(slm, "run-1", "add a thing")
	if err != nil {
		t.Fatal(err)
	}
	turn.CreatedAt = time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	board := plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "a", Column: plan.ColDone, Squad: "backend"},
		{ID: "T2", Title: "b", Column: plan.ColBlocked, Squad: "frontend"},
		{ID: "T3", Title: "c", Column: plan.ColReadyToDev, Squad: "backend"},
	}}
	if err := session.SaveTurnBoard(slm, turn, board); err != nil {
		t.Fatal(err)
	}
	for _, rec := range []session.EventRecord{
		{Phase: "plan", Kind: "usage", Tokens: 1200, CostUSD: 0.002},
		{Phase: "execute", Kind: "token", Message: "delta"},
		{Phase: "execute", Kind: "usage", Tokens: 800, CostUSD: 0.001},
	} {
		if err := session.AppendEvent(slm, "run-1", rec); err != nil {
			t.Fatal(err)
		}
	}
	session.FlushEventLogs()

	rec := apiCall(t, s, http.MethodGet, "/api/queries", "")
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []struct {
		ID          string   `json:"id"`
		DurationMS  int64    `json:"duration_ms"`
		Tokens      int      `json:"tokens"`
		CostUSD     float64  `json:"cost_usd"`
		TasksTotal  int      `json:"tasks_total"`
		TasksDone   int      `json:"tasks_done"`
		FailedTasks int      `json:"failed_tasks"`
		Teams       []string `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items: %s", rec.Body.String())
	}
	it := items[0]
	if it.ID != "run-1" || it.TasksTotal != 3 || it.TasksDone != 1 || it.FailedTasks != 1 {
		t.Fatalf("task figures: %+v", it)
	}
	if it.Tokens != 2000 || it.CostUSD < 0.0029 || it.CostUSD > 0.0031 {
		t.Fatalf("usage figures: %+v", it)
	}
	if it.DurationMS < 80_000 || it.DurationMS > 200_000 {
		t.Fatalf("duration_ms: %d", it.DurationMS)
	}
	if len(it.Teams) != 2 || it.Teams[0] != "backend" || it.Teams[1] != "frontend" {
		t.Fatalf("teams: %v", it.Teams)
	}
	// Second listing is served from the usage cache (same figures).
	rec = apiCall(t, s, http.MethodGet, "/api/queries", "")
	if !strings.Contains(rec.Body.String(), `"tokens": 2000`) {
		t.Fatalf("cached listing: %s", rec.Body.String())
	}
}

package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// laneExec is a worker that writes each task's file and reports the write the
// way the tool layer would, so the team's lane fingerprint moves.
type laneExec struct {
	root    string
	files   map[string]string // task id → the file it writes
	onWrite func(path string)
}

func (e *laneExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		if strings.Contains(req.AgentID, "review") {
			out = append(out, ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
				Output: `{"approved":true,"score":90,"summary":"ok","issues":[]}`})
			continue
		}
		rel, ok := e.files[req.TaskID]
		if !ok {
			// A correction ticket: it works the backend's file again.
			rel = "cmd/main.go"
		}
		full := filepath.Join(e.root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("package main\n// "+req.TaskID+"\n"), 0o600)
		if e.onWrite != nil {
			e.onWrite(rel)
		}
		out = append(out, ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
			Output: fmt.Sprintf("Observation: ws_edit edited %s (1 replacement(s))\n", rel) +
				fmt.Sprintf(`{"status":"done","summary":"done","files_changed":[%q]}`, rel)})
	}
	return out, nil
}

// A two-team board. The backend finishes its lane in wave 1 with a failing
// acceptance command. Its correction ticket must exist BEFORE wave 2 is
// announced, so the fix rides the next wave rather than waiting for the finish
// path — where it used to be raised only after the frontend's last task.
func TestARedHalfIsTicketedBeforeTheNextWave(t *testing.T) {
	var mu sync.Mutex
	var timeline []string
	note := func(s string) {
		mu.Lock()
		timeline = append(timeline, s)
		mu.Unlock()
	}
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		if strings.HasPrefix(cmd, "go test") {
			return quality.SmokeResult{Ran: true, OK: false, Command: cmd,
				Summary: "FAIL: TestMain", Output: "--- FAIL: TestMain\ncmd/main.go:3: boom\nFAIL"}
		}
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})
	o.onEvent = func(e Event) { note("event: " + e.Message) }
	exec := &laneExec{root: o.cfg.Root, onWrite: func(p string) { o.noteChangedFiles(p) },
		files: map[string]string{"T1": "cmd/main.go", "T2": "web/App.tsx"}}

	r := loop.NewRunner(exec, ggagent.NewSharedState())
	r.Root = o.cfg.Root
	r.MaxParallel = 1 // one task per wave: backend first, frontend after
	r.MaxRetries = 0
	r.MaxWaves = 3
	r.IdleWait = time.Millisecond
	r.Timeout = 5 * time.Second
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.WorkerCritique = false
	r.ReviewParallel = false
	r.Squads = o.squadPlanNow()
	r.Log = func(format string, args ...interface{}) {
		if msg := fmt.Sprintf(format, args...); strings.HasPrefix(msg, "wave:") {
			note("log: " + msg)
		}
	}
	// Exactly what buildRunner installs.
	r.AfterWave = func(ctx context.Context, board *plan.Board, _ []plan.Task) { o.earlyTeamGate(ctx, board) }

	board := &plan.Board{QueryID: "run-teams", Query: "build both halves", Tasks: []plan.Task{
		{ID: "T1", Title: "backend handler", Squad: "backend", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "write cmd/main.go", Acceptance: "compiles", Files: []string{"cmd/main.go"}},
		{ID: "T2", Title: "frontend page", Squad: "frontend", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "write web/App.tsx", Acceptance: "renders", Files: []string{"web/App.tsx"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	mu.Lock()
	tl := append([]string(nil), timeline...)
	mu.Unlock()
	ticketAt, wave2At, waves := -1, -1, 0
	for i, s := range tl {
		if strings.HasPrefix(s, "log: wave:") {
			waves++
			if waves == 2 && wave2At < 0 {
				wave2At = i
			}
		}
		if ticketAt < 0 && strings.Contains(s, "raised a correction ticket for team backend") {
			ticketAt = i
		}
	}
	if ticketAt < 0 {
		t.Fatalf("no correction ticket was raised for the red backend half:\n%s", strings.Join(tl, "\n"))
	}
	if wave2At < 0 {
		t.Fatalf("the board never announced a second wave:\n%s", strings.Join(tl, "\n"))
	}
	if ticketAt > wave2At {
		t.Fatalf("the backend ticket (line %d) was raised AFTER wave 2 was announced (line %d):\n%s",
			ticketAt, wave2At, strings.Join(tl, "\n"))
	}

	var ticket *plan.Task
	for i := range board.Tasks {
		if board.Tasks[i].Squad == "backend" && board.Tasks[i].ID != "T1" {
			ticket = &board.Tasks[i]
		}
	}
	if ticket == nil {
		t.Fatalf("the ticket is not on the board: %+v", board.Tasks)
	}
	// The gate is on the record, red, for the UI.
	var backend TeamGate
	for _, g := range o.TeamGates() {
		if g.Team == "backend" {
			backend = g
		}
	}
	if !backend.Ran || backend.OK {
		t.Fatalf("backend gate = %+v, want proved RED", backend)
	}
}

// The between-wave gate proves a lane once per state of that lane: not before
// the team wrote anything in its territory, not again while nothing in it has
// moved, and not for a team whose lane is still open.
func TestEarlyTeamGateProvesEachLaneStateOnce(t *testing.T) {
	var ran []string
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		ran = append(ran, cmd)
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})
	ctx := context.Background()
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone, Status: "done", Files: []string{"cmd/main.go"}},
		{ID: "T2", Squad: "frontend", Column: plan.ColReadyToDev, Files: []string{"web/App.tsx"}},
	}}

	// Complete, but nothing written in its territory yet: nothing to prove.
	o.earlyTeamGate(ctx, board)
	if len(ran) != 0 {
		t.Fatalf("proved a lane the run never wrote to: %v", ran)
	}

	o.noteChangedFiles("cmd/main.go")
	o.earlyTeamGate(ctx, board)
	o.earlyTeamGate(ctx, board)
	if len(ran) != 1 || ran[0] != "go test ./..." {
		t.Fatalf("backend proved %d time(s) on one lane state (%v), want once", len(ran), ran)
	}

	// The frontend's lane is still open: never proved early.
	o.noteChangedFiles("web/App.tsx")
	o.earlyTeamGate(ctx, board)
	if len(ran) != 1 {
		t.Fatalf("a team with open work was proved: %v", ran)
	}

	// The backend lane moved: proved again — through the memo, so the
	// finish-path gate on the same tree costs no further run.
	o.noteChangedFiles("cmd/other.go")
	o.earlyTeamGate(ctx, board)
	if len(ran) != 2 {
		t.Fatalf("a moved lane was not re-proved: %v", ran)
	}
	board.Tasks[1].Column, board.Tasks[1].Status = plan.ColDone, "done"
	o.runTeamAcceptance(ctx, board)
	if len(ran) != 3 { // backend from the memo, frontend for the first time
		t.Fatalf("finish path ran %d command(s) in total, want 3: %v", len(ran), ran)
	}
}

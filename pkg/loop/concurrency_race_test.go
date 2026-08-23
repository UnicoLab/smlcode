package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// streamingExec answers concurrently and observes the backends token-sink
// registry from inside the call — which is where a real streaming provider
// reads it — so the tee registry, the event sink, the per-task budget and the
// board writes are all exercised against each other.
type streamingExec struct {
	root     string
	mu       sync.Mutex
	seen     map[string]int
	maxSinks int
}

func (e *streamingExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		e.mu.Lock()
		if e.seen == nil {
			e.seen = map[string]int{}
		}
		e.seen[req.AgentID]++
		e.mu.Unlock()
		// A real provider reads the registry from the inference goroutine.
		n := backends.TokenSinkCount()
		e.mu.Lock()
		if n > e.maxSinks {
			e.maxSinks = n
		}
		e.mu.Unlock()
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID, Output: e.answer(req),
		}
	}
	return out, nil
}

// answer plays every role the loop drives: the worker really writes its focus
// file (so the disk-evidence gates see a delta), the reviewer really returns a
// review verdict.
func (e *streamingExec) answer(req ggagent.SubAgentRequest) string {
	switch req.AgentID {
	case plan.RoleReviewer, "reviewer-strict":
		return `{"approved":true,"score":90,"summary":"looks good","issues":[]}`
	}
	f := fileForTask(req.TaskID)
	if e.root != "" && req.TaskID != "" {
		_ = os.WriteFile(filepath.Join(e.root, f),
			[]byte("package p\n\nfunc F() { println(\""+req.TaskID+"\") }\n"), 0o644)
	}
	return `{"status":"done","files_changed":["` + f + `"]}`
}

func fileForTask(id string) string { return strings.ToLower(id) + ".go" }

// A full default-configuration board run under -race: 4 parallel workers, the
// parallel self-critique path, the speculative review race, live token
// streaming into a shared event sink, and the board/LiveStore underneath.
func TestConcurrentBoardRunIsRaceFree(t *testing.T) {
	backends.ResetTokenSinks()
	root := t.TempDir()
	var tasks []plan.Task
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("T%d", i)
		f := fileForTask(id)
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, plan.Task{
			ID: id, Title: "edit " + f, Description: "modify " + f,
			Acceptance: "compiles", Role: plan.RoleWorker,
			Column: plan.ColReadyToDev, Files: []string{f},
		})
	}
	board := &plan.Board{Tasks: tasks}
	store := plan.NewLiveStore(filepath.Join(root, ".slmcode"))
	if err := store.Replace(*board); err != nil {
		t.Fatal(err)
	}

	var evMu sync.Mutex
	var events int
	exec := &streamingExec{root: root}
	r := NewRunner(exec, ggagent.NewSharedState())
	r.Root = root
	r.Store = store
	r.MaxParallel = 4
	r.ReviewParallel = true
	r.WorkerCritique = true
	r.Timeout = 5 * time.Second
	r.IdleWait = time.Millisecond
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.Log = func(string, ...interface{}) {}
	r.OnEventFull = func(ev LoopEvent) {
		evMu.Lock()
		events++
		evMu.Unlock()
		if ev.Kind == stream.KindToken && ev.TaskID == "" && ev.Agent == "" {
			t.Error("token event with no attribution")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.RunBoard(ctx, board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	evMu.Lock()
	got := events
	evMu.Unlock()
	if got == 0 {
		t.Fatal("no events emitted — the sinks were never reached")
	}
	if n := backends.TokenSinkCount(); n != 0 {
		t.Fatalf("%d token sink(s) leaked after the run", n)
	}
	exec.mu.Lock()
	maxSinks := exec.maxSinks
	exec.mu.Unlock()
	if maxSinks == 0 {
		t.Fatal("no token sink was ever registered while an agent was running")
	}
	for _, task := range board.Tasks {
		if task.Column != plan.ColDone {
			t.Errorf("%s ended in %q (%s) — a clean board must finish", task.ID, task.Column, task.Review)
		}
	}
}

// MaxParallel <= 0 must mean "one at a time", not "no tasks at all".
func TestZeroMaxParallelStillRunsTheBoard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "t1.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "edit t1.go", Description: "modify t1.go",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"t1.go"},
	}}}
	r := NewRunner(&streamingExec{root: root}, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 0 // zero-valued / un-normalized config
	r.Timeout = 5 * time.Second
	r.IdleWait = time.Millisecond
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.Log = func(string, ...interface{}) {}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.RunBoard(ctx, board); err != nil {
		t.Fatalf("RunBoard with MaxParallel=0: %v", err)
	}
	if got, _ := board.Get("T1"); got.Column != plan.ColDone {
		t.Fatalf("T1 ended in %q — MaxParallel=0 starved the wave", got.Column)
	}
}

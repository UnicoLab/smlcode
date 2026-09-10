package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// agentStarts lists the agents a recorder saw start.
func (r *recorder) agentStarts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if e.Kind == stream.KindAgentStart {
			out = append(out, e.Agent)
		}
	}
	return out
}

func containsAgent(starts []string, name string) bool {
	for _, s := range starts {
		if strings.Contains(s, name) {
			return true
		}
	}
	return false
}

// A run whose board drained with no lessons, no changed files and no failed
// tasks has nothing for the distiller to distill — and says so instead of
// spending a model call to be told the same.
func TestMemoryPhaseIsSkippedWhenThereIsNothingToDistill(t *testing.T) {
	exec := &countingExec{}
	o := objectiveOrch(t, &fakeGate{ok: true}, exec, nil)
	o.pipe.Phases["memory"] = pipeline.PhaseSpec{When: pipeline.WhenAlways}
	o.resetChangedFiles() // the fixture pretends a file changed; this run wrote nothing
	rec := &recorder{}
	o.onEvent = rec.handle

	run := func() {
		t.Helper()
		if _, err := o.completeRun(context.Background(), "run-objective", "implement the thing",
			"", doneBoard(), "", false, false, objectiveCmd, time.Now()); err != nil {
			t.Fatalf("completeRun: %v", err)
		}
	}

	run()
	if containsAgent(rec.agentStarts(), "memory") {
		t.Fatalf("memory agent started on a run with nothing to distill: %v", rec.agentStarts())
	}
	if exec.n() != 0 {
		t.Fatalf("%d model call(s) were spent: %s", exec.n(), exec.roles())
	}
	if !strings.Contains(rec.text(), "memory distillation skipped") {
		t.Fatal("the skip must be announced so the Live view can show it")
	}

	// The same run with one changed file has something to remember.
	o.noteChangedFiles("stats.go")
	run()
	if !containsAgent(rec.agentStarts(), "memory") {
		t.Fatalf("memory agent did not start once a file had changed: %v", rec.agentStarts())
	}
	if !strings.Contains(exec.roles(), "memory") {
		t.Fatalf("the distiller was announced but never called: %s", exec.roles())
	}
}

// A one-step plan over one known file is one task: the splitter — the slowest
// role after the planner — is skipped and the board is built directly, with
// the known file as the task's scope.
func TestOneStepPlanSkipsTheSplitter(t *testing.T) {
	exec := &countingExec{}
	o := objectiveOrch(t, &fakeGate{ok: true}, exec, nil)
	if err := os.WriteFile(filepath.Join(o.cfg.Root, "stats.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	o.onEvent = rec.handle
	in := planSplitInput{
		RunID: "run-objective", Query: "add a greeting to stats.go",
		Inventory: []string{"stats.go"}, Discovered: []string{"stats.go"},
	}

	board, err := o.runSplitPhase(context.Background(), in,
		plan.Plan{Summary: "greet", Steps: []string{"Add a greeting to stats.go"}}, `{"steps":["x"]}`, nil)
	if err != nil {
		t.Fatalf("runSplitPhase: %v", err)
	}
	if containsAgent(rec.agentStarts(), "splitter") || exec.n() != 0 {
		t.Fatalf("the splitter ran on a one-step plan: starts=%v calls=%s", rec.agentStarts(), exec.roles())
	}
	if !strings.Contains(rec.text(), "splitter skipped") {
		t.Fatal("the skip must be announced so the Live view can show it")
	}
	if len(board.Tasks) != 1 {
		t.Fatalf("board has %d task(s), want the plan's one step: %+v", len(board.Tasks), board.Tasks)
	}
	got := board.Tasks[0]
	if got.Column != plan.ColReadyToDev || strings.Join(got.Files, ",") != "stats.go" {
		t.Fatalf("the one task must be ready with the known file as scope, got column=%q files=%v notes=%q",
			got.Column, got.Files, got.Notes)
	}

	// Two steps are a real split: the splitter is asked.
	rec2 := &recorder{}
	o.onEvent = rec2.handle
	if _, err := o.runSplitPhase(context.Background(), in,
		plan.Plan{Summary: "two", Steps: []string{"one", "two"}}, `{"steps":["one","two"]}`, nil); err != nil {
		t.Fatalf("runSplitPhase: %v", err)
	}
	if !containsAgent(rec2.agentStarts(), "splitter") {
		t.Fatalf("a two-step plan must still be split by the splitter: %v", rec2.agentStarts())
	}
}

// After a wave, the coordinator and the wave distiller run only when the wave
// gave them something: a failure, an escalation or a lesson.
func TestQuietWavesAreNotEventful(t *testing.T) {
	// Realistic green tasks: an acceptance and an output, which the extractor
	// turns into routine "success"/"convention" lessons that must NOT count.
	quiet := []plan.Task{
		{ID: "T1", Title: "a", Column: plan.ColDone, Status: "done", Acceptance: "file exists",
			Output: `{"status":"done","summary":"wrote a.go","files_changed":["a.go"]}`},
		{ID: "T2", Title: "b", Column: plan.ColDone, Status: "done", Acceptance: "tests pass",
			Output: `{"status":"done","summary":"wrote b.go","files_changed":["b.go"]}`},
	}
	if ok, why := waveEventful(quiet); ok || !strings.Contains(why, "green") {
		t.Fatalf("a green wave with nothing learned is eventful: %v %q", ok, why)
	}
	if ok, _ := waveEventful(nil); ok {
		t.Fatal("an empty wave is eventful")
	}
	failed := append([]plan.Task{}, quiet...)
	failed = append(failed, plan.Task{ID: "T3", Column: plan.ColBlocked, Error: "build failed"})
	if ok, _ := waveEventful(failed); !ok {
		t.Fatal("a wave with a blocked task is not eventful")
	}
	escalated := append([]plan.Task{}, quiet...)
	escalated = append(escalated, plan.Task{ID: "T4", Column: plan.ColToScope, Notes: "escalated after max retries"})
	if ok, _ := waveEventful(escalated); !ok {
		t.Fatal("a wave with an escalated task is not eventful")
	}
	// A human note the task honored is worth remembering; a harness
	// bookkeeping marker in Notes is not.
	learned := []plan.Task{{ID: "T5", Title: "c", Column: plan.ColDone, Status: "done",
		Notes: "Keep the encoder order: set Content-Type before writing the body."}}
	if ok, _ := waveEventful(learned); !ok {
		t.Fatal("a wave that honored a human note is not eventful")
	}
	bookkeeping := []plan.Task{{ID: "T6", Title: "d", Column: plan.ColDone, Status: "done",
		Notes: "correction-key: tester|x|a.go\ncorrection-attempt: 1"}}
	if ok, _ := waveEventful(bookkeeping); ok {
		t.Fatal("harness bookkeeping in Notes made a wave eventful")
	}
}

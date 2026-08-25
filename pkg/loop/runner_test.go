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

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type countingExec struct {
	mu    sync.Mutex
	calls int
}

// count records one dispatch. The wave now dispatches one request per task
// concurrently (each under its own workspace task ctx), so these counters are
// written from several goroutines.
func (e *countingExec) count() {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
}

func (e *countingExec) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *countingExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.count()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID,
			TaskID:  req.TaskID,
			Output:  `{"status":"done","summary":"updated file","files_changed":["a.go"]}`,
		}
	}
	return out, nil
}

type timeoutExec struct {
	mu    sync.Mutex
	calls int
}

func (e *timeoutExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return nil, context.DeadlineExceeded
}

func (e *timeoutExec) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestStripScopedPack(t *testing.T) {
	in := "# Scoped context for role=worker\n\nbig pack\n\n## Task instructions\n\nDo the thing\n"
	got := StripScopedPack(in)
	if got != "Do the thing" {
		t.Fatalf("got %q", got)
	}
}

func TestFeedbackSection(t *testing.T) {
	r := &Runner{}
	if got := r.feedbackSection(); got != "" {
		t.Fatalf("no-feedback section = %q", got)
	}
	r.Feedback = func() string { return "  prefer smaller diffs  " }
	section := r.feedbackSection()
	if !strings.Contains(section, "## LIVE FEEDBACK FROM USER") {
		t.Fatalf("missing header in %q", section)
	}
	if !strings.Contains(section, "prefer smaller diffs") {
		t.Fatalf("missing text in %q", section)
	}
	r.Feedback = func() string { return "   " }
	if got := r.feedbackSection(); got != "" {
		t.Fatalf("blank feedback section = %q", got)
	}
}

func TestTaskInputAppendsFeedback(t *testing.T) {
	r := NewRunner(nil, nil)
	task := plan.Task{ID: "T1", Title: "x", Role: plan.RoleWorker, Description: "do it", Acceptance: "done"}
	base := r.taskInput(task)
	r.Feedback = func() string { return "use log/slog" }
	got := r.taskInput(task)
	if !strings.HasPrefix(got, base) {
		t.Fatalf("base prompt changed: base=%q got=%q", base, got)
	}
	if !strings.HasSuffix(got, "use log/slog\n") {
		t.Fatalf("feedback not appended: %q", got)
	}
}

func TestRunCorrectiveBoardRespectsMaxWaves(t *testing.T) {
	exec := &countingExec{}
	r := NewRunner(exec, nil)
	r.Root = t.TempDir()
	r.MaxRetries = 0
	r.MaxWaves = 1
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.ReviewParallel = false
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "edit", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update a.go", Files: []string{"a.go"},
	}}}

	ran, err := r.RunCorrectiveBoard(context.Background(), board)
	if err != nil || !ran {
		t.Fatalf("first corrective run: ran=%v err=%v", ran, err)
	}
	firstCalls := exec.callCount()
	if firstCalls == 0 {
		t.Fatal("executor was not called")
	}

	board.Tasks[0].MoveTo(plan.ColReadyToDev)
	ran, err = r.RunCorrectiveBoard(context.Background(), board)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("second corrective wave should be skipped")
	}
	if exec.callCount() != firstCalls {
		t.Fatalf("executor calls changed after skipped wave: before=%d after=%d", firstCalls, exec.callCount())
	}
}

func TestRunBoardTimeoutsDoNotInterruptOrLeaveTasksInProgress(t *testing.T) {
	root := t.TempDir()
	exec := &timeoutExec{}
	r := NewRunner(exec, nil)
	r.Root = root
	r.MaxParallel = 3
	r.MaxRetries = 0
	r.Timeout = time.Second
	r.IdleWait = time.Millisecond
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.FailureHandler = NewEnhancedFailureHandler(root)

	var interventions int
	r.OnEvent = func(kind, agent, taskID, message, scope, output string) {
		if kind == "intervention" && scope == "timeout" {
			interventions++
		}
	}

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "one", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"}},
		{ID: "T2", Title: "two", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"b.go"}},
		{ID: "T3", Title: "three", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"c.go"}},
	}}

	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("timeout wave should be recoverable, got %v", err)
	}
	// One dispatch PER TASK: a wave carries one workspace task id per call, so
	// the three tasks cannot share (and trip) one loop-guard bucket.
	if got := exec.callCount(); got != 3 {
		t.Fatalf("executor calls=%d, want 3 (one per task)", got)
	}
	if interventions != 3 {
		t.Fatalf("timeout interventions=%d, want 3", interventions)
	}
	for _, task := range board.Tasks {
		if task.Column == plan.ColInProgress {
			t.Fatalf("%s left in progress: %+v", task.ID, task)
		}
		if task.Column != plan.ColToScope {
			t.Fatalf("%s column=%s, want %s", task.ID, task.Column, plan.ColToScope)
		}
		if !strings.Contains(strings.ToLower(task.Error), "timed out") {
			t.Fatalf("%s missing timeout error: %q", task.ID, task.Error)
		}
	}
	lessons, err := os.ReadFile(filepath.Join(root, ".slmcode", "errors", "wave_lessons.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lessons), "context deadline exceeded") {
		t.Fatalf("wave lessons missing timeout detail:\n%s", lessons)
	}
}

func TestFormatReviewPromptAppendsFeedback(t *testing.T) {
	r := NewRunner(nil, nil)
	task := plan.Task{ID: "T1", Title: "x", Role: plan.RoleWorker, Acceptance: "a", Output: "out"}
	base := r.formatReviewPrompt(task)
	r.Feedback = func() string { return "check edge cases" }
	got := r.formatReviewPrompt(task)
	if !strings.HasPrefix(got, base) {
		t.Fatalf("review prompt changed: base=%q got=%q", base, got)
	}
	if !strings.Contains(got, "check edge cases") {
		t.Fatalf("feedback missing from review prompt: %q", got)
	}
}

func TestHasToolWriteEvidence(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"ws_edit tool name", "Observation: ws_edit updated pkg/loop/runner.go", true},
		{"ws_edit result line", "Observation: edited hello.go (1 replacement(s))", true},
		{"ws_mv result line", "Observation: moved pkg/old.go → pkg/new.go", true},
		{"dry-run staging", "dry-run: would write main.py (238 bytes)", true},
		{"json only", `{"status":"done","files_changed":["x.go"]}`, false},
		// The whole point of defect #2: a worker that touched nothing but
		// narrated an edit used to be credited with a write, which auto-approved
		// the task and skipped the reviewer entirely.
		{"prose: updated", `{"status":"done","summary":"Updated the parser to handle comments","files_changed":["pkg/x/parser.go"]}`, false},
		{"prose: wrote", "I wrote a new helper function for you.", false},
		{"prose: patched", "I patched the config loader.", false},
		{"prose: edited without tool shape", "I edited the file to add validation.", false},
		// The Disk evidence section is authored by the harness, so counting it
		// as TOOL evidence was a self-reference.
		{"harness disk section", "## Disk evidence\n- modified: hello.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasToolWriteEvidence(tc.output); got != tc.want {
				t.Fatalf("hasToolWriteEvidence(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}

// The "comment" branch of alreadySatisfied returned true for any focus file
// containing a "//" line and a "func "/"def " — i.e. nearly every Go file — so
// "Add doc comments and validate the input" skipped the worker entirely.
func TestAlreadySatisfiedNoLongerFiresOnCommentKeyword(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	body := "package main\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hi\" }\n"
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"hello.go": fileFingerprint(target)}

	cases := []struct {
		name string
		task plan.Task
	}{
		{"doc comment only", plan.Task{
			ID: "T1", Title: "Add doc comment to Hello()", Files: []string{"hello.go"},
			Acceptance: "doc comment above Hello()",
		}},
		{"comment plus real work", plan.Task{
			ID: "T2", Title: "Add doc comments and validate the input",
			Files: []string{"hello.go"}, Acceptance: "input validated",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if alreadySatisfied(dir, tc.task, base) {
				t.Fatal("a comment keyword must never satisfy a task on its own")
			}
		})
	}
}

// alreadySatisfiedRetry is the only predicate allowed to skip the worker LLM,
// and it must never fire on a first execution.
func TestAlreadySatisfiedRetryGate(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	target := filepath.Join(dir, "src", "cfg.py")
	if err := os.WriteFile(target, []byte("def load():\n    return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: dir}
	base := map[string]string{"src/cfg.py": ""} // absent at wave start

	task := plan.Task{
		ID: "T1", Title: "Create src/cfg.py", Description: "scaffold config loader",
		Files: []string{"src/cfg.py"}, Acceptance: "src/cfg.py exists",
	}
	if r.alreadySatisfiedRetry(task, base) {
		t.Fatal("first execution must never be skipped")
	}
	task.Retries = 1
	if !r.alreadySatisfiedRetry(task, base) {
		t.Fatal("a retry whose target this wave created should short-circuit")
	}
}

func TestAlreadySatisfiedCreateBranch(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "src", "lg_agent"), 0o755)
	graph := filepath.Join(dir, "src", "lg_agent", "graph.py")
	real := "def create_graph():\n    return {\"nodes\": []}\n"
	stub := "def create_graph():\n    # Placeholder implementation\n    return {\"output\": \"run_result\"}\n"

	createTask := plan.Task{
		ID: "T2", Title: "Create agent module", Description: "scaffold src/lg_agent/graph.py",
		Files: []string{"src/lg_agent/graph.py"}, Acceptance: "file created",
	}
	// "Create a helper to parse config in pkg/util/cfg.go" is the exact shape a
	// planner SLM emits for a task that still needs real work.
	helperTask := plan.Task{
		ID: "T3", Title: "Create a helper to parse config in hello.go",
		Files: []string{"hello.go"}, Acceptance: "helper present",
	}

	cases := []struct {
		name     string
		body     string
		task     plan.Task
		baseline map[string]string
		want     bool
	}{
		{
			name: "implement never satisfied by mere existence", body: real,
			task: plan.Task{
				ID: "T1", Title: "Edit hello", Description: "implement comment",
				Files: []string{"hello.go"}, Acceptance: "file updated",
			},
			baseline: map[string]string{"hello.go": "len:hash"}, want: false,
		},
		{
			name: "scaffold created during this wave", body: real, task: createTask,
			baseline: map[string]string{"src/lg_agent/graph.py": ""}, want: true,
		},
		{
			name: "file that already existed at wave start is NOT evidence",
			body: real, task: createTask,
			baseline: map[string]string{"src/lg_agent/graph.py": "len:hash"}, want: false,
		},
		{
			name: "unknown baseline falls back to existence", body: real, task: createTask,
			baseline: nil, want: true,
		},
		{
			name: "placeholder stub never satisfies", body: stub, task: createTask,
			baseline: map[string]string{"src/lg_agent/graph.py": ""}, want: false,
		},
		{
			name: "create-a-helper against a pre-existing file", body: real, task: helperTask,
			baseline: map[string]string{"hello.go": "len:hash"}, want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(graph, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := alreadySatisfied(dir, tc.task, tc.baseline); got != tc.want {
				t.Fatalf("alreadySatisfied = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReviewBaselineUsesPreWaveSnapshot(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Add doc comment", Description: "add doc comment",
		Acceptance: "comment present", Files: []string{"hello.go"},
		Output: `{"status":"done","files_changed":["hello.go"],"summary":"added comment"}`,
	}
	baseline := r.snapshotTargets(task)
	_ = os.WriteFile(target, []byte("package main\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	if !r.hasRealWriteEvidence(task, baseline) {
		t.Fatal("pre-wave baseline should detect disk modification")
	}
	// Re-baselining after the write (the old bug) hides the change.
	post := r.snapshotTargets(task)
	if r.hasRealWriteEvidence(task, post) {
		t.Fatal("post-write baseline should not show a content change")
	}
}

func TestEvidenceOKRequiresDiskOrTool(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"},
		Output: `{"status":"done","files_changed":["hello.go"],"summary":"claimed"}`,
	}
	baseline := map[string]string{"hello.go": fileFingerprint(target)}
	if ok, why := r.evidenceOK(task, baseline); ok {
		t.Fatalf("expected reject without real change, why=%s", why)
	}
	// mutate file → disk evidence passes
	_ = os.WriteFile(target, []byte("package main\n// hi\n"), 0o644)
	if ok, why := r.evidenceOK(task, baseline); !ok {
		t.Fatalf("expected pass after disk change: %s", why)
	}
}

func TestHasRealWriteEvidenceTrustsDiskSectionWhenBaselineAmbiguous(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n// done\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"},
		Output: "worker mumbled\n\n## Disk evidence\n- modified: hello.go\n",
	}
	// Ambiguous: nil baseline (late snapshot / lost fingerprints)
	if !r.hasRealWriteEvidence(task, nil) {
		t.Fatal("disk evidence section must count even with nil baseline")
	}
	// Ambiguous: empty map
	if !r.hasRealWriteEvidence(task, map[string]string{}) {
		t.Fatal("disk evidence section must count with empty baseline")
	}
	// Ambiguous: baseline fingerprints missing for focus files
	if !r.hasRealWriteEvidence(task, map[string]string{"other.go": "1:2"}) {
		t.Fatal("disk evidence section must count when focus missing from baseline")
	}
	// Auto-approve path via evidenceOK
	if ok, why := r.evidenceOK(task, nil); !ok {
		t.Fatalf("evidenceOK should pass with disk section: %s", why)
	}
}

func TestEvidenceOKCreateSatisfiedWithoutDelta(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname=\"lg-agent\"\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Create pyproject.toml", Description: "scaffold deps",
		Acceptance: "pyproject.toml exists", Files: []string{"pyproject.toml"},
		Output: "I can see there's already a pyproject.toml file",
	}
	// Absent at wave start, present now → this wave created it.
	if ok, why := r.evidenceOK(task, map[string]string{"pyproject.toml": ""}); !ok {
		t.Fatalf("file created during this wave should pass: %s", why)
	}
	// Present and byte-identical at wave start → nobody wrote anything, so the
	// evidence gate must not be satisfied by the file merely existing.
	stale := map[string]string{"pyproject.toml": fileFingerprint(filepath.Join(dir, "pyproject.toml"))}
	if ok, _ := r.evidenceOK(task, stale); ok {
		t.Fatal("a file that already existed unchanged is not write evidence")
	}
}

func TestReviewFastPathSkipsExecutor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	r := &Runner{Root: dir, Executor: nil} // nil executor must never be called
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"}, Role: plan.RoleWorker,
		Column: plan.ColInReview,
		Output: "done\n\n## Disk evidence\n- modified: hello.go\n",
	}
	baseline := map[string]string{"hello.go": "1:1"} // stale; disk section still counts
	board := &plan.Board{Tasks: []plan.Task{task}}
	// Mutate so hasRealWriteEvidence also sees a delta vs fingerprint-less baseline path
	_ = os.WriteFile(target, []byte("package main\n\n// Hello greets.\nfunc Hello() string { return \"hi\" }\n"), 0o644)

	err := r.reviewAndCorrect(context.Background(), board, task, baseline)
	if err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "skip reviewer LLM") {
		t.Fatalf("expected review fast-path log, got: %s", joined)
	}
	got, ok := board.Get("T1")
	if !ok {
		t.Fatal("missing task")
	}
	if got.Column != plan.ColDone && !strings.Contains(strings.ToLower(got.Review), "auto-approved") {
		// Done column is ideal; at minimum review summary must show auto-approve.
		if !strings.Contains(strings.ToLower(got.Review), "auto-approved") &&
			!strings.Contains(strings.ToLower(got.Review), "disk") {
			t.Fatalf("expected auto-approve review, col=%s review=%q", got.Column, got.Review)
		}
	}
}

func TestIncompleteFinalizeNudgeDetectsToolEndBlock(t *testing.T) {
	r := &Runner{QualityMonitor: true}
	reason, issue, need := r.incompleteFinalizeNudge(plan.Task{
		Output: `{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`,
	})
	if !need || reason != "ended_on_tool_call" {
		t.Fatalf("need=%v reason=%q", need, reason)
	}
	if !strings.Contains(issue, "STRICT") && !strings.Contains(issue, "status JSON") {
		t.Fatalf("issue=%q", issue)
	}
	_, _, needOK := r.incompleteFinalizeNudge(plan.Task{
		Output: `{"status":"done","summary":"ok","files_changed":["a.go"]}`,
	})
	if needOK {
		t.Fatal("done JSON must not need finish-steer")
	}
}

func TestErrorHandlerWritesErrorsMD(t *testing.T) {
	dir := t.TempDir()
	fh := NewEnhancedFailureHandler(dir)
	board := &plan.Board{}
	task := plan.Task{ID: "T9", Title: "x", Role: "worker", Retries: 2, Review: "no changes"}
	if err := fh.ReportTaskFailure(board, task, errString("review rejected after max retries"), 2); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".slmcode", "errors", "errors.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "T9") {
		t.Fatalf("errors.md missing task: %s", data)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// writingExec is a worker that actually writes its task's file, so the review
// fast path approves on disk evidence and no reviewer LLM is involved. It
// records which TASKS were dispatched, which is what "wave 2 never ran" is.
type writingExec struct {
	root string
	mu   sync.Mutex
	ids  []string
}

func (e *writingExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		e.mu.Lock()
		e.ids = append(e.ids, req.TaskID)
		n := len(e.ids)
		e.mu.Unlock()
		name := strings.ToLower(req.TaskID) + ".go"
		_ = os.WriteFile(filepath.Join(e.root, name),
			[]byte(fmt.Sprintf("package main\n\n// rev %d\n", n)), 0o644)
		out = append(out, ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: fmt.Sprintf("Observation: ws_edit edited %s (1 replacement(s))\n", name) +
				fmt.Sprintf(`{"status":"done","summary":"done","files_changed":[%q]}`, name),
		})
	}
	return out, nil
}

func (e *writingExec) dispatched() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ids...)
}

// fourTaskBoard is 4 independent tasks, the shape of the run that motivated the
// between-waves probe (4 tasks, parallel=2 → two waves).
func fourTaskBoard() *plan.Board {
	tasks := make([]plan.Task, 0, 4)
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		tasks = append(tasks, plan.Task{
			ID: id, Title: "build " + id, Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "write " + strings.ToLower(id) + ".go",
			Acceptance:  "file written",
			Files:       []string{strings.ToLower(id) + ".go"},
		})
	}
	return &plan.Board{QueryID: "q1", Tasks: tasks}
}

// TestBetweenWavesStopsTheBoardAndReportsWhatItAbandoned pins the loop half of
// the seam: a hook that says stop ends the board where it stands, without an
// error, and the runner remembers how much planned work that abandoned.
func TestBetweenWavesStopsTheBoardAndReportsWhatItAbandoned(t *testing.T) {
	root := t.TempDir()
	exec := &writingExec{root: root}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 2
	r.MaxRetries = 0
	probes := 0
	r.BetweenWaves = func(context.Context, *plan.Board) (bool, string) {
		probes++
		return true, "objective already met (go test ./... green)"
	}

	board := fourTaskBoard()
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("an early stop is not an error: %v", err)
	}
	if probes != 1 {
		t.Fatalf("BetweenWaves called %d time(s), want exactly 1 (after wave 1)", probes)
	}
	if got := r.Waves(); got != 1 {
		t.Fatalf("waves=%d, want 1 — wave 2 must not run", got)
	}
	if got := len(exec.dispatched()); got != 2 {
		t.Fatalf("dispatched %d task(s) (%v), want the 2 of wave 1",
			got, exec.dispatched())
	}
	stopped, reason, left := r.EarlyStop()
	if !stopped {
		t.Fatal("the runner does not report that it stopped early")
	}
	if !strings.Contains(reason, "objective already met") {
		t.Fatalf("reason=%q — the caller's reason must survive", reason)
	}
	if left != 2 {
		t.Fatalf("unexecuted=%d, want the 2 tasks that never ran", left)
	}
}

// TestBoardWithoutAnEarlyStopRunsToItsNormalBound is the no-behavior-change
// control: a hook that never says stop must leave RunBoard exactly as it was.
func TestBoardWithoutAnEarlyStopRunsToItsNormalBound(t *testing.T) {
	root := t.TempDir()
	exec := &writingExec{root: root}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 2
	r.MaxRetries = 0
	probes := 0
	r.BetweenWaves = func(context.Context, *plan.Board) (bool, string) {
		probes++
		return false, ""
	}

	board := fourTaskBoard()
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	if probes < 2 {
		t.Fatalf("BetweenWaves called %d time(s) — it must be asked after every wave", probes)
	}
	if got := len(exec.dispatched()); got != 4 {
		t.Fatalf("dispatched %d task(s) (%v), want all 4", got, exec.dispatched())
	}
	if stopped, _, left := r.EarlyStop(); stopped || left != 0 {
		t.Fatalf("EarlyStop reported stopped=%v left=%d on a board that ran to its bound", stopped, left)
	}
	if n := unexecutedTaskCount(board); n != 0 {
		t.Fatalf("%d task(s) left unexecuted on a board that ran to its bound", n)
	}
}

// TestBetweenWavesIsOffForCorrectiveBoards: a corrective board is entered
// BECAUSE something was found wanting, so the early stop must not route around
// the finding that scheduled it.
func TestBetweenWavesIsOffForCorrectiveBoards(t *testing.T) {
	root := t.TempDir()
	exec := &writingExec{root: root}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 2
	r.MaxRetries = 0
	r.MaxWaves = 2
	probes := 0
	r.BetweenWaves = func(context.Context, *plan.Board) (bool, string) {
		probes++
		return true, "objective already met"
	}

	board := fourTaskBoard()
	ran, err := r.RunCorrectiveBoard(context.Background(), board)
	if err != nil || !ran {
		t.Fatalf("RunCorrectiveBoard: ran=%v err=%v", ran, err)
	}
	if probes != 0 {
		t.Fatalf("the early stop was consulted %d time(s) on a corrective board", probes)
	}
	if got := len(exec.dispatched()); got != 4 {
		t.Fatalf("corrective board dispatched %d task(s), want all 4", got)
	}
	if stopped, _, _ := r.EarlyStop(); stopped {
		t.Fatal("a corrective board reported an early stop")
	}
	// The hook is restored, not destroyed: the next ordinary board still gets it.
	if r.BetweenWaves == nil || r.betweenWavesOff {
		t.Fatal("RunCorrectiveBoard did not restore the between-waves hook")
	}
}

func TestWavesForIsAFloorNotAGuess(t *testing.T) {
	cases := []struct{ n, perWave, want int }{
		{0, 2, 0}, {1, 2, 1}, {2, 2, 1}, {3, 2, 2}, {4, 2, 2}, {5, 0, 5},
	}
	for _, tc := range cases {
		if got := wavesFor(tc.n, tc.perWave); got != tc.want {
			t.Fatalf("wavesFor(%d,%d)=%d, want %d", tc.n, tc.perWave, got, tc.want)
		}
	}
}

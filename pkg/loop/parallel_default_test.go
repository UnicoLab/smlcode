package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// ── shared fake executor ────────────────────────────────────────────────────

// scriptedExec records every request and answers from a per-agent script. It is
// safe for concurrent use, which the default configuration (MaxParallel=4,
// ReviewParallel=true) needs and none of the pre-existing fakes provided.
type scriptedExec struct {
	mu     sync.Mutex
	reqs   []ggagent.SubAgentRequest
	answer func(req ggagent.SubAgentRequest, seq int) string
}

func (e *scriptedExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		e.mu.Lock()
		e.reqs = append(e.reqs, req)
		seq := len(e.reqs)
		e.mu.Unlock()
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID, Output: e.answer(req, seq),
		}
	}
	return out, nil
}

// promptsFor returns every prompt sent to an agent id.
func (e *scriptedExec) promptsFor(agentID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, r := range e.reqs {
		if r.AgentID == agentID {
			out = append(out, r.Input)
		}
	}
	return out
}

// countFor returns how many calls an agent id received.
func (e *scriptedExec) countFor(agentID string) int { return len(e.promptsFor(agentID)) }

// total returns how many calls were made in all.
func (e *scriptedExec) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.reqs)
}

// defaultRunner builds a Runner on the SHIPPED defaults: MaxParallel=4 and
// ReviewParallel=true. Every pre-existing loop test pinned MaxParallel to 1-3
// with ReviewParallel=false, which is exactly why the defects below survived.
func defaultRunner(t *testing.T, root string, exec SubAgentRunner) *Runner {
	t.Helper()
	r := NewRunner(exec, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 4
	r.ReviewParallel = true
	r.Timeout = 30 * time.Second
	r.IdleWait = time.Millisecond
	r.PostWorkerSmoke = false
	r.RequireSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.Log = func(string, ...interface{}) {}
	return r
}

// failingSmokeSection renders the exact section the harness appends when a
// deterministic smoke command fails.
func failingSmokeSection(cmd, detail string) string {
	return "\n\n" + quality.SmokeSectionHeader + "\n" + quality.SmokeFailedMarker +
		"\ncmd: " + cmd + "\n" + detail + "\n"
}

// ── defect 1: the wave must not mutate a COPY of the task ───────────────────

// TestParallelCritiqueCorrectorSeesWaveEvidence is the regression test for the
// wave operating on a value copy of the task.
//
// With MaxParallel >= 2 — i.e. on the default of 4 — a weak task is deferred to
// the parallel self-critique path. The wave loop used to accumulate the worker
// output, the disk-evidence hint and every gate section onto `t := needExec[j]`,
// a copy, which was then thrown away. The corrector was handed the PRE-run
// value of t.Output (empty on the first wave) and told to "fix smoke/static/
// claims failures" with zero failure detail.
func TestParallelCritiqueCorrectorSeesWaveEvidence(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		switch req.AgentID {
		case plan.RoleReviewer:
			return `{"approved":true,"score":90,"summary":"ok"}`
		case plan.RoleCorrector:
			return `{"status":"done","summary":"fixed","files_changed":["a.go"]}`
		default:
			// The worker's own smoke run failed: that detail is what the
			// corrector must be shown.
			return fmt.Sprintf(
				`{"status":"done","summary":"ws_edit done","files_changed":[%q]}`, req.TaskID+".go") +
				failingSmokeSection("go build ./"+req.TaskID, "undefined: helper"+req.TaskID)
		}
	}

	r := defaultRunner(t, root, exec)
	r.WorkerCritique = true
	r.MaxRetries = 0

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "a", Title: "edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update a.go", Files: []string{"a.go"}},
		{ID: "b", Title: "edit b", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update b.go", Files: []string{"b.go"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	prompts := exec.promptsFor(plan.RoleCorrector)
	if len(prompts) != 2 {
		t.Fatalf("expected one corrector pass per weak task, got %d", len(prompts))
	}
	joined := strings.Join(prompts, "\n=====\n")
	for _, want := range []string{
		quality.SmokeSectionHeader,        // the section itself survived the wave
		quality.SmokeFailedMarker,         // ...including its verdict
		"undefined: helpera",              // ...and task a's actual failure detail
		"undefined: helperb",              // ...and task b's
		"Fix compile/test failures shown", // the derived issue list
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("corrector prompt missing %q.\n--- prompts ---\n%s", want, joined)
		}
	}
	// Each corrector prompt must carry ITS OWN task's failure, not the other's.
	for _, p := range prompts {
		aDetail := strings.Contains(p, "undefined: helpera")
		bDetail := strings.Contains(p, "undefined: helperb")
		if aDetail == bDetail {
			t.Fatalf("corrector prompt mixed up tasks (a=%v b=%v):\n%s", aDetail, bDetail, p)
		}
	}
}

// TestParallelCritiqueKeepsWorkerOutputThroughReview asserts the corrector's
// answer no longer erases the worker's output and gate evidence before review.
func TestParallelCritiqueKeepsWorkerOutputThroughReview(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		switch req.AgentID {
		case plan.RoleReviewer:
			return `{"approved":true,"score":90,"summary":"ok"}`
		case plan.RoleCorrector:
			// Corrector output still fails smoke.
			return `{"status":"done","summary":"tried","files_changed":["a.go"]}` +
				failingSmokeSection("go build", "still broken")
		default:
			return `{"status":"done","summary":"ws_edit done","files_changed":["a.go"]}` +
				failingSmokeSection("go build", "undefined: helper")
		}
	}
	r := defaultRunner(t, root, exec)
	r.WorkerCritique = true
	r.MaxRetries = 0

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "a", Title: "edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update a.go", Files: []string{"a.go"}},
		{ID: "b", Title: "edit b", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update b.go", Files: []string{"b.go"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		got, ok := board.Get(id)
		if !ok {
			t.Fatalf("%s missing from board", id)
		}
		if !quality.SmokeFailedInOutput(got.Output) {
			t.Fatalf("%s lost its smoke evidence before review: %q", id, got.Output)
		}
		if got.Column == plan.ColDone {
			t.Fatalf("%s approved despite a failing smoke section", id)
		}
	}
}

// ── defect 4: parallel review must not touch the board from goroutines ──────

// TestParallelReviewDefaultConfigIsRaceFree drives the exact configuration that
// was never tested: ReviewParallel=true with MaxParallel=4 and enough disjoint
// tasks to produce four independent review groups. Under -race this used to be
// an unsynchronized concurrent read/append of plan.Board.Tasks; without -race
// it silently lost results to last-writer-wins persistence.
func TestParallelReviewDefaultConfigIsRaceFree(t *testing.T) {
	root := t.TempDir()
	ids := []string{"T1", "T2", "T3", "T4"}
	var tasks []plan.Task
	for _, id := range ids {
		name := strings.ToLower(id) + ".go"
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, plan.Task{
			ID: id, Title: "edit " + name, Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update " + name, Acceptance: name + " changed", Files: []string{name},
		})
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":true,"score":90,"summary":"ok"}`
		}
		// Real tool evidence so the evidence gate is satisfied.
		return fmt.Sprintf("Observation: ws_edit edited %s.go (1 replacement(s))\n", strings.ToLower(req.TaskID)) +
			fmt.Sprintf(`{"status":"done","summary":"done","files_changed":["%s.go"]}`, strings.ToLower(req.TaskID))
	}

	// A live store makes persist() run for real, so the whole-board
	// last-writer-wins write is exercised too.
	store := plan.NewLiveStore(filepath.Join(root, ".slmcode"))
	r := defaultRunner(t, root, exec)
	r.Store = store
	r.MaxRetries = 0

	board := &plan.Board{QueryID: "q1", Tasks: tasks}
	if err := store.Replace(*board); err != nil {
		t.Fatal(err)
	}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	if exec.countFor(plan.RoleReviewer) == 0 {
		t.Fatal("reviewer never ran — the parallel review path was not exercised")
	}
	for _, id := range ids {
		got, ok := board.Get(id)
		if !ok {
			t.Fatalf("%s vanished from the board (lost update)", id)
		}
		if got.Column != plan.ColDone {
			t.Fatalf("%s column=%s review=%q — a parallel group's result was dropped",
				id, got.Column, got.Review)
		}
	}
	// Every task must also have survived into the persisted store.
	snap := store.Snapshot()
	done := 0
	for _, tk := range snap.Tasks {
		if tk.Column == plan.ColDone {
			done++
		}
	}
	if done != len(ids) {
		t.Fatalf("persisted board has %d/%d done — last-writer-wins dropped results:\n%+v",
			done, len(ids), snap.Tasks)
	}
}

// ── defect 2: prose is not write evidence ───────────────────────────────────

// TestProseClaimDoesNotAutoApprove covers the hallucinated-edit path end to end
// on the default configuration: a worker that touched nothing but wrote
// "Updated the parser…" used to match the bare participle "updated ", set
// diskWrite, take the fast path, skip the reviewer LLM entirely and go Done.
func TestProseClaimDoesNotAutoApprove(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "parser.go")
	if err := os.WriteFile(target, []byte("package p\n\nfunc Parse() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":true,"score":95,"summary":"looks good"}`
		}
		return `{"status":"done","summary":"Updated the parser to skip comments","files_changed":["parser.go"]}`
	}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update the parser", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update parser.go to skip comments", Acceptance: "comments skipped",
		Files: []string{"parser.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	got, _ := board.Get("T1")
	if got.Column == plan.ColDone {
		t.Fatalf("a hallucinated edit was approved: review=%q", got.Review)
	}
	if !strings.Contains(strings.ToLower(got.Review), "evidence") {
		t.Fatalf("expected an evidence-gate rejection, got review=%q", got.Review)
	}
	// The file must be untouched — proving nothing actually happened.
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "func Parse() {}") {
		t.Fatalf("fixture changed unexpectedly: %v %q", err, data)
	}
}

// ── defect 3: unrelated repo dirt is not this task's evidence ───────────────

// TestDiskEvidenceIgnoresOutOfScopeGitDirt covers the case that is GUARANTEED
// after the first task in a wave writes anything: `git diff --name-only HEAD`
// reports a file that has nothing to do with this task, and every subsequent
// task used to inherit a "## Disk evidence" section, diskSection=true and a
// fast-path auto-approval with the reviewer skipped.
func TestDiskEvidenceIgnoresOutOfScopeGitDirt(t *testing.T) {
	root := initGitRepo(t, map[string]string{
		"mine.go":      "package p\n",
		"unrelated.go": "package p\n",
	})
	// A sibling task (or a human) dirtied an unrelated file.
	if err := os.WriteFile(filepath.Join(root, "unrelated.go"),
		[]byte("package p\n\nfunc Other() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: root}
	task := plan.Task{
		ID: "T1", Title: "Edit mine", Description: "implement something in mine.go",
		Acceptance: "mine.go updated", Files: []string{"mine.go"},
		Output: `{"status":"done","summary":"claimed","files_changed":["mine.go"]}`,
	}
	baseline := map[string]string{"mine.go": fileFingerprint(filepath.Join(root, "mine.go"))}

	hint := r.diskEvidenceHint(task, baseline)
	if strings.Contains(hint, "in-scope git change") {
		t.Fatalf("unrelated dirt reported as in-scope: %q", hint)
	}
	if !strings.Contains(hint, outOfScopeDirtyMarker) {
		t.Fatalf("out-of-scope dirt should still be reported (non-evidentially): %q", hint)
	}
	section := diskEvidenceHeader + "\n" + hint
	if hasDiskEvidenceSection(section) {
		t.Fatalf("out-of-scope dirt must not count as disk evidence: %q", section)
	}
	if hasToolWriteEvidence(section) {
		t.Fatalf("the harness's own section must not count as tool evidence: %q", section)
	}
	task.Output += "\n\n" + section
	if r.hasRealWriteEvidence(task, baseline) {
		t.Fatal("unrelated repo dirt must not prove this task wrote anything")
	}

	// Now dirty the task's OWN file: that is real evidence.
	if err := os.WriteFile(filepath.Join(root, "mine.go"),
		[]byte("package p\n\nfunc Mine() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hint = r.diskEvidenceHint(task, baseline)
	if !strings.Contains(hint, "modified: mine.go") {
		t.Fatalf("in-scope change not reported: %q", hint)
	}
	if !hasDiskEvidenceSection(diskEvidenceHeader + "\n" + hint) {
		t.Fatalf("in-scope change must count as disk evidence: %q", hint)
	}
}

// initGitRepo builds a committed git repository fixture.
func initGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable (%v): %s", err, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "init")
	return root
}

// ── defect 6: the per-task LLM call budget ──────────────────────────────────

// TestMaxTaskCallsEscalatesInsteadOfLooping pins the round-trip explosion.
// Worst case for one task used to be ~16 LLM calls before the test phase.
func TestMaxTaskCallsEscalatesInsteadOfLooping(t *testing.T) {
	cases := []struct {
		name      string
		maxCalls  int
		wantCalls int
	}{
		{"default budget", 0, DefaultMaxTaskCalls},
		{"tight budget", 3, 3},
		{"single call", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			exec := &scriptedExec{}
			exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
				if req.AgentID == plan.RoleReviewer {
					return `{"approved":false,"score":10,"summary":"no evidence","issues":["do it properly"]}`
				}
				return `{"status":"done","summary":"claimed again","files_changed":["a.go"]}`
			}
			r := defaultRunner(t, root, exec)
			r.MaxParallel = 1 // force the plain reviewer path, one task at a time
			r.ReviewParallel = false
			r.MaxRetries = 8 // the ladder would happily burn all of these
			r.MaxTaskCalls = tc.maxCalls

			var budgetEvents int
			r.OnEvent = func(kind, agent, taskID, message, scope, output string) {
				if strings.Contains(output, "max_task_calls=") {
					budgetEvents++
				}
			}
			board := &plan.Board{Tasks: []plan.Task{{
				ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
				Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
			}}}
			if err := r.RunBoard(context.Background(), board); err != nil {
				t.Fatalf("RunBoard: %v", err)
			}
			if got := exec.total(); got != tc.wantCalls {
				t.Fatalf("LLM calls = %d, want %d (budget=%d)", got, tc.wantCalls, tc.maxCalls)
			}
			if budgetEvents == 0 {
				t.Fatal("exhausting the budget must emit an event, not fail silently")
			}
			got, _ := board.Get("T1")
			if got.Column != plan.ColToScope {
				t.Fatalf("budget-exhausted task must escalate, column=%s", got.Column)
			}
			if !strings.Contains(got.Notes, "budget") {
				t.Fatalf("escalation notes should explain the budget: %q", got.Notes)
			}
		})
	}
}

// TestMaxTaskCallsBoundsTheDefaultParallelConfig covers the budget on the
// SHIPPED defaults, where review goes through the speculative race. A race
// fans out to several LLM slots but is one review attempt, so it must cost one
// budget unit — otherwise the configuration everyone actually runs would have
// been the one configuration with no budget at all.
func TestMaxTaskCallsBoundsTheDefaultParallelConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		switch req.AgentID {
		case plan.RoleReviewer, roleReviewerStrict:
			return `{"approved":false,"score":10,"summary":"no evidence","issues":["make a real edit"]}`
		default:
			return `{"status":"done","summary":"claimed again","files_changed":["a.go"]}`
		}
	}
	r := defaultRunner(t, root, exec) // MaxParallel=4, ReviewParallel=true
	r.MaxRetries = 8                  // the ladder alone would burn all of these
	r.MaxTaskCalls = 4

	var budgetEvents int
	r.OnEvent = func(kind, agent, taskID, message, scope, output string) {
		if strings.Contains(output, "max_task_calls=") {
			budgetEvents++
		}
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	// budget 4 = worker + review + correction + review, then escalate.
	if got := exec.countFor(plan.RoleCorrector); got != 1 {
		t.Fatalf("corrector ran %d times on a 4-call budget, want 1", got)
	}
	if budgetEvents == 0 {
		t.Fatal("the default configuration must also report budget exhaustion")
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColToScope {
		t.Fatalf("budget-exhausted task must escalate, column=%s", got.Column)
	}
}

// TestSelfCritiqueIsCappedAtOnePass asserts the pre-review ladder no longer
// escalates itself to min(max(MaxRetries,3),4) passes on a smoke failure.
func TestSelfCritiqueIsCappedAtOnePass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		switch req.AgentID {
		case plan.RoleReviewer:
			return `{"approved":true,"score":90,"summary":"ok"}`
		default:
			// Never improves: the old code would loop until MaxRetries.
			return `{"status":"done","summary":"attempt","files_changed":["a.go"]}` +
				failingSmokeSection("go build", "still broken")
		}
	}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.ReviewParallel = false
	r.WorkerCritique = true
	r.MaxRetries = 4
	r.MaxTaskCalls = 99 // do not let the budget be what caps this

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	// Exactly one pre-review critique call. The review ladder owns the rest.
	critiquePrompts := 0
	for _, p := range exec.promptsFor(plan.RoleCorrector) {
		if strings.Contains(p, "## Review summary\nworker self-critique") {
			critiquePrompts++
		}
	}
	if critiquePrompts != 1 {
		t.Fatalf("pre-review self-critique ran %d times, want exactly 1", critiquePrompts)
	}
}

// TestCorrectorPromptDiffersPerAttempt asserts the corrector is not handed the
// identical generic prompt every round — the shape that makes a small model
// repeat its previous answer verbatim.
func TestCorrectorPromptDiffersPerAttempt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, seq int) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":false,"score":10,"summary":"no evidence","issues":["make a real edit"]}`
		}
		return fmt.Sprintf(`{"status":"done","summary":"attempt %d","files_changed":["a.go"]}`, seq)
	}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.ReviewParallel = false
	r.MaxRetries = 4
	r.MaxTaskCalls = 6

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	prompts := exec.promptsFor(plan.RoleCorrector)
	if len(prompts) < 2 {
		t.Fatalf("need at least two corrector passes to compare, got %d", len(prompts))
	}
	for i := 1; i < len(prompts); i++ {
		if prompts[i] == prompts[i-1] {
			t.Fatalf("corrector prompt %d is byte-identical to prompt %d", i, i-1)
		}
	}
	if !strings.Contains(prompts[len(prompts)-1], "Already tried and FAILED") {
		t.Fatalf("later corrector prompts must name what was already tried:\n%s", prompts[len(prompts)-1])
	}
}

// ── supporting unit tests ───────────────────────────────────────────────────

func TestStripPostSectionsRemovesEveryHarnessSection(t *testing.T) {
	core := `{"status":"done","summary":"ok","files_changed":["a.go"]}`
	cases := []struct {
		name    string
		section string
	}{
		{"disk evidence", "\n\n" + diskEvidenceHeader + "\n- modified: a.go\n"},
		{"deterministic smoke", failingSmokeSection("go build", "boom")},
		{"acceptance smoke", "\n\n" + quality.AcceptanceSectionHeader + "\nFAILED\ncmd: go test\n"},
		// These two never matched: the list looked for "## Static quality" and
		// "## Claims gate", but the emitted headers are "## Static quality gate"
		// and "## Claimed files gate".
		{"static quality gate", "\n\n" + staticGateHeader + "\nFAILED\n- a.go: stub\n"},
		{"claimed files gate", "\n\n" + claimsGateHeader + "\nFAILED\n- missing: b.go\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripPostSections(core + tc.section)
			if got != core {
				t.Fatalf("stripPostSections left harness markdown attached:\n%q", got)
			}
			if strings.Contains(got, "FAILED") {
				t.Fatal("the literal word FAILED leaked into the model-answer view")
			}
		})
	}
}

// TestStripPostSectionHeadersMatchQuality guards the emitted-vs-expected
// contract. The literals that used to live in this package are gone (the
// constants now alias pkg/quality's), so what is left to guard is that the
// formatters still emit what the shared list strips — and that the strip list
// covers every section this loop can append.
func TestStripPostSectionHeadersMatchQuality(t *testing.T) {
	if got := quality.FormatStaticSection([]quality.StaticIssue{{Path: "a.go", Reason: "stub"}}); !strings.Contains(got, staticGateHeader) {
		t.Fatalf("staticGateHeader drifted from quality.FormatStaticSection: %q", got)
	}
	if got := quality.FormatClaimsSection([]quality.ClaimIssue{{Path: "b.go", Reason: "missing"}}); !strings.Contains(got, claimsGateHeader) {
		t.Fatalf("claimsGateHeader drifted from quality.FormatClaimsSection: %q", got)
	}
	for _, header := range []string{
		diskEvidenceHeader, quality.SmokeSectionHeader, quality.AcceptanceSectionHeader,
		staticGateHeader, claimsGateHeader,
	} {
		found := false
		for _, h := range harnessSectionHeaders {
			if h == header {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q is not in the shared strip list — its section would stay glued to the answer", header)
		}
	}
}

func TestFileFingerprintDistinguishesContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	// Same length, different bytes: the old rolling sum collides easily.
	seen := map[string]string{}
	for _, body := range []string{"abcd", "abdc", "badc", "dcba", "acbd"} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		fp := fileFingerprint(p)
		if prev, dup := seen[fp]; dup {
			t.Fatalf("fingerprint collision: %q and %q both hash to %s", prev, body, fp)
		}
		seen[fp] = body
	}
	if fileFingerprint(filepath.Join(dir, "missing")) != "" {
		t.Fatal("a missing file must fingerprint to the empty string")
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"multibyte cut mid-rune", "aé…ü漢字テスト", 4},
		{"emoji", "🚀🚀🚀🚀", 6},
		{"ascii", "hello world", 5},
		{"zero", "hello", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.n)
			if !isValidUTF8(got) {
				t.Fatalf("truncate produced invalid UTF-8: %q", got)
			}
			if got := truncateASCII(tc.in, tc.n); !isValidUTF8(got) {
				t.Fatalf("truncateASCII produced invalid UTF-8: %q", got)
			}
		})
	}
}

func TestFailureHandlerTruncateNeverPanics(t *testing.T) {
	efh := NewEnhancedFailureHandler(t.TempDir())
	for _, n := range []int{-1, 0, 1, 2, 3, 4, 100} {
		got := efh.truncate("a very long message that needs clipping é", n)
		if len(got) > n && n > 0 {
			t.Fatalf("truncate(%d) returned %d bytes: %q", n, len(got), got)
		}
		if !isValidUTF8(got) {
			t.Fatalf("truncate(%d) produced invalid UTF-8: %q", n, got)
		}
	}
}

func isValidUTF8(s string) bool { return utf8.ValidString(s) }

// ── give-up classification ──────────────────────────────────────────────────

func TestGiveUpIsTypedAndAnnounced(t *testing.T) {
	err := (&Runner{}).giveUp(&plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColInProgress},
		{ID: "T2", Column: plan.ColDone},
	}}, ErrIdleTimeout, 31)
	if err == nil {
		t.Fatal("giving up must return an error, not nil")
	}
	var gerr *GaveUpError
	if !errors.As(err, &gerr) {
		t.Fatalf("expected *GaveUpError, got %T", err)
	}
	if gerr.Remaining != 1 {
		t.Fatalf("Remaining=%d, want 1", gerr.Remaining)
	}
	if !errors.Is(err, ErrGaveUp) || !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("give-up error does not unwrap to its class: %v", err)
	}
}

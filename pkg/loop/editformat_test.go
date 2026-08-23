package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// engineWithArm builds an evolve engine whose edit-format arm is pinned, so a
// test can exercise a specific arm end to end instead of whatever ε-greedy
// happens to draw. Deterministic mode turns the bandit into argmax(posterior
// mean), which is exactly the reproducibility knob CI needs.
func engineWithArm(t *testing.T, arm string) *evolve.Engine {
	t.Helper()
	dir := t.TempDir()
	eng, err := evolve.OpenWith(dir, dir, evolve.EngineOptions{
		Deterministic: true, ProjectPolicy: true, NoSeedRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := evolve.Key{Decision: evolve.DecEditFormat}
	for i := 0; i < 30; i++ {
		for _, a := range EditFormatArms() {
			reward := 0.0
			if a == arm {
				reward = 1.0
			}
			eng.Bandit().UpdateReward(key, a, reward)
		}
	}
	return eng
}

// (a) The arm must change what the worker is TOLD to emit.
func TestEachEditFormatArmInstructsADifferentTool(t *testing.T) {
	want := map[string]string{
		EditFormatSearchReplace: "ws_edit",
		EditFormatUnifiedDiff:   "ws_patch",
		EditFormatWholeFile:     "ws_write",
	}
	seen := map[string]bool{}
	for arm, tool := range want {
		r := &Runner{Evolve: engineWithArm(t, arm), Log: func(string, ...interface{}) {}}
		if got := r.chooseEditFormat(); got != arm {
			t.Fatalf("pinned arm %q but the bandit chose %q", arm, got)
		}
		sec := r.editFormatSection()
		if !strings.Contains(sec, "## Edit format") {
			t.Fatalf("%s produced no edit-format section: %q", arm, sec)
		}
		if !strings.Contains(sec, tool) {
			t.Errorf("arm %q does not instruct %s — the arm changes nothing:\n%s", arm, tool, sec)
		}
		for other, otherTool := range want {
			if other == arm {
				continue
			}
			if strings.Contains(sec, otherTool) && otherTool != tool {
				t.Errorf("arm %q also names %s, so the instruction is ambiguous:\n%s", arm, otherTool, sec)
			}
		}
		if seen[sec] {
			t.Errorf("arm %q renders a section identical to another arm's", arm)
		}
		seen[sec] = true
		// And it reaches the actual worker prompt, not just the helper.
		prompt := r.taskInput(taskForEditTest())
		if !strings.Contains(prompt, tool) {
			t.Errorf("arm %q never reached the worker prompt", arm)
		}
	}
}

// The choice is drawn ONCE per run. It used to be re-drawn per prompt render,
// which both let two tasks in one run get different formats and folded one
// bandit update per render into the posterior.
func TestEditFormatIsChosenOncePerRun(t *testing.T) {
	r := &Runner{Evolve: engineWithArm(t, EditFormatUnifiedDiff), Log: func(string, ...interface{}) {}}
	first := r.chooseEditFormat()
	for i := 0; i < 20; i++ {
		if got := r.chooseEditFormat(); got != first {
			t.Fatalf("the arm changed mid-run: %q → %q", first, got)
		}
		_ = r.editFormatSection()
		_ = r.taskInput(taskForEditTest())
	}
	// Exactly one decision record, whatever the render count.
	r.edits.note("call-1", true) // exercise the arm so the record is kept
	recs := r.DecisionRecords()
	n := 0
	for _, rec := range recs {
		if rec.Key.Decision == evolve.DecEditFormat {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d edit-format decisions recorded for one run, want 1", n)
	}
	if got := r.EditFormat(); got != first {
		t.Errorf("EditFormat() = %q, want %q", got, first)
	}
}

// (b) The reward must be about FIRST-ATTEMPT compliance, not eventual success.
func TestBanditRewardTracksFirstAttemptNotEventualSuccess(t *testing.T) {
	cases := []struct {
		name                      string
		attempted, applied, first int
		wantApplied               bool
		wantRetries               int
		wantFailed                bool
	}{
		{"clean run", 3, 3, 3, true, 0, false},
		// The defect this metric exists for: everything landed IN THE END, but
		// only after repairs. The old accounting scored this identically to the
		// clean run.
		{"applied only after repair", 3, 3, 1, false, 2, false},
		{"nothing ever landed", 2, 0, 0, false, 2, true},
	}
	var clean, repaired float64
	for _, tc := range cases {
		out, ok := editFormatOutcome(tc.attempted, tc.applied, tc.first)
		if !ok {
			t.Fatalf("%s: outcome not produced", tc.name)
		}
		if out.Applied != tc.wantApplied || out.Retries != tc.wantRetries || out.Failed != tc.wantFailed {
			t.Errorf("%s: outcome = %+v", tc.name, out)
		}
		switch tc.name {
		case "clean run":
			clean = out.Reward()
		case "applied only after repair":
			repaired = out.Reward()
		}
	}
	if repaired >= clean {
		t.Fatalf("a format that only worked after repair earned %.3f vs %.3f for a clean run — "+
			"the bandit is still learning eventual success", repaired, clean)
	}
	// An unexercised arm earns nothing at all.
	if _, ok := editFormatOutcome(0, 0, 0); ok {
		t.Error("a run with no edits produced a reward for the edit-format arm")
	}
}

// The measured outcome really is stamped onto the drained record — the
// orchestrator receives a reward, not a zero Outcome.
func TestDrainedEditFormatDecisionCarriesTheMeasuredOutcome(t *testing.T) {
	r := &Runner{Evolve: engineWithArm(t, EditFormatUnifiedDiff), Log: func(string, ...interface{}) {}}
	_ = r.chooseEditFormat()

	// Two attempts, both eventually applied, only one as emitted.
	r.noteEdits(editTranscript(
		editCall{id: "c1", tool: "ws_patch", args: `{"path":"a.py"}`, result: "Error: hunk failed to apply"},
		editCall{id: "c2", tool: "ws_edit", args: `{"path":"a.py"}`, result: "edited a.py (1 replacement(s))"},
	))
	attempted, applied, first := r.EditStats()
	if attempted != 2 || applied != 1 || first != 1 {
		t.Fatalf("edit stats = %d/%d/%d (attempted/applied/first)", attempted, applied, first)
	}

	recs := r.DrainDecisionRecords()
	var rec *evolve.DecisionRecord
	for i := range recs {
		if recs[i].Key.Decision == evolve.DecEditFormat {
			rec = &recs[i]
		}
	}
	if rec == nil {
		t.Fatal("no edit-format decision was drained")
	}
	if rec.Outcome.Applied {
		t.Error("outcome claims a clean apply when one of two attempts failed")
	}
	if rec.Outcome.Retries != 1 {
		t.Errorf("retries = %d, want 1", rec.Outcome.Retries)
	}
	zero := evolve.Outcome{}
	if rec.Outcome.Reward() == zero.Reward() {
		t.Error("the drained outcome still scores the same as an empty one — " +
			"every arm would be updated identically")
	}
	// A second drain must not re-emit it.
	if len(r.DrainDecisionRecords()) != 0 {
		t.Error("decisions were not cleared by the drain")
	}
}

// A run that made no edits at all must not feed the arm anything.
func TestUnexercisedEditFormatArmIsNotRewarded(t *testing.T) {
	r := &Runner{Evolve: engineWithArm(t, EditFormatWholeFile), Log: func(string, ...interface{}) {}}
	_ = r.chooseEditFormat()
	for _, rec := range r.DrainDecisionRecords() {
		if rec.Key.Decision == evolve.DecEditFormat {
			t.Fatal("a planning-only run still rewarded the edit-format arm")
		}
	}
}

// The harness repairing the arguments must cost the arm its first-attempt
// credit even though the edit lands.
func TestHarnessRepairCostsFirstAttemptCredit(t *testing.T) {
	r := &Runner{Log: func(string, ...interface{}) {}}
	r.edits.noteRepair()
	r.noteEdits(editTranscript(
		editCall{id: "c1", tool: "ws_edit", args: `{"path":"a.go"}`, result: "edited a.go (1 replacement(s))"},
	))
	attempted, applied, first := r.EditStats()
	if attempted != 1 || applied != 1 || first != 0 {
		t.Fatalf("repaired edit scored %d/%d/%d, want 1/1/0", attempted, applied, first)
	}
}

// A resumed ReAct task is handed its own earlier transcript. Re-scanning it
// must not double-count the edits that already happened.
func TestEditLedgerDedupesRescannedTranscripts(t *testing.T) {
	r := &Runner{Log: func(string, ...interface{}) {}}
	msgs := editTranscript(
		editCall{id: "c1", tool: "ws_edit", args: `{"path":"a.go"}`, result: "edited a.go (1 replacement(s))"},
		editCall{id: "c2", tool: "ws_patch", args: `{"path":"b.go"}`, result: "Error: boom"},
	)
	for i := 0; i < 3; i++ {
		r.noteEdits(msgs)
	}
	attempted, applied, first := r.EditStats()
	if attempted != 2 || applied != 1 || first != 1 {
		t.Fatalf("stats after three scans = %d/%d/%d, want 2/1/1", attempted, applied, first)
	}
	// Non-edit tools are not attempts.
	r.noteEdits(editTranscript(
		editCall{id: "c3", tool: "ws_read", args: `{"path":"a.go"}`, result: "   1|package p"},
		editCall{id: "c4", tool: "ws_shell", args: `{"command":"go test"}`, result: "ok"},
	))
	if a, _, _ := r.EditStats(); a != 2 {
		t.Errorf("a read/shell call was counted as an edit attempt: attempted=%d", a)
	}
}

// A refusal is returned as an ordinary string with a nil error, so classifying
// "not an error" as applied would score every guard refusal as a landed edit.
func TestEditAppliedRecognisesTheWorkspacesOwnResultLines(t *testing.T) {
	applied := []string{
		"edited a.go (1 replacement(s))",
		"patched a.go (2 hunk(s) applied)",
		"wrote a.go (42 bytes)",
		"overwrote a.go (42 bytes)",
		"dry-run: would edit a.go (1 replacement(s))",
		"staged write for review: a.go",
	}
	for _, s := range applied {
		if !editApplied(s) {
			t.Errorf("editApplied(%q) = false", s)
		}
	}
	refused := []string{
		"", "Error: old_str not found in a.go",
		workspace.WriteRefuseReason("a.go"),
		workspace.EditNotFoundReason("a.go"),
		workspace.EditBeforeReadReason("a.go"),
		"hunk 1/2 FAILED: context not found in file",
	}
	for _, s := range refused {
		if editApplied(s) {
			t.Errorf("editApplied(%q…) = true — a refusal was scored as an applied edit", clip60(s))
		}
	}
}

// (c) whole_file must be a REAL path: ws_write on a file read this session is
// allowed by the write guard, so the arm the bandit can choose actually works.
func TestWholeFileArmIsAcceptedByTheToolLayer(t *testing.T) {
	root := t.TempDir()
	path := "calc.go"
	abs := filepath.Join(root, path)
	if err := os.WriteFile(abs, []byte("package calc\n\nfunc Sum(a, b int) int { return a }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewToolRegistry()
	if err := workspace.RegisterCodingToolsOpts(reg, root, workspace.ToolOpts{
		Permission: permissions.ModeAuto,
	}); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) string {
		t.Helper()
		tool, ok := reg.GetTool(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		out, err := tool.Execute(context.Background(), string(raw))
		if err != nil {
			return "Error: " + err.Error()
		}
		s, _ := out.(string)
		return s
	}

	whole := "package calc\n\nfunc Sum(a, b int) int { return a + b }\n"

	// Without a read, the guard refuses — that rule still holds.
	if got := call("ws_write", map[string]any{"path": path, "content": whole}); editApplied(got) {
		t.Fatalf("ws_write on an unread existing file was accepted: %q", clip60(got))
	}

	// ws_read then ws_write the complete file: exactly what the whole_file
	// prompt section instructs.
	if got := call("ws_read", map[string]any{"path": path}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("ws_read failed: %s", got)
	}
	got := call("ws_write", map[string]any{"path": path, "content": whole})
	if !editApplied(got) {
		t.Fatalf("the whole_file arm is not a supported path — ws_write said: %q", clip60(got))
	}
	on, err := os.ReadFile(abs) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != whole {
		t.Errorf("whole-file rewrite did not land:\n%s", on)
	}

	// And the ledger books it as a first-attempt apply, so choosing whole_file
	// can actually be rewarded.
	r := &Runner{Log: func(string, ...interface{}) {}}
	r.noteEdits(editTranscript(editCall{
		id: "w1", tool: "ws_write", args: `{"path":"calc.go"}`, result: got,
	}))
	if a, ap, f := r.EditStats(); a != 1 || ap != 1 || f != 1 {
		t.Errorf("whole-file write scored %d/%d/%d, want 1/1/1", a, ap, f)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

type editCall struct {
	id, tool, args, result string
}

// editTranscript renders the assistant/tool message pairs GoLangGraph returns
// on SubAgentResult.Messages.
func editTranscript(calls ...editCall) []llm.Message {
	var out []llm.Message
	for _, c := range calls {
		out = append(out, llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: c.id, Type: "function",
				Function: llm.FunctionCall{Name: c.tool, Arguments: c.args},
			}},
		})
		out = append(out, llm.Message{Role: "tool", ToolCallID: c.id, Content: c.result})
	}
	return out
}

// taskForEditTest is a minimal task, enough for taskInputFor to render.
func taskForEditTest() plan.Task {
	return plan.Task{
		ID: "T1", Title: "add Sum", Role: plan.RoleWorker,
		Files: []string{"calc.go"}, Column: plan.ColReadyToDev,
		Acceptance: "go build ./...",
	}
}

func clip60(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

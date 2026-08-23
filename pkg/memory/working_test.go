package memory

import (
	"strings"
	"testing"
	"time"
)

func TestWorkingRecordToolClassifies(t *testing.T) {
	tests := []struct {
		name        string
		events      []ToolEvent
		wantRead    []string
		wantEdited  []string
		wantCmds    int
		wantErrors  int
		wantOpen    int
		wantRedund  int
		wantCallCnt int
	}{
		{
			name: "reads and edits",
			events: []ToolEvent{
				{Tool: "ws_read", Path: "a.go", OK: true},
				{Tool: "ws_edit", Path: "a.go", OK: true},
				{Tool: "ws_write", Path: "b.go", OK: true},
			},
			wantRead: []string{"a.go"}, wantEdited: []string{"a.go", "b.go"},
			wantCallCnt: 3,
		},
		{
			name: "failed edit is not counted as edited",
			events: []ToolEvent{
				{Tool: "ws_edit", Path: "a.go", OK: false, Error: "old_str not found in a.go"},
			},
			wantErrors: 1, wantOpen: 1, wantCallCnt: 1,
		},
		{
			name: "shell commands recorded",
			events: []ToolEvent{
				{Tool: "ws_shell", Command: "go test ./...", OK: true},
				{Tool: "ws_shell", Command: "go build ./...", OK: false, Error: "exit status 1"},
			},
			wantCmds: 2, wantErrors: 1, wantOpen: 1, wantCallCnt: 2,
		},
		{
			name: "identical calls counted as redundant",
			events: []ToolEvent{
				{Tool: "ws_read", Path: "a.go", Args: `{"path":"a.go"}`, OK: true},
				{Tool: "ws_read", Path: "a.go", Args: `{"path":"a.go"}`, OK: true},
				{Tool: "ws_read", Path: "a.go", Args: `{"path":"a.go"}`, OK: true},
			},
			wantRead: []string{"a.go"}, wantRedund: 2, wantCallCnt: 3,
		},
		{
			name: "same failure twice opens one failure",
			events: []ToolEvent{
				{Tool: "ws_edit", Path: "a.go", OK: false, Error: "old_str not found", Key: "k1"},
				{Tool: "ws_edit", Path: "a.go", OK: false, Error: "old_str not found", Key: "k1"},
			},
			wantErrors: 2, wantOpen: 1, wantCallCnt: 2, wantRedund: 1,
		},
		{
			name:        "unnamed tool ignored",
			events:      []ToolEvent{{Tool: "  ", OK: true}},
			wantCallCnt: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorking(500)
			for _, e := range tc.events {
				w.RecordTool(e)
			}
			snap := w.Snapshot()
			if got := strings.Join(snap.FilesRead, ","); got != strings.Join(tc.wantRead, ",") {
				t.Errorf("files read = %v, want %v", snap.FilesRead, tc.wantRead)
			}
			if got := strings.Join(snap.FilesEdited, ","); got != strings.Join(tc.wantEdited, ",") {
				t.Errorf("files edited = %v, want %v", snap.FilesEdited, tc.wantEdited)
			}
			if len(snap.Commands) != tc.wantCmds {
				t.Errorf("commands = %d, want %d", len(snap.Commands), tc.wantCmds)
			}
			if len(snap.Open) != tc.wantOpen {
				t.Errorf("open failures = %d, want %d", len(snap.Open), tc.wantOpen)
			}
			calls, errs, redundant := w.Counters()
			if calls != tc.wantCallCnt {
				t.Errorf("tool calls = %d, want %d", calls, tc.wantCallCnt)
			}
			if errs != tc.wantErrors {
				t.Errorf("tool errors = %d, want %d", errs, tc.wantErrors)
			}
			if redundant != tc.wantRedund {
				t.Errorf("redundant = %d, want %d", redundant, tc.wantRedund)
			}
		})
	}
}

func TestWorkingIsBounded(t *testing.T) {
	w := NewWorking(500)
	for i := 0; i < 5000; i++ {
		w.RecordTool(ToolEvent{Tool: "ws_read", Path: "f" + itoa(i) + ".go", OK: true})
		w.RecordTool(ToolEvent{Tool: "ws_shell", Command: "cmd " + itoa(i), OK: true})
		w.RecordTool(ToolEvent{Tool: "ws_edit", Path: "e" + itoa(i) + ".go", OK: false, Error: "boom " + itoa(i)})
		w.Decide("decision " + itoa(i))
		w.Focus("focus" + itoa(i) + ".go")
	}
	snap := w.Snapshot()
	checks := []struct {
		name string
		got  int
		max  int
	}{
		{"events", len(snap.Events), MaxToolEvents},
		{"files read", len(snap.FilesRead), MaxWorkingFiles},
		{"files edited", len(snap.FilesEdited), MaxWorkingFiles},
		{"commands", len(snap.Commands), MaxWorkingCmds},
		{"open failures", len(snap.Open), MaxOpenFailures},
		{"decisions", len(snap.Decisions), MaxDecisions},
		{"focus", len(snap.Focus), MaxFocusFiles},
	}
	for _, c := range checks {
		if c.got > c.max {
			t.Errorf("%s = %d, exceeds cap %d", c.name, c.got, c.max)
		}
	}
}

func TestWorkingResolveMovesFailure(t *testing.T) {
	w := NewWorking(500)
	w.Fail(Failure{Key: "fp1", Tool: "ws_edit", Message: "old_str not found"})
	if got := len(w.OpenFailures()); got != 1 {
		t.Fatalf("open = %d, want 1", got)
	}
	if !w.Resolve("fp1", "re-read then retried with a smaller anchor", "rule_edit_not_found") {
		t.Fatal("Resolve returned false for a known key")
	}
	if got := len(w.OpenFailures()); got != 0 {
		t.Fatalf("open after resolve = %d, want 0", got)
	}
	snap := w.Snapshot()
	if len(snap.Resolved) != 1 || snap.Resolved[0].ResolvedBy != "rule_edit_not_found" {
		t.Fatalf("resolved = %+v", snap.Resolved)
	}
	if w.Resolve("nope", "x", "y") {
		t.Error("Resolve on unknown key should return false")
	}
}

func TestWorkingRenderIsBudgetedAndEmptyWhenEmpty(t *testing.T) {
	w := NewWorking(400)
	if got := w.Render(0); got != "" {
		t.Fatalf("empty working memory rendered %q, want empty", got)
	}

	w.Start("run1", "Add retry to the HTTP client", "worker")
	w.Focus("pkg/http/client.go")
	for i := 0; i < 40; i++ {
		w.RecordTool(ToolEvent{Tool: "ws_read", Path: "pkg/http/f" + itoa(i) + ".go", OK: true})
		w.Decide("considered approach " + itoa(i) + " which is a fairly long decision line to blow the budget")
	}
	out := w.Render(120)
	if out == "" {
		t.Fatal("populated working memory rendered empty")
	}
	if got := DefaultCounter()(out); got > 120 {
		t.Errorf("render used %d tokens, budget was 120", got)
	}
	if !strings.Contains(out, "Add retry to the HTTP client") {
		t.Errorf("render dropped the task: %q", out)
	}
}

func TestWorkingDigestReusesCompactSchema(t *testing.T) {
	w := NewWorking(800)
	w.RecordTool(ToolEvent{Tool: "ws_read", Path: "a.go", OK: true})
	w.RecordTool(ToolEvent{Tool: "ws_edit", Path: "a.go", OK: true})
	w.RecordTool(ToolEvent{Tool: "ws_shell", Command: "go test ./...", OK: false, Error: "FAIL"})
	w.Fail(Failure{Key: "k", Tool: "ws_patch", Message: "hunk 2 did not apply"})
	w.Resolve("k", "fell back to search/replace", "rule_patch_fallback")

	d := w.Digest()
	if len(d.FilesRead) != 1 || len(d.FilesEdited) != 1 {
		t.Fatalf("digest files = %+v", d)
	}
	if len(d.Commands) != 1 || d.Commands[0].Status != "failed" {
		t.Fatalf("digest commands = %+v", d.Commands)
	}
	if len(d.Extra) == 0 {
		t.Fatal("resolved failures should be carried in Extra")
	}
	rendered := d.Render(2000)
	if !strings.Contains(rendered, "fell back to search/replace") {
		t.Errorf("digest render lost the resolution: %s", rendered)
	}
}

func TestWorkingResetClears(t *testing.T) {
	w := NewWorking(400)
	w.Start("r", "task", "worker")
	w.RecordTool(ToolEvent{Tool: "ws_read", Path: "a.go", OK: true})
	w.Reset()
	if calls, _, _ := w.Counters(); calls != 0 {
		t.Errorf("counters not reset: %d", calls)
	}
	if w.Render(0) != "" {
		t.Error("render after reset should be empty")
	}
}

func TestWorkingNilSafe(t *testing.T) {
	var w *Working
	w.RecordTool(ToolEvent{Tool: "ws_read"})
	w.Fail(Failure{})
	w.Decide("x")
	w.Focus("a")
	w.SetTask("t")
	w.SetRole("r")
	w.Summarize("s")
	w.Reset()
	if w.Render(10) != "" || len(w.OpenFailures()) != 0 || w.Resolve("a", "b", "c") {
		t.Error("nil Working must behave as an empty one")
	}
	if _, _, r := w.Counters(); r != 0 {
		t.Error("nil counters")
	}
	_ = w.Snapshot()
	_ = w.Digest()
}

// DefaultCounter exposes the package default token counter for tests.
func DefaultCounter() TokenCounter {
	return func(s string) int { return countTokens(nil, s) }
}

func TestWorkingConcurrentRecord(t *testing.T) {
	w := NewWorking(500)
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			for i := 0; i < 200; i++ {
				w.RecordTool(ToolEvent{Tool: "ws_read", Path: "f.go", OK: true, At: time.Now()})
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	if calls, _, _ := w.Counters(); calls != 1600 {
		t.Fatalf("calls = %d, want 1600", calls)
	}
}

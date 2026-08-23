package main

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestParseReviewVerdict(t *testing.T) {
	v, ok := parseReview(`{"approved":false,"score":41,"summary":"no write evidence","issues":["a","b"]}`)
	if !ok {
		t.Fatal("parseReview refused a well-formed verdict")
	}
	if v.Approved || v.Score != 41 || v.Summary != "no write evidence" || len(v.Issues) != 2 {
		t.Fatalf("parseReview = %+v", v)
	}
	// The engine also writes prose into Review; that must parse as "not JSON",
	// not as an approved verdict.
	if _, ok := parseReview("human mark_done after escalate"); ok {
		t.Error("parseReview accepted prose as a verdict")
	}
	if _, ok := parseReview(""); ok {
		t.Error("parseReview accepted an empty review")
	}
}

func TestParseWorkerOutputToleratesTrailingProse(t *testing.T) {
	raw := `{"status":"done","summary":"added Divide","files_changed":["calc.go"],"notes":""}

## Deterministic smoke
PASSED <!-- slmcode:smoke-pass:abc -->`
	w, ok := parseWorkerOutput(raw)
	if !ok {
		t.Fatal("parseWorkerOutput refused output with a trailing smoke marker")
	}
	if w.Status != "done" || len(w.FilesChanged) != 1 || w.FilesChanged[0] != "calc.go" {
		t.Fatalf("parseWorkerOutput = %+v", w)
	}
}

func TestHumanForcedDoneRecognisesTheEngineMarker(t *testing.T) {
	forced := plan.Task{ID: "T1", Column: plan.ColDone, Review: "human mark_done after escalate"}
	if !humanForcedDone(forced) {
		t.Error("a task closed by a human override was reported as a verified pass")
	}
	verified := plan.Task{ID: "T2", Column: plan.ColDone, Review: `{"approved":true,"score":92}`}
	if humanForcedDone(verified) {
		t.Error("a verified pass was reported as a human override")
	}
}

func TestIsGateMessage(t *testing.T) {
	for _, msg := range []string{
		"T1 hit its 6-call budget — escalating instead of another review round-trip",
		"T1 needs human review — decide in Studio (or wait for timeout)",
		"rejected by evidence gate: edit task has no real write evidence",
	} {
		if !isGateMessage(msg) {
			t.Errorf("isGateMessage(%q) = false, want true", msg)
		}
	}
	if isGateMessage("QUALITY MONITOR: You just made the exact same tool call again") {
		t.Error("a quality-monitor nudge was mistaken for a gate")
	}
}

func TestBoardTaskFlagMarksWhatNeedsAHuman(t *testing.T) {
	cases := []struct {
		name string
		task plan.Task
		want string
	}{
		{"forced", plan.Task{Column: plan.ColDone, Review: "human mark_done after escalate"}, "forced done"},
		{"blocked", plan.Task{Column: plan.ColBlocked}, "blocked"},
		{"escalated", plan.Task{Column: plan.ColToScope, Retries: 2}, "needs you"},
		{"verified", plan.Task{Column: plan.ColDone, Review: `{"approved":true}`}, ""},
		{"fresh", plan.Task{Column: plan.ColReadyToDev}, ""},
	}
	for _, c := range cases {
		got := boardTaskFlag(c.task)
		if c.want == "" {
			if got != "" {
				t.Errorf("%s: boardTaskFlag = %q, want empty", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: boardTaskFlag = %q, want it to contain %q", c.name, got, c.want)
		}
	}
}

func TestFirstStuckIDPrefersTheTaskWithAVerdict(t *testing.T) {
	tasks := []plan.Task{
		{ID: "T1", Column: plan.ColReadyToDev},
		{ID: "T2", Column: plan.ColToScope, Error: "rejected"},
		{ID: "T3", Column: plan.ColBlocked},
	}
	if got := firstStuckID(tasks); got != "T2" {
		t.Errorf("firstStuckID = %q, want T2", got)
	}
	if got := firstStuckID(nil); got != "" {
		t.Errorf("firstStuckID(nil) = %q, want empty", got)
	}
}

func TestTaskNotFoundNamesTheBoardsTasks(t *testing.T) {
	board := plan.Board{Tasks: []plan.Task{{ID: "T2"}, {ID: "T1"}}}
	err := taskNotFound(board, "T9")
	if err == nil || !strings.Contains(err.Error(), "T1, T2") {
		t.Fatalf("taskNotFound = %v, want it to list T1, T2", err)
	}
	if err := taskNotFound(plan.Board{}, "T1"); err == nil ||
		!strings.Contains(err.Error(), "slmcode run") {
		t.Fatalf("empty board: %v, want a pointer at `slmcode run`", err)
	}
}

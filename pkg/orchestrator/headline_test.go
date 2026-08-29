package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Measured on a finished run:
//
//	✔ "assumptions": [ "The REST API will support basic CRUD operations for tasks with — 5/5 tasks done
//
// The counts and the gate verdict on that line were correct, so the run looked
// broken while being fine — the worst way for a summary to be wrong.
func TestHeadlineRejectsRawPlannerJSON(t *testing.T) {
	board := &plan.Board{Query: "Build a Go REST backend in cmd/server/main.go"}
	for _, summary := range []string{
		`"assumptions": [ "The REST API will support basic CRUD operations for tasks with`,
		`{"summary":"build the thing","steps":[]}`,
		`[{"id":"T1"}]`,
	} {
		got := runHeadline(plan.Plan{Summary: summary}, board)
		if looksLikeRawJSON(got) {
			t.Errorf("headline is still JSON: %q", got)
		}
		if !strings.Contains(got, "Build a Go REST backend") {
			t.Errorf("did not fall back to the request: %q", got)
		}
	}
}

// A real summary is used verbatim — the guard must not eat good output.
func TestHeadlineKeepsARealSummary(t *testing.T) {
	board := &plan.Board{Query: "the raw request"}
	got := runHeadline(plan.Plan{Summary: "Add a task store and wire the server to it."}, board)
	if !strings.HasPrefix(got, "Add a task store") {
		t.Errorf("got %q, want the plan's own summary", got)
	}
}

// With neither a usable summary nor a query, say something true rather than
// printing an empty leading dash.
func TestHeadlineFallsBackLast(t *testing.T) {
	if got := runHeadline(plan.Plan{}, &plan.Board{}); got != "Run complete" {
		t.Errorf("got %q, want a truthful fallback", got)
	}
	if got := runHeadline(plan.Plan{Summary: `{"a":1}`}, nil); got != "Run complete" {
		t.Errorf("nil board: got %q", got)
	}
}

func TestLooksLikeRawJSON(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{`{"summary":"x"}`, true},
		{`[{"id":"T1"}]`, true},
		{`"assumptions": [ "the api`, true},
		{"Add a task store and wire the server to it.", false},
		{"Fix the failing test in stats.go", false},
		{"", false},
	} {
		if got := looksLikeRawJSON(tc.in); got != tc.want {
			t.Errorf("looksLikeRawJSON(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

package learning

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestExtractAndRender(t *testing.T) {
	done := plan.Task{
		ID: "T1", Title: "Add helper", Column: plan.ColDone,
		Output:     "Added Greeting(). Tests pass.",
		Acceptance: "compiles",
	}
	done.Normalize()
	lessons := Extract(done)
	if len(lessons) == 0 {
		t.Fatal("expected lessons")
	}
	md := RenderMarkdown(lessons)
	if !strings.Contains(md, "T1") {
		t.Fatalf("md=%s", md)
	}

	blocked := plan.Task{ID: "T2", Column: plan.ColBlocked, Error: "missing import"}
	blocked.Normalize()
	fail := Extract(blocked)
	if len(fail) == 0 || fail[0].Kind != "failure" {
		t.Fatalf("%+v", fail)
	}

	delta := ContextDelta([]plan.Task{done, blocked})
	if !strings.Contains(delta, "Completed") || !strings.Contains(delta, "Blocked") {
		t.Fatalf("delta=%s", delta)
	}

	md2 := JSONLessonsToMarkdown(`{"lessons":[{"kind":"convention","text":"Prefer small edits"}]}`)
	if !strings.Contains(md2, "Prefer small edits") {
		t.Fatalf("json md=%s", md2)
	}
}

package refine

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/learning"
)

func TestBuildAndShouldRun(t *testing.T) {
	if ShouldRun(false, 2, 1, 3) {
		t.Fatal("disabled")
	}
	if !ShouldRun(true, 2, 1, 3) {
		t.Fatal("should run")
	}
	if ShouldRun(true, 2, 3, 3) {
		t.Fatal("over max rounds")
	}
	out := Build(Input{
		Query: "add feature",
		Lessons: []learning.Lesson{
			{Kind: "success", Text: "used ws_edit"},
		},
		Round: 1,
	})
	if out.Skip || !strings.Contains(out.Markdown, "Refine") {
		t.Fatalf("%+v", out)
	}
}

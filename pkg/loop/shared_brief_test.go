package loop

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestBuildSharedBriefPrioritizesDependenciesAndSharedFiles(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{
			ID:     "T1",
			Title:  "Unrelated old task",
			Role:   plan.RoleWorker,
			Column: plan.ColDone,
			Output: `{"status":"done","summary":"old unrelated fact","files_changed":["docs/readme.md"]}`,
			Files:  []string{"docs/readme.md"},
		},
		{
			ID:     "T2",
			Title:  "Dependency",
			Role:   plan.RoleExplorer,
			Column: plan.ColDone,
			Output: `{"status":"done","summary":"found config loader in pkg/config/config.go","files_changed":["pkg/config/config.go"]}`,
			Files:  []string{"pkg/config/config.go"},
		},
		{
			ID:     "T3",
			Title:  "Sibling package task",
			Role:   plan.RoleWorker,
			Column: plan.ColDone,
			Output: `{"status":"done","summary":"updated validation helpers","files_changed":["pkg/config/validate.go"]}`,
			Files:  []string{"pkg/config/validate.go"},
		},
	}}
	current := plan.Task{
		ID:        "T4",
		Title:     "Use config loader",
		Column:    plan.ColReadyToDev,
		Role:      plan.RoleWorker,
		DependsOn: []string{"T2"},
		Files:     []string{"pkg/config/config.go"},
	}

	brief := buildSharedBrief(board, current, 1000)
	if !strings.Contains(brief, "T2 @explorer: found config loader") {
		t.Fatalf("dependency missing from brief:\n%s", brief)
	}
	if !strings.Contains(brief, "T3 @worker: updated validation helpers") {
		t.Fatalf("same-package sibling missing from brief:\n%s", brief)
	}
	if strings.Index(brief, "T2 @explorer") > strings.Index(brief, "T3 @worker") {
		t.Fatalf("dependency should rank before same-package sibling:\n%s", brief)
	}
}

func TestSharedBriefSectionIsBoundedAndDisableable(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{{
		ID:     "T1",
		Title:  "Long done task",
		Role:   plan.RoleWorker,
		Column: plan.ColDone,
		Output: `{"status":"done","summary":"` + strings.Repeat("x", 300) + `","files_changed":["a.go"]}`,
		Files:  []string{"a.go"},
	}}}
	current := plan.Task{ID: "T2", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"}}

	r := &Runner{SharedBriefLimit: 120}
	section := r.sharedBriefSection(board, current)
	if section == "" {
		t.Fatal("expected bounded shared brief")
	}
	if len(section) > 260 {
		t.Fatalf("shared brief too large: len=%d\n%s", len(section), section)
	}

	r.SharedBriefLimit = -1
	if got := r.sharedBriefSection(board, current); got != "" {
		t.Fatalf("disabled shared brief = %q", got)
	}
}

func TestTaskInputForInjectsSharedBriefBeforeFeedback(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{{
		ID:     "T1",
		Title:  "Done dependency",
		Role:   plan.RoleWorker,
		Column: plan.ColDone,
		Output: `{"status":"done","summary":"created helper","files_changed":["helper.go"]}`,
		Files:  []string{"helper.go"},
	}}}
	task := plan.Task{
		ID:        "T2",
		Title:     "Use helper",
		Role:      plan.RoleWorker,
		Column:    plan.ColReadyToDev,
		DependsOn: []string{"T1"},
		Files:     []string{"helper.go"},
	}
	r := NewRunner(nil, nil)
	r.Feedback = func() string { return "prefer small diff" }

	input := r.taskInputFor(board, task)
	briefIdx := strings.Index(input, "## Shared task handoff")
	feedbackIdx := strings.Index(input, "## LIVE FEEDBACK FROM USER")
	if briefIdx < 0 {
		t.Fatalf("missing shared brief:\n%s", input)
	}
	if feedbackIdx < 0 {
		t.Fatalf("missing feedback:\n%s", input)
	}
	if briefIdx > feedbackIdx {
		t.Fatalf("shared brief should precede live feedback:\n%s", input)
	}
	if !strings.Contains(input, "created helper") {
		t.Fatalf("dependency summary missing:\n%s", input)
	}
}

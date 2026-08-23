package loop

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
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
			ID:         "T2",
			Title:      "Dependency",
			Role:       plan.RoleExplorer,
			Column:     plan.ColDone,
			Output:     `{"status":"done","summary":"found config loader in pkg/config/config.go","files_changed":["pkg/config/config.go"]}`,
			Files:      []string{"pkg/config/config.go"},
			Acceptance: "go test ./... exits 0",
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
	if !strings.Contains(brief, "verify=go test ./... exits 0") {
		t.Fatalf("dependency acceptance missing from brief:\n%s", brief)
	}
	if !strings.Contains(brief, "T3 @worker: updated validation helpers") {
		t.Fatalf("same-package sibling missing from brief:\n%s", brief)
	}
	if strings.Index(brief, "T2 @explorer") > strings.Index(brief, "T3 @worker") {
		t.Fatalf("dependency should rank before same-package sibling:\n%s", brief)
	}
}

func TestBuildSharedBriefCarriesTesterVerificationSignals(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{
			ID:     "T1",
			Title:  "Tester pass",
			Role:   plan.RoleTester,
			Column: plan.ColDone,
			// A bare Observation frame is no longer execution evidence — it is
			// emitted for every tool call and a model can type it unprompted.
			Output: "Observation: ws_shell `go test ./...`\nok\nexit status 0\n" +
				`{"passed":true,"commands":["go test ./...","go vet ./..."],"summary":"green"}`,
			Files: []string{"pkg/loop/shared_brief.go"},
		},
		{
			ID:     "T2",
			Title:  "Tester fail",
			Role:   plan.RoleTester,
			Column: plan.ColDone,
			Output: `{"passed":false,"failures":["pkg/loop/shared_brief.go still lacks verify handoff"],"summary":"red"}`,
			Files:  []string{"pkg/loop/shared_brief.go"},
		},
	}}
	current := plan.Task{
		ID:        "T3",
		Role:      plan.RoleWorker,
		Column:    plan.ColReadyToDev,
		DependsOn: []string{"T2"},
		Files:     []string{"pkg/loop/shared_brief.go"},
	}

	brief := buildSharedBrief(board, current, 1000)
	if !strings.Contains(brief, "T2 @tester: red") || !strings.Contains(brief, "verify=fix pkg/loop/shared_brief.go still lacks verify handoff") {
		t.Fatalf("tester failure signal missing:\n%s", brief)
	}
	if !strings.Contains(brief, "T1 @tester: green") || !strings.Contains(brief, "verify=go test ./...,go vet ./...") {
		t.Fatalf("tester command signal missing:\n%s", brief)
	}
}

func TestBuildSharedBriefCarriesRelatedBlockedHistory(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{
			ID:     "T1",
			Title:  "Unrelated blocked task",
			Role:   plan.RoleWorker,
			Column: plan.ColBlocked,
			Error:  "unrelated timeout",
			Files:  []string{"docs/readme.md"},
		},
		{
			ID:      "T2",
			Title:   "Auth implementation",
			Role:    plan.RoleWorker,
			Column:  plan.ColBlocked,
			Status:  plan.StatusFailed,
			Retries: 2,
			Output:  `{"status":"blocked","summary":"jwt verifier still rejects valid tokens","files_changed":["pkg/auth/jwt.go"]}`,
			Review:  "rejected: acceptance smoke failed\nIssues:\n- go test ./pkg/auth still fails",
			Error:   "max retries exceeded: review rejected",
			Files:   []string{"pkg/auth/service.go"},
		},
	}}
	current := plan.Task{
		ID:     "T3",
		Role:   plan.RoleWorker,
		Column: plan.ColReadyToDev,
		Files:  []string{"pkg/auth/jwt.go"},
	}

	brief := buildSharedBrief(board, current, 1200)
	if strings.Contains(brief, "T1 @worker") {
		t.Fatalf("unrelated blocked task leaked into brief:\n%s", brief)
	}
	for _, want := range []string{
		"T2 @worker: jwt verifier still rejects valid tokens",
		"status=blocked,retries=2",
		"files=pkg/auth/service.go,pkg/auth/jwt.go",
		"review=rejected: acceptance smoke failed",
		"error=max retries exceeded: review rejected",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("missing %q in brief:\n%s", want, brief)
		}
	}
}

func TestSharedBriefRendersWorkspaceRelativeFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	board := &plan.Board{Tasks: []plan.Task{{
		ID:     "T1",
		Title:  "Absolute path task",
		Role:   plan.RoleWorker,
		Column: plan.ColDone,
		Output: `{"status":"done","summary":"updated auth files","files_changed":["` + filepath.Join(root, "pkg/auth/tokens.go") + `","` + outside + `"]}`,
		Files:  []string{filepath.Join(root, "pkg/auth/jwt.go"), outside},
	}}}
	current := plan.Task{
		ID:     "T2",
		Role:   plan.RoleWorker,
		Column: plan.ColReadyToDev,
		Files:  []string{filepath.Join(root, "pkg/auth/jwt.go")},
	}

	r := &Runner{Root: root}
	section := r.sharedBriefSection(board, current)
	if !strings.Contains(section, "files=pkg/auth/jwt.go,pkg/auth/tokens.go") {
		t.Fatalf("workspace-relative files missing:\n%s", section)
	}
	if strings.Contains(section, root) || strings.Contains(section, "secret.go") {
		t.Fatalf("unsafe absolute path leaked into brief:\n%s", section)
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

func TestTaskInputInjectsAdaptiveLessonsBeforeFeedback(t *testing.T) {
	shared := ggagent.NewSharedState()
	shared.SetGlobal("adaptive_lessons", "- global memory: Timeout after context deadline exceeded; lower max_parallel or split smaller")
	r := NewRunner(nil, shared)
	r.Feedback = func() string { return "user says keep going" }
	task := plan.Task{
		ID:          "T1",
		Title:       "Retry focused edit",
		Role:        plan.RoleWorker,
		Column:      plan.ColReadyToDev,
		Description: "do it",
	}

	input := r.taskInputFor(nil, task)
	adaptiveIdx := strings.Index(input, "## Adaptive harness lessons")
	feedbackIdx := strings.Index(input, "## LIVE FEEDBACK FROM USER")
	if adaptiveIdx < 0 {
		t.Fatalf("missing adaptive lessons:\n%s", input)
	}
	if feedbackIdx < 0 {
		t.Fatalf("missing feedback:\n%s", input)
	}
	if adaptiveIdx > feedbackIdx {
		t.Fatalf("adaptive lessons should precede live feedback:\n%s", input)
	}
	if !strings.Contains(input, "Timeout adaptation") {
		t.Fatalf("missing deterministic timeout adaptation:\n%s", input)
	}
}

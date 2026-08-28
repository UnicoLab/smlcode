package plan

import (
	"strings"
	"testing"
)

// The registered language packs in a project that has Go and React.
func goReactSpecialists(id string) bool {
	switch id {
	case "go-worker", "react-worker", "ts-worker":
		return true
	}
	return false
}

func TestSpecialistForRoutesByTheFilesThatBroke(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"go files go to the go specialist", []string{"cmd/server/main.go"}, "go-worker"},
		{"tsx goes to react", []string{"web/src/App.tsx"}, "react-worker"},
		{"plain ts goes to ts", []string{"web/src/api.ts"}, "ts-worker"},
		// A ticket touching four frontend files and one Go file is a frontend
		// ticket; whichever file happened to be listed first is not a signal.
		{"majority wins", []string{"cmd/x.go", "web/a.tsx", "web/b.tsx", "web/c.tsx"}, "react-worker"},
		{"no files at all", nil, RoleWorker},
		{"unknown extensions", []string{"README.md", "Makefile"}, RoleWorker},
		// Naming an agent that is not registered fails to dispatch, which is
		// strictly worse than the generic worker doing it slightly less well.
		{"unregistered specialist falls back", []string{"main.rs"}, RoleWorker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpecialistFor(tc.files, goReactSpecialists); got != tc.want {
				t.Errorf("SpecialistFor(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}

func TestSpecialistForIsDeterministicOnATie(t *testing.T) {
	a := SpecialistFor([]string{"a.go", "b.tsx"}, goReactSpecialists)
	b := SpecialistFor([]string{"b.tsx", "a.go"}, goReactSpecialists)
	if a != b {
		t.Fatalf("a tie must not depend on file order: %q vs %q", a, b)
	}
}

func TestSpecialistForToleratesNoRegistry(t *testing.T) {
	if got := SpecialistFor([]string{"main.go"}, nil); got != RoleWorker {
		t.Errorf("with no registry every ticket is a generic worker's, got %q", got)
	}
}

// A ticket has to be a bug report, not an alarm: what broke, how to see it
// break, the evidence, and what "fixed" means.
func TestCorrectionTicketIsActionable(t *testing.T) {
	tk := NewCorrectionTicket(CorrectionInput{
		Source:   SourceTester,
		Failures: []string{"TestTodoStore_Add: want 1 item, got 0", "TestTodoStore_Delete: panic"},
		Summary:  "2 of 9 tests failed",
		Command:  "go test ./internal/store/",
		Output:   "--- FAIL: TestTodoStore_Add\n    store_test.go:41: want 1 item, got 0\nFAIL",
		Files:    []string{"internal/store/todo.go"},
		Squad:    "backend",
		Origin:   "T7",
	}, goReactSpecialists)

	if tk.Role != "go-worker" {
		t.Errorf("Role = %q, want go-worker", tk.Role)
	}
	if tk.Squad != "backend" {
		t.Errorf("the ticket must stay with the squad that owns the files, got %q", tk.Squad)
	}
	if tk.Column != ColReadyToDev || tk.Status != StatusReady {
		t.Errorf("a ticket must be immediately workable, got %s/%s", tk.Column, tk.Status)
	}
	// Acceptance that names the command is checkable; "the tester passes" is not.
	if !strings.Contains(tk.Acceptance, "go test ./internal/store/") {
		t.Errorf("Acceptance = %q", tk.Acceptance)
	}
	if !strings.Contains(tk.Title, "want 1 item, got 0") {
		t.Errorf("the title should carry the headline failure, got %q", tk.Title)
	}
	for _, want := range []string{
		"## What failed", "TestTodoStore_Add", "TestTodoStore_Delete",
		"## Reproduce it", "go test ./internal/store/",
		"## What it printed (tail)", "store_test.go:41",
		"## Implicated files", "internal/store/todo.go",
		"correction of task T7",
	} {
		if !strings.Contains(tk.Description, want) {
			t.Errorf("ticket description is missing %q:\n%s", want, tk.Description)
		}
	}
}

func TestCorrectionTicketSurvivesAThinFailure(t *testing.T) {
	tk := NewCorrectionTicket(CorrectionInput{Source: SourceQAGate}, nil)
	if tk.Role != RoleWorker || tk.Title == "" || tk.Description == "" {
		t.Fatalf("a detail-free failure must still produce a usable ticket: %+v", tk)
	}
	if !strings.Contains(tk.Acceptance, "qa_gate") {
		t.Errorf("with no command, acceptance falls back to the findings, got %q", tk.Acceptance)
	}
}

// A repeat correction must say so, or the next attempt repeats the last one.
func TestRepeatCorrectionTellsTheAgentNotToRepeatItself(t *testing.T) {
	tk := NewCorrectionTicket(CorrectionInput{
		Source: SourceReviewer, Failures: []string{"stub implementation"}, Attempt: 2,
	}, nil)
	if !strings.Contains(tk.Description, "Correction attempt 3") {
		t.Errorf("expected the attempt count:\n%s", tk.Description)
	}
	if !strings.Contains(tk.Description, "do something different") {
		t.Errorf("a repeat must warn against repeating:\n%s", tk.Description)
	}
}

// The output tail matters: a test runner prints the assertion that failed last,
// and the first 2KB is setup noise.
func TestTicketKeepsTheOutputTailNotTheHead(t *testing.T) {
	head := strings.Repeat("setup noise line\n", 300)
	tk := NewCorrectionTicket(CorrectionInput{
		Source: SourceTester, Command: "go test ./...",
		Output: head + "--- FAIL: TestTheThing\n    the_test.go:9: boom\n",
	}, nil)
	if !strings.Contains(tk.Description, "the_test.go:9: boom") {
		t.Error("the failing assertion must survive the clip")
	}
	// Bounded, and visibly truncated so the reader knows there was more.
	if !strings.Contains(tk.Description, "…") {
		t.Error("a clipped log should show that it was clipped")
	}
	if n := strings.Count(tk.Description, "setup noise line"); n >= 300 {
		t.Errorf("the ticket carried the whole %d-line log", n)
	}
	if len(tk.Description) > maxTicketOutput+1200 {
		t.Errorf("ticket is %d bytes; the output block is capped at %d", len(tk.Description), maxTicketOutput)
	}
}

// Three gate runs for one unresolved break must not stack three identical
// tickets — that is what made the board look like it was losing ground.
func TestCorrectionKeyDedupesTheSameDefect(t *testing.T) {
	a := CorrectionInput{Source: SourceTester, Failures: []string{"TestX failed"},
		Files: []string{"a.go", "b.go"}}
	b := CorrectionInput{Source: SourceTester, Failures: []string{"testx FAILED"},
		Files: []string{"b.go", "a.go"}} // different case + order, same defect
	if CorrectionKey(a) == CorrectionKey(b) {
		// Case-insensitive on the text, order-insensitive on the files.
		t.Log("keys match as intended")
	} else {
		t.Fatalf("the same defect produced two keys:\n%q\n%q", CorrectionKey(a), CorrectionKey(b))
	}

	different := CorrectionInput{Source: SourceTester, Failures: []string{"TestY failed"},
		Files: []string{"a.go"}}
	if CorrectionKey(a) == CorrectionKey(different) {
		t.Error("different defects must not share a key")
	}
}

func TestBoardTracksOpenCorrections(t *testing.T) {
	in := CorrectionInput{Source: SourceTester, Failures: []string{"TestX failed"}, Files: []string{"a.go"}}
	key := CorrectionKey(in)

	tk := NewCorrectionTicket(in, nil)
	StampCorrectionKey(&tk, key)
	board := &Board{Tasks: []Task{tk}}

	if !board.HasOpenCorrection(key) {
		t.Fatal("an open ticket must be found by its key")
	}
	if board.HasOpenCorrection("some-other-key") {
		t.Error("an unrelated key must not match")
	}

	// Once fixed, the same defect recurring is a NEW ticket — the old one is
	// evidence that this fix did not hold.
	board.Tasks[0].Column = ColDone
	if board.HasOpenCorrection(key) {
		t.Error("a finished ticket is not an open correction")
	}
	if (&Board{}).HasOpenCorrection(key) {
		t.Error("an empty board has no corrections")
	}
	if board.HasOpenCorrection("") {
		t.Error("an empty key matches nothing")
	}
}

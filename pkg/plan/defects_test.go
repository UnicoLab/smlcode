package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- P1: ParseReviewJSON must default-deny ---------------------------------

func TestParseReviewJSONDefaultDeny(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantApproved bool
		wantMalIssue bool
	}{
		{
			name:         "clean approval",
			raw:          `{"approved":true,"score":9,"issues":[],"summary":"good"}`,
			wantApproved: true,
		},
		{
			name:         "clean rejection",
			raw:          `{"approved":false,"score":2,"issues":["stub"],"summary":"bad"}`,
			wantApproved: false,
		},
		{
			// The regression: reviewer hit max_tokens mid-issues[]. "approve" is
			// a substring of "approved", so the old heuristic approved it.
			name:         "truncated rejection unrepairable",
			raw:          `{"approved": false, "score": 10, "issues": ["stub code in parser.go`,
			wantApproved: false,
		},
		{
			name:         "prose containing the word approve",
			raw:          "I would approve this once tests pass.",
			wantApproved: false,
			wantMalIssue: true,
		},
		{
			name:         "prose rejection",
			raw:          "This is not acceptable; the parser is a stub.",
			wantApproved: false,
			wantMalIssue: true,
		},
		{
			name:         "empty output",
			raw:          "",
			wantApproved: false,
			wantMalIssue: true,
		},
		{
			name:         "tool call echo",
			raw:          `<function=ws_read>{"path":"main.go"}</function>`,
			wantApproved: false,
			wantMalIssue: true,
		},
		{
			// An explicit verdict the model actually stated survives even when
			// the surrounding object is unrecoverable — mirrors ParseTesterJSON.
			name:         "explicit approved true in unparsable prose",
			raw:          "Looks good to me.\n\"approved\": true and nothing else parses {{{",
			wantApproved: true,
		},
		{
			name:         "explicit false wins over explicit true",
			raw:          `garbage {{{ "approved": true ... later "approved": false`,
			wantApproved: false,
			wantMalIssue: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseReviewJSON(tc.raw)
			if got.Approved != tc.wantApproved {
				t.Fatalf("approved=%v want %v (result %+v)", got.Approved, tc.wantApproved, got)
			}
			if !got.Approved && len(got.Issues) == 0 {
				t.Fatal("a rejection must always carry at least one issue")
			}
			if tc.wantMalIssue {
				found := false
				for _, is := range got.Issues {
					if is == ReviewMalformedIssue {
						found = true
					}
				}
				if !found {
					t.Fatalf("want ReviewMalformedIssue in %v", got.Issues)
				}
			}
		})
	}
}

// --- P2: TesterHasShellEvidence must need a harness-controlled frame -------

func TestTesterHasShellEvidence(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"smoke section", SmokeSectionHeader + "\nPASSED\ncmd: go test ./...", true},
		{"react observation", "Observation: ok  slmcode/pkg/plan  0.01s", true},
		{"ws_shell frame", `{"tool":"ws_shell","args":{"command":"go test ./..."}}`, true},
		{"exit status line", "go test ./...\nexit status 1", true},
		{"exit code colon", "npm test\nexit code: 0", true},
		{"exit_code equals", "pytest -q\nexit_code=2", true},
		{"exit error", "exit error: signal killed", true},

		// Everything below is prose a hallucinating tester can write for free.
		{"prose running + ok", "Running the test suite… OK, all tests pass", false},
		{"prose ran", "I ran the tests and they all passed.", false},
		{"prose executed", "Executed go test ./... successfully.", false},
		{"bare command name", "go test ./... should pass", false},
		{"stdout word", "The stdout looked clean.", false},
		{"dollar prompt", "$ go test ./...", false},
		{"commands key only", `{"passed":true,"commands":["pytest -q"]}`, false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TesterHasShellEvidence(tc.raw); got != tc.want {
				t.Fatalf("TesterHasShellEvidence(%q)=%v want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseTesterJSONRejectsProseEvidence(t *testing.T) {
	// The full path: prose "evidence" must not reach Passed=true.
	raw := "Running the test suite… OK, all tests pass\n" +
		`{"passed":true,"commands":["go test ./..."],"summary":"all green"}`
	if r := ParseTesterJSON(raw); r.Passed {
		t.Fatalf("prose evidence must not pass: %+v", r)
	}
}

// --- P3: ReconcileFiles must not fabricate scope ---------------------------

func TestReconcileFilesNeverFabricates(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name       string
		claimed    []string
		discovered []string
		want       []string
	}{
		{"existing claims kept", []string{"a.go", "b.go"}, nil, []string{"a.go", "b.go"}},
		{"greenfield create kept", []string{"src/new.go"}, nil, []string{"src/new.go"}},
		{"hallucinated falls back to discovered", []string{"internal/nope.go"}, []string{"c.go"}, []string{"c.go"}},
		{"hallucinated with no discovered yields nothing", []string{"internal/nope.go"}, nil, nil},
		{"nothing at all yields nothing", nil, nil, nil},
		{"discovered that do not exist yield nothing", nil, []string{"ghost.go"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReconcileFiles(root, tc.claimed, tc.discovered)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

// --- P4: Board must be safe under concurrent review goroutines -------------

func TestBoardConcurrentAccess(t *testing.T) {
	b := &Board{}
	for i := 1; i <= 8; i++ {
		b.Tasks = append(b.Tasks, Task{ID: fmt.Sprintf("T%d", i), Title: "t", Role: RoleWorker, Column: ColReadyToDev})
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("T%d", n+1)
			for r := 0; r < 40; r++ {
				if tk, ok := b.Get(id); ok {
					tk.Review = "reviewed"
					b.UpdateTask(tk)
				}
				_ = b.ReadyTasks()
				_ = b.ExecutableTasks()
				_ = b.FailedCount()
				_ = b.AllDone()
				_ = b.ByColumn()
				_, _ = b.ToMarkdown()
				b.AddTask(Task{Title: "added", Role: RoleWorker})
			}
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, tk := range b.Tasks {
		if seen[tk.ID] {
			t.Fatalf("duplicate task id %q — AddTask must mint ids under the lock", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestAddTaskAssignsUniqueIDs(t *testing.T) {
	b := &Board{Tasks: []Task{{ID: "T1"}, {ID: "T3"}}}
	got := b.AddTask(Task{Title: "new"})
	if got.ID != "T4" {
		t.Fatalf("id=%q want T4", got.ID)
	}
	again := b.AddTask(Task{Title: "another"})
	if again.ID != "T5" {
		t.Fatalf("id=%q want T5", again.ID)
	}
	if b.AddTask(Task{ID: "T1", Title: "replace"}); len(b.Tasks) != 4 {
		t.Fatalf("explicit id must replace, got %d tasks", len(b.Tasks))
	}
}

// --- item 15: parsers use the schema-aware ladder ---------------------------

func TestParsersUseSchemaAwareRepair(t *testing.T) {
	// "passed":"yes" is a string where the contract wants a bool: only the
	// schema-aware ladder coerces it.
	r := ParseTesterJSON(SmokeSectionHeader + "\nok\n" +
		`{"passed":"yes","commands":["go test ./..."],"summary":"green"}`)
	if !r.Passed {
		t.Fatalf(`"passed":"yes" should coerce to true with shell evidence: %+v`, r)
	}
	rev := ParseReviewJSON(`{"approved":"true","score":"8","issues":[],"summary":"ok"}`)
	if !rev.Approved {
		t.Fatalf(`"approved":"true" should coerce: %+v`, rev)
	}
	if _, ok := ParseEscalateDecide(`{"action":"retry","confidence":"0.8"}`); !ok {
		t.Fatal("escalate decide should coerce a string confidence")
	}
}

func TestSmokeSectionHeaderIsLowercaseSafe(t *testing.T) {
	if !strings.Contains(strings.ToLower(SmokeSectionHeader), "deterministic smoke") {
		t.Fatalf("SmokeSectionHeader=%q", SmokeSectionHeader)
	}
}

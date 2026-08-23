package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ASSEMBLED prompt — not merely the sum of the packed bodies — must fit the
// role's token budget. Skills, instructions, repo map, memory docs, file
// excerpts and the user query all firing at once is the worst case, and it is
// the ordinary case for a worker on a small local model.
func TestWorstCasePackRendersUnderBudget(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("func Aaaa(bbbb int) error { return doThing(bbbb) } // relevant target line\n", 900)
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := New(filepath.Join(root, ".slmcode"))
	if err := st.Init("budget"); err != nil {
		t.Fatal(err)
	}
	doc := strings.Repeat("## Section\n\nProject history paragraph mentioning `pkg/x/y.go`.\n\n", 600)
	for _, d := range []string{DocProject, DocContext, DocPlan, DocTasks} {
		if err := st.Write(d, doc); err != nil {
			t.Fatal(err)
		}
	}
	skills := strings.Repeat("## Skill\n\nAlways do the thing carefully and completely.\n\n", 400)
	// A long user request: a pasted spec or stack trace. This used to be copied
	// into the pack verbatim and rendered without ever being budgeted.
	query := strings.Repeat("please refactor the Aaaa helper across the codebase. ", 400)

	for _, window := range []int{4096, 8192, 32768} {
		p := NewPackerWithBudget(st, root, window)
		for _, role := range []string{"worker", "corrector", "reviewer", "planner", "explorer"} {
			pack, err := p.BuildPack(BuildRequest{
				Role: role, Query: query, TaskID: "T1",
				TaskTitle:       strings.Repeat("Refactor the Aaaa helper everywhere ", 40),
				TaskDescription: "Refactor Aaaa in a.go", Acceptance: "go build passes",
				Docs:           []string{DocProject, DocContext, DocPlan, DocTasks},
				Files:          []string{"a.go", "b.go", "c.go", "d.go"},
				SkillsMarkdown: skills,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := DefaultTokenCounter(pack.Render())
			if got > pack.BudgetTokens {
				t.Errorf("window=%d role=%s: rendered pack is %d tokens, budget is %d (overshoot %+d)",
					window, role, got, pack.BudgetTokens, got-pack.BudgetTokens)
			}
		}
	}
}

// A long query must be clipped rather than dropped: it is the request itself.
func TestPackKeepsClippedQuery(t *testing.T) {
	root := t.TempDir()
	st := New(filepath.Join(root, ".slmcode"))
	if err := st.Init("q"); err != nil {
		t.Fatal(err)
	}
	p := NewPackerWithBudget(st, root, 8192)
	query := "FIRST-LINE-MARKER " + strings.Repeat("filler words that go on and on. ", 800)
	pack, err := p.BuildPack(BuildRequest{Role: "worker", Query: query})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Query, "FIRST-LINE-MARKER") {
		t.Fatal("clipping dropped the head of the query")
	}
	if DefaultTokenCounter(pack.Query) > MaxQueryTokens {
		t.Fatalf("query %d tokens exceeds MaxQueryTokens=%d", DefaultTokenCounter(pack.Query), MaxQueryTokens)
	}
}

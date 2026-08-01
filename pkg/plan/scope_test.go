package plan

import (
	"strings"
	"testing"
)

func TestParseScopeInterviewRich(t *testing.T) {
	raw := `{
	  "needs_user":true,
	  "questions":[{
	    "id":"q1","header":"Language","question":"Which runtime?",
	    "options":[
	      {"label":"Python","description":"stdlib","recommended":true},
	      {"label":"Go","description":"modules"}
	    ],
	    "recommended":"Python"
	  }],
	  "assumptions":["minimal CLI"],
	  "acceptance":["python main.py prints hello"],
	  "non_goals":["web UI"],
	  "language":"python","entrypoint":"main.py",
	  "prd":{"summary":"hello CLI","goals":["print hello"],"acceptance":["exits 0"]}
	}`
	in := ParseScopeInterview(raw)
	if !in.NeedsUser || len(in.Questions) != 1 {
		t.Fatalf("%+v", in)
	}
	if in.Questions[0].Recommended != "Python" {
		t.Fatalf("recommended=%q", in.Questions[0].Recommended)
	}
	ans := ResolveWithDefaults(in)
	got := ApplyScopeAnswers(in, ans)
	if got.NeedsUser || len(got.Questions) != 0 {
		t.Fatalf("should clear questions: %+v", got)
	}
	joined := strings.Join(got.Assumptions, "\n")
	if !strings.Contains(joined, "Decision") || !strings.Contains(joined, "Python") {
		t.Fatalf("assumptions=%v", got.Assumptions)
	}
	c := got.ToClarifyResult()
	if strings.ToLower(c.Language) != "python" || c.Entrypoint != "main.py" {
		t.Fatalf("%+v", c)
	}
	if len(c.Acceptance) == 0 {
		t.Fatal("acceptance should carry from PRD")
	}
}

func TestParseScopeInterviewLegacyStrings(t *testing.T) {
	raw := `{"needs_user":false,"questions":["Use Flask or FastAPI?"],"assumptions":["Python"],"acceptance":["ok"],"language":"python","entrypoint":"app.py"}`
	in := ParseScopeInterview(raw)
	if len(in.Questions) != 1 || in.Questions[0].Question == "" {
		t.Fatalf("%+v", in)
	}
	if len(in.Questions[0].Options) < 1 {
		t.Fatal("legacy questions need synthetic options")
	}
}

func TestEnsureTaskPRDs(t *testing.T) {
	prd := ScopePRD{
		Language: "python", Entrypoint: "main.py",
		Acceptance: []string{"python main.py prints hello", "python main.py -h exits 0"},
		NonGoals:   []string{"web UI"},
	}
	tasks := []Task{
		{ID: "T1", Title: "Create main", Role: RoleWorker, Description: "write cli"},
		{ID: "T2", Title: "Verify", Role: RoleTester},
	}
	out := EnsureTaskPRDs(tasks, prd, "Create a Python CLI")
	if strings.TrimSpace(out[0].Acceptance) == "" {
		t.Fatal("worker acceptance missing")
	}
	if len(out[0].Checklist) == 0 {
		t.Fatal("expected checklist seeded from PRD")
	}
	if !strings.Contains(out[0].Notes, "Non-goals") {
		t.Fatalf("notes=%q", out[0].Notes)
	}
	if out[0].Files[0] != "main.py" {
		t.Fatalf("files=%v", out[0].Files)
	}
	if strings.TrimSpace(out[1].Acceptance) == "" {
		t.Fatal("tester acceptance")
	}
}

func TestJudgeTaskScopeHeuristics(t *testing.T) {
	prd := ScopePRD{Acceptance: []string{"x works"}}
	tasks := []Task{
		{ID: "T1", Title: "fix", Role: RoleWorker, Description: "x", Acceptance: "done"},
	}
	j := JudgeTaskScopeHeuristics(tasks, prd)
	if j.OK || len(j.WeakIDs) == 0 {
		t.Fatalf("%+v", j)
	}
	good := []Task{{
		ID: "T1", Title: "Add hello CLI in main.py", Role: RoleWorker,
		Description: "Create main.py with argparse that prints hello",
		Files:       []string{"main.py"},
		Acceptance:  "python main.py prints hello and exits 0",
	}}
	j2 := JudgeTaskScopeHeuristics(good, prd)
	if !j2.OK {
		t.Fatalf("expected ok: %+v", j2)
	}
}

func TestNormalizeClarifyMode(t *testing.T) {
	if NormalizeClarifyMode("interview") != ClarifyAsk {
		t.Fatal()
	}
	if NormalizeClarifyMode("") != ClarifyAuto {
		t.Fatal()
	}
}

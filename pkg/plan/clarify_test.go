package plan

import "testing"

func TestNeedsClarification(t *testing.T) {
	if !NeedsClarification("build an agent") {
		t.Fatal("expected vague query to need clarification")
	}
	if NeedsClarification("Add JWT claims validation in pkg/auth/jwt.go and cover with go test") {
		t.Fatal("expected concrete query to skip clarification")
	}
	if NeedsClarification("Create a FastAPI hello endpoint with pytest") {
		t.Fatal("expected stack-specified query to skip clarification")
	}
}

func TestParseClarifyJSON(t *testing.T) {
	raw := `{"needs_user":false,"questions":[],"assumptions":["Python CLI","entrypoint main.py"],"acceptance":["python main.py --help exits 0"],"language":"python","entrypoint":"main.py"}`
	c := ParseClarifyJSON(raw)
	if c.Language != "python" || len(c.Assumptions) != 2 {
		t.Fatalf("%+v", c)
	}
	pl := MergeClarifyIntoPlan(Plan{Summary: "x"}, c)
	if len(pl.Assumptions) < 3 {
		t.Fatalf("assumptions=%v", pl.Assumptions)
	}
}

func TestParsePlanJSONFallbackNoRawDump(t *testing.T) {
	raw := "```json\n{\"summary\": broken\n```\nWe should build a small CLI."
	p, err := ParsePlanJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if looksLikeRawJSON(p.Summary) {
		t.Fatalf("summary still looks like JSON: %q", p.Summary)
	}
	planMD, _ := (&Board{Plan: p, Query: "build cli"}).ToMarkdown()
	if looksLikeRawJSON(firstLine(planMD)) {
		t.Fatal("plan markdown should not start with JSON")
	}
}

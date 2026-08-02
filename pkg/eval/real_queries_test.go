package eval

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

func TestRealQueriesHarnessMeetsReferenceBar(t *testing.T) {
	for _, rq := range RealQueries() {
		t.Run(rq.ID, func(t *testing.T) {
			res := EvaluateHarnessPlan(rq)
			if !res.OK {
				t.Fatalf("harness below reference bar: gaps=%v tasks=%d", res.Gaps, res.TaskCount)
			}
			if !res.HasTester {
				t.Fatal("expected tester")
			}
		})
	}
}

func TestLangGraphGarbageFailsReferenceWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := WriteLangGraphGarbageFixture(root); err != nil {
		t.Fatal(err)
	}
	rq := RealQueries()[0]
	issues := EvaluateWorkspaceAgainstReference(root, rq)
	if len(issues) < 3 {
		t.Fatalf("garbage must fail hard, got %#v", issues)
	}
	// Expert bar must reject this as incomplete.
	rep := quality.FormatCompletenessReport(issues)
	if !strings.Contains(rep, "reference bar") && !strings.Contains(rep, "completeness") {
		t.Fatalf("report missing header: %s", rep)
	}
}

func TestLangGraphReferenceWorkspacePasses(t *testing.T) {
	root := t.TempDir()
	if err := WriteLangGraphReferenceScaffold(root); err != nil {
		t.Fatal(err)
	}
	rq := RealQueries()[0]
	issues := EvaluateWorkspaceAgainstReference(root, rq)
	if len(issues) != 0 {
		t.Fatalf("expert reference must pass completeness, got %#v", issues)
	}
	// Static on agent module must also be clean.
	st := quality.CheckStaticQuality(root, plan.Task{
		Files: []string{"src/lg_agent/agents/base.py", "main.py"},
	})
	if len(st) != 0 {
		t.Fatalf("static issues on reference: %#v", st)
	}
}

func TestCapPreservesHarnessInSanitizeBoard(t *testing.T) {
	rq := RealQueries()[0]
	weak := weakSplitterTasks(rq)
	// Inflate with filler tasks so sanitize+harness would exceed 8.
	for i := 0; i < 6; i++ {
		weak = append(weak, plan.Task{
			ID: "F" + string(rune('a'+i)), Title: "Filler " + string(rune('a'+i)),
			Role: plan.RoleWorker, Description: "optional docs polish",
			Files: []string{"README.md"}, Acceptance: "readme updated",
		})
	}
	out := plan.SanitizeTasks(weak, "", rq.Query)
	capped := plan.CapTasksPreserveHarness(out, 8)
	if len(capped) > 8 {
		t.Fatalf("cap failed: %d", len(capped))
	}
	blob := ""
	hasTester := false
	for _, tsk := range capped {
		blob += tsk.Title + " " + strings.Join(tsk.Files, " ") + " "
		if tsk.Role == plan.RoleTester {
			hasTester = true
		}
	}
	if !hasTester {
		t.Fatal("tester dropped by cap")
	}
	for _, need := range []string{"requirements.txt", "main.py"} {
		if !strings.Contains(strings.ToLower(blob), need) {
			t.Fatalf("harness file %s dropped by cap; board=%s", need, blob)
		}
	}
}

func TestRealQueryCasesExport(t *testing.T) {
	cases := RealQueryCases()
	if len(cases) != len(RealQueries()) {
		t.Fatal("case export mismatch")
	}
	if cases[0].ID != "langgraph-class-template" {
		t.Fatalf("unexpected first case %s", cases[0].ID)
	}
}

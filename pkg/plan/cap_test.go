package plan

import (
	"strings"
	"testing"
)

func TestCapTasksPreserveHarness(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Docs", Role: RoleWorker, Files: []string{"README.md"}},
		{ID: "T2", Title: "More docs", Role: RoleWorker, Files: []string{"NOTES.md"}},
		{ID: "T3", Title: "Add requirements.txt", Role: RoleWorker, Files: []string{"requirements.txt"}},
		{ID: "T4", Title: "Add main.py", Role: RoleWorker, Files: []string{"main.py"}},
		{ID: "T5", Title: "Add pytest", Role: RoleWorker, Files: []string{"tests/test_smoke.py"}},
		{ID: "T6", Title: "Verify", Role: RoleTester},
		{ID: "T7", Title: "Extra1", Role: RoleWorker, Files: []string{"a.py"}},
		{ID: "T8", Title: "Extra2", Role: RoleWorker, Files: []string{"b.py"}},
		{ID: "T9", Title: "Extra3", Role: RoleWorker, Files: []string{"c.py"}},
	}
	out := CapTasksPreserveHarness(tasks, 8)
	if len(out) != 8 {
		t.Fatalf("len=%d want 8", len(out))
	}
	hasTester, hasReq, hasMain := false, false, false
	for _, tk := range out {
		if tk.Role == RoleTester {
			hasTester = true
		}
		blob := tk.Title + " " + strings.Join(tk.Files, " ")
		if strings.Contains(blob, "requirements.txt") {
			hasReq = true
		}
		if strings.Contains(blob, "main.py") {
			hasMain = true
		}
	}
	if !hasTester || !hasReq || !hasMain {
		t.Fatalf("harness dropped tester=%v req=%v main=%v → %+v", hasTester, hasReq, hasMain, out)
	}
}

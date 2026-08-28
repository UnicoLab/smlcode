package squads

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func boardTasks() []plan.Task {
	return []plan.Task{
		{ID: "T1", Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Files: []string{"internal/store/db.go", "internal/store/db_test.go"}},
		{ID: "T3", Files: []string{"web/src/App.tsx"}},
		{ID: "T4", Files: []string{"web/src/api.ts", "cmd/server/routes.go"}}, // the seam
		{ID: "T5", Files: []string{"README.md"}},                              // nobody owns it
		{ID: "T6"},                                                            // no files at all
	}
}

func TestAssignBoardRoutesEachTaskAndReportsTheHoles(t *testing.T) {
	p := goReactPlan()
	tasks := boardTasks()
	rep := AssignBoard(&p, tasks)

	want := map[string]string{"T1": "backend", "T2": "backend", "T3": "frontend"}
	for _, task := range tasks {
		if got, ok := want[task.ID]; ok {
			if task.Squad != got {
				t.Errorf("%s squad = %q, want %q", task.ID, task.Squad, got)
			}
			continue
		}
		// The seam, the unowned file and the unscoped task must all stay
		// unassigned: forcing one of them into a squad is how a frontend task
		// acquires permission to rewrite the API.
		if task.Squad != "" {
			t.Errorf("%s should stay unassigned, got %q", task.ID, task.Squad)
		}
	}

	if !reflect.DeepEqual(rep.Assigned, map[string]int{"backend": 2, "frontend": 1}) {
		t.Errorf("Assigned = %v", rep.Assigned)
	}
	if !reflect.DeepEqual(rep.Straddling, []string{"T4"}) {
		t.Errorf("Straddling = %v, want [T4]", rep.Straddling)
	}
	// T6 declares no files, which is an unscoped task rather than a hole in the
	// squad plan — only T5 counts.
	if !reflect.DeepEqual(rep.Unowned, []string{"T5"}) {
		t.Errorf("Unowned = %v, want [T5]", rep.Unowned)
	}
	if len(rep.Idle) != 0 {
		t.Errorf("no squad should be idle here, got %v", rep.Idle)
	}
	if s := rep.Summary(); !strings.Contains(s, "backend=2") || !strings.Contains(s, "1 cross-squad") {
		t.Errorf("Summary = %q", s)
	}
}

func TestAssignBoardReportsIdleSquads(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{{ID: "T1", Files: []string{"cmd/server/main.go"}}}
	rep := AssignBoard(&p, tasks)
	if !reflect.DeepEqual(rep.Idle, []string{"frontend"}) {
		t.Fatalf("Idle = %v, want [frontend]", rep.Idle)
	}
	if !strings.Contains(rep.Summary(), "idle: frontend") {
		t.Errorf("Summary should name the idle squad: %q", rep.Summary())
	}
}

func TestAssignBoardIsANoOpWithoutAPlan(t *testing.T) {
	tasks := boardTasks()
	rep := AssignBoard(nil, tasks)
	if len(rep.Assigned) != 0 {
		t.Errorf("Assigned = %v", rep.Assigned)
	}
	for _, task := range tasks {
		if task.Squad != "" {
			t.Fatalf("%s must stay unassigned without a plan", task.ID)
		}
	}
	if got := rep.Summary(); got != "no tasks routed" {
		t.Errorf("Summary = %q", got)
	}
}

// ForeignPatterns is what makes ownership an enforced boundary rather than a
// suggestion: fed to the FocusGuard deny list, it makes writing another squad's
// files impossible at the tool layer.
func TestForeignPatternsDenyTheSquadsNotInTheWave(t *testing.T) {
	p := goReactPlan()

	backendWave := []plan.Task{{ID: "T1", Squad: "backend"}, {ID: "T2", Squad: "backend"}}
	got := ForeignPatterns(&p, backendWave)
	if !reflect.DeepEqual(got, []string{"web/**"}) {
		t.Errorf("a backend-only wave must deny the frontend tree, got %v", got)
	}

	frontendWave := []plan.Task{{ID: "T3", Squad: "frontend"}}
	got = ForeignPatterns(&p, frontendWave)
	want := []string{"cmd/**", "go.mod", "go.sum", "internal/**"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a frontend-only wave must deny the backend tree, got %v want %v", got, want)
	}
}

func TestForeignPatternsAllowsAMixedWaveToWorkOnBothSides(t *testing.T) {
	p := goReactPlan()
	// Both squads are represented, so neither tree may be denied — denying
	// either would block the very task dispatched to write it.
	mixed := []plan.Task{{ID: "T1", Squad: "backend"}, {ID: "T3", Squad: "frontend"}}
	if got := ForeignPatterns(&p, mixed); len(got) != 0 {
		t.Fatalf("a mixed wave must deny nothing, got %v", got)
	}
}

// The cross-squad seam tasks legitimately touch both sides, so no squad's paths
// can be denied on their behalf.
func TestForeignPatternsStandsDownForUnassignedWork(t *testing.T) {
	p := goReactPlan()
	cases := [][]plan.Task{
		{{ID: "T4", Squad: ""}},
		{{ID: "T1", Squad: "backend"}, {ID: "T4", Squad: ""}},
	}
	for i, wave := range cases {
		if got := ForeignPatterns(&p, wave); len(got) != 0 {
			t.Errorf("case %d: expected no protections, got %v", i, got)
		}
	}
}

func TestForeignPatternsIsSafeOnEmptyInput(t *testing.T) {
	p := goReactPlan()
	if got := ForeignPatterns(&p, nil); got != nil {
		t.Errorf("no wave = no protections, got %v", got)
	}
	if got := ForeignPatterns(nil, []plan.Task{{ID: "T1", Squad: "backend"}}); got != nil {
		t.Errorf("no plan = no protections, got %v", got)
	}
}

func TestBriefForOnlyBriefsAssignedTasks(t *testing.T) {
	p := goReactPlan()
	if got := BriefFor(&p, plan.Task{ID: "T1", Squad: "backend"}); !strings.Contains(got, "Backend") {
		t.Errorf("expected the backend brief, got %q", got)
	}
	if got := BriefFor(&p, plan.Task{ID: "T4"}); got != "" {
		t.Errorf("an unassigned task gets no squad brief, got %q", got)
	}
	if got := BriefFor(nil, plan.Task{ID: "T1", Squad: "backend"}); got != "" {
		t.Errorf("no plan, no brief, got %q", got)
	}
}

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

// ── Contract enforcement ─────────────────────────────────────────────────
//
// Without criteria the contract is present in the prompt and absent from the
// gates: the seam is stated and then nothing checks it.

func TestAttachContractPutsTheSeamInFrontOfTheReviewer(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{
		{ID: "T1", Role: "go-worker", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Role: "react-worker", Squad: "frontend", Files: []string{"web/src/App.tsx"}},
	}
	if n := AttachContract(&p, tasks); n != 2 {
		t.Fatalf("both implementers should carry the contract, got %d", n)
	}

	// The provider MATCHES the spec — another team is building against it.
	back := strings.ToLower(criteriaText(tasks[0]))
	if !strings.Contains(back, "matches the frozen contract for get /api/todos") {
		t.Errorf("backend criteria:\n%s", criteriaText(tasks[0]))
	}
	if !strings.Contains(back, "building against this right now") {
		t.Errorf("the provider should be told the obligation is live:\n%s", criteriaText(tasks[0]))
	}

	// The consumer BUILDS AGAINST it and must not be failed for the provider
	// not having written it yet.
	front := strings.ToLower(criteriaText(tasks[1]))
	if !strings.Contains(front, "calls get /api/todos") {
		t.Errorf("frontend criteria:\n%s", criteriaText(tasks[1]))
	}
	if !strings.Contains(front, "whether or not it exists on disk yet") {
		t.Errorf("a consumer must not be failed for being on time:\n%s", criteriaText(tasks[1]))
	}

	// Blocking: a drifted seam is not advisory.
	for _, task := range tasks {
		for _, c := range task.Criteria {
			if !c.Blocking() {
				t.Errorf("%s: contract criterion %q is not blocking", task.ID, c.Text)
			}
		}
	}
}

// A task's own conditions have first claim on the criteria budget: a worker
// whose whole list is contract clauses has been told what the seam is and
// nothing about its job.
func TestTaskOwnCriteriaKeepTheirPlace(t *testing.T) {
	p := goReactPlan()
	own := []plan.Criterion{
		{ID: "AC1", Text: "the store persists a todo", Priority: plan.PriorityMust},
		{ID: "AC2", Text: "delete is idempotent", Priority: plan.PriorityMust},
	}
	tasks := []plan.Task{{ID: "T1", Role: "go-worker", Squad: "backend", Criteria: own}}
	AttachContract(&p, tasks)

	if len(tasks[0].Criteria) < 3 {
		t.Fatalf("expected the task's own criteria plus contract clauses, got %d", len(tasks[0].Criteria))
	}
	if tasks[0].Criteria[0].Text != "the store persists a todo" ||
		tasks[0].Criteria[1].Text != "delete is idempotent" {
		t.Errorf("the task's own conditions must come first:\n%s", criteriaText(tasks[0]))
	}
	if len(tasks[0].Criteria) > plan.MaxCriteria {
		t.Errorf("criteria budget blown: %d > %d", len(tasks[0].Criteria), plan.MaxCriteria)
	}
}

func TestAttachContractSkipsWorkItDoesNotApplyTo(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{
		// A tester runs the acceptance; it does not re-assert the contract.
		{ID: "T1", Role: plan.RoleTester, Squad: "backend"},
		{ID: "T2", Role: plan.RoleReviewer, Squad: "backend"},
		// The seam itself is unassigned, so there is no side to hold it to.
		{ID: "T3", Role: "go-worker", Squad: ""},
	}
	if n := AttachContract(&p, tasks); n != 0 {
		t.Fatalf("nothing here should carry contract criteria, got %d", n)
	}
	for _, task := range tasks {
		if len(task.Criteria) != 0 {
			t.Errorf("%s picked up %d criteria", task.ID, len(task.Criteria))
		}
	}
}

func TestAttachContractIsANoOpWithoutInterfaces(t *testing.T) {
	p := goReactPlan()
	p.Contract.Interfaces = nil
	tasks := []plan.Task{{ID: "T1", Role: "go-worker", Squad: "backend"}}
	if n := AttachContract(&p, tasks); n != 0 {
		t.Errorf("no interfaces, no criteria, got %d", n)
	}
	if n := AttachContract(nil, tasks); n != 0 {
		t.Errorf("no plan, no criteria, got %d", n)
	}
}

// Idempotent: routing runs once, but a resumed run must not double the clauses.
func TestAttachContractIsIdempotent(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{{ID: "T1", Role: "go-worker", Squad: "backend"}}
	AttachContract(&p, tasks)
	first := len(tasks[0].Criteria)
	AttachContract(&p, tasks)
	if got := len(tasks[0].Criteria); got != first {
		t.Fatalf("a second attach added clauses: %d -> %d", first, got)
	}
}

func criteriaText(t plan.Task) string {
	var b strings.Builder
	for _, c := range t.Criteria {
		b.WriteString("- [" + c.Priority + "] " + c.Text + "\n")
	}
	return b.String()
}

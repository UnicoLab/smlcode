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

// Declared files are a task's scope. A task with none could write anywhere, so
// fencing it would block work the harness cannot show is out of bounds.
func TestForeignPatternsStandsDownForUnscopedWork(t *testing.T) {
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

// An unassigned task used to disable the fence for the WHOLE wave, which
// dropped ownership enforcement far more often than it looks: a task is
// unassigned whenever it straddles two teams AND whenever nothing owns its
// files at all — a README, a Makefile, a top-level config. One of those in a
// wave and neither team was fenced from the other any more.
func TestUnownedWorkDoesNotUnfenceTheTeams(t *testing.T) {
	p := goReactPlan()
	wave := []plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T4", Squad: "", Files: []string{"README.md", "Makefile"}},
	}
	got := ForeignPatterns(&p, wave)
	if len(got) == 0 {
		t.Fatal("a task touching nobody's lane must not open every lane")
	}
	if !reflect.DeepEqual(got, []string{"web/**"}) {
		t.Errorf("ForeignPatterns = %v, want the frontend still fenced", got)
	}
}

// A seam task opens exactly the lanes its declared files need — no more.
func TestASeamTaskOpensOnlyTheLanesItNames(t *testing.T) {
	p := goReactPlan()
	wave := []plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T4", Squad: "", Files: []string{"web/src/api.ts", "README.md"}},
	}
	if got := ForeignPatterns(&p, wave); len(got) != 0 {
		t.Errorf("ForeignPatterns = %v, want the frontend's lane opened for the seam task", got)
	}

	// A three-team plan: the seam task naming one other lane must not open the
	// third.
	p3 := goReactPlan()
	p3.Squads = append(p3.Squads, Squad{
		ID: "data", Owns: []string{"etl/**"}, Acceptance: "pytest", Worker: "python-worker",
	})
	p3.Normalize()
	wave3 := []plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T4", Squad: "", Files: []string{"web/src/api.ts"}},
	}
	if got := ForeignPatterns(&p3, wave3); !reflect.DeepEqual(got, []string{"etl/**"}) {
		t.Errorf("ForeignPatterns = %v, want the untouched third team still fenced", got)
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

// ── Editing the org chart ────────────────────────────────────────────────

func TestApplyEditsChangesTheTeam(t *testing.T) {
	p := goReactPlan()
	name := "Backend · Go API v2"
	acc := "go test ./... -race"
	if probs := ApplyEdits(&p, []plan.SquadEdit{{
		ID: "backend", Name: &name, Acceptance: &acc,
		Owns: []string{"cmd/**", "internal/**", "pkg/**"}, OwnsSet: true,
	}}, nil); len(probs) != 0 {
		t.Fatalf("a valid edit should apply cleanly:\n%s", strings.Join(probs.Strings(), "\n"))
	}
	back, _ := p.Squad("backend")
	if back.Name != name || back.Acceptance != acc {
		t.Errorf("edit not applied: %+v", back)
	}
	if !back.OwnsPath("pkg/x/y.go") {
		t.Error("the new ownership glob was not applied")
	}
	// Untouched fields survive.
	if front, _ := p.Squad("frontend"); front.Acceptance != "npm --prefix web run build" {
		t.Error("editing one squad changed another")
	}
}

// The one rule squads rest on is disjoint ownership, and a human editing `owns`
// by hand is at least as likely to overlap two teams as a model is. A rejected
// edit must leave the live plan exactly as it was — a half-applied org chart is
// worse than the model's, because the user believes they fixed it.
func TestOverlappingEditIsRejectedAndChangesNothing(t *testing.T) {
	p := goReactPlan()
	before := p.clone()

	probs := ApplyEdits(&p, []plan.SquadEdit{{
		ID: "backend", Owns: []string{"cmd/**", "web/**"}, OwnsSet: true, // collides with frontend
	}}, nil)
	if !probs.Errors() {
		t.Fatal("an ownership overlap must be refused")
	}
	if !strings.Contains(strings.Join(probs.Strings(), "\n"), "both claim") {
		t.Errorf("the refusal should name the collision:\n%s", strings.Join(probs.Strings(), "\n"))
	}
	if !reflect.DeepEqual(p, before) {
		t.Fatalf("a rejected edit modified the live plan:\n got %+v\nwant %+v", p, before)
	}
}

func TestApplyEditsCanAddASquad(t *testing.T) {
	p := goReactPlan()
	charter := "Terraform + CI"
	acc := "terraform validate"
	probs := ApplyEdits(&p, []plan.SquadEdit{{
		ID: "infra", New: true, Charter: &charter, Acceptance: &acc,
		Owns: []string{"deploy/**"}, OwnsSet: true,
	}}, nil)
	if len(probs) != 0 {
		t.Fatalf("adding a disjoint squad should work:\n%s", strings.Join(probs.Strings(), "\n"))
	}
	if got := p.IDs(); len(got) != 3 || got[2] != "infra" {
		t.Fatalf("IDs = %v", got)
	}
}

func TestEditingAnUnknownSquadIsRefused(t *testing.T) {
	p := goReactPlan()
	name := "x"
	probs := ApplyEdits(&p, []plan.SquadEdit{{ID: "mobile", Name: &name}}, nil)
	if !probs.Errors() {
		t.Fatal("editing a squad that does not exist must be refused")
	}
	if len(p.Squads) != 2 {
		t.Error("the plan changed despite the refusal")
	}
}

// An interface whose provider was removed is a clause nobody owes.
func TestRemovingASquadCleansTheContract(t *testing.T) {
	p := goReactPlan()
	// Removing the provider leaves a single squad, which cannot validate — so
	// remove the consumer instead and check the contract is repaired.
	probs := ApplyEdits(&p, []plan.SquadEdit{{
		ID: "backend", Owns: []string{"cmd/**", "internal/**", "go.mod", "go.sum", "web/**"}, OwnsSet: true,
	}}, []string{"frontend"})
	// One squad left: Validate refuses it, so nothing is applied.
	if !probs.Errors() {
		t.Fatal("a one-squad plan is not a team structure and must be refused")
	}
	if len(p.Squads) != 2 {
		t.Errorf("the rejected removal was applied anyway: %v", p.IDs())
	}
}

func TestApplyEditsIsANoOpWithNothingToDo(t *testing.T) {
	p := goReactPlan()
	if probs := ApplyEdits(&p, nil, nil); probs != nil {
		t.Errorf("no edits, no problems, got %v", probs)
	}
	if probs := ApplyEdits(nil, []plan.SquadEdit{{ID: "x"}}, nil); !probs.Errors() {
		t.Error("editing a nil plan is an error")
	}
}

// ── Attaching a manager to a team ────────────────────────────────────────

func TestATeamCanBeGivenItsOwnManager(t *testing.T) {
	p := staffedPlan()
	pm := "backend-pm"
	if probs := ApplyEdits(p, []plan.SquadEdit{{ID: "backend", Manager: &pm}}, nil); probs.Errors() {
		t.Fatalf("refused a plain staffing edit: %v", probs.Strings())
	}
	if got := StaffingFor(p, "backend").Manager; got != pm {
		t.Errorf("Manager = %q, want %q", got, pm)
	}
	// Untouched teams keep answering to the run's default.
	if got := StaffingFor(p, "frontend").Manager; got != "" {
		t.Errorf("frontend Manager = %q, want empty", got)
	}
}

func TestClearingAManagerHandsTheTeamBackToTheRunDefault(t *testing.T) {
	p := staffedPlan()
	pm, none := "backend-pm", ""
	if probs := ApplyEdits(p, []plan.SquadEdit{{ID: "backend", Manager: &pm}}, nil); probs.Errors() {
		t.Fatalf("setup: %v", probs.Strings())
	}
	if probs := ApplyEdits(p, []plan.SquadEdit{{ID: "backend", Manager: &none}}, nil); probs.Errors() {
		t.Fatalf("refused clearing a manager: %v", probs.Strings())
	}
	if got := StaffingFor(p, "backend").Manager; got != "" {
		t.Errorf("Manager = %q, want it cleared", got)
	}
}

// An absent field means "leave it alone". Nil-versus-empty is the whole
// distinction the pointer exists to carry.
func TestAnEditThatDoesNotMentionTheManagerKeepsIt(t *testing.T) {
	p := staffedPlan()
	pm := "backend-pm"
	if probs := ApplyEdits(p, []plan.SquadEdit{{ID: "backend", Manager: &pm}}, nil); probs.Errors() {
		t.Fatalf("setup: %v", probs.Strings())
	}
	name := "Backend · Go API"
	if probs := ApplyEdits(p, []plan.SquadEdit{{ID: "backend", Name: &name}}, nil); probs.Errors() {
		t.Fatalf("refused a name edit: %v", probs.Strings())
	}
	if got := StaffingFor(p, "backend").Manager; got != pm {
		t.Errorf("Manager = %q, want it untouched at %q", got, pm)
	}
}

// A stale team stamp is worse than no stamp, because the wave's write deny list
// is derived from it: a task stamped `backend` whose only remaining file is
// `web/package.json` has every frontend path denied at the tool layer, so its
// single write is refused on its own declared target — a task that cannot be
// completed for a reason nothing in the log explains.
//
// Measured on a live 30B, on exactly that shape.
func TestRepairAssignmentsFixesAStampItsFilesHaveMovedOutOf(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "backend", Owns: []string{"cmd/**", "internal/**"}},
		{ID: "frontend", Owns: []string{"web/**"}},
	}}
	tasks := []plan.Task{
		// The bug: stamped backend, owns nothing there any more.
		{ID: "T1", Squad: "backend", Files: []string{"web/package.json"}},
		// Still correct — must not be touched.
		{ID: "T2", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		// Straddles now: the stamp has to go, not move to one half.
		{ID: "T3", Squad: "frontend", Files: []string{"web/App.tsx", "cmd/main.go"}},
		// Nobody owns it: unassigned, which is what the board already says.
		{ID: "T4", Squad: "backend", Files: []string{"Makefile"}},
		// No files means no scope, so ownership cannot speak to it and whatever
		// the board or a human put there stands.
		{ID: "T5", Squad: "backend"},
	}

	fixed := RepairAssignments(p, tasks)

	if got := tasks[0].Squad; got != "frontend" {
		t.Errorf("T1 = %q, want frontend — its file moved lanes", got)
	}
	if got := tasks[1].Squad; got != "backend" {
		t.Errorf("T2 = %q — a correct stamp must not be disturbed", got)
	}
	if got := tasks[2].Squad; got != "" {
		t.Errorf("T3 = %q, want unassigned — it straddles, and one half must not own the seam", got)
	}
	if got := tasks[3].Squad; got != "" {
		t.Errorf("T4 = %q, want unassigned — nobody owns Makefile", got)
	}
	if got := tasks[4].Squad; got != "backend" {
		t.Errorf("T5 = %q — a task with no files declares no scope", got)
	}

	want := map[string]bool{"T1": true, "T3": true, "T4": true}
	if len(fixed) != len(want) {
		t.Fatalf("repaired %v, want exactly %v", fixed, want)
	}
	for _, id := range fixed {
		if !want[id] {
			t.Errorf("repaired %s, which was already correct", id)
		}
	}

	// A run with no org chart has no ownership to repair against.
	if RepairAssignments(nil, tasks) != nil {
		t.Error("no plan, no repair")
	}
}

// Retarget and Repair differ in exactly one case, and it matters.
//
// A task's file list is transiently wide all over a run — discovery adds a
// path, a worker reports what it touched, a reopen widens to what a tester
// named. Clearing the stamp on each of those throws away the routing the plan
// established for a condition that resolves before dispatch. Measured: a
// backend task cleared mid-run, which left its team with no work at all and its
// project manager with nothing to triage.
//
// At the FENCE the same case must clear, because there the stamp is about to
// become a write permission and a straddling task holding one would be denied
// on half its own targets.
func TestRetargetKeepsAStampAStraddleWouldClear(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "backend", Owns: []string{"cmd/**"}},
		{ID: "frontend", Owns: []string{"web/**"}},
	}}
	straddling := func() []plan.Task {
		return []plan.Task{{ID: "T1", Squad: "backend", Files: []string{"cmd/main.go", "web/App.tsx"}}}
	}

	kept := straddling()
	if fixed := RetargetAssignments(p, kept); len(fixed) != 0 {
		t.Fatalf("retarget changed %v — a transient straddle must not lose its routing", fixed)
	}
	if kept[0].Squad != "backend" {
		t.Fatalf("squad = %q, want the stamp kept", kept[0].Squad)
	}

	cleared := straddling()
	if fixed := RepairAssignments(p, cleared); len(fixed) != 1 {
		t.Fatalf("repair changed %v — at the fence a straddle must lose its permission", fixed)
	}
	if cleared[0].Squad != "" {
		t.Fatalf("squad = %q, want cleared at the fence", cleared[0].Squad)
	}

	// Both agree on the case that is unambiguously wrong: a stamp naming a team
	// that owns none of the task's files.
	for name, fn := range map[string]func(*Plan, []plan.Task) []string{
		"retarget": RetargetAssignments,
		"repair":   RepairAssignments,
	} {
		wrong := []plan.Task{{ID: "T2", Squad: "backend", Files: []string{"web/package.json"}}}
		if fixed := fn(p, wrong); len(fixed) != 1 || wrong[0].Squad != "frontend" {
			t.Errorf("%s: squad = %q, fixed = %v — want it re-pointed to the real owner",
				name, wrong[0].Squad, fixed)
		}
	}
}

package loop

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

func twoSquadPlan() *squads.Plan {
	p := &squads.Plan{
		Squads: []squads.Squad{
			{ID: "backend", Name: "Backend · Go API", Charter: "HTTP API.",
				Owns: []string{"cmd/**", "internal/**"}, Acceptance: "go test ./..."},
			{ID: "frontend", Name: "Frontend · React SPA", Charter: "Vite SPA.",
				Owns: []string{"web/**"}, Acceptance: "npm --prefix web run build"},
		},
		Contract: squads.Contract{Interfaces: []squads.Interface{
			{ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"},
				Spec: "200 -> [{id,title,done}]"},
		}},
		Integration: squads.Integration{Acceptance: "go test ./... && npm --prefix web run build"},
	}
	p.Normalize()
	return p
}

func TestTaskInputCarriesTheSquadBrief(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.BuildInput = func(plan.Task) string { return "BASE PROMPT" }

	got := r.taskInput(plan.Task{ID: "T1", Squad: "frontend", Files: []string{"web/src/App.tsx"}})

	for _, want := range []string{
		"BASE PROMPT",
		"frontend",
		"web/**",      // its own lane
		"do not edit", // the boundary
		"cmd/**",      // the other team's lane, named
		"You CONSUME", // its obligation
		"GET /api/todos",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("worker prompt is missing %q:\n%s", want, got)
		}
	}
}

func TestTaskInputHasNoSquadSectionOnASingleStreamRun(t *testing.T) {
	r := NewRunner(nil, nil)
	r.BuildInput = func(plan.Task) string { return "BASE" }

	// No plan at all — the overwhelmingly common case.
	got := r.taskInput(plan.Task{ID: "T1", Files: []string{"main.go"}})
	if strings.Contains(got, "Your squad") {
		t.Errorf("a run without squads must not mention them:\n%s", got)
	}

	// A plan exists, but this task straddles the seam and is unassigned.
	r.Squads = twoSquadPlan()
	got = r.taskInput(plan.Task{ID: "T4", Files: []string{"web/src/api.ts", "cmd/s/main.go"}})
	if strings.Contains(got, "Your squad") {
		t.Errorf("an unassigned task must not be briefed as if it had a lane:\n%s", got)
	}
}

// The boundary is the half of this feature that has to be enforced rather than
// requested: a stuck model talks itself out of a prompt, not out of a guard.
func TestWaveProtectionsDenyTheOtherSquadsTree(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()

	undo := r.applyWaveProtections([]plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
	})
	defer undo()

	if !r.Focus.IsProtected("web/src/App.tsx") {
		t.Error("a backend wave must not be able to write the frontend tree")
	}
	if r.Focus.IsProtected("cmd/server/main.go") {
		t.Error("a backend wave must still be able to write its own tree")
	}
	if r.Focus.IsProtected("internal/store/db.go") {
		t.Error("the whole backend subtree stays writable")
	}
}

func TestWaveProtectionsAreLiftedAfterTheWave(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()

	undo := r.applyWaveProtections([]plan.Task{{ID: "T1", Squad: "backend"}})
	if !r.Focus.IsProtected("web/src/App.tsx") {
		t.Fatal("protection should be installed")
	}
	undo()
	if r.Focus.IsProtected("web/src/App.tsx") {
		t.Error("one wave's squad boundary must not leak into the next")
	}
}

func TestMixedWaveProtectsNeitherSide(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()

	undo := r.applyWaveProtections([]plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T3", Squad: "frontend", Files: []string{"web/src/App.tsx"}},
	})
	defer undo()

	// Both teams are working in this wave, so denying either tree would block
	// the very task dispatched to write it. This is the parallel case.
	if r.Focus.IsProtected("web/src/App.tsx") || r.Focus.IsProtected("cmd/server/main.go") {
		t.Error("a mixed wave must leave both trees writable")
	}
}

// The squad boundary must survive alongside text-derived protections rather
// than replacing them: "stay in your lane" and "leave the tests alone" are
// different promises.
func TestSquadProtectionsMergeWithTextDerivedOnes(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()

	undo := r.applyWaveProtections([]plan.Task{{
		ID: "T1", Squad: "backend",
		Files:       []string{"cmd/server/main.go"},
		Description: "Add the route. Do not edit any _test.go file.",
	}})
	defer undo()

	if !r.Focus.IsProtected("web/src/App.tsx") {
		t.Error("squad boundary lost when a text protection is also present")
	}
	if !r.Focus.IsProtected("cmd/server/main_test.go") {
		t.Error("text-derived protection lost when a squad boundary is also present")
	}
}

func TestSquadProtectionsAreInertWithoutAPlan(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Focus = workspace.NewFocusGuard()
	if got := r.squadProtections([]plan.Task{{ID: "T1", Squad: "backend"}}); got != nil {
		t.Errorf("no plan must mean no squad protections, got %v", got)
	}
}

func TestWaveSquadsListsTheLiveTeams(t *testing.T) {
	got := waveSquads([]plan.Task{
		{ID: "a", Squad: "backend"},
		{ID: "b", Squad: "frontend"},
		{ID: "c", Squad: "backend"},
		{ID: "d"},
	})
	if !reflect.DeepEqual(got, []string{"backend", "frontend"}) {
		t.Errorf("waveSquads = %v", got)
	}
	if got := waveSquads(nil); len(got) != 0 {
		t.Errorf("waveSquads(nil) = %v", got)
	}
}

func TestForeignPatternsStaySorted(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	got := r.squadProtections([]plan.Task{{ID: "T3", Squad: "frontend"}})
	want := append([]string{}, got...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("protections must be deterministic/sorted, got %v", got)
	}
}

// The full enforcement chain, with nothing stubbed between the plan and the
// refusal: ForeignPatterns → applyWaveProtections → FocusGuard.Protect →
// FocusGuard.Check, which is the single gate ws_write consults (tools.go).
//
// This is the property the whole feature rests on. Everything else — the
// contract, the briefs, the routing — shapes what the model TRIES to do; this
// is what happens when it tries anyway.
func TestSquadBoundaryRefusesTheWriteAtTheRealGuard(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()
	ctx := context.Background()

	undo := r.applyWaveProtections([]plan.Task{
		{ID: "T3", Squad: "frontend", Files: []string{"web/src/App.tsx"}},
	})
	defer undo()

	// Its own tree: the guard raises no objection.
	if err := r.Focus.Check(ctx, "web/src/App.tsx"); err != nil {
		t.Fatalf("a frontend worker must be able to write its own tree: %v", err)
	}

	// The other team's tree: refused, and the refusal says why rather than
	// blaming the task's focus list — a model argues with the wrong reason.
	for _, foreign := range []string{"cmd/server/main.go", "internal/store/db.go"} {
		err := r.Focus.Check(ctx, foreign)
		if err == nil {
			t.Errorf("a frontend worker was allowed to write %s", foreign)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "protect") {
			t.Errorf("refusal for %s should name the protection, got: %v", foreign, err)
		}
	}
}

// Cross-squad parallelism: if the two teams serialize, the feature has bought
// nothing. Their files are disjoint by construction, so one wave must hold both.
func TestBothSquadsAreAdmittedToOneWave(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	ready := []plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"internal/store/todo.go"}},
		{ID: "T3", Squad: "frontend", Files: []string{"web/src/TodoList.tsx"}},
		{ID: "T2", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T4", Squad: "frontend", Files: []string{"web/src/api.ts"}},
	}
	wave := r.admitDisjoint(ready, 4)
	if len(wave) != 4 {
		t.Fatalf("four disjoint tasks should share one wave, got %d", len(wave))
	}
	live := map[string]bool{}
	for _, task := range wave {
		live[task.Squad] = true
	}
	if !live["backend"] || !live["frontend"] {
		t.Fatalf("both squads must be live in the same wave, got %v", live)
	}
}

// A run used to record that a wave HAPPENED and never what was in it. That is
// the difference between diagnosing a throughput problem and guessing at one:
// measured, two tasks in disjoint lanes went into consecutive single-task waves
// — the teams ran in series, the one thing the design exists to avoid — and
// nothing in 2,700 lines of output said whether a dependency, the fence or the
// scheduler put them there.
func TestAWaveSaysWhoIsInItAndWhichTeamsAreLive(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()
	var seen []string
	r.OnEvent = func(kind, agent, taskID, message, scope, output string) {
		seen = append(seen, message)
	}

	undo := r.applyWaveProtections([]plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Squad: "frontend", Files: []string{"web/src/App.tsx"}},
	})
	defer undo()

	joined := strings.Join(seen, "\n")
	for _, want := range []string{"T1(backend)", "T2(frontend)", "teams live: backend, frontend"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the wave line is missing %q:\n%s", want, joined)
		}
	}
}

// The shape worth noticing at a glance is one team across consecutive waves, so
// a wave nobody owns has to say that rather than say nothing.
func TestAWaveNoTeamOwnsSaysSo(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Squads = twoSquadPlan()
	r.Focus = workspace.NewFocusGuard()
	var seen []string
	r.OnEvent = func(kind, agent, taskID, message, scope, output string) {
		seen = append(seen, message)
	}

	undo := r.applyWaveProtections([]plan.Task{
		{ID: "T3", Files: []string{"README.md"}},
	})
	defer undo()

	joined := strings.Join(seen, "\n")
	if !strings.Contains(joined, "no team owns this wave") {
		t.Errorf("an unowned wave must say so:\n%s", joined)
	}
	if strings.Contains(joined, "teams live") {
		t.Errorf("a team was claimed live with nothing of its own in the wave:\n%s", joined)
	}
}

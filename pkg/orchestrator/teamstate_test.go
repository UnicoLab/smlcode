package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// Two runs on one Orchestrator. The first proves two halves green; the second
// has one team idle. Its verdict must NOT read the first run's gate for the
// idle team — which is exactly what happened when the gates were never reset:
// runTeamAcceptance skips Total==0 teams, so the stale green survived and
// allHalvesProved upgraded a run that proved one half.
func TestTeamGatesDoNotSurviveIntoTheNextRun(t *testing.T) {
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})

	// Run 1: both halves have work, both prove green.
	o.runTeamAcceptance(context.Background(), boardWithBothHalves())
	if !o.allHalvesProved() {
		t.Fatal("run 1: two green halves must count as proved")
	}

	// Run 2 begins the way runSLM and Resume begin.
	o.resetTeamState()
	if o.squadPlanNow() != nil || len(o.TeamGates()) != 0 {
		t.Fatalf("reset left team state behind: plan=%v gates=%+v", o.squadPlanNow(), o.TeamGates())
	}
	o.mu.Lock()
	o.squadPlan = twoTeams()
	o.mu.Unlock()
	oneHalf := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone, Status: "done", Files: []string{"cmd/main.go"}},
	}}
	o.runTeamAcceptance(context.Background(), oneHalf)

	if gates := o.TeamGates(); len(gates) != 1 || gates[0].Team != "backend" {
		t.Fatalf("run 2 gates = %+v, want only the team that had work", gates)
	}
	if o.allHalvesProved() {
		t.Fatal("run 2 proved ONE half and reported both — the previous run's gate leaked in")
	}
}

// The reset must happen at the top of both entry points, before anything can
// read or set team state. Pinned on the source so a refactor that moves it
// back below the charter phase, or drops it from Resume, fails here.
func TestBothRunEntryPointsResetTeamStateFirst(t *testing.T) {
	body := func(file, fn string) string {
		t.Helper()
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(string(src), fn)
		if i < 0 {
			t.Fatalf("%s not found in %s", fn, file)
		}
		return string(src[i:])
	}
	run := body("orchestrator.go", "func (o *Orchestrator) runSLM(")
	reset, assemble := strings.Index(run, "o.resetTeamState()"), strings.Index(run, "o.assembleSquads(")
	if reset < 0 || assemble < 0 || reset > assemble {
		t.Fatalf("runSLM must reset team state before the charter phase (reset at %d, assemble at %d)", reset, assemble)
	}
	resume := body("resume.go", "func (o *Orchestrator) Resume(")
	reset, restore := strings.Index(resume, "o.resetTeamState()"), strings.Index(resume, "o.restoreSquadPlan(")
	if reset < 0 || restore < 0 || reset > restore {
		t.Fatalf("Resume must reset team state before restoring the saved plan (reset at %d, restore at %d)", reset, restore)
	}
}

// squads.json holding ONE squad is a run a single library team staffed. Resume
// restores it as the staffing team — its manager triages the resumed board's
// rejected work — rather than dropping it because one squad is not a plan.
func TestRestoreSquadPlanRestoresASingleStaffingTeam(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.Squads = true })
	one := squads.Plan{Squads: []squads.Squad{{
		ID: "backend-go", Name: "Backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./...",
		Manager: "manager", Worker: "go-worker",
	}}}
	if err := squads.Save(o.cfg.SlmDir(), one); err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend-go", Column: plan.ColReadyToDev, Files: []string{"cmd/main.go"}},
	}}

	o.restoreSquadPlan(board)

	if o.squadPlanNow() != nil {
		t.Fatal("one squad was restored as a squad PLAN — Enabled() needs two")
	}
	st := o.singleTeamStaffing()
	if st.Squad != "backend-go" || st.Manager != "manager" {
		t.Fatalf("staffing after restore = %+v, want the saved team and its manager", st)
	}

	// A board no team touched gets nothing, as before.
	o.resetTeamState()
	o.restoreSquadPlan(&plan.Board{Tasks: []plan.Task{{ID: "T1", Column: plan.ColReadyToDev}}})
	if o.singleTeamStaffing().Squad != "" {
		t.Fatal("a board with no stamped task must not restore a team")
	}
}

// The team library selection is computed once per run and cleared with the
// rest of the team state.
func TestTeamSelectionIsMemoizedPerRun(t *testing.T) {
	o := testOrch(t, nil)
	o.cfg.TeamLibrary = true
	sel1, _ := o.preselectTeamsWith("add a Go API endpoint", nil, nil)
	o.mu.Lock()
	pick := o.teamPick
	o.mu.Unlock()
	if len(o.teamRoster()) > 0 && pick == nil {
		t.Fatal("the run's selection was not memoized")
	}
	sel2, _ := o.preselectTeamsWith("add a Go API endpoint", nil, nil)
	if strings.Join(sel1.IDs(), ",") != strings.Join(sel2.IDs(), ",") {
		t.Fatalf("memoized selection differs: %v vs %v", sel1.IDs(), sel2.IDs())
	}
	// A preview with explicit pins never reads or writes the run's cache.
	o.preselectTeamsWith("add a Go API endpoint", nil, []string{"frontend-react"})
	o.mu.Lock()
	same := o.teamPick == pick
	o.mu.Unlock()
	if !same {
		t.Fatal("a pinned preview overwrote the run's memoized selection")
	}
	o.resetTeamState()
	o.mu.Lock()
	cleared := o.teamPick == nil
	o.mu.Unlock()
	if !cleared {
		t.Fatal("resetTeamState left the previous run's selection behind")
	}
	_ = time.Now()
}

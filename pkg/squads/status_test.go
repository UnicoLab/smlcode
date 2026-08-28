package squads

import (
	"reflect"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func task(id, squad, col string) plan.Task {
	return plan.Task{ID: id, Squad: squad, Column: col}
}

func TestProgressSeparatesTheTeams(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{
		task("b1", "backend", plan.ColDone),
		task("b2", "backend", plan.ColDone),
		task("f1", "frontend", plan.ColDone),
		task("f2", "frontend", plan.ColInProgress),
		task("f3", "frontend", plan.ColBlocked),
		task("x1", "", plan.ColInProgress), // unassigned seam work
	}
	got := Progress(&p, tasks)
	if len(got) != 2 {
		t.Fatalf("expected one status per squad, got %d", len(got))
	}

	back := got[0]
	if back.ID != "backend" || back.Total != 2 || back.Done != 2 || !back.Complete {
		t.Errorf("backend = %+v", back)
	}
	if back.Stuck {
		t.Error("a finished squad is not stuck")
	}

	front := got[1]
	if front.Total != 3 || front.Done != 1 || front.Blocked != 1 || front.InFlight != 1 {
		t.Errorf("frontend = %+v", front)
	}
	if front.Complete {
		t.Error("frontend is not complete")
	}
	// Something is still in flight, so it can still make progress on its own.
	if front.Stuck {
		t.Error("frontend has work in flight and is not stuck")
	}

	// The aggregate number a single-stream run would show hides all of this.
	if line := ProgressLine(&p, tasks); !strings.Contains(line, "backend 2/2 done") ||
		!strings.Contains(line, "frontend 1/3") {
		t.Errorf("ProgressLine = %q", line)
	}
}

func TestStuckNeedsNothingInFlight(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{
		task("b1", "backend", plan.ColInProgress),
		task("f1", "frontend", plan.ColBlocked),
	}
	got := Progress(&p, tasks)
	if got[0].Stuck {
		t.Error("backend has work in flight")
	}
	if !got[1].Stuck {
		t.Error("frontend has only blocked work and cannot progress alone")
	}
}

// The cross-team failure a per-task reviewer cannot see: retrying the
// consumer's tasks forever is the wrong response when the provider owes it an
// interface that does not exist yet.
func TestWaitingOnFindsTheCrossTeamStall(t *testing.T) {
	p := goReactPlan()
	tasks := []plan.Task{
		task("b1", "backend", plan.ColInProgress), // provider still building
		task("f1", "frontend", plan.ColBlocked),   // consumer stuck
	}
	got := WaitingOn(&p, tasks)
	want := []Stall{
		{Squad: "frontend", Interface: "GET /api/todos", Provider: "backend"},
		{Squad: "frontend", Interface: "POST /api/todos", Provider: "backend"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WaitingOn = %+v, want %+v", got, want)
	}
	if s := got[0].String(); !strings.Contains(s, "frontend is waiting on backend") {
		t.Errorf("Stall.String = %q", s)
	}
}

func TestWaitingOnStaysQuietWhenNobodyIsActuallyStalled(t *testing.T) {
	p := goReactPlan()
	cases := []struct {
		name  string
		tasks []plan.Task
	}{
		{"consumer still progressing", []plan.Task{
			task("b1", "backend", plan.ColInProgress),
			task("f1", "frontend", plan.ColInProgress),
		}},
		{"provider already delivered", []plan.Task{
			task("b1", "backend", plan.ColDone),
			task("f1", "frontend", plan.ColBlocked),
		}},
		{"consumer already finished", []plan.Task{
			task("b1", "backend", plan.ColInProgress),
			task("f1", "frontend", plan.ColDone),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WaitingOn(&p, tc.tasks); len(got) != 0 {
				t.Errorf("expected no stall, got %+v", got)
			}
		})
	}
}

func TestReadyForIntegrationHoldsUntilEveryHalfIsGreen(t *testing.T) {
	p := goReactPlan()
	cases := []struct {
		name     string
		tasks    []plan.Task
		ready    bool
		contains string
	}{
		{"one half still building", []plan.Task{
			task("b1", "backend", plan.ColDone),
			task("f1", "frontend", plan.ColInProgress),
		}, false, "still building: frontend (0/1)"},
		{"a blocked half is not ready", []plan.Task{
			task("b1", "backend", plan.ColDone),
			task("f1", "frontend", plan.ColBlocked),
		}, false, "still building"},
		{"both green", []plan.Task{
			task("b1", "backend", plan.ColDone),
			task("f1", "frontend", plan.ColDone),
		}, true, ""},
		{"no work at all", nil, false, "no squad has any work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadyForIntegration(&p, tc.tasks)
			if got.Ready != tc.ready {
				t.Fatalf("Ready = %v, want %v (reason %q)", got.Ready, tc.ready, got.Reason)
			}
			if tc.contains != "" && !strings.Contains(got.Reason, tc.contains) {
				t.Errorf("Reason = %q, want it to mention %q", got.Reason, tc.contains)
			}
			if tc.ready && got.Command != p.Integration.Acceptance {
				t.Errorf("Command = %q, want the integration acceptance", got.Command)
			}
		})
	}
}

// A squad that was assembled but never given work must not hold integration
// hostage — that is a planning miss the report already surfaces as idle.
func TestIntegrationIgnoresASquadWithNoWork(t *testing.T) {
	p := goReactPlan()
	got := ReadyForIntegration(&p, []plan.Task{task("b1", "backend", plan.ColDone)})
	if !got.Ready {
		t.Fatalf("Ready = false (%q); an idle squad must not block integration", got.Reason)
	}
}

func TestStatusHelpersAreNilSafe(t *testing.T) {
	if got := Progress(nil, nil); got != nil {
		t.Errorf("Progress(nil) = %v", got)
	}
	if got := WaitingOn(nil, nil); got != nil {
		t.Errorf("WaitingOn(nil) = %v", got)
	}
	if got := ReadyForIntegration(nil, nil); got.Ready || got.Reason != "no squads" {
		t.Errorf("ReadyForIntegration(nil) = %+v", got)
	}
	if got := ProgressLine(nil, nil); got != "" {
		t.Errorf("ProgressLine(nil) = %q", got)
	}
}

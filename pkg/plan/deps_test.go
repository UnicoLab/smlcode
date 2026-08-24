package plan

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// A dependency that ended BLOCKED must not release its dependents. The old
// behavior (blocked counts as satisfied) let a worker build on a foundation
// that was never laid; the new one blocks the dependent instead, so the failure
// reaches a human rather than multiplying.
func TestReadinessOnlyDoneSatisfiesDependency(t *testing.T) {
	cases := []struct {
		name        string
		tasks       []Task
		wantReady   []string
		wantBlocked []string
		wantNoteSub string // must appear in every newly blocked task's Error+Notes
	}{
		{
			name: "blocked dependency blocks the dependent",
			tasks: []Task{
				{ID: "T1", Column: ColBlocked, Role: RoleExplorer},
				{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
			},
			wantReady:   nil,
			wantBlocked: []string{"T1", "T2"},
			wantNoteSub: "dependency T1 is blocked",
		},
		{
			name: "done dependency still satisfies (regression guard)",
			tasks: []Task{
				{ID: "T1", Column: ColDone, Role: RoleExplorer},
				{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
			},
			wantReady:   []string{"T2"},
			wantBlocked: nil,
		},
		{
			name: "blockage propagates transitively down a chain",
			tasks: []Task{
				{ID: "T1", Column: ColBlocked, Role: RoleWorker},
				{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
				{ID: "T3", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T2"}},
				{ID: "T4", Column: ColReadyToDev, Role: RoleWorker},
			},
			wantReady:   []string{"T4"},
			wantBlocked: []string{"T1", "T2", "T3"},
			wantNoteSub: "is blocked",
		},
		{
			name: "an in-progress dependency is waited on, not blocked",
			tasks: []Task{
				{ID: "T1", Column: ColInProgress, Role: RoleWorker},
				{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
			},
			wantReady:   nil,
			wantBlocked: nil,
		},
		{
			name: "a dependency that is not on the board is left alone",
			tasks: []Task{
				{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T9"}},
			},
			wantReady:   nil,
			wantBlocked: nil,
		},
		{
			name: "one blocked dependency among several still blocks",
			tasks: []Task{
				{ID: "T1", Column: ColDone, Role: RoleWorker},
				{ID: "T2", Column: ColBlocked, Role: RoleWorker},
				{ID: "T3", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1", "T2"}},
			},
			wantReady:   nil,
			wantBlocked: []string{"T2", "T3"},
			wantNoteSub: "dependency T2 is blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &Board{Tasks: append([]Task(nil), tc.tasks...)}
			got := ids(b.ReadyTasks())
			if !reflect.DeepEqual(got, tc.wantReady) {
				t.Fatalf("ready=%v want %v", got, tc.wantReady)
			}
			var blocked []string
			for _, task := range b.AllTasks() {
				if task.Column != ColBlocked {
					continue
				}
				blocked = append(blocked, task.ID)
				// Only the tasks this pass moved carry a note; a task that was
				// already blocked keeps whatever it had.
				if tc.wantNoteSub == "" || task.Error == "" {
					continue
				}
				if !strings.Contains(task.Error, tc.wantNoteSub) {
					t.Fatalf("%s Error=%q want it to mention %q", task.ID, task.Error, tc.wantNoteSub)
				}
				if !strings.Contains(task.Notes, "BLOCKED:") {
					t.Fatalf("%s Notes=%q want a BLOCKED: line", task.ID, task.Notes)
				}
			}
			if !reflect.DeepEqual(blocked, tc.wantBlocked) {
				t.Fatalf("blocked=%v want %v", blocked, tc.wantBlocked)
			}
		})
	}
}

// Blocking is a one-shot transition: repeated readiness passes must not keep
// re-appending the same note, or a long run turns the field into a log.
func TestPropagateBlockedIsIdempotent(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColBlocked, Role: RoleWorker},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
	}}
	if moved := b.PropagateBlocked(); !reflect.DeepEqual(moved, []string{"T2"}) {
		t.Fatalf("first pass moved=%v want [T2]", moved)
	}
	first, _ := b.Get("T2")
	for i := 0; i < 3; i++ {
		if moved := b.PropagateBlocked(); len(moved) != 0 {
			t.Fatalf("pass %d moved=%v want none", i+2, moved)
		}
	}
	again, _ := b.Get("T2")
	if again.Notes != first.Notes || again.Error != first.Error {
		t.Fatalf("note grew across passes:\nfirst=%q\nlater=%q", first.Notes, again.Notes)
	}
	if strings.Count(again.Notes, "BLOCKED:") != 1 {
		t.Fatalf("Notes=%q want exactly one BLOCKED: line", again.Notes)
	}
}

// A cycle must be detected rather than silently un-schedulable, and detecting
// it must not hang — the whole point of the check is the input that loops.
func TestDependencyCycles(t *testing.T) {
	cases := []struct {
		name  string
		tasks []Task
		want  [][]string
	}{
		{
			name: "acyclic chain has no cycle",
			tasks: []Task{
				{ID: "T1"},
				{ID: "T2", DependsOn: []string{"T1"}},
				{ID: "T3", DependsOn: []string{"T1", "T2"}},
			},
			want: nil,
		},
		{
			name: "two-task cycle",
			tasks: []Task{
				{ID: "T1", DependsOn: []string{"T2"}},
				{ID: "T2", DependsOn: []string{"T1"}},
			},
			want: [][]string{{"T1", "T2"}},
		},
		{
			name: "three-task cycle",
			tasks: []Task{
				{ID: "T1", DependsOn: []string{"T3"}},
				{ID: "T2", DependsOn: []string{"T1"}},
				{ID: "T3", DependsOn: []string{"T2"}},
			},
			want: [][]string{{"T1", "T2", "T3"}},
		},
		{
			name:  "self dependency is a cycle",
			tasks: []Task{{ID: "T1", DependsOn: []string{"T1"}}},
			want:  [][]string{{"T1"}},
		},
		{
			name: "two disjoint cycles are reported separately",
			tasks: []Task{
				{ID: "T1", DependsOn: []string{"T2"}},
				{ID: "T2", DependsOn: []string{"T1"}},
				{ID: "T3", DependsOn: []string{"T4"}},
				{ID: "T4", DependsOn: []string{"T3"}},
			},
			want: [][]string{{"T1", "T2"}, {"T3", "T4"}},
		},
		{
			name: "a task that merely depends on a cycle is not a member",
			tasks: []Task{
				{ID: "T1", DependsOn: []string{"T2"}},
				{ID: "T2", DependsOn: []string{"T1"}},
				{ID: "T3", DependsOn: []string{"T1"}},
			},
			want: [][]string{{"T1", "T2"}},
		},
		{
			name: "a dangling dependency is not a cycle",
			tasks: []Task{
				{ID: "T1", DependsOn: []string{"T404"}},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &Board{Tasks: append([]Task(nil), tc.tasks...)}
			// Timeout guard: a cycle check that loops forever fails here as a
			// test failure instead of wedging the whole package run.
			done := make(chan [][]string, 1)
			go func() { done <- b.DependencyCycles() }()
			select {
			case got := <-done:
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("cycles=%v want %v", got, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("DependencyCycles hung on a cyclic board")
			}
		})
	}
}

// A cycle among ready tasks must land in blocked with a note naming the members,
// and readiness must terminate rather than spin on a board that can never order
// itself.
func TestCycleMembersAreBlockedNotStranded(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T2"}},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
		{ID: "T3", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
		{ID: "T4", Column: ColReadyToDev, Role: RoleWorker},
	}}

	done := make(chan []string, 1)
	go func() { done <- ids(b.ReadyTasks()) }()
	var ready []string
	select {
	case ready = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadyTasks hung on a cyclic board")
	}
	if !reflect.DeepEqual(ready, []string{"T4"}) {
		t.Fatalf("ready=%v want [T4]", ready)
	}

	for _, id := range []string{"T1", "T2"} {
		task, _ := b.Get(id)
		if task.Column != ColBlocked {
			t.Fatalf("%s column=%s want blocked", id, task.Column)
		}
		if !strings.Contains(task.Error, "dependency cycle between T1, T2") {
			t.Fatalf("%s Error=%q want it to name the cycle members", id, task.Error)
		}
	}
	// T3 only DEPENDS on the cycle, so it gets the ordinary upstream message.
	t3, _ := b.Get("T3")
	if t3.Column != ColBlocked || !strings.Contains(t3.Error, "dependency T1 is blocked") {
		t.Fatalf("T3 column=%s Error=%q", t3.Column, t3.Error)
	}
	// The board must now be able to finish: nothing is left waiting forever.
	t4, _ := b.Get("T4")
	t4.MoveTo(ColDone)
	b.UpdateTask(t4)
	if b.AgentWorkRemaining() {
		t.Fatal("agent work still remaining — blocked tasks must not keep the board open")
	}
}

func ids(tasks []Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

package loop

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The measured shape: a task on its fourth attempt sits at the head of the
// board and keeps taking the slot from work nobody has tried, because the two
// collide on a file and only one can be in the wave.
func TestAFirstAttemptGoesBeforeAFourth(t *testing.T) {
	r := NewRunner(nil, nil)
	for i := 0; i < 3; i++ {
		r.waveAttempts.bump("T3")
	}

	got := r.preferFreshWork([]plan.Task{{ID: "T3"}, {ID: "T4"}})

	if got[0].ID != "T4" {
		t.Fatalf("order = %s, %s — untried work must go first", got[0].ID, got[1].ID)
	}
	// And nothing is dropped, or the retry would be starved instead.
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want both", len(got))
	}
}

// Equally-tried tasks keep the board's order, which carries meaning the attempt
// count does not — dependency order, the order a person approved.
func TestEquallyTriedTasksKeepTheBoardsOrder(t *testing.T) {
	r := NewRunner(nil, nil)
	in := []plan.Task{{ID: "T1"}, {ID: "T2"}, {ID: "T3"}}

	for name, prep := range map[string]func(){
		"none tried": func() {},
		"all tried once": func() {
			r.waveAttempts.bump("T1")
			r.waveAttempts.bump("T2")
			r.waveAttempts.bump("T3")
		},
	} {
		prep()
		got := r.preferFreshWork(in)
		for i := range in {
			if got[i].ID != in[i].ID {
				t.Fatalf("%s: order changed to %s at %d", name, got[i].ID, i)
			}
		}
	}
}

// Among tasks with the same attempt count the board's order still decides, so
// the rule is a tie-break on top of the existing schedule rather than a new one.
func TestOrderIsStableWithinAnAttemptCount(t *testing.T) {
	r := NewRunner(nil, nil)
	r.waveAttempts.bump("T1")
	r.waveAttempts.bump("T1")

	got := r.preferFreshWork([]plan.Task{{ID: "T1"}, {ID: "T2"}, {ID: "T3"}})

	if got[0].ID != "T2" || got[1].ID != "T3" || got[2].ID != "T1" {
		t.Fatalf("order = %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestPreferFreshWorkHandlesTrivialInput(t *testing.T) {
	r := NewRunner(nil, nil)
	if got := r.preferFreshWork(nil); got != nil {
		t.Errorf("nil = %v", got)
	}
	one := []plan.Task{{ID: "T1"}}
	if got := r.preferFreshWork(one); len(got) != 1 || got[0].ID != "T1" {
		t.Errorf("single = %v", got)
	}
}

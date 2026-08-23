package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// The board-level bounds.
//
// RunBoard's only bound used to be a 200-round safety guard, which is a
// backstop rather than a guard: reproduced against a one-task board whose gate
// answered "retry" forever, it cost 200 gate retries and ~2,000 model calls
// (~9,100 in the reviewer's real-binary run, where speculative review fans out)
// before it fired. Two bounds fire first and cheaply.
const (
	// DefaultMaxTaskAttempts is how many times ONE task may be dispatched to a
	// wave before the board parks it in the human backlog.
	//
	// It is the backstop for the escalate gate's own cap
	// (plan.DefaultMaxGateRetries): the gate cap only binds handlers that go
	// through plan.ApplyEscalateAction, and an embedding UI can move a task back
	// to ready_to_dev by any route it likes. This one binds all of them, because
	// it is counted where the dispatch happens. 1 + DefaultMaxGateRetries = 3.
	DefaultMaxTaskAttempts = 3

	// DefaultMaxStallRounds is how many CONSECUTIVE rounds may complete with
	// nothing changing before the board gives up. A round that changes nothing
	// cannot become a round that changes something by being repeated.
	DefaultMaxStallRounds = 3

	// boardRoundFloor / boardRoundCeiling bound the derived hard round guard.
	boardRoundFloor   = 20
	boardRoundCeiling = 200
)

// maxTaskAttempts is the per-task dispatch ceiling in force.
func (r *Runner) maxTaskAttempts() int {
	if r != nil && r.MaxTaskAttempts > 0 {
		return r.MaxTaskAttempts
	}
	return DefaultMaxTaskAttempts
}

// maxStallRounds is the consecutive-no-progress ceiling in force.
func (r *Runner) maxStallRounds() int {
	if r != nil && r.MaxStallRounds > 0 {
		return r.MaxStallRounds
	}
	return DefaultMaxStallRounds
}

// roundGuard is the hard backstop, derived from the board rather than fixed.
//
// A 100-task board at max_parallel=2 legitimately needs 50 rounds, so the guard
// has to scale; a 1-task board must never be allowed 200. Both real bounds
// (attempt ceiling, stall detector) fire long before this does — if this one
// ever trips, that is a bug in the other two.
func (r *Runner) roundGuard(board *plan.Board) int {
	n := 0
	if board != nil {
		n = len(board.AllTasks())
	}
	guard := boardRoundFloor + n*(r.maxTaskAttempts()+2)
	if guard > boardRoundCeiling {
		guard = boardRoundCeiling
	}
	return guard
}

// attemptTracker counts wave dispatches per task.
type attemptTracker struct {
	mu sync.Mutex
	n  map[string]int
}

func (a *attemptTracker) bump(taskID string) int {
	if a == nil || taskID == "" {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.n == nil {
		a.n = map[string]int{}
	}
	a.n[taskID]++
	return a.n[taskID]
}

// clear forgets a task's attempts. The ceiling bounds CONSECUTIVE attempts
// that never completed the task, not lifetime dispatches: a task that reached
// done and was later reopened (by the tester, the QA gate, or a human) has
// made progress, and charging it for the attempts that produced that progress
// would park a task that is working.
func (a *attemptTracker) clear(taskID string) {
	if a == nil || taskID == "" {
		return
	}
	a.mu.Lock()
	delete(a.n, taskID)
	a.mu.Unlock()
}

func (a *attemptTracker) get(taskID string) int {
	if a == nil || taskID == "" {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n[taskID]
}

// admitWave splits a ready wave into the tasks that may still be attempted and
// the ones that have spent their board-level attempt ceiling.
//
// Over-ceiling tasks are PARKED in the human backlog rather than aborting the
// whole board: on a multi-task board the other tasks can still finish, and a
// parked task is a terminal state an operator can act on. Every park is
// announced — a task that silently stops being scheduled is the failure mode
// this replaces, not an improvement on it.
func (r *Runner) admitWave(board *plan.Board, wave []plan.Task) []plan.Task {
	out := make([]plan.Task, 0, len(wave))
	for _, t := range wave {
		attempt := r.waveAttempts.bump(t.ID)
		if attempt <= r.maxTaskAttempts() {
			out = append(out, t)
			continue
		}
		why := fmt.Sprintf("attempted %d times (ceiling %d) without ever passing review — "+
			"needs a smaller scope or a corrected acceptance, not another attempt",
			attempt-1, r.maxTaskAttempts())
		t.MoveTo(plan.ColToScope)
		t.Error = why
		t.Notes = strings.TrimSpace(t.Notes + "\nESCALATED: " + ErrTaskAttempts.Error() + ". " + why)
		board.UpdateTask(t)
		r.logf("%s parked: %s", t.ID, why)
		r.fireIntervention(t.ID, "attempt_ceiling",
			fmt.Sprintf("%s hit its %d-attempt ceiling — parking it instead of another wave",
				t.ID, r.maxTaskAttempts()),
			fmt.Sprintf("max_task_attempts=%d attempts=%d gate_retries=%d llm_requests=%d",
				r.maxTaskAttempts(), attempt-1, t.GateRetries, r.budget().sentRequests(t.ID)))
	}
	r.persist(board)
	return out
}

// progressSignature fingerprints everything a productive round has to change.
//
// Three independent signals, because a stall can hide behind any one of them:
// the board (a task changing column, retry count or output), the disk (a file
// the run is scoped to changing content), and git's own view of the working
// tree (a file the run was NOT scoped to changing, e.g. a worker creating a
// helper). If none of the three moved across a whole round, the round did
// nothing, and repeating it will do nothing again.
func (r *Runner) progressSignature(board *plan.Board) string {
	h := sha256.New()
	tasks := board.AllTasks()
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	seen := map[string]bool{}
	for _, t := range tasks {
		out := sha256.Sum256([]byte(t.Output))
		_, _ = fmt.Fprintf(h, "%s|%s|%d|%d|%x|%d\n",
			t.ID, t.Column, t.Retries, t.GateRetries, out[:8], len(t.Review))
		for _, f := range t.Files {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			_, _ = fmt.Fprintf(h, "f|%s|%s\n", f, fileFingerprint(filepath.Join(r.rootDir(), f)))
		}
	}
	for _, f := range r.gitChangedFiles() {
		if seen[f] {
			continue
		}
		seen[f] = true
		_, _ = fmt.Fprintf(h, "g|%s|%s\n", f, fileFingerprint(filepath.Join(r.rootDir(), f)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stallReport describes the tasks that were open when the board stopped
// advancing, and what to do about them.
func (r *Runner) stallReport(board *plan.Board) ([]string, string) {
	var stalled []string
	var ids []string
	for _, t := range board.AllTasks() {
		switch t.Column {
		case plan.ColDone, plan.ColToScope, plan.ColScoped:
			continue
		}
		ids = append(ids, t.ID)
		reason := strings.TrimSpace(collapseSpace(t.Error))
		if reason == "" {
			reason = strings.TrimSpace(collapseSpace(t.Review))
		}
		if len(reason) > 160 {
			reason = reason[:160] + "…"
		}
		entry := fmt.Sprintf("%s [%s, attempts=%d, gate_retries=%d, llm_requests=%d]",
			t.ID, t.Column, r.waveAttempts.get(t.ID), t.GateRetries, r.budget().sentRequests(t.ID))
		if reason != "" {
			entry += ": " + reason
		}
		stalled = append(stalled, entry)
	}
	remedy := "nothing changed on disk and no task changed column across " +
		fmt.Sprintf("%d consecutive rounds", r.maxStallRounds())
	if len(ids) > 0 {
		remedy += "; re-scope " + strings.Join(ids, ", ") +
			" with a smaller acceptance (or fix the blocking evidence the review names) and re-run"
	} else {
		remedy += "; the board has no schedulable work left"
	}
	return stalled, remedy
}

// noteStall announces a stall give-up with the full diagnosis attached.
func (r *Runner) noteStall(board *plan.Board, rounds int) error {
	stalled, remedy := r.stallReport(board)
	gerr := &GaveUpError{
		Reason:    ErrNoProgress,
		Rounds:    rounds,
		Remaining: openTaskCount(board),
		Stalled:   stalled,
		Remedy:    remedy,
	}
	r.logf("RunBoard: %v", gerr)
	r.fireLevel(stream.KindLoop, "harness", "", gerr.Error(), "give_up", "", stream.LevelError)
	return gerr
}

// collapseSpace folds whitespace runs so a multi-line review reads as one line.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

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

// admitDisjoint picks up to maxP tasks off the ordered ready list, skipping any
// whose files a task already picked for this wave has claimed.
//
// This is scheduling-level mutual exclusion. A wave fans out one goroutine per
// task against ONE working tree, and the FocusGuard write allowlist is the
// UNION of the wave's files — it constrains the wave against the repo, not the
// workers against each other. Two tasks that both edit main.go therefore raced,
// and each worker's context went stale the moment the other one wrote. Planning
// already folds tasks with IDENTICAL file sets into one (plan's
// shouldCollapseSameFile); overlapping-but-not-identical sets are the case that
// slipped past it.
//
// A deferred task is NOT dropped and NOT failed — it stays in ready_to_dev and
// is the first thing the next wave takes, because `ready` keeps its order. The
// per-path write lock in pkg/workspace is the correctness backstop underneath:
// this can only see the files a task DECLARED, and a worker that writes a file
// its task never listed is still serialized there.
//
// Deterministic: the ready order is preserved and the claim list is a slice
// scanned in admission order, so the same board always produces the same wave.
func (r *Runner) admitDisjoint(ready []plan.Task, maxP int) []plan.Task {
	if maxP < 1 {
		maxP = 1
	}
	type claim struct{ path, taskID string }
	claims := make([]claim, 0, len(ready))
	out := make([]plan.Task, 0, maxP)
	for _, t := range ready {
		if len(out) >= maxP {
			break
		}
		files := waveClaimPaths(t)
		deferred := false
		for _, f := range files {
			for _, c := range claims {
				if !pathsCollide(f, c.path) {
					continue
				}
				r.logf("%s deferred to the next wave: %s is already claimed by %s in this one",
					t.ID, f, c.taskID)
				deferred = true
				break
			}
			if deferred {
				break
			}
		}
		if deferred {
			continue
		}
		for _, f := range files {
			claims = append(claims, claim{path: f, taskID: t.ID})
		}
		out = append(out, t)
	}
	return out
}

// waveClaimPaths normalizes a task's declared files into contention keys, using
// the same cleaning as the workspace path lock so "./a.go" and "a.go" are one
// file here too.
func waveClaimPaths(t plan.Task) []string {
	out := make([]string, 0, len(t.Files))
	for _, f := range t.Files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Clean(strings.TrimSpace(f))))
	}
	return out
}

// pathsCollide reports whether two cleaned paths are the same file, or one is a
// directory holding the other. Tasks legitimately declare directories ("src/"),
// and src vs src/main.go is the same contention as two tasks on one file.
func pathsCollide(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// admitWave splits a ready wave into the tasks that may still be attempted and
// the ones that have spent their board-level attempt ceiling.
//
// Over-ceiling tasks are PARKED in the human backlog rather than aborting the
// whole board: on a multi-task board the other tasks can still finish, and a
// parked task is a terminal state an operator can act on. Every park is
// announced — a task that silently stops being scheduled is the failure mode
// this replaces, not an improvement on it.
// reclaimOrphaned returns tasks a finished wave left mid-flight to ready_to_dev,
// and reports how many it moved.
//
// runWave is SYNCHRONOUS: when it returns, every worker, reviewer and corrector
// it started has finished. So at the top of RunBoard's loop nothing is actually
// in flight, and a task still sitting in in_progress or in_review is not busy —
// it is abandoned. Nothing ever re-dispatches it either, because
// executableTasksLocked only ever returns ready_to_dev.
//
// That combination deadlocks the board: AgentWorkRemaining sees in_review and
// says "work is in progress", the scheduler finds nothing executable, and the
// loop idles for 31 rounds waiting on an agent that exited long ago. Every task
// that depended on the orphan waits with it. Measured on a live run: one task
// stranded in in_review, four dependents stuck in ready_to_dev, ~9 minutes of
// real work discarded and reported as a failure — with the actual edit already
// on disk and compiling.
//
// Re-dispatching is bounded by the same ceiling every other dispatch pays:
// admitWave bumps the attempt count and parks a task that has spent it, so a
// task that keeps stranding itself lands in to_scope for a human rather than
// looping forever.
func (r *Runner) reclaimOrphaned(board *plan.Board) int {
	if board == nil {
		return 0
	}
	moved := 0
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column != plan.ColInProgress && t.Column != plan.ColInReview {
			continue
		}
		was := t.Column
		t.MoveTo(plan.ColReadyToDev)
		t.Notes = strings.TrimSpace(t.Notes +
			"\nRECLAIMED: left in " + was + " by a finished wave; re-queued for dispatch.")
		board.UpdateTask(t)
		r.logf("%s reclaimed from %s — the wave that owned it has already returned", t.ID, was)
		moved++
	}
	if moved > 0 {
		r.persist(board)
	}
	return moved
}

// preferFreshWork orders ready tasks so the least-tried go first.
//
// # WHY THIS EXISTS
//
// Nothing stopped a task from taking a run to itself. Measured live: one seam
// task took 17 of a run's 23 agent starts — corrector, reviewer, corrector,
// reviewer, across three consecutive waves — while four tasks sitting in lanes
// were attempted once or not at all. The run ended with 5 of 5 unexecuted and
// nothing failed.
//
// It is not a bug in any one ceiling. Review retries, gate retries and the
// corrective-wave continuation each grant attempts for their own good reason,
// and they compose; no one of them can see that another task has had no turn.
// The wave is filled in board order, so whichever task the board lists first
// keeps winning the slot — and a retry usually collides on files with the work
// it would otherwise share a wave with, which is exactly when it excludes it.
//
// A first attempt at untried work is worth more than a fourth at work that
// keeps failing: it is more likely to succeed, it tells the run something new,
// and it is what a person watching sees as progress. So the ready list is
// ordered by attempts before the wave is filled.
//
// This only ever REORDERS. Nothing is dropped, so nothing can be starved by it:
// a retried task runs as soon as no fresher task is available, and the attempt
// ceiling still parks it if it never passes.
func (r *Runner) preferFreshWork(ready []plan.Task) []plan.Task {
	if r == nil || len(ready) < 2 {
		return ready
	}
	attempts := make(map[string]int, len(ready))
	spread := false
	for _, t := range ready {
		attempts[t.ID] = r.waveAttempts.get(t.ID)
		if attempts[t.ID] != attempts[ready[0].ID] {
			spread = true
		}
	}
	// Everything equally tried is the normal case, and the board's own order
	// carries meaning the attempt count does not.
	if !spread {
		return ready
	}
	out := make([]plan.Task, len(ready))
	copy(out, ready)
	sort.SliceStable(out, func(i, j int) bool {
		return attempts[out[i].ID] < attempts[out[j].ID]
	})
	if out[0].ID != ready[0].ID {
		r.logf("wave order: %s (%d attempt(s)) goes before %s (%d) — a first attempt at "+
			"untried work beats another attempt at work that keeps failing",
			out[0].ID, attempts[out[0].ID], ready[0].ID, attempts[ready[0].ID])
	}
	return out
}

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

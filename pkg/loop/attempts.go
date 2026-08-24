package loop

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Attempt lineage — the loop half.
//
// pkg/plan owns the record and the store; this file owns the two things only
// the loop knows: WHEN a pass at a task is finished (so it can be persisted
// with the verdict that judged it, before Task.Output is overwritten by the
// next corrector pass), and WHAT the previous rejections were (so the next
// prompt carries them).
//
// Everything here is best-effort. Losing lineage costs the next prompt some
// context; failing a run over it would cost the run.

// maxAttemptSectionBytes bounds the rejected-approach block injected into
// worker and corrector prompts. The whole point of this stage is to spend a
// small model's context on the rejections it has NOT yet seen, so the block is
// deduplicated and capped rather than allowed to crowd out the task itself.
const maxAttemptSectionBytes = 1200

// attemptStore returns the run's attempt store, opening it once.
//
// A nil result means lineage is unavailable (unwritable directory, no root);
// every method on *plan.Attempts is nil-safe, so callers do not branch on it.
func (r *Runner) attemptStore() *plan.Attempts {
	if r == nil {
		return nil
	}
	r.attemptOnce.Do(func() {
		s, err := plan.OpenAttemptsWith("", plan.AttemptOptions{SlmDir: r.slmDir()})
		if err != nil {
			r.logf("attempt lineage disabled: %v", err)
			return
		}
		for _, w := range s.Warnings() {
			r.logf("attempt lineage: %s", w)
		}
		r.attemptDB = s
	})
	return r.attemptDB
}

// attemptGraph returns the run's edge store, opening it once.
//
// One store per Runner rather than one per attempt: the index is a whole-file
// cache, so two stores over the same directory would each flush a view missing
// the other's edges. Guarded exactly like the backfill hook in
// pkg/orchestrator — an error disables edges and never fails a run.
func (r *Runner) attemptGraph() *graph.Store {
	if r == nil || strings.TrimSpace(r.Root) == "" {
		return nil
	}
	r.attemptGraphOnce.Do(func() {
		g, err := graph.Open(r.Root)
		if err != nil || g == nil {
			if err != nil {
				r.logf("attempt graph edges disabled: %v", err)
			}
			return
		}
		r.attemptGraphDB = g
	})
	return r.attemptGraphDB
}

// attemptRunID names the run an attempt belongs to. TurnID is the orchestrator's
// run id (see buildRunner); embedders that never set it still get well-formed
// attempt ids and graph nodes.
func (r *Runner) attemptRunID() string {
	if r == nil {
		return plan.UnknownRunID
	}
	if id := strings.TrimSpace(r.TurnID); id != "" {
		return id
	}
	return plan.UnknownRunID
}

// attemptLineage is one task's chain of attempts, in progress.
//
// It is created per review ladder and used from that ladder's goroutine only,
// so the chain state (parent, number, motivation) needs no locking; the stores
// it writes through are shared and take their own.
type attemptLineage struct {
	r          *Runner
	store      *plan.Attempts
	graph      *graph.Store
	runID      string
	taskID     string
	parentID   string
	parentNode string
	n          int
	// motivating carries the reviewer issues that caused the NEXT attempt, so a
	// corrector pass whose own output says nothing useful can still record what
	// it was sent to fix.
	motivating []string
	wrote      bool
}

// newLineage starts (or resumes) the attempt chain for a task.
//
// It seeds from the store rather than from zero so a task re-dispatched by the
// escalate gate — or resumed in a new process — extends its chain instead of
// starting a second root.
func (r *Runner) newLineage(t plan.Task) *attemptLineage {
	lin := &attemptLineage{
		r:      r,
		store:  r.attemptStore(),
		graph:  r.attemptGraph(),
		runID:  r.attemptRunID(),
		taskID: t.ID,
		n:      1,
	}
	prior := lin.store.ForTask(lin.runID, t.ID)
	if len(prior) > 0 {
		last := prior[len(prior)-1]
		lin.parentID = last.ID
		lin.parentNode = graph.AttemptNode(last.RunID, last.TaskID, last.N)
		lin.n = last.N + 1
		lin.motivating = last.Issues
	}
	return lin
}

// record persists one completed attempt: what it tried, what it changed, which
// gates fired and the verdict that judged it.
//
// It must be called BEFORE the corrector overwrites Task.Output — that
// overwrite is what used to destroy the intermediate attempt and its verdict.
func (l *attemptLineage) record(t plan.Task, g gateState, review plan.ReviewResult,
	verdict string, started time.Time) {
	if l == nil || l.store == nil || strings.TrimSpace(t.ID) == "" {
		return
	}
	a := plan.Attempt{
		RunID:        l.runID,
		TaskID:       t.ID,
		N:            l.n,
		ParentID:     l.parentID,
		Role:         t.Role,
		Hypothesis:   plan.DeriveHypothesis(t.Output, l.motivating),
		Output:       t.Output,
		FilesTouched: attemptFiles(t),
		DiffStat:     l.r.diffStat(t),
		GateSignals:  g.signals(),
		Verdict:      verdict,
		Score:        float64(review.Score),
		Issues:       review.Issues,
		FailureClass: attemptFailureClass(l.r, t, review, verdict),
		At:           started,
		DurationMS:   time.Since(started).Milliseconds(),
	}
	stored, err := l.store.Append(a)
	if err != nil {
		l.r.logf("%s attempt %d not persisted: %v", t.ID, l.n, err)
		return
	}
	l.wrote = true
	l.addEdges(stored)
	l.parentID = stored.ID
	l.parentNode = graph.AttemptNode(stored.RunID, stored.TaskID, stored.N)
	l.n = stored.N + 1
	l.motivating = review.Issues
}

// addEdges materializes the edges this attempt implies. Best effort by design:
// the graph is derived data, so losing an edge costs one backfill and must
// never cost a run.
func (l *attemptLineage) addEdges(a plan.Attempt) {
	if l == nil || l.graph == nil {
		return
	}
	node := graph.AttemptNode(a.RunID, a.TaskID, a.N)
	if node == "" {
		return
	}
	edges := make([]graph.Edge, 0, len(a.FilesTouched)+2)
	if task := graph.TaskNode(a.RunID, a.TaskID); task != "" {
		edges = append(edges, graph.Edge{
			From: task, To: node, Type: graph.Produced, RunID: a.RunID, At: a.At,
		})
	}
	if l.parentNode != "" && l.parentNode != node {
		edges = append(edges, graph.Edge{
			From: l.parentNode, To: node, Type: graph.ParentOf, RunID: a.RunID, At: a.At,
		})
	}
	for _, f := range a.FilesTouched {
		if fn := graph.FileNode(f); fn != "" && fn != node {
			edges = append(edges, graph.Edge{
				From: node, To: fn, Type: graph.Touched, RunID: a.RunID, At: a.At,
			})
		}
	}
	if err := l.graph.Add(edges...); err != nil {
		l.r.logf("%s attempt graph edges: %v", a.TaskID, err)
	}
}

// flush persists the rebuildable indexes once the ladder is done. The logs
// themselves are already durable; this only saves the next open a rescan.
func (l *attemptLineage) flush() {
	if l == nil || !l.wrote {
		return
	}
	if err := l.store.Flush(); err != nil {
		l.r.logf("%s attempt index flush: %v", l.taskID, err)
	}
	if l.graph != nil {
		if err := l.graph.Flush(); err != nil {
			l.r.logf("%s attempt graph flush: %v", l.taskID, err)
		}
	}
}

// attemptFiles is everything this attempt touched or was aimed at: the task's
// focus files plus whatever the finalize claimed.
func attemptFiles(t plan.Task) []string {
	files := append([]string(nil), t.Files...)
	return append(files, parseFilesChanged(t.Output)...)
}

// attemptFailureClass reuses the evolve fingerprint taxonomy so a stored
// attempt names its failure the same way every other record does.
//
// An approved attempt has no failure class, and neither does one the taxonomy
// could not place: ClassUnknown is the absence of a classification, and storing
// the word "unknown" would spend prompt bytes saying nothing.
func attemptFailureClass(r *Runner, t plan.Task, review plan.ReviewResult, verdict string) string {
	if verdict == plan.AttemptApproved {
		return ""
	}
	msg := strings.TrimSpace(review.Summary)
	if msg == "" && len(review.Issues) > 0 {
		msg = review.Issues[0]
	}
	if msg == "" {
		return ""
	}
	root := ""
	if r != nil {
		root = r.Root
	}
	class := evolve.Classify(evolve.Signal{
		Message:  msg,
		Phase:    "review",
		Role:     t.Role,
		Language: detectSignalLanguage(root),
	})
	if class == evolve.ClassUnknown {
		return ""
	}
	return string(class)
}

// signals names the harness gates that fired on an attempt, in a fixed order so
// two identical attempts produce identical records.
func (g gateState) signals() []string {
	out := make([]string, 0, 12)
	for _, s := range []struct {
		on   bool
		name string
	}{
		{g.claimsFail, "claims_failed"},
		{g.staticFail, "static_failed"},
		{g.acceptFail, "acceptance_failed"},
		{g.smokeFail, "smoke_failed"},
		{g.smokeMissing, "smoke_missing"},
		{g.shellFail, "shell_failure_evidence"},
		{g.renameDisk, "rename_satisfied_on_disk"},
		{g.satisfied, "acceptance_satisfied_on_disk"},
		{g.diskWrite, "disk_write_evidence"},
		{g.toolWrite, "tool_write_evidence"},
		{g.diskSection, "disk_evidence_section"},
		{g.done, "worker_reported_done"},
	} {
		if s.on {
			out = append(out, s.name)
		}
	}
	if strings.TrimSpace(g.scopeWhy) != "" {
		out = append(out, "scope_violation")
	}
	return out
}

// diffStat summarizes what the working tree actually looks like for a task's
// focus files: "3 files, +42/-7". Compact on purpose — a full diff belongs in
// the prompt for ONE attempt, never in a log that keeps two thousand of them.
func (r *Runner) diffStat(t plan.Task) string {
	if r == nil || strings.TrimSpace(r.Root) == "" || len(t.Files) == 0 {
		return ""
	}
	args := []string{"-C", r.Root, "diff", "--numstat", "--"}
	args = append(args, t.Files...)
	out, err := exec.Command("git", args...).Output() // #nosec G204 -- paths come from the task's own focus list
	if err != nil {
		return ""
	}
	files, added, deleted := 0, 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		cols := strings.Fields(strings.TrimSpace(line))
		if len(cols) < 3 {
			continue
		}
		files++
		// "-" in place of a count is git's marker for a binary file.
		if n, cerr := strconv.Atoi(cols[0]); cerr == nil {
			added += n
		}
		if n, cerr := strconv.Atoi(cols[1]); cerr == nil {
			deleted += n
		}
	}
	if files == 0 {
		return ""
	}
	return strconv.Itoa(files) + " files, +" + strconv.Itoa(added) + "/-" + strconv.Itoa(deleted)
}

// rejectedApproachSection renders the persisted lineage as "here is what has
// already been tried, and here is why each one was refused".
//
// This is the structured replacement for six truncated prose lines: the SLM is
// not told "attempt 2 failed", it is told which approach failed and what the
// reviewer said about it, deduplicated by reason and bounded in bytes.
func (r *Runner) rejectedApproachSection(t plan.Task) string {
	if r == nil || strings.TrimSpace(t.ID) == "" {
		return ""
	}
	store := r.attemptStore()
	if store == nil {
		return ""
	}
	lineage := store.Lineage(t.ID)
	if len(lineage) == 0 {
		return ""
	}
	return plan.RejectedApproachSection(lineage, maxAttemptSectionBytes)
}

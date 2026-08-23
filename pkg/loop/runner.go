package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/repair"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Logger is an optional progress sink.
type Logger func(format string, args ...interface{})

// AfterWave is called after each execute/review wave with the tasks that were in that wave
// (post-update). Used for dynamic context + learning.
type AfterWave func(ctx context.Context, board *plan.Board, wave []plan.Task)

// AgentEvent reports live agent activity for CLI/GUI streaming.
type AgentEvent func(kind, agent, taskID, message, scope, output string)

// UsageEvent reports token usage from a subagent result (may be estimated).
type UsageEvent func(usage llm.Usage, estimated bool, input, output string)

// BuildInput optionally builds the worker/corrector prompt for a task.
// When set, packs stay ephemeral and are not persisted into task.Description.
type BuildInput func(t plan.Task) string

// EscalateHandler pauses on max-retry escalate for HITL (Studio/TUI).
// Mutates board for the chosen action (retry / re_scope / mark_done / abort).
type EscalateHandler func(ctx context.Context, board *plan.Board, t plan.Task, detail string)

// weakTaskEntry holds metadata for a task that needs self-critique refinement.
//
// There is no `passes` field any more: pre-review self-critique is capped at
// ONE pass. The old escalation (`passes = min(max(MaxRetries,3),4)` whenever
// smoke/static/acceptance failed) ran a second, redundant retry ladder on top
// of reviewAndCorrect's, which owns the retry budget.
type weakTaskEntry struct {
	idx        int
	role       string
	snapshot   map[string]string
	incomplete bool
}

// Runner executes parallel workers → review → correct against a live board.
type Runner struct {
	Executor  SubAgentRunner
	Shared    *ggagent.SharedState
	Store     *plan.LiveStore // optional — reload mid-run for human edits
	Root      string          // workspace root for evidence checks
	SlmDir    string          // optional; defaults to Root/.slmcode
	TurnID    string          // query turn id for react checkpoints
	Focus     *workspace.FocusGuard
	AfterWave AfterWave
	OnEvent   AgentEvent
	// OnEventFull is the structured event sink (carries Level + typed Data).
	// OnEvent still receives everything; set this to get agent_end levels and
	// token-by-token streaming.
	OnEventFull StructuredEvent
	OnUsage     UsageEvent
	BuildInput  BuildInput

	// ── contract with the orchestrator ──────────────────────────────────────

	// ContextLimitTokens is the model's context window in tokens; 0 = unknown.
	ContextLimitTokens int
	// MaxTaskCalls is the per-task LLM call budget; 0 => DefaultMaxTaskCalls.
	MaxTaskCalls int
	// ResolveRole maps a slot role id -> a registered agent id; nil => identity.
	ResolveRole func(string) string
	// OnTaskStart is called once when a task begins executing, so the tool
	// layer can reset that task's CallTracker bucket.
	OnTaskStart func(taskID string)
	// Evolve is the self-improvement engine; may be nil — every use is nil-safe.
	Evolve *evolve.Engine
	// MemoryTokens budgets the injected memory block; 0 => DefaultMemoryTokens.
	MemoryTokens int
	// SharedBriefLimit bounds compact sibling-task handoff injected into worker
	// prompts. Zero uses the default; negative disables the brief.
	SharedBriefLimit int
	// Feedback returns live user steering text — always re-invoked so it stays
	// fresh mid-run; nil means no feedback. Injected into worker + reviewer
	// prompts (see feedbackSection).
	Feedback   func() string
	MaxRetries int
	// MaxWaves caps post-test/QA corrective RunBoard re-entry waves.
	// Zero means unlimited legacy behavior.
	MaxWaves       int
	correctiveRuns int
	MaxParallel    int
	Timeout        time.Duration
	IdleWait       time.Duration // wait for human to promote to_scope → ready
	Log            Logger
	FailureHandler *EnhancedFailureHandler
	// PostWorkerSmoke runs deterministic Go py_compile/go test after workers
	// before review can auto-approve (default true).
	PostWorkerSmoke bool
	// QualityMonitor nudges corrector on empty / tool-junk / looped finalizes
	// (little-coder quality-monitor port).
	QualityMonitor bool
	// StaticQuality rejects stub/placeholder code before approve.
	StaticQuality bool
	// RequireSmoke blocks fast-path approve for coding tasks without smoke pass.
	RequireSmoke bool
	// ClaimsGate rejects hallucinated files_changed paths.
	ClaimsGate bool
	// WorkerCritique runs one auto self-fix pass on weak worker output.
	WorkerCritique bool
	// ThinkPasses deepens worker output when >1 (critique/refine on incomplete JSON).
	ThinkPasses int
	// ThinkingBudget enables hard-abort recovery when deliberation exceeds tokens.
	ThinkingBudget bool
	// ThinkingBudgetTokens is the hard threshold (0 = default 4096).
	ThinkingBudgetTokens int
	// AutoTextTools strengthens recovery when prose embeds tool JSON (default off).
	AutoTextTools bool
	// FinalizeWarn injects mid-run turn-budget steer on ReAct resume.
	FinalizeWarn bool
	// ReactCompact enables conversation compaction when usage crosses the threshold.
	ReactCompact bool
	// ReactCompactAtPercent is the usage trigger (default 80).
	ReactCompactAtPercent int
	// MaxContextKB is the soft conversation window used by the react watchdog.
	MaxContextKB int
	// WaveSnapshots enables pre-wave file rewind points.
	WaveSnapshots bool
	// RewindMgr stores wave file snapshots when WaveSnapshots is on.
	RewindMgr *rewind.Manager
	// ReviewerRole / CorrectorRole come from pipeline.execute (defaults reviewer/corrector).
	ReviewerRole  string
	CorrectorRole string
	// DefaultRole comes from pipeline.execute.default_role — the worker agent
	// used for tasks without an explicit role (e.g. language-specific go-worker).
	DefaultRole string
	// ReviewParallel enables parallel review+correct for tasks without shared files.
	ReviewParallel bool
	// OnEscalate pauses the task for human decision (nil = leave in to_scope).
	OnEscalate EscalateHandler
	// OnOverflowCompact is called once per wave when an LLM reports context overflow;
	// after it returns, the failed task is retried once.
	OnOverflowCompact func(ctx context.Context) error
	waveN             int
	// ResumedReact is set true when any task continued from message history
	// (used by tests / observability to assert no cold replan).
	ResumedReact bool
	// reactWatch is the mid-run conversation compaction hysteresis state.
	reactWatch   *compact.Watchdog
	reactWatchMu sync.Mutex

	// persistMu serializes LiveStore.Replace: it writes the WHOLE board, so
	// concurrent callers are last-writer-wins and silently drop each other's
	// results.
	persistMu sync.Mutex

	// budgetOnce/callBudget implement the per-task LLM call budget.
	budgetOnce sync.Once
	callBudget *callBudget

	// attempts remembers what the corrector was already told, per task.
	attempts attemptLedger

	// evo accumulates evolve failure events + bandit decisions for the
	// orchestrator to drain at the end of a run.
	evo evolveState

	// fpMu/fpCache cache per-wave content fingerprints (sha256 of file bytes).
	fpMu    sync.Mutex
	fpCache map[string]string
}

func NewRunner(exec SubAgentRunner, shared *ggagent.SharedState) *Runner {
	return &Runner{
		Executor:        exec,
		Shared:          shared,
		MaxRetries:      4,
		MaxParallel:     2,
		Timeout:         12 * time.Minute,
		IdleWait:        2 * time.Second,
		PostWorkerSmoke: true,
		Log:             func(string, ...interface{}) {},
	}
}

func (r *Runner) WithFailureHandler(fh *EnhancedFailureHandler) *Runner {
	r.FailureHandler = fh
	return r
}

// RunBoard processes executable tasks; reloads LiveStore each wave so humans
// can add / move / edit tasks while agents work.
func (r *Runner) RunBoard(ctx context.Context, board *plan.Board) error {
	guard := 0
	idleRounds := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		guard++
		if guard > 200 {
			return r.giveUp(board, ErrSafetyGuard, guard)
		}

		// Pick up CLI/UI edits
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			snap := r.Store.Snapshot()
			*board = snap
		}

		ready := scheduleReady(board.ReadyTasks())
		if len(ready) == 0 {
			if board.AgentWorkRemaining() {
				idleRounds++
				if idleRounds > 30 {
					return r.giveUp(board, ErrIdleTimeout, idleRounds)
				}
				wait := r.IdleWait
				if wait > 500*time.Millisecond {
					wait = 500 * time.Millisecond // less idle spin while in-progress
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			if board.HumanBacklogRemaining() {
				r.logf("waiting: human backlog (to_scope/scoped) — promote tasks to ready_to_dev")
				idleRounds++
				if idleRounds > 3 {
					// Not a failure: escalated tasks legitimately end a run in
					// the human columns. Still announce it so "finished" and
					// "handed back to a human" are distinguishable.
					_ = r.announceGiveUp(board, ErrHumanBacklog, idleRounds, stream.LevelWarn)
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(r.IdleWait):
				}
				continue
			}
			return nil
		}
		idleRounds = 0

		wave := ready
		if len(wave) > r.MaxParallel {
			wave = wave[:r.MaxParallel]
		}
		r.logf("wave: %d ready task(s)", len(wave))
		ids := make([]string, len(wave))
		for i, t := range wave {
			ids[i] = t.ID
		}
		if err := r.runWave(ctx, board, wave); err != nil {
			return err
		}
		r.persist(board)
		if r.AfterWave != nil {
			var finished []plan.Task
			for _, id := range ids {
				if t, ok := board.Get(id); ok {
					finished = append(finished, t)
				}
			}
			r.AfterWave(ctx, board, finished)
			r.persist(board)
		}
	}
}

// RunCorrectiveBoard runs one post-validation corrective execute pass if the
// pipeline's execute.max_waves budget allows it.
func (r *Runner) RunCorrectiveBoard(ctx context.Context, board *plan.Board) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("nil runner")
	}
	if r.MaxWaves > 0 && r.correctiveRuns >= r.MaxWaves {
		if r.Log != nil {
			r.logf("corrective wave budget exhausted (max_waves=%d)", r.MaxWaves)
		}
		return false, nil
	}
	r.correctiveRuns++
	return true, r.RunBoard(ctx, board)
}

// persist writes the board to the LiveStore. LiveStore.Replace takes the WHOLE
// board by value, so two goroutines persisting concurrently are last-writer-
// wins over everything — serialize it.
func (r *Runner) persist(board *plan.Board) {
	if r == nil || r.Store == nil {
		return
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	_ = r.Store.Replace(*board)
}

// openTaskCount counts tasks that are neither done nor human-owned.
func openTaskCount(board *plan.Board) int {
	if board == nil {
		return 0
	}
	n := 0
	for _, t := range board.Tasks {
		switch t.Column {
		case plan.ColDone, plan.ColToScope, plan.ColScoped:
			continue
		}
		n++
	}
	return n
}

// announceGiveUp emits the event without producing an error.
func (r *Runner) announceGiveUp(board *plan.Board, reason error, rounds int, level string) *GaveUpError {
	gerr := &GaveUpError{Reason: reason, Rounds: rounds, Remaining: openTaskCount(board)}
	r.logf("RunBoard: %v", gerr)
	r.fireLevel(stream.KindLoop, "harness", "", gerr.Error(), "give_up", "", level)
	return gerr
}

// giveUp announces and returns a typed error so the orchestrator can tell
// "finished" from "gave up".
func (r *Runner) giveUp(board *plan.Board, reason error, rounds int) error {
	return r.announceGiveUp(board, reason, rounds, stream.LevelError)
}

func (r *Runner) taskInput(t plan.Task) string {
	return r.taskInputFor(nil, t)
}

func (r *Runner) taskInputFor(board *plan.Board, t plan.Task) string {
	prompt := ""
	if r.BuildInput != nil {
		prompt = r.BuildInput(t)
	} else {
		prompt = fallbackTaskPrompt(t)
	}
	if brief := r.sharedBriefSection(board, t); brief != "" {
		prompt += brief
	}
	if lessons := r.adaptiveLessonsSection(); lessons != "" {
		prompt += lessons
	}
	if mem := r.memorySection(r.normalizeExecRole(t.Role)); mem != "" {
		prompt += mem
	}
	if fmtHint := r.editFormatSection(); fmtHint != "" {
		prompt += fmtHint
	}
	return prompt + r.feedbackSection()
}

func (r *Runner) adaptiveLessonsSection() string {
	if r == nil || r.Shared == nil {
		return ""
	}
	raw := ""
	for _, key := range []string{"adaptive_lessons", "latest_lessons"} {
		if v, ok := r.Shared.GetGlobal(key); ok {
			if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
				raw = s
				break
			}
		}
	}
	if raw == "" {
		return ""
	}
	return "\n## Adaptive harness lessons\n" +
		"Apply these learned corrections now; do not repeat the failed pattern.\n" +
		adaptiveGuidance(raw, 900) + "\n"
}

func adaptiveGuidance(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || limit <= 0 {
		return ""
	}
	lower := strings.ToLower(raw)
	var lines []string
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline") {
		lines = append(lines, "- Timeout adaptation: make the smallest focused edit, avoid broad scans, run one targeted smoke command, and finish JSON promptly.")
		lines = append(lines, "- If the task is too broad for one worker turn, state the exact smaller split instead of continuing to churn.")
	}
	if strings.Contains(lower, "max_parallel") || strings.Contains(lower, "contention") {
		lines = append(lines, "- Concurrency adaptation: assume local SLM contention; keep tool calls short and avoid long speculative detours.")
	}
	if strings.Contains(lower, "smoke") || strings.Contains(lower, "qa_gate") || strings.Contains(lower, "acceptance") {
		lines = append(lines, "- Verification adaptation: fix the reported command failure first and include real shell evidence before claiming done.")
	}
	if strings.Contains(lower, "placeholder") || strings.Contains(lower, "stub") {
		lines = append(lines, "- Quality adaptation: replace placeholders/stubs with real implementation before finalizing.")
	}
	lines = append(lines, recentLessonLines(raw)...)
	out := strings.TrimSpace(strings.Join(dedupeLines(lines), "\n"))
	if len(out) > limit {
		out = truncateASCII(out, limit)
	}
	return out
}

func recentLessonLines(raw string) []string {
	fields := strings.Split(raw, "\n")
	var picked []string
	for i := len(fields) - 1; i >= 0 && len(picked) < 6; i-- {
		line := strings.TrimSpace(fields[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") ||
			strings.Contains(lower, "smoke") || strings.Contains(lower, "qa_gate") ||
			strings.Contains(lower, "acceptance") || strings.Contains(lower, "placeholder") ||
			strings.Contains(lower, "stub") || strings.Contains(lower, "max retries") {
			picked = append(picked, normalizeLessonLine(line))
		}
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked
}

func normalizeLessonLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-•⚠✓ ")
	if line == "" {
		return ""
	}
	return "- Learned: " + line
}

func dedupeLines(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// feedbackSection renders the live user feedback block to append to agent
// prompts, or "" when no feedback is set.
func (r *Runner) feedbackSection() string {
	if r == nil || r.Feedback == nil {
		return ""
	}
	text := strings.TrimSpace(r.Feedback())
	if text == "" {
		return ""
	}
	return "\n\n## LIVE FEEDBACK FROM USER (highest priority — adjust your work now)\n" + text + "\n"
}

// waveState holds the parallel arrays a wave works on. needExec is addressed
// by POINTER throughout: the wave accumulates the worker output, the disk
// evidence hint and every gate section onto the task, and all of it must
// survive into review. Taking a value copy here (the old `t := needExec[j]`)
// silently threw all of that away for every task that was deferred to the
// parallel self-critique path — i.e. for every weak task on the shipped
// default of MaxParallel=4.
type waveState struct {
	needExec  []plan.Task
	needIdx   []int
	snapshots []map[string]string
	roles     []string
	weak      []weakTaskEntry
}

func (r *Runner) runWave(ctx context.Context, board *plan.Board, wave []plan.Task) error {
	r.waveN++
	r.snapshotWaveFiles(wave)

	for _, t := range wave {
		t.MoveTo(plan.ColInProgress)
		board.UpdateTask(t)
	}
	r.persist(board)

	// Constrain writes to the union of this wave's focus files (+ paths from task text).
	if r.Focus != nil {
		lists := make([][]string, 0, len(wave))
		for _, t := range wave {
			lists = append(lists, expandTaskFocus(t))
		}
		r.Focus.SetWave(lists)
		defer r.Focus.Clear()
	}

	ws := r.prepareWave(ctx, board, wave)
	if len(ws.needExec) == 0 {
		r.persist(board)
		return nil
	}

	reqs, results, execErr := r.executeWave(ctx, board, ws)
	if r.Executor == nil {
		return fmt.Errorf("nil executor")
	}
	canceled := r.collectWaveResults(ctx, board, ws, reqs, results, execErr)

	r.driveCritique(ctx, board, ws)
	canceled = r.driveReview(ctx, board, ws) || canceled

	r.persist(board)
	if canceled {
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	return nil
}

// snapshotWaveFiles takes the pre-wave rewind checkpoint.
func (r *Runner) snapshotWaveFiles(wave []plan.Task) {
	if !r.WaveSnapshots || r.RewindMgr == nil {
		return
	}
	var paths, ids []string
	for _, t := range wave {
		ids = append(ids, t.ID)
		paths = append(paths, t.Files...)
	}
	if snap, err := r.RewindMgr.SnapshotPaths(r.TurnID, r.waveN, ids, paths); err == nil && snap != nil {
		r.logf("wave %d snapshot %s (%d files)", r.waveN, snap.ID, len(snap.Files))
		r.fire(stream.KindDebug, "rewind", "", fmt.Sprintf("snapshot %s", snap.ID), "", "")
	}
}

// prepareWave fingerprints every task's targets and short-circuits retries whose
// acceptance is already satisfied on disk.
func (r *Runner) prepareWave(ctx context.Context, board *plan.Board, wave []plan.Task) *waveState {
	// Fingerprints are memoized for the duration of THIS baseline pass only:
	// sibling tasks in a wave often share focus files, but any snapshot taken
	// after a worker has written must read from disk again.
	r.enableFingerprintCache()
	defer r.disableFingerprintCache()

	ws := &waveState{
		snapshots: make([]map[string]string, len(wave)),
		roles:     make([]string, len(wave)),
	}
	for i, t := range wave {
		role := r.normalizeExecRole(t.Role)
		ws.roles[i] = role
		ws.snapshots[i] = r.snapshotTargets(t)
		scope := strings.Join(t.Files, ", ")
		if r.alreadySatisfiedRetry(t, ws.snapshots[i]) && r.scopeOK(t) == "" {
			r.logf("%s skip worker LLM: retry with acceptance already satisfied on disk", t.ID)
			r.fire(stream.KindAgentStart, role, t.ID, "skip — already satisfied", scope, "")
			t.Output = `{"status":"done","summary":"already satisfied on disk — skipped worker LLM"}` +
				"\n\n" + diskEvidenceHeader + "\n- created/present: " + strings.Join(t.Files, ", ")
			r.fireLevel(stream.KindAgentEnd, role, t.ID, "skipped worker (already satisfied)",
				scope, truncate(t.Output, 400), stream.LevelSuccess)
			t.MoveTo(plan.ColInReview)
			board.UpdateTask(t)
			if err := r.reviewAndCorrect(ctx, board, t, ws.snapshots[i]); err != nil {
				r.logf("review/correct %s: %v", t.ID, err)
			}
			continue
		}
		r.fire(stream.KindAgentStart, role, t.ID, t.Title, scope, "")
		ws.needExec = append(ws.needExec, t)
		ws.needIdx = append(ws.needIdx, i)
	}
	return ws
}

// executeWave dispatches the worker calls for the wave and normalizes the
// result slice so results[j] always corresponds to needExec[j].
func (r *Runner) executeWave(ctx context.Context, board *plan.Board, ws *waveState) (
	[]ggagent.SubAgentRequest, []ggagent.SubAgentResult, error) {
	reqs := make([]ggagent.SubAgentRequest, 0, len(ws.needExec))
	for _, t := range ws.needExec {
		// Per-task tool isolation: reset the task's CallTracker bucket and
		// budget. Without it every parallel task shares one bucket and can
		// hard-stop its neighbours' legitimate repeated reads.
		ctx = r.startTask(ctx, t.ID)
		_ = r.spend(t.ID, "worker")
		req := ggagent.SubAgentRequest{
			AgentID:    r.normalizeExecRole(t.Role),
			Input:      r.taskInputFor(board, t),
			Timeout:    r.Timeout,
			ShareState: true,
			TaskID:     t.ID,
		}
		if r.applyResumeRequest(&req, t.ID) {
			r.ResumedReact = true
		}
		reqs = append(reqs, req)
	}
	if r.Executor == nil {
		return reqs, nil, fmt.Errorf("nil executor")
	}
	// A single-task wave can carry its task id in ctx; a batched wave cannot —
	// the executor must derive the tool ctx from SubAgentRequest.TaskID there.
	callCtx := ctx
	if len(reqs) == 1 {
		callCtx = r.taskCtx(ctx, reqs[0].TaskID)
	}
	results, err := r.Executor.ExecuteSubAgents(callCtx, reqs, r.Shared)
	if err != nil {
		r.logf("wave execution warning: %v", err)
	}
	if len(results) > len(reqs) {
		results = results[:len(reqs)]
	}
	if err != nil && len(results) < len(reqs) {
		filled := make([]ggagent.SubAgentResult, len(reqs))
		copy(filled, results)
		for k := len(results); k < len(reqs); k++ {
			filled[k] = ggagent.SubAgentResult{
				AgentID: reqs[k].AgentID,
				TaskID:  reqs[k].TaskID,
				Error:   err,
			}
		}
		results = filled
	}
	return reqs, results, err
}

// collectWaveResults folds each worker result back into its OWN slice element,
// runs the harness gates, and queues weak tasks for self-critique.
func (r *Runner) collectWaveResults(ctx context.Context, board *plan.Board, ws *waveState,
	reqs []ggagent.SubAgentRequest, results []ggagent.SubAgentResult, execErr error) bool {
	canceled := false
	for j := range results {
		if j >= len(ws.needExec) {
			break
		}
		res := results[j]
		i := ws.needIdx[j]
		t := &ws.needExec[j] // POINTER: everything below must survive into review.
		role := ws.roles[i]
		input := ""
		if j < len(reqs) {
			input = reqs[j].Input
		}
		r.noteUsage(res, input, outputString(res))
		t.Output = outputString(res)

		if isTimeoutResult(execErr, res) {
			r.handleTaskTimeout(board, *t, role, res, timeoutErr(execErr, res))
			continue
		}
		if isCancelResult(execErr, res) || (res.Error != nil && isCancelResult(res.Error, res)) {
			r.handleTaskCancel(board, t, role, res, execErr)
			canceled = true
			continue
		}
		if res.Error != nil {
			var ok bool
			res, ok = r.recoverWaveError(ctx, board, t, role, res, execErr)
			if !ok {
				continue
			}
		}
		r.clearReact(t.ID)
		t.Output = outputString(res)
		if res.Iteration > 0 {
			r.fireTurn(t.ID, res.Iteration, roleMaxIter(role))
		}
		// SLMs often empty-finalize or end on tool-junk / synthetic blocked JSON.
		r.recoverIncompleteFinalize(ctx, t, ws.snapshots[i])
		r.runGates(ctx, t, role, ws.snapshots[i], gateOpts{verbose: true})

		// Auto self-critique when output is weak, or when think_passes>=2 and
		// status JSON looks incomplete (worker multipass port).
		deferred := false
		if r.wantCritique(role) {
			incomplete := !multipass.LooksCompleteJSON(stripPostSections(t.Output))
			if r.outputWeak(*t, incomplete) {
				if r.MaxParallel >= 2 {
					ws.weak = append(ws.weak, weakTaskEntry{
						idx: j, role: role, snapshot: ws.snapshots[i], incomplete: incomplete,
					})
					deferred = true
				} else {
					r.critiquePass(ctx, t, role, ws.snapshots[i], incomplete)
				}
			}
		}
		if !deferred {
			r.fire(stream.KindAgentEnd, role, t.ID, "worker finished",
				strings.Join(t.Files, ", "), truncate(t.Output, 1200))
			t.MoveTo(plan.ColInReview)
			board.UpdateTask(*t)
		}
	}
	// Persist BEFORE review. reviewAndCorrectTask re-reads the LiveStore to pick
	// up human edits, so a store still holding the pre-worker task would hand
	// review an empty Output and reject on "no write evidence".
	r.persist(board)
	return canceled
}

// wantCritique reports whether a role participates in pre-review self-critique.
func (r *Runner) wantCritique(role string) bool {
	if !r.WorkerCritique && r.ThinkPasses < 2 {
		return false
	}
	return role == plan.RoleWorker || role == "deep" || role == plan.RoleCorrector
}

// handleTaskCancel checkpoints a canceled task's ReAct conversation.
func (r *Runner) handleTaskCancel(board *plan.Board, t *plan.Task, role string,
	res ggagent.SubAgentResult, execErr error) {
	r.saveReactFromResult(t.ID, role, res)
	t.MoveTo(plan.ColBlocked)
	switch {
	case res.Error != nil:
		t.Error = res.Error.Error()
	case execErr != nil:
		t.Error = execErr.Error()
	default:
		t.Error = "context canceled"
	}
	board.UpdateTask(*t)
	r.fireLevel(stream.KindAgentEnd, role, t.ID, "interrupted — react checkpointed",
		strings.Join(t.Files, ", "), t.Error, stream.LevelWarn)
}

// recoverWaveError handles a failed worker result. A context overflow shrinks
// the pack and retries once; anything else blocks the task. The overflow test
// is backends.IsContextOverflow, which classifies the provider's error rather
// than pattern-matching a generic HTTP 400.
func (r *Runner) recoverWaveError(ctx context.Context, board *plan.Board, t *plan.Task,
	role string, res ggagent.SubAgentResult, execErr error) (ggagent.SubAgentResult, bool) {
	class := backends.Classify(res.Error)
	overflow := backends.IsContextOverflow(res.Error)
	if overflow && r.OnOverflowCompact != nil {
		r.logf("%s context overflow (%s) — compacting and retrying once", t.ID, class.Class)
		adv := r.noteFailure(evolve.Signal{
			Message: res.Error.Error(), Phase: "execute", Role: role,
			Language: detectSignalLanguage(r.Root),
		}, "")
		if cerr := r.OnOverflowCompact(ctx); cerr != nil {
			r.logf("%s overflow compact: %v", t.ID, cerr)
		} else {
			retryReq := ggagent.SubAgentRequest{
				AgentID: role, Input: r.taskInputFor(board, *t), Timeout: r.Timeout,
				ShareState: true, TaskID: t.ID,
			}
			if r.applyResumeRequest(&retryReq, t.ID) {
				r.ResumedReact = true
			}
			retryRes, ok := r.execOne(ctx, t.ID, "overflow retry", retryReq)
			if ok && retryRes.Error == nil && strings.TrimSpace(outputString(retryRes)) != "" {
				r.noteResolved(adv, "context compacted, retried once")
				return retryRes, true
			}
			if ok && retryRes.Error != nil {
				r.logf("%s overflow retry still failed: %v", t.ID, retryRes.Error)
				res = retryRes
			}
		}
	} else if res.Error != nil {
		r.noteFailure(evolve.Signal{
			Message: res.Error.Error(), Phase: "execute", Role: role,
			Language: detectSignalLanguage(r.Root),
		}, "")
	}
	// Still persist partial conversation when available.
	if len(res.Messages) > 0 {
		r.saveReactFromResult(t.ID, role, res)
	}
	if res.Error == nil {
		return res, true
	}
	t.Output = outputString(res)
	t.MoveTo(plan.ColBlocked)
	t.Error = res.Error.Error()
	board.UpdateTask(*t)
	r.fireLevel(stream.KindAgentEnd, role, t.ID, "error",
		strings.Join(t.Files, ", "), t.Error, stream.LevelError)
	if r.FailureHandler != nil {
		r.reportTaskFailure(board, *t, res.Error, 0, "execute", false)
	}
	return res, false
}

// driveCritique refines every weak task, in parallel when there is more than one.
func (r *Runner) driveCritique(ctx context.Context, board *plan.Board, ws *waveState) {
	if len(ws.weak) == 0 {
		return
	}
	if len(ws.weak) > 1 && r.MaxParallel >= 2 {
		r.runSelfCritiqueParallel(ctx, board, ws.needExec, ws.weak)
		return
	}
	for _, wt := range ws.weak {
		t := &ws.needExec[wt.idx]
		r.critiquePass(ctx, t, wt.role, wt.snapshot, wt.incomplete)
		r.fire(stream.KindAgentEnd, wt.role, t.ID, "worker finished",
			strings.Join(t.Files, ", "), truncate(t.Output, 1200))
		t.MoveTo(plan.ColInReview)
		board.UpdateTask(*t)
	}
}

// driveReview runs review+correct for everything that finished execution.
func (r *Runner) driveReview(ctx context.Context, board *plan.Board, ws *waveState) bool {
	var reviewTasks []plan.Task
	for _, t := range ws.needExec {
		if bt, ok := board.Get(t.ID); ok && bt.Column == plan.ColInReview && bt.Error == "" {
			reviewTasks = append(reviewTasks, bt)
		}
	}
	if len(reviewTasks) == 0 {
		return false
	}
	snaps := snapIndexed(ws.snapshots, ws.needExec, ws.needIdx)
	if r.ReviewParallel && len(reviewTasks) > 1 {
		r.logf("parallel review: %d task(s), max_parallel=%d", len(reviewTasks), r.MaxParallel)
		r.reviewWave(ctx, board, reviewTasks, snaps)
		return false
	}
	canceled := false
	for _, t := range reviewTasks {
		// Use the pre-wave snapshot so disk evidence survives finalize JSON.
		if err := r.reviewAndCorrect(ctx, board, t, snaps[t.ID]); err != nil {
			r.logf("review/correct %s: %v", t.ID, err)
			if isCancelResult(err, ggagent.SubAgentResult{}) {
				canceled = true
			}
		}
	}
	return canceled
}

func (r *Runner) handleTaskTimeout(board *plan.Board, t plan.Task, role string, res ggagent.SubAgentResult, err error) {
	if err == nil {
		err = context.DeadlineExceeded
	}
	if len(res.Messages) > 0 {
		r.saveReactFromResult(t.ID, role, res)
	}
	scope := strings.Join(t.Files, ", ")
	baseOut := strings.TrimSpace(outputString(res))
	after := r.Timeout
	if after <= 0 {
		after = 12 * time.Minute
	}
	timeoutMsg := fmt.Sprintf("task timed out after %s: %s", after.Round(time.Second), err.Error())
	if baseOut == "" {
		t.Output = fmt.Sprintf(`{"status":"blocked","summary":%q,"files_changed":[],"notes":"harness timeout; retry with smaller scope or lower concurrency"}`, timeoutMsg)
	} else {
		t.Output = baseOut + "\n\n## Harness timeout\n" + timeoutMsg
	}
	t.Error = timeoutMsg
	t.Notes = strings.TrimSpace(t.Notes + "\nTIMEOUT: " +
		"the worker exceeded task_timeout. Retry with smaller scope, lower max_parallel, or a larger task_timeout.")
	t.MoveTo(plan.ColToScope)
	board.UpdateTask(t)
	r.persist(board)
	r.fireLevel(stream.KindAgentEnd, role, t.ID, "timed out — task needs retry/re-scope", scope, timeoutMsg, stream.LevelError)
	r.fireIntervention(t.ID, "timeout",
		fmt.Sprintf("%s timed out — choose retry/re-scope or continue another corrective wave", t.ID),
		timeoutMsg)
	r.reportTaskFailure(board, t, err, t.Retries, "timeout", true)
}

func (r *Runner) reviewerID() string {
	if r != nil && strings.TrimSpace(r.ReviewerRole) != "" {
		return strings.TrimSpace(r.ReviewerRole)
	}
	return plan.RoleReviewer
}

func (r *Runner) correctorID() string {
	if r != nil && strings.TrimSpace(r.CorrectorRole) != "" {
		return strings.TrimSpace(r.CorrectorRole)
	}
	return plan.RoleCorrector
}

// normalizeExecRole maps a task role to the executor agent id. Empty/legacy
// roles use the pipeline's execute.default_role (a language-specific worker
// when a preset is active), falling back to the generic worker.
func (r *Runner) normalizeExecRole(role string) string {
	if role == "" || role == "implementer" {
		if r != nil && r.DefaultRole != "" {
			return r.DefaultRole
		}
		return plan.RoleWorker
	}
	switch role {
	case plan.RolePlanner, "splitter", "orchestrator", "memory", "coordinator", "architect", "context":
		return plan.RoleWorker
	default:
		return role
	}
}

func (r *Runner) fireIntervention(taskID, reason, msg, detail string) {
	code := quality.ClassifyIntervention(reason)
	if msg == "" {
		msg = quality.PhraseForUser(reason)
	}
	scope := code
	r.fireLevel(stream.KindIntervention, "harness", taskID, msg, scope, detail, stream.LevelWarn)
}

func (r *Runner) fireTurn(taskID string, iter, maxIter int) {
	if maxIter <= 0 {
		return
	}
	msg := fmt.Sprintf("turn %d/%d", iter, maxIter)
	if quality.ShouldFinalizeSteer(iter, maxIter) {
		msg += " · finalize soon"
	}
	r.fire(stream.KindTurn, "harness", taskID, msg, fmt.Sprintf("%d/%d", iter, maxIter), "")
}

func (r *Runner) noteUsage(res ggagent.SubAgentResult, input, output string) {
	if r.OnUsage == nil {
		return
	}
	u := res.Usage
	est := res.UsageEstimated
	if u.TotalTokens == 0 && u.PromptTokens == 0 && u.CompletionTokens == 0 {
		u = llm.Usage{
			PromptTokens:     llm.EstimateTokens(input),
			CompletionTokens: llm.EstimateCompletionTokens(output, nil),
		}
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
		est = true
	}
	r.OnUsage(u, est, input, output)
}

// gateState bundles the review-gate booleans for one task. They used to be a
// dozen loose locals inside a ~400-line function with nesting depth 6.
type gateState struct {
	renameDisk  bool
	satisfied   bool
	diskWrite   bool
	toolWrite   bool
	diskSection bool
	done        bool
	scopeWhy    string

	shellFail    bool
	smokeFail    bool
	acceptFail   bool
	staticFail   bool
	claimsFail   bool
	smokeMissing bool
}

// hasEvidence reports whether anything at all proves a write happened.
func (g gateState) hasEvidence() bool {
	return g.diskWrite || g.toolWrite || g.diskSection || g.renameDisk
}

// blocking reports whether a hard gate failed.
func (g gateState) blocking() bool {
	return g.shellFail || g.smokeFail || g.acceptFail || g.staticFail || g.claimsFail || g.smokeMissing
}

// fastPath reports whether the reviewer LLM can be skipped entirely.
func (g gateState) fastPath(role string) bool {
	if g.renameDisk {
		return true
	}
	return role != plan.RoleTester && !g.blocking() &&
		(g.satisfied || g.diskWrite || g.diskSection) && g.scopeWhy == ""
}

// rejectReason returns the summary/issue for the highest-priority hard failure.
func (g gateState) rejectReason() (summary string, issue string) {
	switch {
	case g.claimsFail:
		return "rejected: hallucinated files_changed paths",
			"files_changed lists paths missing on disk — reconcile claims"
	case g.staticFail:
		return "rejected: static quality gate (stubs/placeholders)",
			"stub/placeholder code detected — replace with real implementation"
	case g.acceptFail:
		return "rejected: acceptance smoke failed",
			"whitelisted acceptance command failed — make the acceptance command exit 0"
	case g.smokeFail:
		return "rejected: deterministic smoke failed",
			"deterministic smoke command failed — corrector must fix"
	case g.smokeMissing:
		return "rejected: coding task missing deterministic smoke pass",
			"run the project's language smoke (py_compile / go test / node --check) before claiming done"
	default:
		return "rejected: ws_shell failure evidence in worker output",
			"worker ran a command that failed — fix before approve"
	}
}

// gatherGateSignals computes the review gate state for a task, running the two
// review-time insurance gates (static, smoke) when the worker path skipped them.
// It may append sections to current.Output.
func (r *Runner) gatherGateSignals(ctx context.Context, current *plan.Task, baseline map[string]string) gateState {
	g := gateState{}
	g.renameDisk = renameOK(r.Root, *current)
	g.satisfied = alreadySatisfied(r.Root, *current, baseline) || g.renameDisk
	g.diskWrite = r.hasRealWriteEvidence(*current, baseline) || g.renameDisk
	g.toolWrite = hasToolWriteEvidence(current.Output)
	g.diskSection = hasDiskEvidenceSection(current.Output)
	g.done = plan.WorkerLooksComplete(current.Output) || workerReportedDone(current.Output)
	g.scopeWhy = r.scopeOK(*current)
	g.shellFail = hasShellFailureEvidence(current.Output)
	g.smokeFail = quality.SmokeFailedInOutput(current.Output)
	g.acceptFail = quality.AcceptanceFailedInOutput(current.Output)
	g.staticFail = quality.StaticFailedInOutput(current.Output)
	g.claimsFail = quality.ClaimsFailedInOutput(current.Output)

	// Review-time static insurance: skipped-worker / already-satisfied paths
	// never ran CheckStaticQuality — catch Placeholder stubs before fast-path.
	if r.StaticQuality && !g.staticFail && !g.renameDisk {
		if issues := quality.CheckStaticQuality(r.Root, *current); len(issues) > 0 {
			current.Output = strings.TrimSpace(current.Output) + quality.FormatStaticSection(issues)
			g.staticFail = true
			r.logf("%s review-time static FAILED (%d issue(s))", current.ID, len(issues))
			r.fireIntervention(current.ID, "review",
				"stub/placeholder code blocked auto-approve — needs real implementation",
				quality.FormatStaticSection(issues))
		}
	}

	smokeFiles := append([]string{}, current.Files...)
	smokeFiles = append(smokeFiles, parseFilesChanged(current.Output)...)
	// Review-time smoke insurance: if PostWorkerSmoke somehow didn't attach a
	// section (corrector overwrite, truncated finalize), run it now so
	// RequireSmoke cannot false-reject a green compile/test.
	if r.RequireSmoke && r.PostWorkerSmoke && quality.ShouldSmokeTask(*current) &&
		!quality.SmokePassedInOutput(current.Output) && !g.smokeFail && !g.renameDisk &&
		quality.HasSmokeCommand(r.Root, smokeFiles) {
		sr := quality.RunPostWorkerSmoke(ctx, r.Root, *current, r.Timeout)
		if sec := quality.FormatSmokeSection(sr); sec != "" {
			current.Output = strings.TrimSpace(current.Output) + sec
			if sr.Ran && !sr.OK {
				g.smokeFail = true
				r.logf("%s review-time smoke FAILED: %s", current.ID, sr.Command)
			} else if sr.Ran {
				r.logf("%s review-time smoke PASSED: %s", current.ID, sr.Command)
			}
		}
	}
	g.smokeMissing = r.RequireSmoke && quality.HasSmokeCommand(r.Root, smokeFiles) &&
		!quality.SmokePassedInOutput(current.Output) && !g.smokeFail && !g.renameDisk
	return g
}

// decideReview produces the review verdict: the disk fast path, the speculative
// race, or a single reviewer LLM call.
func (r *Runner) decideReview(ctx context.Context, current plan.Task, g gateState,
	baseline map[string]string) (plan.ReviewResult, string, error) {
	if g.fastPath(current.Role) {
		review := plan.ReviewResult{Approved: true, Score: 85}
		switch {
		case g.renameDisk:
			review.Summary = "auto-approved: rename satisfied on disk"
		case g.satisfied && !g.hasEvidence():
			review.Summary = "auto-approved: acceptance already satisfied on disk"
		default:
			review.Summary = "auto-approved: disk write evidence on focus files"
		}
		r.logf("%s review fast-path (skip reviewer LLM): %s", current.ID, review.Summary)
		return review, "", nil
	}
	if r.MaxParallel >= 2 {
		return r.speculativeReview(ctx, current, g, baseline)
	}
	return r.plainReview(ctx, current, g)
}

// speculativeReview races a disk/acceptance probe against the reviewer LLM (and
// a strict second reviewer when capacity allows).
func (r *Runner) speculativeReview(ctx context.Context, current plan.Task, g gateState,
	baseline map[string]string) (plan.ReviewResult, string, error) {
	// One race is one review attempt against the task's budget, however many
	// speculative paths it fans out to. Without this the DEFAULT configuration
	// (MaxParallel=4, which always takes this branch) would never be budgeted
	// at all — reviews are exactly where the round-trip ladder lives.
	if !r.spend(current.ID, "review") {
		return r.slmApprovalFallback(plan.ReviewResult{
			Approved: false, Summary: "review skipped: per-task call budget exhausted",
		}, current, g, ""), "", nil
	}
	reviewIn := r.formatReviewPrompt(current)
	revRole := r.resolveRole(r.reviewerID())
	slots := r.reviewSlots(current, g, baseline, reviewIn, revRole)
	r.fire(stream.KindAgentStart, revRole, current.ID, "speculative review race",
		strings.Join(current.Files, ", "), "")
	r.logf("%s speculative review (%d paths, max_parallel=%d)", current.ID, len(slots), r.MaxParallel)
	res := r.speculate(r.taskCtx(ctx, current.ID), slots)

	var acceptOut, revOut, strictOut string
	var revErr error
	for _, sr := range res {
		switch {
		case sr.Role == "acceptance":
			if !sr.Skipped && sr.Err == nil {
				acceptOut = sr.Output
			}
		case sr.Role == revRole:
			revOut, revErr = sr.Output, sr.Err
		default:
			if !sr.Skipped && strings.TrimSpace(sr.Output) != "" {
				strictOut = sr.Output
			}
		}
	}
	if strings.TrimSpace(acceptOut) != "" {
		r.logf("%s review acceptance won — cancelled reviewer LLM", current.ID)
		return plan.ParseReviewJSON(acceptOut), acceptOut, nil
	}
	reviewRaw := revOut
	if strings.TrimSpace(reviewRaw) == "" {
		reviewRaw = strictOut
	}
	if revErr != nil && strings.TrimSpace(reviewRaw) == "" {
		return plan.ReviewResult{}, "", revErr
	}
	review := r.parseReviewOutput(ctx, current, reviewRaw, revRole, reviewIn)
	return r.slmApprovalFallback(review, current, g, reviewRaw), reviewRaw, nil
}

// reviewSlots builds the speculative review race: a local disk/acceptance probe,
// the reviewer LLM, and — when capacity allows — a strict second reviewer.
func (r *Runner) reviewSlots(current plan.Task, g gateState, baseline map[string]string,
	reviewIn, revRole string) []SpecSlot {
	cur := current
	base := baseline
	// Use a shorter reviewer timeout when disk evidence already exists — the
	// acceptance probe should win quickly; the full 12 min is excessive.
	revTimeout := r.Timeout
	if g.hasEvidence() {
		revTimeout = 3 * time.Minute
	}
	slots := []SpecSlot{{
		Role: "acceptance", Required: false,
		Local: func(ctx context.Context) (string, error) {
			return acceptanceProbe(ctx, func() bool {
				return renameOK(r.Root, cur) || alreadySatisfied(r.Root, cur, base) ||
					r.hasRealWriteEvidence(cur, base)
			}, `{"approved":true,"score":85,"summary":"auto-approved: acceptance race won"}`)
		},
	}, {
		Role: revRole, Prompt: reviewIn, Required: false, Timeout: revTimeout,
	}}
	// Second reviewer when capacity allows. reviewer-strict went unregistered
	// for as long as this code referenced it, so the path never ran; assert the
	// role is real instead of failing silently at runtime.
	if r.MaxParallel >= 3 {
		if strict, ok := r.resolveBuiltinSlot(roleReviewerStrict); ok {
			slots = append(slots, SpecSlot{
				Role: strict, Required: false,
				Prompt:  reviewIn + "\n\nSTRICT: reject unless focus files + acceptance clearly met. Return JSON.",
				Timeout: revTimeout,
			})
		}
	}
	return slots
}

// plainReview runs one reviewer LLM call.
func (r *Runner) plainReview(ctx context.Context, current plan.Task, g gateState) (
	plan.ReviewResult, string, error) {
	revRole := r.resolveRole(r.reviewerID())
	r.fire(stream.KindAgentStart, revRole, current.ID, "self-critic review",
		strings.Join(current.Files, ", "), "")
	reviewIn := r.formatReviewPrompt(current)
	res, ok := r.execOne(ctx, current.ID, "review", ggagent.SubAgentRequest{
		AgentID: revRole, Input: reviewIn, Timeout: r.Timeout, ShareState: true, TaskID: current.ID,
	})
	if !ok {
		// Budget exhausted: approve on evidence or reject — never loop.
		return r.slmApprovalFallback(plan.ReviewResult{
			Approved: false, Summary: "review skipped: per-task call budget exhausted",
		}, current, g, ""), "", nil
	}
	reviewRaw := outputString(res)
	if res.Error != nil && reviewRaw == "" {
		return plan.ReviewResult{}, "", res.Error
	}
	review := r.parseReviewOutput(ctx, current, reviewRaw, revRole, reviewIn)
	return r.slmApprovalFallback(review, current, g, reviewRaw), reviewRaw, nil
}

// parseReviewOutput repairs the reviewer's JSON against the reviewer schema.
// A response cut off by max_tokens is re-asked with a bigger budget rather than
// answered with a corrector round-trip at what is only a truncation.
func (r *Runner) parseReviewOutput(ctx context.Context, current plan.Task, raw, role, prompt string) plan.ReviewResult {
	if strings.TrimSpace(raw) == "" {
		return plan.ReviewResult{}
	}
	fixed, rung, err := repair.RepairRole(raw, plan.RoleReviewer)
	switch {
	case err == nil:
		if rung != "" && rung != "clean" {
			r.logf("%s reviewer JSON repaired (%s)", current.ID, rung)
		}
		return plan.ParseReviewJSON(string(fixed))
	case errors.Is(err, repair.ErrTruncated):
		r.logf("%s reviewer output truncated mid-string — re-asking with a larger budget", current.ID)
		r.fireIntervention(current.ID, "truncated_output",
			"reviewer answer was cut off by max_tokens — re-asking, not correcting", rung)
		retry, ok := r.execOne(ctx, current.ID, "review retry (truncated)", ggagent.SubAgentRequest{
			AgentID: role,
			Input: prompt + "\n\nYour previous answer was CUT OFF mid-JSON. " +
				"Answer again with the SHORTEST valid JSON object: " +
				`{"approved":<bool>,"score":<int>,"summary":"<one short line>","issues":[]}`,
			Timeout: r.Timeout, ShareState: true, TaskID: current.ID,
		})
		if ok {
			if again := outputString(retry); strings.TrimSpace(again) != "" {
				if fixed2, _, err2 := repair.RepairRole(again, plan.RoleReviewer); err2 == nil {
					return plan.ParseReviewJSON(string(fixed2))
				}
				return plan.ParseReviewJSON(again)
			}
		}
		return plan.ParseReviewJSON(raw)
	default:
		return plan.ParseReviewJSON(raw)
	}
}

// slmApprovalFallback trusts clear worker completion + disk/tool evidence when
// a small reviewer model returns a broken or over-strict verdict.
func (r *Runner) slmApprovalFallback(review plan.ReviewResult, current plan.Task,
	g gateState, reviewRaw string) plan.ReviewResult {
	if review.Approved || current.Role == plan.RoleTester || g.blocking() {
		return review
	}
	if g.satisfied || g.diskWrite || g.diskSection ||
		(g.done && (looksLikeBrokenReview(reviewRaw) || g.hasEvidence())) {
		review.Approved = true
		review.Score = 80
		switch {
		case g.satisfied && !g.hasEvidence():
			review.Summary = "auto-approved: acceptance already satisfied on disk"
		case g.diskSection || g.diskWrite:
			review.Summary = "auto-approved: disk write evidence on focus files"
		default:
			review.Summary = "auto-approved: worker completion + write evidence"
		}
		review.Issues = nil
	}
	return review
}

// applyHardGates lets a deterministic failure beat any earlier auto-approve and
// runs the tester + evidence gates.
func (r *Runner) applyHardGates(current *plan.Task, review plan.ReviewResult, g gateState,
	baseline map[string]string) plan.ReviewResult {
	if review.Approved && g.blocking() && current.Role != plan.RoleTester && !g.renameDisk {
		summary, issue := g.rejectReason()
		review.Approved = false
		review.Score = 20
		review.Summary = summary
		review.Issues = []string{issue}
		r.logf("%s overriding approve: %s", current.ID, review.Summary)
	}
	// Tester gate: never accept "does not work" / passed:false / empty finalize.
	// Exception: rename already satisfied on disk — do not reopen/escalate.
	if review.Approved && strings.EqualFold(current.Role, plan.RoleTester) {
		if g.renameDisk {
			r.logf("%s tester gate skipped: rename satisfied on disk", current.ID)
		} else if tr, ok := r.parseTesterOutput(*current); ok && !tr.Passed {
			why := "tester reported failure"
			if len(tr.Failures) > 0 {
				why = tr.Failures[0]
			} else if tr.Summary != "" {
				why = tr.Summary
			}
			review.Approved = false
			review.Score = 0
			review.Summary = "rejected by tester gate: " + why
			review.Issues = append([]string{why}, review.Issues...)
			r.logf("%s tester gate blocked approval: %s", current.ID, why)
		}
	}
	// Evidence gate: never mark done when targets are missing or no write occurred.
	if review.Approved && !g.renameDisk {
		if ok, why := r.evidenceOK(*current, baseline); !ok {
			review.Approved = false
			review.Score = 0
			review.Summary = "rejected by evidence gate: " + why
			review.Issues = append([]string{why}, review.Issues...)
			if r.Root != "" {
				real := plan.ReconcileFiles(r.Root, current.Files,
					plan.DiscoverRelevantFiles(r.Root, current.Title+" "+current.Description, current.Output))
				if len(real) > 0 {
					current.Files = real
				}
			}
			r.logf("%s evidence gate blocked approval: %s", current.ID, why)
		}
	}
	return review
}

// parseTesterOutput repairs the tester's finalize JSON before judging it.
func (r *Runner) parseTesterOutput(t plan.Task) (plan.TesterResult, bool) {
	raw := stripPostSections(t.Output)
	if strings.TrimSpace(raw) == "" {
		return plan.ParseTesterJSON(t.Output), true
	}
	fixed, rung, err := repair.RepairRole(raw, plan.RoleTester)
	if err == nil {
		if rung != "" && rung != "clean" {
			r.logf("%s tester JSON repaired (%s)", t.ID, rung)
		}
		return plan.ParseTesterJSON(string(fixed)), true
	}
	if errors.Is(err, repair.ErrTruncated) {
		r.logf("%s tester output truncated mid-string — not treating truncation as a test failure", t.ID)
		r.fireIntervention(t.ID, "truncated_output",
			"tester answer was cut off by max_tokens", rung)
		return plan.TesterResult{}, false
	}
	return plan.ParseTesterJSON(t.Output), true
}

// reviewAndCorrect reviews a task and runs correction rounds against the board.
func (r *Runner) reviewAndCorrect(ctx context.Context, board *plan.Board, t plan.Task, baseline map[string]string) error {
	final, esc, err := r.reviewAndCorrectTask(ctx, t, baseline)
	board.UpdateTask(final)
	r.persist(board)
	if esc != nil && r.OnEscalate != nil {
		r.OnEscalate(ctx, board, final, esc.detail)
		// Reload after HITL mutation (retry / re_scope / mark_done / abort).
		if updated, ok := board.Get(final.ID); ok {
			board.UpdateTask(updated)
			r.persist(board)
			final = updated
		}
	}
	if esc != nil {
		r.reportTaskFailure(board, final, errMaxRetries, esc.attempt, "review", true)
	}
	if err != nil {
		r.reportTaskFailure(board, final, err, final.Retries, "review", false)
	}
	return err
}

// escalation records that a task ran out of retries and needs a human.
type escalation struct {
	detail  string
	attempt int
}

// reviewAndCorrectTask is the board-free review+correct ladder for ONE task.
//
// It never touches *plan.Board: reviewWave used to hand the shared board to N
// goroutines which then called board.Get / board.UpdateTask (8 sites) and
// persist(*board) with no synchronization at all — an unsynchronized concurrent
// read/append of the same slice, plus a last-writer-wins whole-board write that
// silently dropped other groups' results.
func (r *Runner) reviewAndCorrectTask(ctx context.Context, t plan.Task, baseline map[string]string) (
	plan.Task, *escalation, error) {
	if baseline == nil {
		baseline = r.snapshotTargets(t)
	}
	current := t
	ctx = r.taskCtx(ctx, t.ID)
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		// Pick up human edits from the live store.
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			if latest, ok := r.Store.GetTask(current.ID); ok {
				switch latest.Column {
				case plan.ColDone, plan.ColBlocked, plan.ColToScope, plan.ColScoped:
					return latest, nil, nil
				}
				current = latest
			}
		}

		g := r.gatherGateSignals(ctx, &current, baseline)
		review, reviewRaw, err := r.decideReview(ctx, current, g, baseline)
		if err != nil {
			current.MoveTo(plan.ColBlocked)
			current.Error = err.Error()
			return current, nil, err
		}
		review = r.applyHardGates(&current, review, g, baseline)

		current.Review = review.Summary
		if len(review.Issues) > 0 {
			current.Review += "\nIssues:\n- " + strings.Join(review.Issues, "\n- ")
		}
		endOut := truncate(reviewRaw, 600)
		if endOut == "" {
			endOut = review.Summary
		}
		level := stream.LevelSuccess
		if !review.Approved {
			level = stream.LevelProblem
		}
		r.fireLevel(stream.KindAgentEnd, r.reviewerID(), current.ID,
			fmt.Sprintf("review approved=%v score=%d", review.Approved, review.Score),
			"", endOut, level)

		if review.Approved {
			mergeFilesChanged(&current)
			current.MoveTo(plan.ColDone)
			current.Error = ""
			r.logf("%s approved → done (score=%d)", current.ID, review.Score)
			return current, nil, nil
		}

		r.noteFailure(evolve.Signal{
			Message:  review.Summary,
			Phase:    "review",
			Role:     current.Role,
			Language: detectSignalLanguage(r.Root),
		}, "")

		outOfBudget := r.budgetExhausted(current.ID)
		if attempt == r.MaxRetries || outOfBudget {
			return r.escalateTask(current, review, attempt, outOfBudget)
		}

		current.MoveTo(plan.ColInProgress)
		current.Retries = attempt + 1
		r.logf("%s correcting (attempt %d)", current.ID, attempt+1)
		r.fire(stream.KindAgentStart, r.correctorID(), current.ID, "correction pass",
			strings.Join(current.Files, ", "), "")

		corrIn := r.formatCorrectPrompt(current, review)
		corr, ok := r.execOne(ctx, current.ID, "correction", ggagent.SubAgentRequest{
			AgentID: r.correctorID(), Input: corrIn,
			Timeout: r.Timeout, ShareState: true, TaskID: current.ID,
		})
		if !ok {
			return r.escalateTask(current, review, attempt, true)
		}
		if corr.Error != nil && strings.TrimSpace(outputString(corr)) == "" {
			current.MoveTo(plan.ColBlocked)
			current.Error = corr.Error.Error()
			return current, nil, corr.Error
		}
		if out := outputString(corr); strings.TrimSpace(out) != "" {
			current.Output = out
		}
		r.noteAttempt(current.ID, review.Issues)
		// Re-run EVERY gate after a corrector pass — including the claims gate,
		// which used to be skipped, so a corrector that hallucinated
		// files_changed after a rejection went un-regated.
		r.runGates(ctx, &current, r.correctorID(), baseline, gateOpts{})
		r.fire(stream.KindAgentEnd, r.correctorID(), current.ID, "corrector finished", "",
			truncate(current.Output, 800))
		current.MoveTo(plan.ColInReview)
	}
	return current, nil, nil
}

// escalateTask moves a task to the human backlog with a full explanation.
func (r *Runner) escalateTask(current plan.Task, review plan.ReviewResult, attempt int, budget bool) (
	plan.Task, *escalation, error) {
	why := "review rejected after max retries — needs human input or smaller scope"
	note := "ESCALATED: review rejected after max retries. "
	if budget {
		why = fmt.Sprintf("review rejected and the %d-call per-task budget is spent — needs human input or smaller scope",
			r.budget().max)
		note = fmt.Sprintf("ESCALATED: %s. ", ErrTaskCallBudget)
	}
	current.MoveTo(plan.ColToScope)
	current.Error = why
	current.Notes = strings.TrimSpace(current.Notes + "\n" + note +
		"Fix acceptance/context, then promote back to ready_to_dev.\n" + current.Review)
	r.logf("%s escalated to to_scope after %d retries (budget_spent=%d/%d)",
		current.ID, attempt, r.budget().spent(current.ID), r.budget().max)
	if budget {
		r.fireIntervention(current.ID, "call_budget",
			fmt.Sprintf("%s hit its %d-call budget — escalating instead of another review round-trip",
				current.ID, r.budget().max),
			fmt.Sprintf("max_task_calls=%d used=%d blocked=review",
				r.budget().max, r.budget().spent(current.ID)))
	}

	detail := strings.TrimSpace(current.Review)
	if detail == "" {
		detail = current.Error
	}
	r.fireIntervention(current.ID, "escalate",
		fmt.Sprintf("%s needs human review — decide in Studio (or wait for timeout)", current.ID),
		detail)
	return current, &escalation{detail: detail, attempt: attempt}, nil
}

// fallbackTaskPrompt is a MINIMAL rendering of a task, used only when
// BuildInput is nil — i.e. in tests and in embedding callers that forgot to set
// it. Production always goes through the orchestrator's formatWorkerPromptFor.
//
// This deliberately does NOT restate the worker rules (focus scope, no extra
// helper files, ws_patch retry, ws_shell smoke, no stubs). The old
// formatWorkerPrompt did, which made pkg/loop look like a second source of
// truth for the worker contract while production silently used a different,
// shorter prompt that omitted exactly the rules the review gates reject on.
// One builder must own those rules: the orchestrator's.
func fallbackTaskPrompt(t plan.Task) string {
	var b strings.Builder
	b.WriteString("Atomic task — complete only this:\n\n")
	b.WriteString(fmt.Sprintf("ID: %s\nTitle: %s\nColumn: %s\nRole: %s\n\n", t.ID, t.Title, t.Column, t.Role))
	b.WriteString(StripScopedPack(t.Description))
	b.WriteString("\n")
	if len(t.Files) > 0 {
		b.WriteString("\n## Focus files\n- " + strings.Join(t.Files, "\n- ") + "\n")
	}
	if t.Acceptance != "" {
		b.WriteString("\nAcceptance criteria:\n" + t.Acceptance + "\n")
	}
	if len(t.Checklist) > 0 {
		b.WriteString("\nChecklist:\n")
		for _, c := range t.Checklist {
			mark := "[ ]"
			if c.Done {
				mark = "[x]"
			}
			b.WriteString(fmt.Sprintf("- %s %s\n", mark, c.Text))
		}
	}
	if t.Notes != "" {
		b.WriteString("\nHuman notes:\n" + t.Notes + "\n")
	}
	b.WriteString("\nEnd with STRICT JSON only:\n" +
		`{"status":"done","summary":"...","files_changed":["real/path.go"],"notes":"..."}` + "\n")
	return b.String()
}

// StripScopedPack removes ephemeral pack headers so TASKS.md stays lean.
func StripScopedPack(desc string) string {
	desc = strings.TrimSpace(desc)
	if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
	}
	if strings.HasPrefix(desc, "# Scoped context") {
		if idx := strings.Index(desc, "\n# "); idx > 0 {
			// keep looking for task body markers
		}
		if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
			desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
		}
	}
	return strings.TrimSpace(desc)
}

func (r *Runner) formatReviewPrompt(t plan.Task) string {
	langHint := ""
	if r != nil {
		if h := detectProjectLangHint(r.Root); h != "" {
			langHint = "\n## Project language\n" + h
		}
	}
	return fmt.Sprintf(`Review task %s (%s) role=@%s. Reply with JSON only — no tools.%s

## Acceptance
%s

## Agent output
%s

Rules:
- Approve if worker JSON status is done AND there is real write evidence (ws_write/ws_edit/ws_patch tool result OR Disk evidence section showing changed files).
- Reject if output is only claims/JSON with no tool or disk evidence for edit tasks.
- Reject if files_changed includes paths outside focus scope (especially unwanted main.go).
- Reject if "## Deterministic smoke" shows FAILED or Observation has exit error / SyntaxError / traceback.
- Reject if "## Acceptance smoke" shows FAILED (pytest / go test / python main.py from acceptance).
- Reject if "## Static quality gate" shows FAILED (stubs/placeholders).
- Reject if "## Claimed files gate" shows FAILED (hallucinated paths).
- @explorer: approve if a real file path was found.
- @tester: approve ONLY when output JSON has "passed":true AND real shell Observation (not fabricated commands[]). Reject if passed:false, failures listed, or "does not work".
- Reject only if work is clearly missing or out of scope.
`, t.ID, t.Title, t.Role, langHint, t.Acceptance, truncate(t.Output, 3500)) + r.feedbackSection()
}

// detectProjectLangHint returns a language-pinned verification hint based on
// marker files at the project root. Mirrors the orchestrator's detectProjectLang
// without importing it (loop must stay independent of the orchestrator).
func detectProjectLangHint(root string) string {
	if root == "" {
		return ""
	}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return "Project language: Go. Use ONLY go build ./..., go vet ./..., go test ./... — never pytest/pip/python."
	case has("pyproject.toml"), has("setup.py"), has("requirements.txt"):
		return "Project language: Python. Use python -m pytest -q (or uv run pytest -q) — never go test."
	case has("package.json"):
		return "Project language: JS/TS. Use npm test / npx tsc --noEmit / npm run build — never pytest or go test."
	case has("Cargo.toml"):
		return "Project language: Rust. Use cargo build --quiet / cargo test --quiet — never pytest/go test/npm test."
	case has("pom.xml"), has("build.gradle"), has("build.gradle.kts"):
		return "Project language: Java. Use mvn -q test (or ./gradlew test) — never pytest/go test/npm test."
	case has("CMakeLists.txt"):
		return "Project language: C/C++. Use cmake --build build (or make) and ctest — never pytest/go test/npm test."
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".go") {
				return "Project language: Go (single-file, no go.mod). Use gofmt -e FILE for syntax; never pytest/go test without a go.mod."
			}
		}
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") {
				return "Project language: static web (HTML/CSS/JS, no build step). Verify a usable index.html exists and asset refs resolve; node --check each .js. Never run pytest/go test/npm test (no package.json)."
			}
		}
	}
	return ""
}

// truncate clips s to at most n bytes on a RUNE boundary. Naive s[:n] split
// multi-byte runes; invalid UTF-8 tokenizes into replacement-character byte
// fallbacks, which wastes tokens and measurably degrades small-model reading.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return textutil.Clip(s, n) + "…"
}

func outputString(res ggagent.SubAgentResult) string {
	if res.Output == nil {
		return ""
	}
	if s, ok := res.Output.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", res.Output)
}

// recoverIncompleteFinalize nudges the corrector (up to 2 passes) when the
// worker ended empty / on tool-junk / with GoLangGraph's synthetic blocked JSON.
// If recovery still fails but disk/tool evidence shows writes, synthesize a
// provisional done JSON so review/smoke can decide on real evidence.
func (r *Runner) recoverIncompleteFinalize(ctx context.Context, t *plan.Task, baseline map[string]string) {
	if r == nil || t == nil {
		return
	}
	const maxPasses = 2
	for pass := 0; pass < maxPasses; pass++ {
		reason, nudgeIssue, ok := r.incompleteFinalizeNudge(*t)
		if !ok {
			return
		}
		r.logf("%s incomplete finalize (%s); finish-steer pass %d/%d", t.ID, reason, pass+1, maxPasses)
		r.fireIntervention(t.ID, reason, quality.PhraseForUser(reason), nudgeIssue)
		r.fire(stream.KindAgentStart, r.correctorID(), t.ID, "fix incomplete finalize",
			strings.Join(t.Files, ", "), "")
		corrIn := r.formatCorrectPrompt(*t, plan.ReviewResult{
			Approved: false,
			Issues:   []string{nudgeIssue},
			Summary:  "incomplete finalize",
		})
		corr, ok2 := r.execOne(ctx, t.ID, "finalize recovery", ggagent.SubAgentRequest{
			AgentID: r.correctorID(), Input: corrIn,
			Timeout: r.Timeout, ShareState: true, TaskID: t.ID,
		})
		if !ok2 {
			break
		}
		if out := outputString(corr); strings.TrimSpace(out) != "" {
			t.Output = out
		}
		r.noteAttempt(t.ID, []string{nudgeIssue})
		r.fire(stream.KindAgentEnd, r.correctorID(), t.ID, "corrector finished", "", truncate(t.Output, 800))
	}
	reason, _, stillBad := r.incompleteFinalizeNudge(*t)
	if !stillBad {
		return
	}
	hasWrite := r.hasRealWriteEvidence(*t, baseline) || hasToolWriteEvidence(t.Output) || hasDiskEvidenceSection(t.Output)
	if !hasWrite {
		r.logf("%s incomplete finalize persists after recovery (%s)", t.ID, reason)
		return
	}
	files := append([]string{}, t.Files...)
	files = append(files, parseFilesChanged(t.Output)...)
	provisional := quality.ProvisionalDoneFromEvidence(uniqStrings(files), reason)
	// Keep prior observations for smoke/static appendices that may already be present.
	if rest := strings.TrimSpace(t.Output); rest != "" && !strings.HasPrefix(rest, "{") {
		t.Output = provisional + "\n\n" + rest
	} else {
		t.Output = provisional
	}
	r.logf("%s provisional done from write evidence after incomplete finalize (%s)", t.ID, reason)
	r.fireIntervention(t.ID, "provisional_finalize", "recovered finalize from disk/tool evidence", reason)
}

// incompleteFinalizeNudge returns whether the task output needs a finish-steer
// pass, plus the reason and corrective issue text.
func (r *Runner) incompleteFinalizeNudge(t plan.Task) (reason, nudgeIssue string, need bool) {
	core := stripPostSections(t.Output)
	if reason = quality.IncompleteFinalizeReason(core); reason != "" {
		hasWrite := hasToolWriteEvidence(t.Output) || hasDiskEvidenceSection(t.Output)
		return reason, quality.FinishSteerMessage(reason, hasWrite), true
	}
	if quality.LooksLikeToolJunk(core) {
		return "ended_on_tool_call", quality.FinishSteerMessage("ended_on_tool_call", false), true
	}
	if r.QualityMonitor {
		assess := quality.AssessResponse(core, nil, nil, nil)
		if !assess.OK {
			nudgeIssue = quality.CorrectionMessage(assess.Reason)
			if strings.HasPrefix(assess.Reason, "text_tool_calls:") {
				if calls := quality.ParseTextToolCalls(core); len(calls) > 0 {
					nudgeIssue = quality.TextToolNudge(calls)
				}
			}
			return assess.Reason, nudgeIssue, true
		}
	}
	if calls := quality.ParseTextToolCalls(core); len(calls) > 0 {
		nudgeIssue = quality.TextToolNudge(calls)
		if r.AutoTextTools {
			nudgeIssue = "AUTO_TEXT_TOOLS: re-issue these as NATIVE tool calls immediately, then status JSON.\n" + nudgeIssue
		}
		return "text_tool_calls", nudgeIssue, true
	}
	if r.ThinkingBudget && quality.ThinkingBudgetExceeded(core, r.ThinkingBudgetTokens) {
		return "thinking_budget_exceeded", quality.ThinkingBudgetBreachMessage(), true
	}
	return "", "", false
}

func uniqStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// harnessSectionHeaders are the exact headers the harness appends to a worker's
// output. Two of them were WRONG here: the emitted headers are
// "## Static quality gate" and "## Claimed files gate" (see
// quality.FormatStaticSection / quality.FormatClaimsSection), but this list
// looked for "## Static quality" and "## Claims gate" followed by a newline —
// which never matched. The consequence was that harness-authored gate markdown,
// including the literal word FAILED, stayed glued to the model's finalize text
// when multipass.LooksCompleteJSON, quality.IncompleteFinalizeReason,
// quality.LooksLikeToolJunk and quality.AssessResponse judged it.
var harnessSectionHeaders = []string{
	diskEvidenceHeader,
	quality.SmokeSectionHeader,
	quality.AcceptanceSectionHeader,
	staticGateHeader,
	claimsGateHeader,
}

// staticGateHeader / claimsGateHeader mirror quality.FormatStaticSection and
// quality.FormatClaimsSection. pkg/quality does not export them yet (only
// SmokeSectionHeader and AcceptanceSectionHeader are exported); they belong
// there, and stripPostSectionsHeadersMatchQuality guards the duplication.
const (
	staticGateHeader = "## Static quality gate"
	claimsGateHeader = "## Claimed files gate"
)

// stripPostSections removes harness-appended evidence/gate sections so JSON
// completeness checks look at the model answer, not smoke/claims appendices.
func stripPostSections(s string) string {
	for _, header := range harnessSectionHeaders {
		if i := strings.Index(s, "\n"+header); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

func roleMaxIter(role string) int {
	switch role {
	case "deep":
		return 20
	case plan.RoleCorrector, plan.RoleTester:
		return 12
	case plan.RoleExplorer, "docs":
		return 10
	case plan.RoleWorker, "implementer", "":
		return 16
	default:
		return 16
	}
}

func looksLikeBrokenReview(raw string) bool {
	lower := strings.ToLower(raw)
	if quality.LooksLikeToolJunk(raw) {
		return true
	}
	if strings.Contains(lower, `"approved"`) {
		return false
	}
	return !strings.Contains(raw, "{") || strings.TrimSpace(raw) == ""
}

func workerReportedDone(output string) bool {
	lower := strings.ToLower(output)
	hasDone := strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`)
	hasFiles := strings.Contains(lower, `"files_changed"`) || strings.Contains(lower, "dry-run: would")
	return hasDone && hasFiles
}

func mergeFilesChanged(t *plan.Task) {
	files := parseFilesChanged(t.Output)
	if len(files) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, f := range t.Files {
		seen[f] = true
	}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		t.Files = append(t.Files, f)
	}
}

func parseFilesChanged(output string) []string {
	raw := extractJSONObject(output)
	if raw == "" {
		return nil
	}
	var payload struct {
		FilesChanged []string `json:"files_changed"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload.FilesChanged
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return ""
}

func (r *Runner) evidenceOK(t plan.Task, baseline map[string]string) (bool, string) {
	if r.Root == "" {
		return true, ""
	}
	// Rename acceptance first: old gone / new present / symbols updated — even when
	// t.Files still lists the old path or worker left weak/out-of-scope claims.
	if renameOK(r.Root, t) {
		return true, ""
	}
	if why := r.scopeOK(t); why != "" {
		return false, why
	}
	if len(t.Files) > 0 {
		missing := 0
		for _, f := range t.Files {
			if !plan.FileExists(r.Root, f) {
				missing++
			}
		}
		if missing == len(t.Files) {
			return false, "all task target files are missing on disk (hallucinated paths)"
		}
	}
	// Create/scaffold (or doc-comment) already on disk → accept even without a
	// fresh baseline delta (common when a prior attempt or sibling wrote the file).
	if alreadySatisfied(r.Root, t, baseline) {
		return true, ""
	}
	if looksLikeEditTask(t) {
		if r.hasRealWriteEvidence(t, baseline) {
			return true, ""
		}
		if hasToolWriteEvidence(t.Output) {
			return true, ""
		}
		return false, "edit task has no real write evidence (tool result or disk/git change)"
	}
	return true, ""
}

// scopeOK rejects wander: claimed or newly created files outside task focus.
func (r *Runner) scopeOK(t plan.Task) string {
	claimed := parseFilesChanged(t.Output)
	// Build a task-local guard from expanded focus (includes scaffold paths).
	g := workspace.NewFocusGuard()
	focus := expandTaskFocus(t)
	if len(focus) > 0 {
		g.SetWave([][]string{focus})
	}
	if bad := g.OutOfScopeFiles(claimed); len(bad) > 0 {
		return "out-of-scope files_changed: " + strings.Join(bad, ", ")
	}
	// Detect newly created entrypoints on disk that are not in focus.
	if r.Root == "" || len(t.Files) == 0 {
		return ""
	}
	for _, name := range []string{"main.go", "main.py", "index.js", "index.ts", "app.js", "app.ts"} {
		p := filepath.Join(r.Root, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		rel := name
		if g.Allow(rel) {
			continue
		}
		// Only flag if created during this task (not in baseline focus fingerprints)
		// or simply present while not allowed — prefer git/status dirty.
		dirty := false
		for _, c := range r.gitChangedFiles() {
			if c == rel || strings.HasSuffix(c, "/"+rel) {
				dirty = true
				break
			}
		}
		// Also treat as wander if worker claimed it.
		for _, c := range claimed {
			if filepath.Base(c) == name && !g.Allow(c) {
				dirty = true
				break
			}
		}
		if dirty {
			return "out-of-scope entrypoint created/modified: " + rel
		}
	}
	return ""
}

// expandTaskFocus merges declared files with path-like mentions in the task text
// so greenfield scaffolding (src/pkg/…) is not blocked by a single-manifest focus.
func expandTaskFocus(t plan.Task) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, f := range t.Files {
		add(f)
	}
	blob := t.Title + "\n" + StripScopedPack(t.Description) + "\n" + t.Acceptance
	for _, f := range plan.ExtractFilePaths(blob) {
		add(f)
	}
	// Common greenfield directories when the task is clearly creating a project.
	lower := strings.ToLower(blob)
	if strings.Contains(lower, "create") || strings.Contains(lower, "scaffold") ||
		strings.Contains(lower, "pyproject") || strings.Contains(lower, "langgraph") ||
		strings.Contains(lower, "new project") || strings.Contains(lower, "mvp") {
		add("src/")
		add("tests/")
		add("README.md")
		add("pyproject.toml")
		add("main.py")
	}
	return out
}

// scheduleReady orders ready tasks for better parallel utilization:
// focused (has files) + fewer deps-looking + shorter titles first.
func scheduleReady(ready []plan.Task) []plan.Task {
	if len(ready) < 2 {
		return ready
	}
	out := append([]plan.Task(nil), ready...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if taskPriority(out[j]) < taskPriority(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func taskPriority(t plan.Task) int {
	p := 100
	if len(t.Files) > 0 {
		p -= 40
	}
	p += len(t.DependsOn) * 5
	p += len(t.Title) / 20
	switch strings.ToLower(t.Role) {
	case "explorer", "docs":
		p -= 10 // discover first when parallel slots free
	case "tester":
		p += 20 // tests after implementers when possible
	}
	return p
}

func looksLikeEditTask(t plan.Task) bool {
	blob := strings.ToLower(t.Title + " " + StripScopedPack(t.Description) + " " + t.Acceptance)
	for _, k := range []string{"add ", "edit", "fix", "implement", "write", "update", "doc comment", "comment", "refactor", "rename", "create ", "improve", "revamp", "redesign"} {
		if strings.Contains(blob, k) {
			return true
		}
	}
	return false
}

// Structured tool-result markers. A worker that touched nothing and emitted
// {"status":"done","summary":"Updated the parser…"} used to match the bare
// participle "updated " and be credited with a write — which auto-approved the
// task and skipped the reviewer entirely. Only markers a TOOL emits count here;
// English prose from the model does not. Note especially that
// "## Disk evidence" is NOT in this list: that section is written by the
// harness, so treating it as tool evidence was a self-reference.
var toolWriteMarkers = []string{
	"ws_write", "ws_edit", "ws_patch", "ws_mv", "ws_delete",
	"dry-run: would write", "dry-run: would edit", "dry-run: would patch",
	"dry-run: would mv", "dry-run: would delete",
	"staged write",
}

// wsEditResultRe matches ws_edit's own result line, "edited <path> (N
// replacement(s))" — structured output, not prose.
var wsEditResultRe = regexp.MustCompile(`(?i)\bedited\s+\S+\s+\(\d+\s+replacement\(s\)\)`)

// wsMvResultRe matches ws_mv's result line, "moved <from> → <to>".
var wsMvResultRe = regexp.MustCompile(`(?i)\bmoved\s+\S+\s+→\s+\S+`)

// hasToolWriteEvidence reports whether the output contains a structured
// tool-result marker proving a write tool ran.
//
// This is a FALLBACK signal only. hasRealWriteEvidence consults the disk first
// whenever a workspace root is known; text matching decides nothing when the
// filesystem can answer.
func hasToolWriteEvidence(output string) bool {
	lower := strings.ToLower(output)
	for _, m := range toolWriteMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return wsEditResultRe.MatchString(output) || wsMvResultRe.MatchString(output)
}

// hasRealWriteEvidence reports whether a write really happened.
//
// Precedence matters: the DISK is authoritative. Text matching (tool markers,
// the harness's own Disk evidence section) is consulted only when there is no
// workspace root to interrogate, or as a tie-breaker once the deterministic
// comparison has already run and come back ambiguous. Checking the text first
// (the old order) meant a hallucinating worker short-circuited both the
// baseline-fingerprint comparison and the git-diff check.
func (r *Runner) hasRealWriteEvidence(t plan.Task, baseline map[string]string) bool {
	if renameOK(r.Root, t) {
		return true
	}
	if r.Root == "" {
		// No filesystem to consult — text is all there is.
		return hasToolWriteEvidence(t.Output) || hasDiskEvidenceSection(t.Output)
	}
	// Pending review queue counts as a write attempt.
	pending := filepath.Join(r.Root, ".slmcode", "pending")
	if entries, err := os.ReadDir(pending); err == nil && len(entries) > 0 {
		return true
	}
	// Content hash changes vs baseline — including deletions (rename-away).
	delta := false
	for _, f := range t.Files {
		cur := fileFingerprint(filepath.Join(r.Root, f))
		prev := baseline[f]
		if cur != "" && prev != "" && cur != prev {
			delta = true
			break
		}
		if prev == "" && cur != "" && plan.FileExists(r.Root, f) {
			// Newly created file (delete+create rename pair / scaffold).
			delta = true
			break
		}
		if prev != "" && cur == "" {
			// Deleted / renamed-away focus path.
			delta = true
			break
		}
	}
	// Detect rename pair: old deleted + new created from intent.
	if !delta {
		if spec := plan.DetectRenameIntent(t.Title, StripScopedPack(t.Description), t.Acceptance, strings.Join(t.Files, " ")); spec.Kind == plan.RenameFile {
			oldGone := spec.OldPath != "" && !plan.FileExists(r.Root, spec.OldPath)
			newPresent := spec.NewPath != "" && plan.FileExists(r.Root, spec.NewPath)
			if oldGone && newPresent {
				delta = true
			}
		}
	}
	if delta {
		return true
	}
	changed := r.gitChangedFiles()
	// Ambiguous baseline (empty / missing / matches current after a late
	// snapshot): the deterministic comparison above could not decide, so now —
	// and only now — text evidence is allowed to break the tie.
	if baselineAmbiguous(baseline, t) {
		if focusGitDirty(changed, t.Files) {
			return true
		}
		if hasDiskEvidenceSection(t.Output) {
			return true
		}
		// Focus file present + edit task with a structured tool-write marker.
		if looksLikeEditTask(t) && focusFilesPresent(r.Root, t.Files) &&
			hasToolWriteEvidence(t.Output) {
			return true
		}
	}
	// Git diff for target files (includes rename old+new paths).
	if len(changed) == 0 {
		return false
	}
	if len(t.Files) == 0 {
		return true
	}
	focus := append([]string{}, t.Files...)
	if spec := plan.DetectRenameIntent(t.Title, StripScopedPack(t.Description), t.Acceptance, strings.Join(t.Files, " ")); spec.Kind == plan.RenameFile {
		if spec.OldPath != "" {
			focus = append(focus, spec.OldPath)
		}
		if spec.NewPath != "" {
			focus = append(focus, spec.NewPath)
		}
	}
	return focusGitDirty(changed, focus)
}

func hasShellFailureEvidence(output string) bool {
	lower := strings.ToLower(output)
	needles := []string{
		"exit error:", "exit status", "traceback (most recent call last)",
		"syntaxerror", "modulenotfounderror", "importerror",
		"argumenterror", "nameerror:", "typeerror:", "indentationerror",
		"compilation failed", "build failed",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

// diskEvidenceHeader is the harness-authored evidence section header.
const diskEvidenceHeader = "## Disk evidence"

// evidentialDiskMarkers are the Disk-evidence lines that actually prove this
// task changed something. "git dirty:" is deliberately absent — see
// outOfScopeDirtyMarker.
var evidentialDiskMarkers = []string{
	"modified:", "created/present:", "renamed:", "deleted:",
	"in-scope git change:", "pending:",
}

// outOfScopeDirtyMarker labels repository dirt that has nothing to do with this
// task. It is reported for the reviewer's situational awareness and is NOT
// accepted as evidence: `git diff --name-only HEAD` returns every uncommitted
// change in the repo — the normal state of a working tree, and *guaranteed*
// once the first task in a wave writes anything — so accepting it handed every
// subsequent task a Disk evidence section and a fast-path auto-approval.
const outOfScopeDirtyMarker = "out-of-scope repo dirt (NOT evidence for this task):"

func hasDiskEvidenceSection(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, strings.ToLower(diskEvidenceHeader)) {
		return false
	}
	for _, m := range evidentialDiskMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func baselineAmbiguous(baseline map[string]string, t plan.Task) bool {
	if len(t.Files) == 0 {
		return true
	}
	if baseline == nil || len(baseline) == 0 {
		return true
	}
	missing := 0
	for _, f := range t.Files {
		if _, ok := baseline[f]; !ok {
			missing++
		}
	}
	return missing == len(t.Files)
}

func focusFilesPresent(root string, files []string) bool {
	if root == "" || len(files) == 0 {
		return false
	}
	for _, f := range files {
		if plan.FileExists(root, f) {
			return true
		}
	}
	return false
}

func focusGitDirty(changed, focus []string) bool {
	if len(changed) == 0 {
		return false
	}
	if len(focus) == 0 {
		return true
	}
	for _, f := range focus {
		f = filepath.ToSlash(f)
		base := strings.ToLower(filepath.Base(f))
		for _, c := range changed {
			c = filepath.ToSlash(c)
			if c == f || strings.HasSuffix(c, "/"+f) || strings.HasSuffix(f, "/"+c) {
				return true
			}
			if base != "" && strings.ToLower(filepath.Base(c)) == base {
				return true
			}
		}
	}
	return false
}

func (r *Runner) snapshotTargets(t plan.Task) map[string]string {
	out := map[string]string{}
	if r.Root == "" {
		return out
	}
	for _, f := range t.Files {
		out[f] = r.baselineFingerprint(filepath.Join(r.Root, f))
	}
	return out
}

func (r *Runner) diskEvidenceHint(t plan.Task, baseline map[string]string) string {
	var lines []string
	if spec := plan.DetectRenameIntent(t.Title, StripScopedPack(t.Description), t.Acceptance, strings.Join(t.Files, " ")); spec.Kind != plan.RenameNone {
		if spec.Kind == plan.RenameFile && spec.OldPath != "" && spec.NewPath != "" {
			if !plan.FileExists(r.Root, spec.OldPath) && plan.FileExists(r.Root, spec.NewPath) {
				lines = append(lines, "- renamed: "+spec.OldPath+" → "+spec.NewPath)
			} else if !plan.FileExists(r.Root, spec.OldPath) {
				lines = append(lines, "- deleted: "+spec.OldPath)
			}
		}
		if spec.Kind == plan.RenameSymbol && renameOK(r.Root, t) {
			lines = append(lines, "- renamed: "+spec.OldSymbol+" → "+spec.NewSymbol)
		}
	}
	for _, f := range t.Files {
		cur := fileFingerprint(filepath.Join(r.Root, f))
		prev := baseline[f]
		switch {
		case prev != "" && cur == "":
			lines = append(lines, "- deleted: "+f)
		case prev == "" && cur != "":
			lines = append(lines, "- created/present: "+f)
		case prev != "" && cur != "" && prev != cur:
			lines = append(lines, "- modified: "+f)
		}
	}
	// Git-dirty paths, split by whether they are inside THIS task's focus.
	focus := expandTaskFocus(t)
	inScope, outScope := 0, 0
	for _, c := range r.gitChangedFiles() {
		if focusGitDirty([]string{c}, focus) {
			if inScope < 8 {
				lines = append(lines, "- in-scope git change: "+c)
			}
			inScope++
			continue
		}
		outScope++
	}
	if outScope > 0 {
		lines = append(lines, fmt.Sprintf("- %s %d file(s) outside the task focus are dirty",
			outOfScopeDirtyMarker, outScope))
	}
	pending := filepath.Join(r.Root, ".slmcode", "pending")
	if entries, err := os.ReadDir(pending); err == nil {
		for _, e := range entries {
			lines = append(lines, "- pending: "+e.Name())
			if len(lines) > 16 {
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (r *Runner) gitChangedFiles() []string {
	if r.Root == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", r.Root, "diff", "--name-only", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("git", "-C", r.Root, "status", "--porcelain")
		out, err = cmd.Output()
		if err != nil {
			return nil
		}
		var files []string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// status --porcelain: XY PATH  or  R  old -> new
			if len(line) <= 3 {
				continue
			}
			rest := strings.TrimSpace(line[3:])
			if strings.Contains(rest, " -> ") {
				parts := strings.SplitN(rest, " -> ", 2)
				if len(parts) == 2 {
					files = append(files, filepath.ToSlash(strings.TrimSpace(parts[0])))
					files = append(files, filepath.ToSlash(strings.TrimSpace(parts[1])))
					continue
				}
			}
			files = append(files, filepath.ToSlash(rest))
		}
		return files
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, filepath.ToSlash(line))
		}
	}
	return files
}

// fileFingerprint is the content identity of a file: length + sha256.
//
// This is the ONLY deterministic write detector in the loop, so a rolling
// `(sum*131 + b + i) mod 1e9+7` — which collides trivially and was recomputed
// from scratch on every check — is the wrong instrument for the job.
func fileFingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%d:%s", len(data), hex.EncodeToString(sum[:]))
}

// enableFingerprintCache arms the per-wave-baseline fingerprint memo. Sibling
// tasks in one wave routinely list the same focus files, and the old code
// re-read and re-hashed each of them once per task.
func (r *Runner) enableFingerprintCache() {
	if r == nil {
		return
	}
	r.fpMu.Lock()
	r.fpCache = make(map[string]string)
	r.fpMu.Unlock()
}

// disableFingerprintCache turns memoization back off. It MUST be off outside a
// baseline pass: once a worker has written, a cached fingerprint is a lie, and
// this detector is what decides whether a write happened at all.
func (r *Runner) disableFingerprintCache() {
	if r == nil {
		return
	}
	r.fpMu.Lock()
	r.fpCache = nil
	r.fpMu.Unlock()
}

// baselineFingerprint is fileFingerprint, memoized while a baseline pass is in
// flight and a plain read otherwise.
func (r *Runner) baselineFingerprint(path string) string {
	if r == nil {
		return fileFingerprint(path)
	}
	r.fpMu.Lock()
	if r.fpCache == nil {
		r.fpMu.Unlock()
		return fileFingerprint(path)
	}
	if v, ok := r.fpCache[path]; ok {
		r.fpMu.Unlock()
		return v
	}
	r.fpMu.Unlock()

	v := fileFingerprint(path)

	r.fpMu.Lock()
	if r.fpCache != nil {
		r.fpCache[path] = v
	}
	r.fpMu.Unlock()
	return v
}

// alreadySatisfied reports whether a task's acceptance is ALREADY met on disk
// without the worker having to run.
//
// Two keyword branches used to make this fire constantly:
//
//   - "create" matched any task whose text contained the substring — including
//     "Create a helper to parse config in pkg/util/cfg.go" — and was satisfied
//     by a bare os.Stat, so an existing non-empty file skipped the worker LLM
//     entirely and auto-approved on `satisfied`.
//   - "comment" returned true if ANY focus file contained a `//` line and a
//     `func `/`def ` — that is, nearly every Go file in existence, so
//     "Add doc comments and validate the input" skipped the whole task.
//
// A planner SLM writes "create" and "add" constantly. The fix is positive,
// task-specific evidence instead of a keyword: the create branch now requires
// the target to have been ABSENT at wave start (baseline holds an empty
// fingerprint for it), which is true for a real scaffold and for a sibling task
// that just wrote the file, and false for a file that was simply already there.
// The comment branch is gone.
//
// baseline is the pre-wave snapshot; nil means "unknown", which falls back to a
// plain existence check.
func alreadySatisfied(root string, t plan.Task, baseline map[string]string) bool {
	if root == "" {
		return false
	}
	if renameOK(root, t) {
		return true
	}
	blob := strings.ToLower(t.Title + " " + StripScopedPack(t.Description) + " " + t.Acceptance)
	if !isCreateTask(blob) {
		return false
	}
	// Implement/class-agent work is never "already satisfied" by mere existence.
	if strings.Contains(blob, "implement") || strings.Contains(blob, "class") ||
		strings.Contains(blob, "langgraph") || strings.Contains(blob, "langchain") {
		return false
	}
	targets := t.Files
	if len(targets) == 0 {
		targets = plan.InferCreateFiles(blob)
	}
	present, needed, fresh := 0, 0, 0
	for _, f := range targets {
		if strings.HasSuffix(f, "/") || f == "src" || f == "tests" {
			continue
		}
		needed++
		if !plan.FileExists(root, f) {
			continue
		}
		info, err := os.Stat(filepath.Join(root, f))
		if err != nil || info.Size() == 0 {
			continue
		}
		present++
		if baseline == nil {
			// No snapshot to compare against — cannot prove staleness.
			fresh++
			continue
		}
		if prev, tracked := baseline[f]; !tracked || prev == "" {
			// Absent (or untracked) at wave start and present now: this wave
			// created it.
			fresh++
		}
	}
	if needed == 0 || present < needed || fresh == 0 {
		return false
	}
	// Placeholder stubs must never count as already satisfied.
	return len(quality.CheckStaticQuality(root, t)) == 0
}

// isCreateTask reports whether a task's text describes scaffolding.
func isCreateTask(blob string) bool {
	for _, k := range []string{"create", "scaffold", "initialize", "greenfield"} {
		if strings.Contains(blob, k) {
			return true
		}
	}
	return false
}

// alreadySatisfiedRetry is the ONLY predicate allowed to skip the worker LLM.
// It requires a previous attempt: skipping a task's very first execution
// because a file happens to exist is how "Create a helper in pkg/util/cfg.go"
// ended up never running at all.
func (r *Runner) alreadySatisfiedRetry(t plan.Task, baseline map[string]string) bool {
	return t.Retries > 0 && alreadySatisfied(r.Root, t, baseline)
}

// renameOK is true when disk state matches a detected rename (symbol or file).
func renameOK(root string, t plan.Task) bool {
	spec := plan.DetectRenameIntent(t.Title, StripScopedPack(t.Description), t.Acceptance, strings.Join(t.Files, " "))
	if spec.Kind == plan.RenameNone {
		return false
	}
	focus := t.Files
	if len(focus) == 0 {
		focus = expandTaskFocus(t)
	}
	return plan.RenameSatisfied(root, spec, focus)
}

// snapIndexed builds a snapshot map indexed by task ID from the parallel arrays.
func snapIndexed(snapshots []map[string]string, tasks []plan.Task, indices []int) map[string]map[string]string {
	m := make(map[string]map[string]string, len(tasks))
	for j, t := range tasks {
		i := indices[j]
		if i < len(snapshots) {
			m[t.ID] = snapshots[i]
		}
	}
	return m
}

// reviewGroup is a set of tasks that share focus files and must therefore be
// reviewed sequentially with respect to one another.
type reviewGroup struct {
	tasks []plan.Task
	files map[string]bool
}

// groupReviewTasks partitions tasks so that any two tasks sharing a focus file
// land in the same group.
func groupReviewTasks(tasks []plan.Task) []*reviewGroup {
	var groups []*reviewGroup
	for _, t := range tasks {
		assigned := false
		for _, g := range groups {
			for _, f := range t.Files {
				if g.files[f] {
					g.tasks = append(g.tasks, t)
					for _, f2 := range t.Files {
						g.files[f2] = true
					}
					assigned = true
					break
				}
			}
			if assigned {
				break
			}
		}
		if !assigned {
			fm := make(map[string]bool, len(t.Files))
			for _, f := range t.Files {
				fm[f] = true
			}
			groups = append(groups, &reviewGroup{tasks: []plan.Task{t}, files: fm})
		}
	}
	return groups
}

// groupReviewOutcome is what one parallel group produces. Goroutines return
// values; only the parent writes to the board.
type groupReviewOutcome struct {
	tasks      []plan.Task
	escalated  []escalation
	escalIndex []int // index into tasks for each escalation
}

// reviewWave runs review+correct for every task that finished in the wave.
// Groups run in parallel; tasks within a group run sequentially.
//
// No goroutine touches *plan.Board. The previous version handed the shared
// board to N goroutines, which then called board.Get / board.UpdateTask (8
// sites inside reviewAndCorrect) and persist(*board) with no synchronization —
// an unsynchronized concurrent read/append of b.Tasks, plus a whole-board
// last-writer-wins write that silently dropped other groups' results. It never
// showed under -race only because no test drove ReviewParallel=true with two or
// more groups.
func (r *Runner) reviewWave(ctx context.Context, board *plan.Board, tasks []plan.Task,
	snapshots map[string]map[string]string) {
	if len(tasks) == 0 {
		return
	}
	if len(tasks) == 1 || r.MaxParallel < 2 {
		for _, t := range tasks {
			if err := r.reviewAndCorrect(ctx, board, t, snapshots[t.ID]); err != nil {
				r.logf("review/correct %s: %v", t.ID, err)
			}
		}
		return
	}

	groups := groupReviewTasks(tasks)
	outcomes := make([]groupReviewOutcome, len(groups))
	sem := make(chan struct{}, r.MaxParallel)
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, g *reviewGroup) {
			defer wg.Done()
			defer func() { <-sem }()
			out := groupReviewOutcome{}
			for _, t := range g.tasks {
				r.logf("%s review (parallel group, %d files)", t.ID, len(g.files))
				final, esc, err := r.reviewAndCorrectTask(ctx, t, snapshots[t.ID])
				if err != nil {
					r.logf("review/correct %s: %v", t.ID, err)
				}
				out.tasks = append(out.tasks, final)
				if esc != nil {
					out.escalated = append(out.escalated, *esc)
					out.escalIndex = append(out.escalIndex, len(out.tasks)-1)
				}
			}
			outcomes[i] = out
		}(i, g)
	}
	wg.Wait()

	// Single-threaded apply: board writes, persistence and HITL all happen here.
	for _, out := range outcomes {
		for _, t := range out.tasks {
			board.UpdateTask(t)
		}
	}
	r.persist(board)
	for _, out := range outcomes {
		for k, esc := range out.escalated {
			t := out.tasks[out.escalIndex[k]]
			if r.OnEscalate != nil {
				r.OnEscalate(ctx, board, t, esc.detail)
				if updated, ok := board.Get(t.ID); ok {
					board.UpdateTask(updated)
					t = updated
				}
			}
			r.reportTaskFailure(board, t, errMaxRetries, esc.attempt, "review", true)
		}
	}
	r.persist(board)
}

package loop

import (
	"context"
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

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/repair"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/squads"
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

// BetweenWaves is consulted after a wave's results are persisted and BEFORE the
// next wave is picked. A true return stops the board where it stands; RunBoard
// then finishes normally (nil error) and the reason is what the caller reports.
//
// It exists because the objective gate lives in the orchestrator while the wave
// cycle lives here — the same split AfterWave already bridges. Asking the gate
// only once the board has DRAINED is what made the check useless on the run
// that motivated it: one task kept getting rejected, so "no ready agent work
// remains" was never true, and the harness ground corrective rounds for ~15
// minutes after the fixture's test suite had already gone green. The waste
// happens WHILE the board is still churning, so the question has to be asked
// there.
//
// The hook decides nothing about cost or safety: every refusal (weak gate,
// escalated tasks, an outstanding tester rejection, an operator-configured test
// slot, the probe budget) belongs to the gate on the other side of the seam.
type BetweenWaves func(ctx context.Context, board *plan.Board) (stop bool, reason string)

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
	Executor SubAgentRunner
	Shared   *ggagent.SharedState
	Store    *plan.LiveStore // optional — reload mid-run for human edits
	Root     string          // workspace root for evidence checks
	SlmDir   string          // optional; defaults to Root/.slmcode
	TurnID   string          // query turn id for react checkpoints
	Focus    *workspace.FocusGuard
	// Squads is the virtual-team plan for this run, nil on a single-stream run
	// (which is almost all of them). When set, it adds a per-task brief to the
	// worker prompt and an ownership deny list to every wave.
	Squads *squads.Plan
	// RoleExists reports whether an agent id is registered. Set it to
	// agents.Factory.HasRole. nil means "no registry": reassignment then
	// declines rather than naming an agent that cannot be dispatched.
	RoleExists func(id string) bool
	// RosterIDs are the agent ids offered to the project manager when it
	// triages a rejected delivery. Filtered through RoleExists before use.
	RosterIDs []string
	// Triage asks the project manager who should take a rejected delivery next
	// and what they need to know. nil falls back to the deterministic ladder —
	// which is also where an unusable answer lands.
	Triage func(context.Context, TriageRequest) (plan.TriageDecision, bool)
	// TakeShellScope drains the workspace's out-of-scope ws_shell ledger — set
	// it to Workspace.TakeShellScopeEvents. nil simply means the loop reports no
	// shell-scope evidence; every gate that reads it is nil-safe.
	//
	// It is a func rather than a *Workspace on purpose: this is the ONE thing
	// the loop needs from the tool layer at gate time, and handing the runner a
	// whole Workspace would put every file-writing method one dot away from
	// code whose job is to judge writes, not make them.
	TakeShellScope func() []workspace.ShellScopeEvent
	AfterWave      AfterWave
	// BetweenWaves is the early-stop seam described above; nil disables it.
	BetweenWaves BetweenWaves
	// OnProtect fires when a wave installs its protected paths, BEFORE any
	// agent in that wave runs — the only moment those files are known to be
	// untouched. The orchestrator uses it to snapshot them, so a later
	// violation can be undone rather than merely reported. See
	// pkg/workspace/selfheal.go.
	OnProtect func(patterns []string)
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
	// MaxTaskAttempts caps how many times ONE task may be dispatched to a wave
	// before the board parks it in the human backlog; 0 =>
	// DefaultMaxTaskAttempts. This is the board-level companion to
	// MaxTaskCalls, which only bounds a SINGLE attempt: startTask resets the
	// call budget every dispatch, so without this a task reopened by the
	// escalate gate got a fresh 10-call budget forever.
	MaxTaskAttempts int
	// MaxStallRounds is how many consecutive rounds may complete with nothing
	// changing — no column move, no new output, no file changed on disk —
	// before RunBoard gives up; 0 => DefaultMaxStallRounds.
	MaxStallRounds int
	// MaxWaves caps post-test/QA corrective RunBoard re-entry waves.
	// Zero means unlimited legacy behavior.
	MaxWaves       int
	correctiveRuns int
	MaxParallel    int
	// BootstrapDeps is the dependency-install policy for acceptance smoke:
	// off | ask | auto. Empty means "ask", i.e. NOTHING is installed
	// unattended — the acceptance path used to run `pip install -r
	// requirements.txt` / `npm install` against a manifest the worker may have
	// written moments earlier in the same run.
	BootstrapDeps  string
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
	// EscalationRungs is how many model rungs are registered above the base
	// agents. Zero disables failure escalation entirely. See escalate.go.
	EscalationRungs int
	// EscalateAfter is how many recorded failures a task takes before stepping
	// up a rung. Zero uses DefaultEscalateAfter.
	EscalateAfter int
	// HasRole reports whether an agent id is registered. Escalation consults it
	// before every dispatch: an unregistered id is a hard task failure, and it
	// would land on exactly the tasks that were already struggling.
	HasRole func(string) bool
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
	// FrontendAssembler is the component-library assembler this run chose, or
	// "" to write React components by hand. Decided once per run by
	// agents.ChooseFrontend from the request and the workspace, and applied by
	// assembleFrontend to react tasks only.
	FrontendAssembler string
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

	// attemptDB is the PERSISTED companion to `attempts`: one record per pass
	// at a task, carrying its hypothesis, diffstat, gate signals and reviewer
	// verdict, and pointing at the pass it grew out of. attemptGraphDB is the
	// edge index those records are mirrored into. Both are opened once per
	// runner because their indexes are whole-file caches — two stores over one
	// directory would each flush a view missing the other's writes. See
	// attempts.go.
	attemptOnce      sync.Once
	attemptDB        *plan.Attempts
	attemptGraphOnce sync.Once
	attemptGraphDB   *graph.Store

	// waveAttempts counts how many times each task has been dispatched to a
	// wave — the board-level attempt ceiling. See progress.go.
	waveAttempts attemptTracker

	// evo accumulates evolve failure events + bandit decisions for the
	// orchestrator to drain at the end of a run.
	evo evolveState

	// edits is the run's edit-format ledger (see editstats.go). It is what
	// EditStats reports and what the edit-format bandit arm is rewarded on.
	edits editStats

	// editFmtOnce memoizes the edit-format arm. chooseEditFormat is called
	// once per prompt render, and it used to pull the bandit — and append a
	// DecisionRecord — every single time, so one run credited the arm with as
	// many pulls as it built prompts.
	editFmtOnce sync.Once
	editFmt     string

	// fpMu/fpCache cache per-wave content fingerprints (sha256 of file bytes).
	fpMu    sync.Mutex
	fpCache map[string]string

	// scopeMu/scopeLog hold the out-of-scope ws_shell writes drained from the
	// workspace ledger, filed per task. See shellscope.go.
	scopeMu  sync.Mutex
	scopeLog map[string]*taskScopeLog

	// betweenWavesOff suppresses the BetweenWaves early stop for the duration
	// of a corrective board — see RunCorrectiveBoard.
	betweenWavesOff bool

	// runReserve is the slice of wall-clock held back for the FINISH path — the
	// QA gate, the board write, the summary. Computed once per run from the
	// original runway and then held FIXED, which is the whole point: a reserve
	// recomputed as a fraction of what is left is geometric and reserves
	// nothing. Measured: with a 6s runway and a per-call 4/5 clamp, the calls
	// came out at 4.8s, 0.96s, 0.19s … and summed straight back to 6s. Every
	// call was individually affordable and together they ate the entire run.
	runReserveMu sync.Mutex
	runReserve   time.Duration
	runReserveOK bool

	// earlyStop* record a BetweenWaves stop: whether it happened, why, and how
	// many planned tasks were never executed because of it. A stop is a
	// deliberate abandonment of work, and the caller must be able to say so
	// rather than report a board that merely looks finished.
	earlyStopMu     sync.Mutex
	earlyStopped    bool
	earlyStopReason string
	earlyStopLeft   int

	// repairMu/pendingRepairs remember which rule produced a given set of
	// repaired tool arguments, so ToolRetryOutcome can credit or blame it
	// without the tool layer having to carry an opaque token. Keyed on the
	// repaired-argument JSON ReportToolFailure handed back, and consumed on
	// first lookup so the map cannot grow across a long run.
	repairMu       sync.Mutex
	pendingRepairs map[string]pendingRepair
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
//
// Three bounds, in the order they fire:
//
//  1. the per-task attempt ceiling (MaxTaskAttempts) — a task that has been
//     dispatched too many times is parked in the human backlog, so the rest of
//     the board can still finish;
//  2. the stall detector (MaxStallRounds) — consecutive rounds in which no
//     task changed column, no output changed and no file changed on disk;
//  3. the derived round guard, a backstop that should never be the thing that
//     fires. It used to be the ONLY bound, at a fixed 200 rounds, which on a
//     one-task board whose gate kept answering "retry" cost ~2,000 model calls
//     before it said anything.
func (r *Runner) RunBoard(ctx context.Context, board *plan.Board) error {
	r.noteRunway(ctx)
	guard := 0
	idleRounds := 0
	stallRounds := 0
	lastSig := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		guard++
		if guard > r.roundGuard(board) {
			return r.giveUp(board, ErrSafetyGuard, guard)
		}

		// Pick up CLI/UI edits
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			snap := r.Store.Snapshot()
			*board = snap
		}

		// Out of runway: stop scheduling agent work so the finish path — the QA
		// gate, the board write, the summary — still has time to run. Clamping
		// each call is not enough on its own; dispatch has to stop. Reported,
		// never silent: whatever is left on the board is real abandoned work.
		if r.runwaySpent(ctx) {
			return r.stopOutOfRunway(board)
		}

		ready := scheduleReady(board.ReadyTasks())
		if len(ready) == 0 {
			if board.AgentWorkRemaining() {
				// Nothing is executable, yet the board says work is in
				// progress. runWave is synchronous, so nothing actually is:
				// whatever sits in in_progress/in_review was abandoned by a
				// wave that already returned, and no code path re-dispatches
				// those columns. Re-queue them and schedule again rather than
				// idling 31 rounds waiting on an agent that exited.
				if r.reclaimOrphaned(board) > 0 {
					continue
				}
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

		// MaxParallel <= 0 must mean "one at a time", never "no tasks". A
		// zero-valued Runner (struct literal, embedding caller, a config path
		// that skipped normalization) sliced the wave to wave[:0], executed
		// nothing, moved nothing off ready_to_dev, and span for 200 guard
		// rounds before failing with ErrSafetyGuard — a board that can never
		// finish for a reason nothing in the log names.
		maxP := r.MaxParallel
		if maxP < 1 {
			maxP = 1
		}
		// Fill the wave with tasks that do NOT share files, rather than taking
		// the first maxP off the list: workers in one wave run concurrently
		// against one working tree, so two tasks on the same file are two
		// workers overwriting each other. Whatever is skipped here keeps its
		// place at the head of the next wave.
		wave := r.admitDisjoint(ready, maxP)
		// Park anything that has spent its board-level attempt ceiling BEFORE
		// spending another worker call on it.
		if wave = r.admitWave(board, wave); len(wave) == 0 {
			continue
		}
		before := r.progressSignature(board)
		r.logf("wave: %d ready task(s)", len(wave))
		ids := make([]string, len(wave))
		for i, t := range wave {
			ids[i] = t.ID
		}
		if err := r.runWave(ctx, board, wave); err != nil {
			return err
		}
		r.persist(board)
		// A task that reached done has made progress: forget its attempts so a
		// later reopen (tester, QA gate, human) starts from a full ceiling.
		for _, id := range ids {
			if t, ok := board.Get(id); ok && t.Column == plan.ColDone {
				r.waveAttempts.clear(id)
			}
		}
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

		// The between-waves early stop. The board is at rest exactly here: this
		// wave's results are written and the next wave has not been picked, so
		// this is the last moment at which "the objective is already met" can
		// still save the waves nobody has spent yet. Waiting for the board to
		// drain cannot: a task that keeps getting rejected keeps the board
		// moving forever, which is the run this hook exists for.
		if r.BetweenWaves != nil && !r.betweenWavesOff {
			if stop, reason := r.BetweenWaves(ctx, board); stop {
				return r.stopBetweenWaves(board, reason)
			}
		}

		// A round that changed nothing cannot become a round that changes
		// something by being repeated. The signature is compared against the
		// state this round started from AND against the previous round's, so a
		// board oscillating between two identical states counts as stalled too.
		after := r.progressSignature(board)
		if after == before || after == lastSig {
			stallRounds++
			r.logf("no progress in round %d (%d consecutive stall round(s))", guard, stallRounds)
			if stallRounds >= r.maxStallRounds() {
				return r.noteStall(board, guard)
			}
		} else {
			stallRounds = 0
		}
		lastSig = before
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
	// A corrective board is entered BECAUSE something was found wanting — a
	// tester rejection, placeholder gaps, a red QA gate, a human answering
	// "continue". Stopping such a board between waves on a green objective
	// would route around the very finding that scheduled it, so the early stop
	// is off for its duration. The orchestrator's post-drain probes still cover
	// this ground, and they carry the tester verdict explicitly.
	prev := r.betweenWavesOff
	r.betweenWavesOff = true
	defer func() { r.betweenWavesOff = prev }()
	return true, r.RunBoard(ctx, board)
}

// noteRunway fixes this run's finish-path reserve, once.
//
// It is computed from the ORIGINAL runway and then never recomputed. A reserve
// taken as a fraction of the REMAINING time is geometric and therefore reserves
// nothing: measured on a 6s runway, successive 4/5 clamps handed out 4.8s,
// 0.96s, 0.19s … which sum back to the whole 6s. Every call looked affordable
// and together they consumed the run, which is the bug this was supposed to fix.
//
// RunBoard is called more than once per run (every corrective board), so the
// first call wins and later ones leave it alone.
func (r *Runner) noteRunway(ctx context.Context) {
	if r == nil || ctx == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	r.runReserveMu.Lock()
	defer r.runReserveMu.Unlock()
	if r.runReserveOK {
		return
	}
	total := time.Until(deadline)
	if total <= 0 {
		return
	}
	r.runReserve = total / loopFinishReserveDivisor
	r.runReserveOK = true
}

// finishReserve is the wall-clock held back for the finish path, or 0 when the
// run has no deadline to reason about.
func (r *Runner) finishReserve() time.Duration {
	if r == nil {
		return 0
	}
	r.runReserveMu.Lock()
	defer r.runReserveMu.Unlock()
	return r.runReserve
}

// runwaySpent reports that the run has less than its finish reserve left, so no
// further agent work may be scheduled.
//
// This is the half that actually protects the reserve. Clamping each call is
// not enough on its own — see noteRunway. Dispatch has to STOP.
func (r *Runner) runwaySpent(ctx context.Context) bool {
	if r == nil || ctx == nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return false
	}
	reserve := r.finishReserve()
	if reserve <= 0 {
		return false
	}
	return time.Until(deadline) <= reserve
}

// callTimeout is r.Timeout clamped so a call cannot eat the finish reserve.
//
// MEASURED. Every fix-a-bug ceiling miss across v0.18.1, v0.18.2 and the
// harvest build is the same event: a worker burns its full task timeout
// producing nothing, and the harness then starts ANOTHER full-length attempt
// with less wall-clock than that remaining. It cannot finish. The deadline
// arrives mid-call and takes the finish path with it — no QA gate, no board
// write, no summary — in runs that had already left a correct tree on disk.
//
// The orchestrator's own role calls learned this first (roleTimeoutWithin), and
// it made no measurable difference, because the calls that burn the budget are
// these: the loop dispatches workers, reviewers and correctors on a FLAT
// r.Timeout that never looked at the clock.
//
// This cannot make a stalled model productive. It stops the harness betting
// time it does not have, which is the part that belongs to the harness.
func (r *Runner) callTimeout(ctx context.Context) time.Duration {
	if r == nil {
		return 0
	}
	budget := r.Timeout
	if ctx == nil {
		return budget
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return budget
	}
	runway := time.Until(deadline)
	if runway <= 0 {
		// Already past it: the call fails instantly either way, and a negative
		// budget is worse than the real one.
		return budget
	}
	usable := runway - r.finishReserve()
	if usable <= 0 {
		// runwaySpent should have stopped dispatch before here; if something
		// still calls out, make it fail fast rather than eat the reserve.
		return minCallTimeout
	}
	if usable < budget {
		return usable
	}
	return budget
}

// loopFinishReserveDivisor keeps 1/N of the run's ORIGINAL runway for the
// finish path, so a run that runs out of time reports what it did.
const loopFinishReserveDivisor = 5

// minCallTimeout is the floor for a dispatch that slipped past runwaySpent.
const minCallTimeout = 250 * time.Millisecond

// stopOutOfRunway ends the board because the run has only its finish reserve
// left. It reuses the between-waves early-stop bookkeeping, so the abandoned
// task count reaches Result.UnexecutedTasks and the summary exactly as a
// green-objective stop does — a run that ran out of time must say so, not look
// like one that finished.
func (r *Runner) stopOutOfRunway(board *plan.Board) error {
	return r.stopBetweenWaves(board,
		"out of time — stopped scheduling new agent work so the run could finish and report")
}

// stopBetweenWaves ends the board because BetweenWaves said the run is already
// done, and records what that abandoned.
//
// The tasks counted here are real work that was planned and will not run. The
// count is taken at THIS moment because this is the decision point: everything
// downstream (the green-gate board promotion, a human promoting a column, a
// later resume) reshapes the board, and afterwards nothing distinguishes "never
// ran" from "ran and finished". The caller turns this into the number and the
// sentence a run reports.
func (r *Runner) stopBetweenWaves(board *plan.Board, reason string) error {
	left := unexecutedTaskCount(board)
	waves := wavesFor(left, r.parallelism())
	r.earlyStopMu.Lock()
	r.earlyStopped = true
	r.earlyStopReason = reason
	r.earlyStopLeft = left
	r.earlyStopMu.Unlock()

	msg := fmt.Sprintf("%s — stopping after wave %d: %d task(s) not executed, "+
		"at least %d further wave(s) skipped", reason, r.waveN, left, waves)
	r.logf("RunBoard: %s", msg)
	r.fireLevel(stream.KindLoop, "harness", "", msg, "objective_met", "", stream.LevelSuccess)
	return nil
}

// EarlyStop reports a BetweenWaves stop: whether it happened, the reason given,
// and how many planned tasks were never executed because of it.
func (r *Runner) EarlyStop() (bool, string, int) {
	if r == nil {
		return false, "", 0
	}
	r.earlyStopMu.Lock()
	defer r.earlyStopMu.Unlock()
	return r.earlyStopped, r.earlyStopReason, r.earlyStopLeft
}

// Waves reports how many waves this runner has dispatched, so a caller (or a
// test) can assert on waves rather than on prose.
func (r *Runner) Waves() int {
	if r == nil {
		return 0
	}
	return r.waveN
}

// parallelism is MaxParallel normalized the way RunBoard normalizes it.
func (r *Runner) parallelism() int {
	if r == nil || r.MaxParallel < 1 {
		return 1
	}
	return r.MaxParallel
}

// wavesFor is the FLOOR on how many more waves n tasks would have cost: one per
// full batch, and none of the review retries each of them could still trigger.
func wavesFor(n, perWave int) int {
	if n <= 0 {
		return 0
	}
	if perWave < 1 {
		perWave = 1
	}
	return (n + perWave - 1) / perWave
}

// unexecutedTaskCount counts the tasks agents still owned when the board
// stopped: planned, never executed, abandoned.
func unexecutedTaskCount(board *plan.Board) int {
	if board == nil {
		return 0
	}
	n := 0
	for _, t := range board.AllTasks() {
		t.Normalize()
		switch t.Column {
		case plan.ColReadyToDev, plan.ColInProgress, plan.ColInReview:
			n++
		}
	}
	return n
}

// CorrectiveRuns reports how many corrective waves this runner has spent
// against MaxWaves, so a caller that decides to stop early can say how much
// budget it left on the table instead of asserting a number nobody can check.
func (r *Runner) CorrectiveRuns() int {
	if r == nil {
		return 0
	}
	return r.correctiveRuns
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
		// The fallback now restates the same worker rules the gates enforce,
		// including the project's language-appropriate smoke command.
		prompt = fallbackTaskPromptWithLang(t, detectProjectLangHint(r.rootDir()))
	}
	if led := r.attemptLogSection(t); led != "" {
		prompt += led
	}
	if squad := r.squadBriefSection(t); squad != "" {
		prompt += squad
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

// attemptLogSection renders the cross-attempt "do not repeat this" ledger a
// gate retry carries forward.
//
// Without it, answering "retry" at the escalate gate sent the worker back with
// a byte-identical prompt: same task text, same scope, same acceptance, and no
// record that this exact attempt had already been made and rejected. A small
// model given the same prompt returns the same answer, which is why the gate
// loop never converged.
//
// It has two halves. The first is the PERSISTED lineage: the approaches already
// tried at this task and the reason each one was refused, deduplicated by
// reason and bounded in bytes (see attempts.go). The second is the original
// plan.Task.AttemptLog prose, which the escalate gate still writes and reads —
// this is additive, not a replacement. The structured half comes first because
// it is the half that names an approach; six truncated strings only ever named
// a number.
func (r *Runner) attemptLogSection(t plan.Task) string {
	var b strings.Builder
	b.WriteString(r.rejectedApproachSection(t))
	b.WriteString(attemptLogProse(t))
	return b.String()
}

// attemptLogProse renders plan.Task.AttemptLog, the prose ledger carried across
// gate retries.
func attemptLogProse(t plan.Task) string {
	if len(t.AttemptLog) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Previous attempts at THIS task failed — do not repeat them\n")
	for _, line := range t.AttemptLog {
		if s := strings.TrimSpace(line); s != "" {
			b.WriteString("- " + s + "\n")
		}
	}
	b.WriteString("This task has been reopened " + fmt.Sprintf("%d", t.GateRetries) +
		" time(s) after failing review. Repeating the previous approach will fail again: " +
		"re-read the target files first, make a smaller and more precise change, " +
		"and prove it with a tool call rather than a claim.\n")
	return b.String()
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

// adaptiveGuidance turns the stored lessons into prompt lines.
//
// The canned advice below is ENRICHMENT: for the handful of failure classes the
// harness understands natively it adds a concrete instruction the raw lesson
// text does not spell out. It is no longer a gate. Selecting *which* lessons
// survive is done upstream by learning.RecentAdaptiveMemoryFor, which ranks
// them against the actual task with the BM25F scorer in pkg/memory; every
// lesson that reaches this function is emitted, whatever words it happens to
// contain.
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

// recentLessonLines emits the most recent stored lessons verbatim.
//
// It used to drop any line that did not contain one of eight hardcoded
// substrings (timeout, deadline, smoke, qa_gate, acceptance, placeholder, stub,
// max retries) — the tail of the same allowlist that used to gate
// learning.RecentAdaptiveMemory. A lesson about an import cycle, a flaky
// fixture or a naming convention was learned, written to disk, and then
// silently discarded on its way to the only place it could ever be useful.
// Relevance is now decided upstream, by ranking lessons against the actual
// task; here every line survives and only the count is bounded.
func recentLessonLines(raw string) []string {
	fields := strings.Split(raw, "\n")
	var picked []string
	for i := len(fields) - 1; i >= 0 && len(picked) < 6; i-- {
		line := strings.TrimSpace(fields[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if norm := normalizeLessonLine(line); norm != "" {
			picked = append(picked, norm)
		}
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked
}

func normalizeLessonLine(line string) string {
	line = stripLessonProvenance(strings.TrimSpace(line))
	line = strings.TrimSpace(strings.TrimLeft(line, "-•⚠⚙✓ "))
	if line == "" {
		return ""
	}
	return "- Learned: " + line
}

// stripLessonProvenance removes the trailing `<!-- slm … -->` bookkeeping that
// MEMORY.md bullets carry so a lesson can be parsed back into a typed fact.
// A model must never see it. Belt-and-braces: the orchestrator already strips
// it before publishing lessons to shared state, but shared state has other
// writers and a stray HTML comment in a prompt is pure noise for a 7B model.
func stripLessonProvenance(line string) string {
	i := strings.LastIndex(line, "<!--")
	if i < 0 {
		return line
	}
	rest := line[i:]
	if !strings.HasSuffix(strings.TrimSpace(rest), "-->") {
		return line
	}
	return strings.TrimRight(line[:i], " \t")
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
		// …and enforce what the tasks themselves said was off limits. SetWave
		// builds the anti-wander ALLOWLIST; this builds the DENY list, which
		// outranks it and which Clear deliberately does not touch — hence its
		// own undo. See protect.go for why the derivation is as narrow as it is.
		unprotect := r.applyWaveProtections(wave)
		defer unprotect()
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
	// A cancellation is only the RUN's cancellation when the run's context says
	// so. Sub-agent cancellations the harness caused itself — a speculative
	// racer cut short by its winner, a slot that hit its own per-slot timeout —
	// used to be reported here as context.Canceled regardless, which the
	// orchestrator's interrupt checkpoint then classified as a user interrupt:
	// the run aborted with "interrupted at execute" and exit 130 with nobody
	// having pressed anything. The affected task is already parked in blocked
	// by handleTaskCancel; the board, not the wave, decides what happens next.
	if canceled {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.logf("wave %d: sub-agent cancellation with a live run context — not a user interrupt", r.waveN)
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
		role := r.execAgentFor(t)
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
			AgentID:    r.execAgentFor(t),
			Input:      r.taskInputFor(board, t),
			Timeout:    r.callTimeout(ctx),
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
	results, err := r.dispatchWave(ctx, reqs)
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

// dispatchWave runs the wave's requests with a PER-TASK tool context.
//
// workspace.WithTaskID keys the tool layer's loop guard, and a batched call has
// exactly one ctx for N tasks — so every task in a batched wave used to share
// the "" bucket and trip its neighbours' loop detection (two workers
// legitimately reading go.mod hard-stopping each other). The task id cannot be
// pushed down from inside the batch either: GoLangGraph's SubAgentExecutor
// hands the SAME ctx to every subagent goroutine and offers no per-request
// hook, so a batched call can only ever carry one id.
//
// The fix is to stop batching. Each request is dispatched on its own, under a
// ctx tagged with its own SubAgentRequest.TaskID, and the calls run
// concurrently here instead of inside the executor. That is behavior-neutral
// for GoLangGraph's executor — its parallel path is one goroutine per request
// calling executeSingle, which is exactly what this does — and the wave is
// already capped at MaxParallel, so the concurrency is unchanged.
//
// results[j] always corresponds to reqs[j]. The returned error is the first
// non-nil one, matching the batched executor's contract.
func (r *Runner) dispatchWave(ctx context.Context, reqs []ggagent.SubAgentRequest) (
	[]ggagent.SubAgentResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if len(reqs) == 1 {
		defer r.streamTokens(reqs[0].AgentID, reqs[0].TaskID)()
		return r.Executor.ExecuteSubAgents(
			r.agentCtx(ctx, reqs[0].TaskID, reqs[0].AgentID), reqs, r.Shared)
	}

	results := make([]ggagent.SubAgentResult, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := reqs[i]
			// One sink per (agent, task) for the life of this call: that pair is
			// exactly what the terminal needs to keep four concurrent workers'
			// deltas apart.
			defer r.streamTokens(req.AgentID, req.TaskID)()
			out, err := r.Executor.ExecuteSubAgents(
				r.agentCtx(ctx, req.TaskID, req.AgentID), []ggagent.SubAgentRequest{req}, r.Shared)
			errs[i] = err
			if len(out) > 0 {
				results[i] = out[0]
				return
			}
			results[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: err}
		}(i)
	}
	wg.Wait()

	var first error
	for _, err := range errs {
		if err != nil {
			first = err
			break
		}
	}
	return results, first
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
		// Per-RESULT, not per-wave: execErr is the first error across the whole
		// dispatch, so folding it into every iteration marked healthy siblings
		// canceled too. It still counts when this wave dispatched one request,
		// where "the first error" and "this task's error" are the same thing.
		if isCancelResult(res.Error, res) || (len(results) == 1 && IsContextCancelErr(execErr)) {
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
			if r.outputWeak(*t, ws.snapshots[i], incomplete) {
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
	role = baseRole(role)
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
				AgentID: role, Input: r.taskInputFor(board, *t), Timeout: r.callTimeout(ctx),
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
			// ctx.Err() is the only authority on whether the RUN was canceled.
			// A reviewer slot that died on its own per-slot timeout, or a
			// speculative loser, reports a cancellation too — and reporting it
			// here as the run's cancellation is what produced the phantom
			// "interrupted at execute".
			if ctx.Err() != nil && isCancelResult(err, ggagent.SubAgentResult{}) {
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

// noteUsage is the single choke point every agent result passes through, which
// makes it the right place to fold the transcript into the edit ledger too —
// wave results, sequential round-trips and the speculative race all land here.
func (r *Runner) noteUsage(res ggagent.SubAgentResult, input, output string) {
	r.noteEdits(res.Messages)
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
	// criteriaFail is a REPRODUCED failure of a must-criterion: a whitelisted
	// command ran in this repo and did not exit 0. It is a hard gate.
	criteriaFail bool
	// criteriaOpen means at least one stated criterion was never checked —
	// no verify command, or one the whitelist refused. It is NOT a failure and
	// never blocks; it only denies the reviewer fast path, because an
	// unchecked condition is exactly the case a judgement call is for.
	criteriaOpen bool
}

// hasEvidence reports whether anything at all proves a write happened.
func (g gateState) hasEvidence() bool {
	return g.diskWrite || g.toolWrite || g.diskSection || g.renameDisk
}

// blocking reports whether a hard gate failed.
func (g gateState) blocking() bool {
	return g.shellFail || g.smokeFail || g.acceptFail || g.staticFail || g.claimsFail ||
		g.smokeMissing || g.criteriaFail
}

// fastPath reports whether the reviewer LLM can be skipped entirely.
func (g gateState) fastPath(role string) bool {
	if g.renameDisk {
		return true
	}
	// An open criterion denies the fast path even when every hard gate is
	// clean. Disk write evidence proves the worker CHANGED something; it says
	// nothing about whether the condition the task was given is now true. That
	// gap is the reviewer's job, and skipping it here is how "the harness did
	// not check" would quietly become "auto-approved".
	if g.criteriaOpen {
		return false
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
	case g.criteriaFail:
		return "rejected: acceptance criterion failed",
			"a must-criterion's verify command failed — make it exit 0 before claiming done"
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
	g.criteriaFail = quality.CriteriaBlockedInOutput(current.Output)
	g.criteriaOpen = quality.CriteriaUnverifiedInOutput(current.Output)

	// Review-time criteria insurance, mirroring the static and smoke ones
	// below: a corrector that overwrote the output, or a skipped-worker path,
	// never ran the criteria gate, so a task with a stated contract would
	// reach the reviewer with no section at all — indistinguishable from a
	// task that has no contract. Run it now.
	// The SAME role filter the worker path uses, and for a concrete reason: a
	// tester task's whole job is to run the project's suite, and its criteria
	// almost always name that same suite. Without this the harness would run it
	// once as the tester and again as review-time insurance — the exact
	// double-execution VerifyCriteria's command cache exists to prevent, just
	// spread across two calls where the cache cannot see it.
	if acceptanceSmokeRole(current.Role) && len(current.Criteria) > 0 && !hasCriteriaSection(current.Output) {
		rep := quality.VerifyCriteria(ctx, r.Root, *current, r.Timeout,
			quality.NormalizeBootstrapPolicy(r.BootstrapDeps))
		if sec := quality.FormatCriteriaSection(rep); sec != "" {
			current.Output = appendHarnessSection(current.Output, sec)
			g.criteriaFail = rep.Blocked()
			_, _, unverified, _ := rep.Counts()
			g.criteriaOpen = unverified > 0
			if g.criteriaFail {
				o, _ := rep.FirstBlocking()
				r.logf("%s review-time criteria FAILED: %s (%s)", current.ID, o.Criterion.ID, o.Command)
			}
		}
	}

	// Review-time static insurance: skipped-worker / already-satisfied paths
	// never ran CheckStaticQuality — catch Placeholder stubs before fast-path.
	if r.StaticQuality && !g.staticFail && !g.renameDisk {
		if issues := quality.CheckStaticQuality(r.Root, *current); len(issues) > 0 {
			current.Output = appendHarnessSection(current.Output, quality.FormatStaticSection(issues))
			g.staticFail = true
			r.logf("%s review-time static FAILED (%d issue(s))", current.ID, len(issues))
			r.fireIntervention(current.ID, "review",
				"stub/placeholder code blocked auto-approve — needs real implementation",
				quality.FormatStaticSection(issues))
		}
	}

	smokeFiles := append([]string{}, current.Files...)
	smokeFiles = append(smokeFiles, parseFilesChanged(current.Output)...)
	// smokeApplicable is the ONLY condition under which a missing smoke section
	// may block approval: the harness must actually be willing and able to run
	// a smoke for this task. HasSmokeCommand alone is not that test —
	// quality.ShouldSmokeTask excludes whole roles (docs, tester, explorer,
	// planner…) and non-code focus files, and RunPostWorkerSmoke additionally
	// declines any command whose language does not match the project's (a .py
	// file in a Go module, a .js file in a Go repo's web/ tree). In every one of
	// those cases FormatSmokeSection returns "", so SmokePassedInOutput is
	// false forever and the old gate rejected the task on EVERY retry until it
	// escalated to a human. A gate the task cannot possibly satisfy is not a
	// gate, it is a deadlock.
	smokeApplicable := r.RequireSmoke && r.PostWorkerSmoke && !g.renameDisk &&
		quality.ShouldSmokeTask(*current) && quality.HasSmokeCommand(r.Root, smokeFiles)
	// Review-time smoke insurance: if the worker path didn't attach a section
	// (corrector overwrite, truncated finalize), run it now so RequireSmoke
	// cannot false-reject a green compile/test.
	if smokeApplicable && !quality.SmokePassedInOutput(current.Output) && !g.smokeFail {
		sr := quality.RunPostWorkerSmoke(ctx, r.Root, *current, r.Timeout)
		if sec := quality.FormatSmokeSection(sr); sec != "" {
			current.Output = appendHarnessSection(current.Output, sec)
			if sr.Ran && !sr.OK {
				g.smokeFail = true
				r.logf("%s review-time smoke FAILED: %s", current.ID, sr.Command)
			} else if sr.Ran {
				r.logf("%s review-time smoke PASSED: %s", current.ID, sr.Command)
			}
		}
		if !sr.Ran {
			// The harness itself declined to run the smoke. Not evidence of a
			// missing smoke — evidence that there is no smoke to miss.
			smokeApplicable = false
			r.logf("%s smoke not applicable (%s) — not treating as a missing smoke",
				current.ID, sr.Summary)
		}
	}
	g.smokeMissing = smokeApplicable &&
		!quality.SmokePassedInOutput(current.Output) && !g.smokeFail
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

// reviewerStrictDelay gives the primary reviewer a head start before the
// strict second reviewer is even dispatched. A real LLM essentially never
// answers this fast, so production still fans out to both reviewers exactly
// as before; the delay only matters against a fast/local answer (disk
// evidence, a quick model, a test double), where it lets the race actually
// skip the second slot instead of firing it and discarding the result.
const reviewerStrictDelay = 20 * time.Millisecond

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
	// Budget honesty: the race costs ONE unit but issues one real LLM request
	// per non-local slot — reviewer plus reviewer-strict at the default
	// max_parallel=4. Against a server that runs inference serially a 10-unit
	// budget is therefore up to ~13 round-trips, and the operator waits on the
	// round-trips. spend() already counted one; record the rest so the budget
	// event reports both numbers instead of understating the wait.
	llmSlots := 0
	for _, sl := range slots {
		if sl.Local == nil {
			llmSlots++
		}
	}
	if llmSlots > 1 {
		r.noteExtraRequests(current.ID, llmSlots-1)
	}
	r.logf("%s speculative review (%d paths, %d LLM requests, 1 budget unit, max_parallel=%d)",
		current.ID, len(slots), llmSlots, r.MaxParallel)
	res := r.speculate(r.taskCtx(ctx, current.ID), slots)

	// Only a slot that RAN TO COMPLETION carries a verdict. A racer the winner
	// canceled comes back Skipped (see speculate), and a streaming one can
	// come back with a partial body: reading `{"approved":true,"score":92,` off
	// a cut-short stream and calling it a verdict is how the winner's complete
	// approval was rendered and acted on as `approved=false score=0`, costing a
	// correction round the model never needed.
	var acceptOut, revOut, strictOut string
	var revErr error
	for _, sr := range res {
		done := !sr.Skipped && sr.Err == nil && strings.TrimSpace(sr.Output) != ""
		switch sr.Role {
		case "acceptance":
			if done {
				acceptOut = sr.Output
			}
		case revRole:
			revErr = sr.Err
			if done {
				revOut = sr.Output
			}
		default:
			if done {
				strictOut = sr.Output
			}
		}
	}
	if strings.TrimSpace(acceptOut) != "" {
		r.logf("%s review acceptance won — canceled reviewer LLM", current.ID)
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
				// A genuine head start for the primary reviewer: a real LLM
				// almost never answers inside this window, so production
				// still races both reviewers as before. But it means the
				// strict slot's dispatch — and its real budget/wall-clock
				// cost — is actually skippable, not just cancelable after
				// the fact, whenever the primary reviewer is fast enough to
				// have already decided the race.
				Delay: reviewerStrictDelay,
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
		AgentID: revRole, Input: reviewIn, Timeout: r.callTimeout(ctx), ShareState: true, TaskID: current.ID,
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
		// NOT plan.ReviewResult{}. The zero value is byte-identical to a
		// considered `approved=false score=0` rejection carrying no issue and
		// no summary, so a reviewer that answered NOTHING was reported, logged
		// and acted on exactly like a reviewer that had judged the work and
		// found it wanting. Route it through the parser so it carries
		// ReviewMalformedIssue and NoVerdict like every other unreadable reply.
		return plan.ParseReviewJSON(raw)
	}
	fixed, rung, err := repair.RepairRole(raw, plan.RoleReviewer)
	switch {
	case err == nil:
		if rung != "" && rung != "clean" {
			r.logf("%s reviewer JSON repaired (%s)", current.ID, rung)
		}
		return bestReviewParse(raw, string(fixed))
	case errors.Is(err, repair.ErrTruncated):
		r.logf("%s reviewer output truncated mid-string — re-asking with a larger budget", current.ID)
		r.fireIntervention(current.ID, "truncated_output",
			"reviewer answer was cut off by max_tokens — re-asking, not correcting", rung)
		retry, ok := r.execOne(ctx, current.ID, "review retry (truncated)", ggagent.SubAgentRequest{
			AgentID: role,
			Input: prompt + "\n\nYour previous answer was CUT OFF mid-JSON. " +
				"Answer again with the SHORTEST valid JSON object: " +
				`{"approved":<bool>,"score":<int>,"summary":"<one short line>","issues":[]}`,
			Timeout: r.callTimeout(ctx), ShareState: true, TaskID: current.ID,
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

// bestReviewParse keeps the repair ladder from making the verdict WORSE.
//
// repair.RepairRole's "extract" rung carves the FIRST balanced JSON document
// out of the reply and throws the rest away. formatReviewPrompt hands the
// reviewer the worker's own `{"status":"done","files_changed":[…]}` under
// "## Agent output", and a small reviewer routinely restates it before judging
// — so "extract" returned the echoed worker JSON and DELETED the verdict that
// followed it. Parsing the repaired document then found no verdict at all, and
// the harness reported that as approved=false score=0: a rejection the reviewer
// never made, on work it had just approved.
//
// The repaired document still wins whenever it carries a verdict — that is what
// the ladder is for. It only loses to the raw reply when the repair is the
// thing that lost the verdict.
func bestReviewParse(raw, repaired string) plan.ReviewResult {
	fixed := plan.ParseReviewJSON(repaired)
	if !fixed.NoVerdict {
		return fixed
	}
	if direct := plan.ParseReviewJSON(raw); !direct.NoVerdict {
		return direct
	}
	return fixed
}

// slmApprovalFallback trusts clear worker completion + disk/tool evidence when
// a small reviewer model returns a broken or over-strict verdict.
func (r *Runner) slmApprovalFallback(review plan.ReviewResult, current plan.Task,
	g gateState, reviewRaw string) plan.ReviewResult {
	if review.Approved || plan.IsTesterRole(current.Role) || g.blocking() {
		return review
	}
	// review.NoVerdict is the PARSER's own report that it could not read a
	// verdict out of the reply. looksLikeBrokenReview re-derives that from the
	// raw text and gets it wrong in exactly the case that matters: it returns
	// false the moment the text contains `"approved"` — which is precisely what
	// a reviewer whose verdict the extraction just destroyed looks like. Ask
	// the parse, do not re-guess it.
	broken := review.NoVerdict || looksLikeBrokenReview(reviewRaw)
	if g.satisfied || g.diskWrite || g.diskSection ||
		(g.done && (broken || g.hasEvidence())) {
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

// ReviewNoVerdictSummary is the summary of a rejection the REVIEWER never made:
// its reply could not be read, no harness evidence rescued the task, and the
// gates named nothing specific. Callers may match on it to tell "the reviewer
// found problems" from "the reviewer never answered".
const ReviewNoVerdictSummary = "reviewer produced no verdict — judged on harness evidence"

// noVerdictIssue is what the CORRECTOR is told when the reviewer said nothing.
//
// It used to be handed plan.ReviewMalformedIssue verbatim — "reviewer returned
// malformed or truncated JSON — treated as a rejection" — as its "## Review
// issues" list, i.e. the worker was asked to fix the reviewer's JSON. That is
// not a defect the worker can act on, so every correction round re-emitted the
// same code and was rejected again until the run hit its ceiling.
const noVerdictIssue = "no write evidence for this task: the acceptance criterion is not " +
	"demonstrated on disk. Make a real ws_edit/ws_write/ws_patch on a focus file and prove " +
	"it by running the project's test command with ws_shell."

// resolveNoVerdict turns "the reviewer could not be read" into something the
// rest of the ladder can act on.
//
// A no-verdict review is not a judgement, so it must not be reported as one.
// When a hard gate fired, that gate is the real reason and it is stated. When
// nothing fired — and slmApprovalFallback already declined to approve on disk
// evidence — the honest reason is that nothing proved the work, which is an
// instruction the corrector can actually follow.
func resolveNoVerdict(review plan.ReviewResult, g gateState) plan.ReviewResult {
	if review.Approved || !review.NoVerdict {
		return review
	}
	if g.blocking() {
		summary, issue := g.rejectReason()
		review.Summary = summary
		review.Issues = []string{issue}
		return review
	}
	review.Summary = ReviewNoVerdictSummary
	review.Issues = []string{noVerdictIssue}
	return review
}

// reviewVerdictLine renders the review event.
//
// A no-verdict rejection is NOT reported as `approved=false score=0`: that line
// is a claim about what the reviewer decided, and reading it off a reply the
// reviewer never produced is how ~15 minutes of a live run were spent
// correcting an implementation whose tests were already green.
func reviewVerdictLine(review plan.ReviewResult) string {
	if !review.Approved && review.NoVerdict {
		return "review no verdict (reviewer reply unreadable) — judged on harness evidence"
	}
	return fmt.Sprintf("review approved=%v score=%d", review.Approved, review.Score)
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
	// Last: a rejection nobody made must not leave here dressed as one. This
	// runs after the gates so a gate that DID fire owns the verdict text.
	return resolveNoVerdict(review, g)
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
		return restoreTesterEvidence(plan.ParseTesterJSON(string(fixed)), t.Output), true
	}
	if errors.Is(err, repair.ErrTruncated) {
		r.logf("%s tester output truncated mid-string — not treating truncation as a test failure", t.ID)
		r.fireIntervention(t.ID, "truncated_output",
			"tester answer was cut off by max_tokens", rung)
		return plan.TesterResult{}, false
	}
	return plan.ParseTesterJSON(t.Output), true
}

// restoreTesterEvidence undoes the one way JSON repair can turn a real pass
// into a failure.
//
// ParseTesterJSON refuses passed:true unless an execution trace sits NEXT TO
// the JSON — the whole point being that a tester must not be able to claim a
// pass it never ran. But repair.RepairRole extracts the JSON object and
// discards everything around it, which is precisely that trace: a tester that
// ran ws_shell and echoed the observation had its evidence deleted by the
// repair and was then failed for having none.
//
// So when the ONLY complaint is the missing trace, look for it in the
// unrepaired output, harness-appended smoke section included. The bar is
// unchanged — the same harness-minted markers, just checked against the text
// that still carries them.
func restoreTesterEvidence(res plan.TesterResult, rawOutput string) plan.TesterResult {
	if res.Passed || len(res.Failures) != 1 || res.Failures[0] != plan.TesterNoEvidenceFailure {
		return res
	}
	if !plan.TesterHasShellEvidence(rawOutput) {
		return res
	}
	res.Passed = true
	res.Failures = nil
	return res
}

// reviewAndCorrect reviews a task and runs correction rounds against the board.
func (r *Runner) reviewAndCorrect(ctx context.Context, board *plan.Board, t plan.Task, baseline map[string]string) error {
	final, esc, err := r.reviewAndCorrectTask(ctx, t, baseline)

	// Before asking a human: hand the work to a different specialist ONCE,
	// with the failure ledger as its context. The agent that just failed has
	// already had every retry the ladder allows, so re-running it is the loop
	// that produced the escalation in the first place — and "needs human input
	// or smaller scope" is the least actionable thing the harness can say.
	if esc != nil && err == nil {
		if next, ok := r.reassignFailedTask(ctx, final, esc.review); ok {
			from := t.Role
			final = next
			board.UpdateTask(final)
			r.persist(board)
			r.announceHandoff(final, from)
			return nil
		}
	}

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

// escalation records that a task ran out of retries.
type escalation struct {
	detail  string
	attempt int
	// review is the verdict that ended the ladder. Carried so a reassignment
	// can hand the next specialist the actual findings rather than re-deriving
	// them from the task's prose.
	review plan.ReviewResult
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
	// Attempt lineage. Every pass through this ladder is one persisted record
	// pointing at the pass it grew out of, so the history survives the corrector
	// overwrite below — and survives the process.
	lin := r.newLineage(current)
	defer lin.flush()
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		startedAt := time.Now()
		// Pick up human edits from the live store.
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			if latest, ok := r.Store.GetTask(current.ID); ok {
				switch latest.Column {
				case plan.ColDone, plan.ColBlocked, plan.ColToScope, plan.ColScoped:
					return latest, nil, nil
				}
				current = mergeHumanEdits(current, latest)
			}
		}

		g := r.gatherGateSignals(ctx, &current, baseline)
		review, reviewRaw, err := r.decideReview(ctx, current, g, baseline)
		if err != nil {
			// The reviewer never produced a verdict. That is still an attempt
			// that happened, and the next one must be able to see it.
			//
			// NoVerdict is set for the same reason parseReviewOutput sets it on
			// an empty reply: without it this result is byte-identical to a
			// considered `approved=false score=0` rejection, and a reviewer that
			// TIMED OUT would be read as a reviewer that judged the work
			// worthless. The distinction has to survive as far as the ledger,
			// because that is what the next attempt reads.
			lin.record(current, g, plan.ReviewResult{
				Summary: err.Error(), Issues: []string{err.Error()}, NoVerdict: true,
			}, plan.AttemptError, startedAt)
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
			reviewVerdictLine(review), "", endOut, level)

		// Persist THIS attempt — its output, its diffstat, the gates that fired
		// and the verdict just reached — BEFORE anything overwrites
		// current.Output further down. That overwrite is what used to destroy
		// the intermediate attempt and the verdict that judged it.
		outOfBudget := r.budgetExhausted(current.ID)
		verdict := plan.AttemptRejected
		switch {
		case review.Approved:
			verdict = plan.AttemptApproved
		case attempt == r.MaxRetries || outOfBudget:
			verdict = plan.AttemptEscalated
		}
		lin.record(current, g, review, verdict, startedAt)

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

		if attempt == r.MaxRetries || outOfBudget {
			return r.escalateTask(current, review, attempt, outOfBudget)
		}

		current.MoveTo(plan.ColInProgress)
		current.Retries = attempt + 1
		// By this point the task has a failure ledger, so the corrector is the
		// agent actually holding the failing work — the one place a model
		// ladder is worth spending. Resolved ONCE so the start event, the
		// dispatch, the gate pass and the end event all name the same agent.
		corrector := r.correctorIDFor(current)
		if note := r.escalationNote(current); note != "" {
			r.logf("%s %s", current.ID, note)
		}
		r.logf("%s correcting (attempt %d)", current.ID, attempt+1)
		r.fire(stream.KindAgentStart, corrector, current.ID, "correction pass",
			strings.Join(current.Files, ", "), "")

		corrIn := r.formatCorrectPrompt(current, review)
		corr, ok := r.execOne(ctx, current.ID, "correction", ggagent.SubAgentRequest{
			AgentID: corrector, Input: corrIn,
			Timeout: r.callTimeout(ctx), ShareState: true, TaskID: current.ID,
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
		r.runGates(ctx, &current, corrector, baseline, gateOpts{})
		r.fire(stream.KindAgentEnd, corrector, current.ID, "corrector finished", "",
			truncate(current.Output, 800))
		current.MoveTo(plan.ColInReview)
	}
	return current, nil, nil
}

// mergeHumanEdits folds a human's board edits into the task the review ladder is
// holding, WITHOUT discarding the ladder's own in-flight state.
//
// The reload used to be a wholesale `current = latest`, and the LiveStore copy
// is only written once — at the END of the wave, before the first review. Every
// retry therefore reset Output back to the worker's original answer, so:
//
//   - the corrector's output was thrown away between rounds and the reviewer
//     re-judged the identical text up to MaxRetries+1 times (measured: 4
//     corrector passes, 5 reviews, none of the corrections ever seen);
//   - the review-time gate sections appended to Output were discarded with it;
//   - Retries was reset to 0 on every pass, so the value that reached the board
//     (and alreadySatisfiedRetry, which gates skipping the worker) was always 0.
//
// Human-owned fields (scope, text, priority, dependencies, notes, column) come
// from the store; loop-owned fields (Output, Review, Retries, Error) are kept.
func mergeHumanEdits(cur, latest plan.Task) plan.Task {
	out := latest
	if strings.TrimSpace(cur.Output) != "" {
		out.Output = cur.Output
	}
	if strings.TrimSpace(cur.Review) != "" {
		out.Review = cur.Review
	}
	if cur.Retries > out.Retries {
		out.Retries = cur.Retries
	}
	if strings.TrimSpace(cur.Error) != "" {
		out.Error = cur.Error
	}
	return out
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
	// No remedy in the message. This string is persisted into task notes and
	// TASKS.md, so it outlives the renderer that produced it: naming Studio
	// here is wrong for every CLI, TUI and headless run that reads the board
	// back. Each renderer supplies its own "how to answer this".
	r.fireIntervention(current.ID, "escalate",
		fmt.Sprintf("%s needs human review", current.ID), detail)
	return current, &escalation{detail: detail, attempt: attempt, review: review}, nil
}

// rootDir is the nil-safe project root.
func (r *Runner) rootDir() string {
	if r == nil {
		return ""
	}
	return r.Root
}

// fallbackTaskPromptWithLang renders a task for the worker when BuildInput is
// nil — tests and embedding callers that do not install the orchestrator's
// builder.
//
// It delegates to agents.BuildWorkerPrompt, the ONE builder that owns the
// worker contract. The previous version deliberately restated nothing, on the
// grounds that production used the orchestrator's builder anyway; but that
// builder was itself missing the checklist, the "no extra helper files" rule,
// the ws_patch retry rule, the ws_shell smoke step and the no-stubs rule — the
// exact things the review gates reject on. A second, quieter prompt was not
// the fix; one shared builder is.
func fallbackTaskPromptWithLang(t plan.Task, langHint string) string {
	return agents.BuildWorkerPrompt(t, agents.WorkerPromptOptions{
		LangHint:    langHint,
		Description: StripScopedPack(t.Description),
	})
}

// StripScopedPack removes ephemeral pack headers so TASKS.md stays lean.
func StripScopedPack(desc string) string {
	desc = strings.TrimSpace(desc)
	if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
	}
	if strings.HasPrefix(desc, "# Scoped context") {
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
`, t.ID, t.Title, t.Role, langHint, t.Acceptance, clipForReview(t.Output, 3500)) + r.feedbackSection()
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
			Timeout: r.callTimeout(ctx), ShareState: true, TaskID: t.ID,
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
// output. They now come from pkg/quality, which OWNS them and emits them.
//
// This list used to re-declare them, and two were wrong: it looked for
// "## Static quality" and "## Claims gate" while the formatters emit
// "## Static quality gate" and "## Claimed files gate". Nothing matched, so
// harness-authored gate markdown — the literal word FAILED included — stayed
// glued to the model's finalize text when multipass.LooksCompleteJSON,
// quality.IncompleteFinalizeReason, quality.LooksLikeToolJunk and
// quality.AssessResponse judged it. Duplicated literals is how that drift
// happened; the drift guard below is kept, but there is nothing left to drift.
var harnessSectionHeaders = quality.HarnessSectionHeaders

// staticGateHeader / claimsGateHeader alias the exported constants so the
// call sites and tests below read the same as before.
const (
	staticGateHeader = quality.StaticSectionHeader
	claimsGateHeader = quality.ClaimsSectionHeader
)

// stripPostSections removes harness-appended evidence/gate sections so JSON
// completeness checks look at the model answer, not smoke/claims appendices.
func stripPostSections(s string) string {
	return quality.StripHarnessSections(s)
}

// reviewEvidenceBudget bounds the harness-authored evidence handed to the
// reviewer. It is generous relative to reviewPromptBudget because these
// sections are short, factual and the only objective input the reviewer gets.
const reviewEvidenceBudget = 2500

// clipForReview budgets the review prompt so the harness's OWN evidence can
// never be truncated away by the model's prose.
//
// runGates APPENDS its sections (## Disk evidence, ## Deterministic smoke,
// ## Acceptance smoke, ## Static quality gate, ## Claimed files gate,
// ## Knowledge conflicts) to the end of Task.Output, and the reviewer's rules
// are written in terms of them — "approve if ... Disk evidence section shows
// changed files", "reject if ## Deterministic smoke shows FAILED". A head-first
// clip therefore deleted exactly the part the reviewer was told to judge on:
// once a reasoning worker's prose passed the budget, the reviewer saw claims
// with no evidence and applied its "reject if output is only claims" rule to
// correct, test-passing code.
//
// Observed live on a 9B: seven consecutive rejections (score 0) of an
// implementation whose tests all passed, ~22 minutes of correction rounds that
// could not have succeeded, because no output the worker produced could put the
// evidence back inside the window.
//
// Split first, budget each part separately, and keep the evidence whole.
func clipForReview(out string, budget int) string {
	body := strings.TrimSpace(stripPostSections(out))
	evidence := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), body))
	if evidence == "" {
		return truncate(body, budget)
	}
	// The evidence is the smaller, denser half; clip the prose around it.
	evidence = truncate(evidence, reviewEvidenceBudget)
	room := budget - len(evidence)
	if room < 600 {
		// Never let a large evidence block starve the answer entirely: the
		// reviewer still needs to see what the worker claims it did.
		room = 600
	}
	return truncate(body, room) + "\n\n" + evidence
}

func roleMaxIter(role string) int {
	switch baseRole(role) {
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
		// Every declared target absent reads as "the worker invented these
		// paths" — unless the pre-wave baseline recorded CONTENT for them, in
		// which case they existed when the wave started and this task removed
		// them. A task whose whole job is a deletion ("remove the dead legacy
		// helper", the delete half of a manual rename) ends in exactly that
		// state, and rejecting it here meant it could never be approved: the
		// corrector cannot un-delete a file into existence, so it burned every
		// retry and escalated to a human on every run.
		if missing == len(t.Files) && !deletedSinceBaseline(t.Files, baseline) {
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

// deletedSinceBaseline reports whether EVERY path had content at wave start and
// is gone now — a deterministic disk delta, the opposite of a hallucinated path.
// A nil/empty baseline proves nothing and returns false.
func deletedSinceBaseline(files []string, baseline map[string]string) bool {
	if len(files) == 0 || len(baseline) == 0 {
		return false
	}
	for _, f := range files {
		if strings.TrimSpace(baseline[f]) == "" {
			return false
		}
	}
	return true
}

// scopeOK rejects wander: claimed or newly created files outside task focus.
func (r *Runner) scopeOK(t plan.Task) string {
	// A ws_shell command that wrote a PROTECTED path is checked first because it
	// is the only signal here that cannot be a mistake of bookkeeping: the other
	// branches infer wander from what the worker CLAIMED, this one is a
	// before/after sha256 of a file the task was forbidden to touch.
	//
	// Only Protected events count. An ordinary out-of-focus shell write is
	// reported as disk evidence and left to the reviewer: `go build` writes
	// caches and `make generate` writes generated code, and failing those would
	// make the guard useless within a day.
	if bad := r.shellScopeViolations(t.ID); len(bad) > 0 {
		return shellScopeReason(bad)
	}
	claimed := parseFilesChanged(t.Output)
	// Build a task-local guard from expanded focus (includes scaffold paths).
	g := workspace.NewFocusGuard()
	focus := expandTaskFocus(t)
	if len(focus) > 0 {
		g.SetWave([][]string{focus})
	}
	if bad := g.OutOfScopeFiles(claimed); len(bad) > 0 {
		return workspace.OutOfScopeReason(t.Role, bad)
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

// diskEvidenceHeader is the harness-authored evidence section header. pkg/loop
// writes it; pkg/quality exports it so every strip list can share one slice.
const diskEvidenceHeader = quality.DiskEvidenceHeader

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

// hasCriteriaSection reports whether a GENUINE criteria evidence section is
// already attached — one this process wrote, not one the model typed.
//
// The provenance stamp is what separates them, and it has to be checked here.
// A match suppresses the review-time criteria gate, so a worker that merely
// echoes "## Acceptance criteria" — a plausible thing to write, since the
// reviewer contract in its own prompt names that heading — can switch the gate
// off. The consequence is not neutral: with no section actually run,
// CriteriaUnverifiedInOutput stays false, and false is the value that ALLOWS
// the reviewer fast path (see gateState.fastPath). "Nothing was checked" would
// read as "nothing needs checking".
//
// The cost objection to checking forgery does not apply to a STAMP check: a
// genuine section always carries the stamp, so it is still recognized and the
// commands are still not re-run. Only a forged header pays for a real
// verification, which is the correct place for that cost to land.
func hasCriteriaSection(output string) bool {
	idx := strings.Index(output, quality.CriteriaSectionHeader)
	if idx < 0 {
		return false
	}
	// The stamp is emitted immediately before the section it vouches for
	// (quality.StampHarnessSection), so a genuine one always precedes the
	// header. Its nonce is unguessable to anything that has not seen this
	// process's own output.
	return strings.Contains(output[:idx], quality.SectionStamp())
}

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
	if len(baseline) == 0 {
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
	cmd := exec.Command("git", "-C", r.Root, "diff", "--name-only", "HEAD") //nolint:gosec // fixed argv git invocation, no shell; only the project root varies
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("git", "-C", r.Root, "status", "--porcelain") //nolint:gosec // fixed argv git invocation, no shell; only the project root varies
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
//
// The implementation lives in pkg/workspace, which owns path semantics and is
// where the ws_shell scope guard needs the identical function. Two copies of
// one hash format is how the loop's write detector and the tool layer's write
// detector quietly stop agreeing; this is the one-line delegation that stops
// that from being possible.
func fileFingerprint(path string) string {
	return workspace.FileFingerprint(path)
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

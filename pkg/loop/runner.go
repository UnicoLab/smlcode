package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/rewind"
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

// Runner executes parallel workers → review → correct against a live board.
type Runner struct {
	Executor       SubAgentRunner
	Shared         *ggagent.SharedState
	Store          *plan.LiveStore // optional — reload mid-run for human edits
	Root           string          // workspace root for evidence checks
	SlmDir         string          // optional; defaults to Root/.slmcode
	TurnID         string          // query turn id for react checkpoints
	Focus          *workspace.FocusGuard
	AfterWave      AfterWave
	OnEvent        AgentEvent
	OnUsage        UsageEvent
	BuildInput     BuildInput
	MaxRetries     int
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
	// OnEscalate pauses the task for human decision (nil = leave in to_scope).
	OnEscalate EscalateHandler
	waveN      int
	// ResumedReact is set true when any task continued from message history
	// (used by tests / observability to assert no cold replan).
	ResumedReact bool
	// reactWatch is the mid-run conversation compaction hysteresis state.
	reactWatch *compact.Watchdog
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
			return fmt.Errorf("task loop exceeded safety guard")
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
					return nil
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
				r.Log("waiting: human backlog (to_scope/scoped) — promote tasks to ready_to_dev")
				idleRounds++
				if idleRounds > 3 {
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
		r.Log("wave: %d ready task(s)", len(wave))
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

func (r *Runner) persist(board *plan.Board) {
	if r.Store != nil {
		_ = r.Store.Replace(*board)
	}
}

func (r *Runner) taskInput(t plan.Task) string {
	if r.BuildInput != nil {
		return r.BuildInput(t)
	}
	return formatWorkerPrompt(t)
}

func (r *Runner) runWave(ctx context.Context, board *plan.Board, wave []plan.Task) error {
	r.waveN++
	if r.WaveSnapshots && r.RewindMgr != nil {
		var paths, ids []string
		for _, t := range wave {
			ids = append(ids, t.ID)
			paths = append(paths, t.Files...)
		}
		if snap, err := r.RewindMgr.SnapshotPaths(r.TurnID, r.waveN, ids, paths); err == nil && snap != nil {
			r.Log("wave %d snapshot %s (%d files)", r.waveN, snap.ID, len(snap.Files))
			r.fire("debug", "rewind", "", fmt.Sprintf("snapshot %s", snap.ID), "", "")
		}
	}
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

	// Skip worker LLM when acceptance is already satisfied on disk (multi-turn / retries).
	var needExec []plan.Task
	var needIdx []int
	snapshots := make([]map[string]string, len(wave))
	roles := make([]string, len(wave))
	for i, t := range wave {
		role := normalizeExecRole(t.Role)
		roles[i] = role
		snapshots[i] = r.snapshotTargets(t)
		scope := strings.Join(t.Files, ", ")
		if alreadySatisfied(r.Root, t) && r.scopeOK(t) == "" {
			r.Log("%s skip worker LLM: acceptance already satisfied on disk", t.ID)
			r.fire("agent_start", role, t.ID, "skip — already satisfied", scope, "")
			t.Output = `{"status":"done","summary":"already satisfied on disk — skipped worker LLM"}` +
				"\n\n## Disk evidence\n- present: " + strings.Join(t.Files, ", ")
			r.fire("agent_end", role, t.ID, "skipped worker (already satisfied)", scope, truncate(t.Output, 400))
			t.MoveTo(plan.ColInReview)
			board.UpdateTask(t)
			if err := r.reviewAndCorrect(ctx, board, t, snapshots[i]); err != nil {
				r.Log("review/correct %s: %v", t.ID, err)
			}
			continue
		}
		r.fire("agent_start", role, t.ID, t.Title, scope, "")
		needExec = append(needExec, t)
		needIdx = append(needIdx, i)
	}
	if len(needExec) == 0 {
		r.persist(board)
		return nil
	}

	reqs := make([]ggagent.SubAgentRequest, 0, len(needExec))
	for _, t := range needExec {
		req := ggagent.SubAgentRequest{
			AgentID:    normalizeExecRole(t.Role),
			Input:      r.taskInput(t),
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
		return fmt.Errorf("nil executor")
	}
	results, err := r.Executor.ExecuteSubAgents(ctx, reqs, r.Shared)
	if err != nil {
		r.Log("wave execution warning: %v", err)
	}

	canceled := false
	for j, res := range results {
		i := needIdx[j]
		t := needExec[j]
		role := roles[i]
		r.noteUsage(res, reqs[j].Input, outputString(res))
		if isCancelResult(err, res) || (res.Error != nil && isCancelResult(res.Error, res)) {
			r.saveReactFromResult(t.ID, role, res)
			canceled = true
			t.MoveTo(plan.ColBlocked)
			if res.Error != nil {
				t.Error = res.Error.Error()
			} else if err != nil {
				t.Error = err.Error()
			} else {
				t.Error = "context canceled"
			}
			board.UpdateTask(t)
			r.fire("agent_end", role, t.ID, "interrupted — react checkpointed", strings.Join(t.Files, ", "), t.Error)
			continue
		}
		if res.Error != nil {
			// Still persist partial conversation when available.
			if len(res.Messages) > 0 {
				r.saveReactFromResult(t.ID, role, res)
			}
			t.MoveTo(plan.ColBlocked)
			t.Error = res.Error.Error()
			board.UpdateTask(t)
			r.fire("agent_end", role, t.ID, "error", strings.Join(t.Files, ", "), t.Error)
			if r.FailureHandler != nil {
				_ = r.FailureHandler.ReportTaskFailure(board, t, res.Error, 0)
			}
			continue
		}
		r.clearReact(t.ID)
		t.Output = outputString(res)
		if res.Iteration > 0 {
			r.fireTurn(t.ID, res.Iteration, roleMaxIter(role))
		}
		// SLMs sometimes emit a bare tool call / empty finalize — nudge one corrective pass.
		needNudge := looksLikeToolJunk(t.Output)
		nudgeIssue := "Finish the task and return status JSON. Do not end on a tool call. Prefer ws_edit/ws_patch (ws_read first); ws_write only for NEW files."
		if r.QualityMonitor && !needNudge {
			assess := quality.AssessResponse(t.Output, nil, nil, nil)
			if !assess.OK {
				needNudge = true
				nudgeIssue = quality.CorrectionMessage(assess.Reason)
				if strings.HasPrefix(assess.Reason, "text_tool_calls:") {
					if calls := quality.ParseTextToolCalls(t.Output); len(calls) > 0 {
						nudgeIssue = quality.TextToolNudge(calls)
					}
				}
				r.Log("%s quality-monitor: %s", t.ID, quality.PhraseForUser(assess.Reason))
				r.fireIntervention(t.ID, assess.Reason, quality.PhraseForUser(assess.Reason), assess.Reason)
			}
		}
		if !needNudge {
			if calls := quality.ParseTextToolCalls(t.Output); len(calls) > 0 {
				needNudge = true
				nudgeIssue = quality.TextToolNudge(calls)
				if r.AutoTextTools {
					nudgeIssue = "AUTO_TEXT_TOOLS: re-issue these as NATIVE tool calls immediately, then status JSON.\n" + nudgeIssue
				}
				r.Log("%s text-tool-parser: recovered %d call(s)", t.ID, len(calls))
				r.fireIntervention(t.ID, "text_tool_calls", "text tool calls recovered", nudgeIssue)
			}
		}
		if !needNudge && r.ThinkingBudget &&
			quality.ThinkingBudgetExceeded(t.Output, r.ThinkingBudgetTokens) {
			needNudge = true
			nudgeIssue = quality.ThinkingBudgetBreachMessage()
			r.Log("%s thinking-budget: exceeded — forcing commit pass", t.ID)
			r.fireIntervention(t.ID, "thinking_budget_exceeded", "thinking budget exceeded", nudgeIssue)
		}
		if needNudge {
			r.Log("%s produced incomplete finalize; running corrector once", t.ID)
			r.fire("agent_start", r.correctorID(), t.ID, "fix incomplete finalize", strings.Join(t.Files, ", "), "")
			corrIn := formatCorrectPrompt(t, plan.ReviewResult{
				Approved: false,
				Issues:   []string{nudgeIssue},
				Summary:  "incomplete finalize",
			})
			corr, _ := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
				AgentID: r.correctorID(),
				Input:   corrIn,
				Timeout: r.Timeout, ShareState: true,
			}}, r.Shared)
			if len(corr) > 0 {
				r.noteUsage(corr[0], corrIn, outputString(corr[0]))
				if out := outputString(corr[0]); strings.TrimSpace(out) != "" {
					t.Output = out
				}
			}
			r.fire("agent_end", r.correctorID(), t.ID, "corrector finished", "", truncate(t.Output, 800))
		}
		mergeFilesChanged(&t)
		// Attach disk evidence hint for reviewer.
		if hint := r.diskEvidenceHint(t, snapshots[i]); hint != "" {
			t.Output = strings.TrimSpace(t.Output) + "\n\n## Disk evidence\n" + hint
		}
		// Deterministic smoke BEFORE review — blocks approve-on-disk-only for broken code.
		if r.PostWorkerSmoke && quality.ShouldSmokeTask(t) {
			sr := quality.RunPostWorkerSmoke(ctx, r.Root, t, r.Timeout)
			if sec := quality.FormatSmokeSection(sr); sec != "" {
				t.Output = strings.TrimSpace(t.Output) + sec
			}
			if sr.Ran && !sr.OK {
				t.Output += "\nObservation: exit error: deterministic smoke failed\n" + truncate(sr.Output, 1500)
				r.Log("%s deterministic smoke FAILED: %s", t.ID, sr.Command)
				r.fire("agent_end", "qa", t.ID, "deterministic smoke failed",
					strings.Join(t.Files, ", "), truncate(sr.Output, 800))
			} else if sr.Ran {
				r.Log("%s deterministic smoke PASSED: %s", t.ID, sr.Command)
			}
		}
		// Whitelisted acceptance commands (pytest / go test / python main.py …).
		if (role == plan.RoleWorker || role == "deep" || role == plan.RoleCorrector) &&
			strings.TrimSpace(t.Acceptance) != "" {
			if ar := quality.RunAcceptanceSmoke(ctx, r.Root, t.Acceptance, r.Timeout); ar.Ran {
				if sec := quality.FormatAcceptanceSection(ar); sec != "" {
					t.Output = strings.TrimSpace(t.Output) + sec
				}
				if !ar.OK {
					t.Output += "\nObservation: exit error: acceptance smoke failed\n" + truncate(ar.Output, 1500)
					r.Log("%s acceptance smoke FAILED: %s", t.ID, ar.Command)
					r.fire("agent_end", "qa", t.ID, "acceptance smoke failed",
						strings.Join(t.Files, ", "), truncate(ar.Output, 800))
				} else {
					r.Log("%s acceptance smoke PASSED: %s", t.ID, ar.Command)
				}
			}
		}
		// Static stub/placeholder gate — beats "looks done" claims from giant LLMs too.
		if r.StaticQuality {
			if issues := quality.CheckStaticQuality(r.Root, t); len(issues) > 0 {
				t.Output = strings.TrimSpace(t.Output) + quality.FormatStaticSection(issues)
				r.Log("%s static quality FAILED (%d issue(s))", t.ID, len(issues))
				r.fire("agent_end", "qa", t.ID, "static quality failed",
					strings.Join(t.Files, ", "), truncate(quality.FormatStaticSection(issues), 600))
			}
		}
		if r.ClaimsGate && role != plan.RoleTester && role != plan.RoleExplorer {
			if issues := quality.CheckClaimedFiles(r.Root, t); len(issues) > 0 {
				t.Output = strings.TrimSpace(t.Output) + quality.FormatClaimsSection(issues)
				r.Log("%s claims gate FAILED (%d path(s))", t.ID, len(issues))
				r.fire("agent_end", "qa", t.ID, "claims gate failed",
					strings.Join(t.Files, ", "), truncate(quality.FormatClaimsSection(issues), 600))
			}
		}
		// Auto self-critique when output is weak, or when think_passes>=2 and
		// status JSON looks incomplete (worker multipass port).
		wantCritique := r.WorkerCritique || r.ThinkPasses >= 2
		if wantCritique && (role == plan.RoleWorker || role == "deep" || role == plan.RoleCorrector) {
			coreOut := stripPostSections(t.Output)
			incomplete := !multipass.LooksCompleteJSON(coreOut)
			weak := quality.SmokeFailedInOutput(t.Output) ||
				quality.StaticFailedInOutput(t.Output) ||
				quality.ClaimsFailedInOutput(t.Output) ||
				quality.AcceptanceFailedInOutput(t.Output) ||
				(!hasToolWriteEvidence(t.Output) && looksLikeEditTask(t) && !alreadySatisfied(r.Root, t)) ||
				(r.ThinkPasses >= 2 && incomplete)
			if weak {
				passes := 1
				if r.ThinkPasses >= 3 && incomplete {
					passes = 2
				}
				// Keep refining while smoke/static still fail — bounded by MaxRetries.
				if quality.SmokeFailedInOutput(t.Output) || quality.StaticFailedInOutput(t.Output) ||
					quality.AcceptanceFailedInOutput(t.Output) {
					max := r.MaxRetries
					if max <= 0 {
						max = 3
					}
					if max > 4 {
						max = 4
					}
					if passes < max {
						passes = max
					}
				}
				for pass := 1; pass <= passes; pass++ {
					r.Log("%s worker-critique: weak/incomplete output — refine pass %d/%d", t.ID, pass, passes)
					r.fire("agent_start", r.correctorID(), t.ID, "worker self-critique", strings.Join(t.Files, ", "), "")
					issues := []string{
						"Self-critique: fix smoke/static/claims failures; make real ws_edit/ws_patch; re-smoke; finish with status JSON.",
					}
					if r.ThinkPasses >= 2 && incomplete {
						issues = append(issues,
							"think_passes: previous answer lacked complete status JSON — refine and finish the task.")
					}
					if quality.StaticFailedInOutput(t.Output) {
						issues = append(issues, "Replace stubs/placeholders with real code")
					}
					if quality.ClaimsFailedInOutput(t.Output) {
						issues = append(issues, "Only list files_changed paths that exist on disk")
					}
					if quality.SmokeFailedInOutput(t.Output) {
						issues = append(issues, "Fix compile/test failures shown in Deterministic smoke")
					}
					if quality.AcceptanceFailedInOutput(t.Output) {
						issues = append(issues, "Fix failures shown in Acceptance smoke — make acceptance commands exit 0")
					}
					corrIn := formatCorrectPrompt(t, plan.ReviewResult{
						Approved: false, Issues: issues, Summary: "worker self-critique",
					})
					corr, _ := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
						AgentID: r.correctorID(), Input: corrIn,
						Timeout: r.Timeout, ShareState: true,
					}}, r.Shared)
					if len(corr) > 0 {
						r.noteUsage(corr[0], corrIn, outputString(corr[0]))
						if out := outputString(corr[0]); strings.TrimSpace(out) != "" {
							t.Output = out
							mergeFilesChanged(&t)
							if hint := r.diskEvidenceHint(t, snapshots[i]); hint != "" {
								t.Output = strings.TrimSpace(t.Output) + "\n\n## Disk evidence\n" + hint
							}
							if r.PostWorkerSmoke && quality.ShouldSmokeTask(t) {
								sr := quality.RunPostWorkerSmoke(ctx, r.Root, t, r.Timeout)
								if sec := quality.FormatSmokeSection(sr); sec != "" {
									t.Output = strings.TrimSpace(t.Output) + sec
								}
							}
							if role == plan.RoleWorker || role == "deep" {
								if ar := quality.RunAcceptanceSmoke(ctx, r.Root, t.Acceptance, r.Timeout); ar.Ran {
									if sec := quality.FormatAcceptanceSection(ar); sec != "" {
										t.Output = strings.TrimSpace(t.Output) + sec
									}
								}
							}
							if r.StaticQuality {
								if issues := quality.CheckStaticQuality(r.Root, t); len(issues) > 0 {
									t.Output = strings.TrimSpace(t.Output) + quality.FormatStaticSection(issues)
								}
							}
							if r.ClaimsGate {
								if issues := quality.CheckClaimedFiles(r.Root, t); len(issues) > 0 {
									t.Output = strings.TrimSpace(t.Output) + quality.FormatClaimsSection(issues)
								}
							}
						}
					}
					r.fire("agent_end", r.correctorID(), t.ID, "worker self-critique finished", "", truncate(t.Output, 800))
					coreOut = stripPostSections(t.Output)
					incomplete = !multipass.LooksCompleteJSON(coreOut)
					if !incomplete && !quality.SmokeFailedInOutput(t.Output) &&
						!quality.StaticFailedInOutput(t.Output) && !quality.ClaimsFailedInOutput(t.Output) &&
						!quality.AcceptanceFailedInOutput(t.Output) {
						break
					}
				}
			}
		}
		r.fire("agent_end", role, t.ID, "worker finished", strings.Join(t.Files, ", "), truncate(t.Output, 1200))
		t.MoveTo(plan.ColInReview)
		board.UpdateTask(t)
		// Use the pre-wave snapshot so disk evidence survives finalize JSON that
		// omits tool traces (reviewAndCorrect must not re-baseline after writes).
		if err := r.reviewAndCorrect(ctx, board, t, snapshots[i]); err != nil {
			r.Log("review/correct %s: %v", t.ID, err)
			if isCancelResult(err, ggagent.SubAgentResult{}) {
				canceled = true
			}
		}
	}
	r.persist(board)
	if canceled {
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	return nil
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

func normalizeExecRole(role string) string {
	if role == "" || role == "implementer" {
		return plan.RoleWorker
	}
	switch role {
	case plan.RolePlanner, "splitter", "orchestrator", "memory", "coordinator", "architect", "context":
		return plan.RoleWorker
	default:
		return role
	}
}

func (r *Runner) fire(kind, agent, taskID, msg, scope, output string) {
	if r.OnEvent != nil {
		r.OnEvent(kind, agent, taskID, msg, scope, output)
	}
}

func (r *Runner) fireIntervention(taskID, reason, msg, detail string) {
	code := quality.ClassifyIntervention(reason)
	if msg == "" {
		msg = quality.PhraseForUser(reason)
	}
	scope := code
	r.fire("intervention", "harness", taskID, msg, scope, detail)
}

func (r *Runner) fireTurn(taskID string, iter, maxIter int) {
	if maxIter <= 0 {
		return
	}
	msg := fmt.Sprintf("turn %d/%d", iter, maxIter)
	if quality.ShouldFinalizeSteer(iter, maxIter) {
		msg += " · finalize soon"
	}
	r.fire("turn", "harness", taskID, msg, fmt.Sprintf("%d/%d", iter, maxIter), "")
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

func (r *Runner) reviewAndCorrect(ctx context.Context, board *plan.Board, t plan.Task, baseline map[string]string) error {
	if baseline == nil {
		baseline = r.snapshotTargets(t)
	}
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			if latest, ok := r.Store.GetTask(t.ID); ok {
				if latest.Column == plan.ColDone || latest.Column == plan.ColBlocked {
					board.UpdateTask(latest)
					return nil
				}
				if latest.Column == plan.ColToScope || latest.Column == plan.ColScoped {
					board.UpdateTask(latest)
					return nil
				}
				t = latest
			}
		}

		current, ok := board.Get(t.ID)
		if !ok {
			return fmt.Errorf("missing task %s", t.ID)
		}

		// Fast path: skip reviewer LLM when disk/acceptance evidence is already clear.
		// Prefer RenameSatisfied / rename disk OK before any reviewer call.
		reviewRaw := ""
		review := plan.ReviewResult{}
		renameDisk := renameOK(r.Root, current)
		satisfied := alreadySatisfied(r.Root, current) || renameDisk
		diskWrite := r.hasRealWriteEvidence(current, baseline) || renameDisk
		toolWrite := hasToolWriteEvidence(current.Output)
		diskSection := hasDiskEvidenceSection(current.Output)
		done := plan.WorkerLooksComplete(current.Output) || workerReportedDone(current.Output)
		hasEvidence := diskWrite || toolWrite || diskSection || renameDisk
		scopeWhy := r.scopeOK(current)
		shellFail := hasShellFailureEvidence(current.Output)
		smokeFail := quality.SmokeFailedInOutput(current.Output)
		acceptFail := quality.AcceptanceFailedInOutput(current.Output)
		staticFail := quality.StaticFailedInOutput(current.Output)
		claimsFail := quality.ClaimsFailedInOutput(current.Output)
		// Review-time static insurance: skipped-worker / already-satisfied paths
		// never ran CheckStaticQuality — catch Placeholder stubs before fast-path.
		if r.StaticQuality && !staticFail && !renameDisk {
			if issues := quality.CheckStaticQuality(r.Root, current); len(issues) > 0 {
				current.Output = strings.TrimSpace(current.Output) + quality.FormatStaticSection(issues)
				board.UpdateTask(current)
				staticFail = true
				r.Log("%s review-time static FAILED (%d issue(s))", current.ID, len(issues))
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
		if r.RequireSmoke && r.PostWorkerSmoke && quality.ShouldSmokeTask(current) &&
			!quality.SmokePassedInOutput(current.Output) && !smokeFail && !renameDisk &&
			quality.HasSmokeCommand(r.Root, smokeFiles) {
			sr := quality.RunPostWorkerSmoke(ctx, r.Root, current, r.Timeout)
			if sec := quality.FormatSmokeSection(sr); sec != "" {
				current.Output = strings.TrimSpace(current.Output) + sec
				board.UpdateTask(current)
				if sr.Ran && !sr.OK {
					smokeFail = true
					r.Log("%s review-time smoke FAILED: %s", current.ID, sr.Command)
				} else if sr.Ran {
					r.Log("%s review-time smoke PASSED: %s", current.ID, sr.Command)
				}
			}
		}
		smokeMissing := r.RequireSmoke && quality.HasSmokeCommand(r.Root, smokeFiles) &&
			!quality.SmokePassedInOutput(current.Output) && !smokeFail && !renameDisk
		// Rename on disk wins even when scope claims are noisy (weak tool log).
		// Never disk-auto-approve tester roles (they need passed JSON) or workers
		// whose own ws_shell / deterministic smoke / static / claims gate failed — except rename.
		fastPath := renameDisk ||
			(current.Role != plan.RoleTester && !shellFail && !smokeFail && !acceptFail && !staticFail && !claimsFail && !smokeMissing &&
				(satisfied || diskWrite || diskSection) && scopeWhy == "")
		if fastPath {
			review.Approved = true
			review.Score = 85
			switch {
			case renameDisk:
				review.Summary = "auto-approved: rename satisfied on disk"
			case satisfied && !hasEvidence:
				review.Summary = "auto-approved: acceptance already satisfied on disk"
			default:
				review.Summary = "auto-approved: disk write evidence on focus files"
			}
			r.Log("%s review fast-path (skip reviewer LLM): %s", current.ID, review.Summary)
		} else if r.MaxParallel >= 2 {
			// Race disk/acceptance probe vs reviewer LLM; acceptance win cancels reviewer.
			// Optional second reviewer strategy when capacity allows (duplicate paths).
			reviewIn := formatReviewPrompt(current)
			cur := current
			base := baseline
			slots := []SpecSlot{{
				Role: "acceptance", Required: false,
				Local: func(ctx context.Context) (string, error) {
					return acceptanceProbe(ctx, func() bool {
						return renameOK(r.Root, cur) || alreadySatisfied(r.Root, cur) ||
							r.hasRealWriteEvidence(cur, base)
					}, `{"approved":true,"score":85,"summary":"auto-approved: acceptance race won"}`)
				},
			}, {
				Role: r.reviewerID(), Prompt: reviewIn, Required: false,
			}}
			if r.MaxParallel >= 3 {
				slots = append(slots, SpecSlot{
					Role: "reviewer-strict", Required: false,
					Prompt: reviewIn + "\n\nSTRICT: reject unless focus files + acceptance clearly met. Return JSON.",
				})
			}
			revRole := r.reviewerID()
			r.fire("agent_start", revRole, current.ID, "speculative review race", strings.Join(current.Files, ", "), "")
			r.Log("%s speculative review (%d paths, max_parallel=%d)", current.ID, len(slots), r.MaxParallel)
			res := r.speculate(ctx, slots)
			var acceptOut, revOut, strictOut string
			var revErr error
			for _, sr := range res {
				switch sr.Role {
				case "acceptance":
					if !sr.Skipped && sr.Err == nil {
						acceptOut = sr.Output
					}
				case "reviewer-strict":
					if !sr.Skipped && strings.TrimSpace(sr.Output) != "" {
						strictOut = sr.Output
					}
				default:
					if sr.Role == revRole {
						revOut, revErr = sr.Output, sr.Err
					}
				}
			}
			if strings.TrimSpace(acceptOut) != "" {
				reviewRaw = acceptOut
				review = plan.ParseReviewJSON(reviewRaw)
				r.Log("%s review acceptance won — cancelled reviewer LLM", current.ID)
			} else {
				reviewRaw = revOut
				if strings.TrimSpace(reviewRaw) == "" {
					reviewRaw = strictOut
				}
				if revErr != nil && strings.TrimSpace(reviewRaw) == "" {
					current.MoveTo(plan.ColBlocked)
					current.Error = revErr.Error()
					board.UpdateTask(current)
					r.persist(board)
					if r.FailureHandler != nil {
						_ = r.FailureHandler.ReportTaskFailure(board, current, revErr, attempt)
					}
					return revErr
				}
				review = plan.ParseReviewJSON(reviewRaw)
				if !review.Approved && current.Role != plan.RoleTester && !shellFail && !smokeFail && !acceptFail && !staticFail && !claimsFail && !smokeMissing {
					if satisfied || diskWrite || diskSection || (done && (looksLikeBrokenReview(reviewRaw) || hasEvidence)) {
						review.Approved = true
						review.Score = 80
						switch {
						case satisfied && !hasEvidence:
							review.Summary = "auto-approved: acceptance already satisfied on disk"
						case diskSection || diskWrite:
							review.Summary = "auto-approved: disk write evidence on focus files"
						default:
							review.Summary = "auto-approved: worker completion + write evidence"
						}
						review.Issues = nil
					}
				}
			}
		} else {
			r.fire("agent_start", r.reviewerID(), current.ID, "self-critic review", strings.Join(current.Files, ", "), "")
			reviewIn := formatReviewPrompt(current)
			results, err := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
				AgentID: r.reviewerID(), Input: reviewIn,
				Timeout: r.Timeout, ShareState: true,
			}}, r.Shared)
			if len(results) > 0 {
				r.noteUsage(results[0], reviewIn, outputString(results[0]))
			}
			if err != nil && len(results) == 0 {
				current.MoveTo(plan.ColBlocked)
				current.Error = err.Error()
				board.UpdateTask(current)
				r.persist(board)
				if r.FailureHandler != nil {
					_ = r.FailureHandler.ReportTaskFailure(board, current, err, attempt)
				}
				return err
			}
			if len(results) > 0 {
				reviewRaw = outputString(results[0])
				if results[0].Error != nil && reviewRaw == "" {
					reviewRaw = results[0].Error.Error()
				}
			}
			review = plan.ParseReviewJSON(reviewRaw)
			// SLM fallback: trust clear worker completion + tool/disk evidence.
			if !review.Approved && current.Role != plan.RoleTester && !shellFail && !smokeFail && !acceptFail && !staticFail && !claimsFail && !smokeMissing {
				if satisfied || diskWrite || diskSection || (done && (looksLikeBrokenReview(reviewRaw) || hasEvidence)) {
					review.Approved = true
					review.Score = 80
					switch {
					case satisfied && !hasEvidence:
						review.Summary = "auto-approved: acceptance already satisfied on disk"
					case diskSection || diskWrite:
						review.Summary = "auto-approved: disk write evidence on focus files"
					default:
						review.Summary = "auto-approved: worker completion + write evidence"
					}
					review.Issues = nil
				}
			}
		}
		// Deterministic smoke / acceptance / shell / static / claims failure beats any earlier auto-approve.
		if review.Approved && (shellFail || smokeFail || acceptFail || staticFail || claimsFail || smokeMissing) && current.Role != plan.RoleTester && !renameDisk {
			review.Approved = false
			review.Score = 20
			switch {
			case claimsFail:
				review.Summary = "rejected: hallucinated files_changed paths"
				review.Issues = []string{"files_changed lists paths missing on disk — reconcile claims"}
			case staticFail:
				review.Summary = "rejected: static quality gate (stubs/placeholders)"
				review.Issues = []string{"stub/placeholder code detected — replace with real implementation"}
			case acceptFail:
				review.Summary = "rejected: acceptance smoke failed"
				review.Issues = []string{"whitelisted acceptance command failed — make pytest/go test/main.py exit 0"}
			case smokeFail:
				review.Summary = "rejected: deterministic smoke failed"
				review.Issues = []string{"Go ran py_compile/go test on focus files and it failed — corrector must fix"}
			case smokeMissing:
				review.Summary = "rejected: coding task missing deterministic smoke pass"
				review.Issues = []string{"run py_compile / go test / node --check via tools before claiming done"}
			default:
				review.Summary = "rejected: ws_shell failure evidence in worker output"
				review.Issues = []string{"worker ran a command that failed — fix before approve"}
			}
			r.Log("%s overriding approve: %s", current.ID, review.Summary)
		}
		// Tester gate: never accept "does not work" / passed:false / empty finalize as done.
		// Exception: rename already satisfied on disk — do not reopen/escalate.
		if review.Approved && strings.EqualFold(current.Role, plan.RoleTester) {
			if renameOK(r.Root, current) {
				r.Log("%s tester gate skipped: rename satisfied on disk", current.ID)
			} else {
				tr := plan.ParseTesterJSON(current.Output)
				if !tr.Passed {
					review.Approved = false
					review.Score = 0
					why := "tester reported failure"
					if len(tr.Failures) > 0 {
						why = tr.Failures[0]
					} else if tr.Summary != "" {
						why = tr.Summary
					}
					review.Summary = "rejected by tester gate: " + why
					review.Issues = append([]string{why}, review.Issues...)
					r.Log("%s tester gate blocked approval: %s", current.ID, why)
				}
			}
		}
		// Evidence gate: never mark done when targets are missing or no write occurred.
		if review.Approved {
			if renameOK(r.Root, current) {
				// Disk rename is authoritative — skip scope/evidence reopen.
			} else if ok, why := r.evidenceOK(current, baseline); !ok {
				review.Approved = false
				review.Score = 0
				review.Summary = "rejected by evidence gate: " + why
				review.Issues = append([]string{why}, review.Issues...)
				if r.Root != "" {
					real := plan.ReconcileFiles(r.Root, current.Files, plan.DiscoverRelevantFiles(r.Root, current.Title+" "+current.Description, current.Output))
					if len(real) > 0 {
						current.Files = real
					}
				}
				r.Log("%s evidence gate blocked approval: %s", current.ID, why)
			}
		}
		current.Review = review.Summary
		if len(review.Issues) > 0 {
			current.Review += "\nIssues:\n- " + strings.Join(review.Issues, "\n- ")
		}

		endOut := truncate(reviewRaw, 600)
		if endOut == "" {
			endOut = review.Summary
		}
		r.fire("agent_end", r.reviewerID(), current.ID,
			fmt.Sprintf("review approved=%v score=%d", review.Approved, review.Score),
			"", endOut)

		if review.Approved {
			mergeFilesChanged(&current)
			current.MoveTo(plan.ColDone)
			current.Error = ""
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s approved → done (score=%d)", current.ID, review.Score)
			return nil
		}

		if attempt == r.MaxRetries {
			// Escalate to human backlog instead of hard-blocking forever.
			current.MoveTo(plan.ColToScope)
			current.Error = "review rejected after max retries — needs human input or smaller scope"
			current.Notes = strings.TrimSpace(current.Notes + "\n" +
				"ESCALATED: review rejected after max retries. " +
				"Fix acceptance/context, then promote back to ready_to_dev.\n" +
				current.Review)
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s escalated to to_scope after %d retries", current.ID, r.MaxRetries)
			// Surface in Studio/TUI so humans see review / precise-fix prompts.
			detail := strings.TrimSpace(current.Review)
			if detail == "" {
				detail = current.Error
			}
			r.fireIntervention(current.ID, "escalate",
				fmt.Sprintf("%s needs human review — decide in Studio (or wait for timeout)",
					current.ID),
				detail)

			if r.OnEscalate != nil {
				r.OnEscalate(ctx, board, current, detail)
				// Reload task after HITL mutation (retry / re_scope / mark_done / abort).
				if updated, ok := board.Get(current.ID); ok {
					current = updated
					board.UpdateTask(current)
					r.persist(board)
				}
			}

			if r.FailureHandler != nil {
				failErr := fmt.Errorf("max retries exceeded: review rejected")
				_ = r.FailureHandler.ReportTaskFailure(board, current, failErr, attempt)
				_ = r.FailureHandler.AddWaveLesson(board, current, failErr)
			}
			return nil
		}

		current.MoveTo(plan.ColInProgress)
		current.Retries = attempt + 1
		board.UpdateTask(current)
		r.persist(board)
		r.Log("%s correcting (attempt %d)", current.ID, attempt+1)
		r.fire("agent_start", r.correctorID(), current.ID, "correction pass", strings.Join(current.Files, ", "), "")

		corrIn := formatCorrectPrompt(current, review)
		corrResults, corrErr := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
			AgentID: r.correctorID(), Input: corrIn,
			Timeout: r.Timeout, ShareState: true,
		}}, r.Shared)
		if len(corrResults) > 0 {
			r.noteUsage(corrResults[0], corrIn, outputString(corrResults[0]))
		}
		if corrErr != nil && len(corrResults) == 0 {
			current.MoveTo(plan.ColBlocked)
			current.Error = corrErr.Error()
			board.UpdateTask(current)
			r.persist(board)
			if r.FailureHandler != nil {
				_ = r.FailureHandler.ReportTaskFailure(board, current, corrErr, attempt)
			}
			return corrErr
		}
		if len(corrResults) > 0 {
			current.Output = outputString(corrResults[0])
			if corrResults[0].Error != nil && current.Output == "" {
				current.Error = corrResults[0].Error.Error()
			}
		}
		mergeFilesChanged(&current)
		if hint := r.diskEvidenceHint(current, baseline); hint != "" {
			current.Output = strings.TrimSpace(current.Output) + "\n\n## Disk evidence\n" + hint
		}
		r.fire("agent_end", r.correctorID(), current.ID, "corrector finished", "", truncate(current.Output, 800))
		current.MoveTo(plan.ColInReview)
		board.UpdateTask(current)
		r.persist(board)
		t = current
	}
	return nil
}

func formatWorkerPrompt(t plan.Task) string {
	var b strings.Builder
	b.WriteString("Atomic task — complete only this:\n\n")
	b.WriteString(fmt.Sprintf("ID: %s\nTitle: %s\nColumn: %s\nRole: %s\n\n", t.ID, t.Title, t.Column, t.Role))
	b.WriteString(StripScopedPack(t.Description))
	b.WriteString("\n")
	if len(t.Files) > 0 {
		b.WriteString("\n## Focus files (HARD SCOPE)\nOnly edit these paths or files in the same package directory:\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
		b.WriteString("\nDo NOT create main.go / index.js / other entrypoints unless listed above.\n")
		b.WriteString("Do NOT add extra helper files or unrelated functions — only what acceptance requires.\n")
		b.WriteString("If ws_patch fails, re-read the file and retry a minimal SEARCH/REPLACE; never invent new root files.\n")
	}
	if t.Acceptance != "" {
		b.WriteString("\nAcceptance criteria:\n")
		b.WriteString(t.Acceptance)
		b.WriteString("\n")
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
		b.WriteString("\nHuman notes:\n")
		b.WriteString(t.Notes)
		b.WriteString("\n")
	}
	b.WriteString(`
## Required finish
1. ws_read focus files first, then ws_edit / ws_patch (prefer over rewrites).
2. ws_write is NEW files only — refused on existing paths. No cat> overwrites.
3. After edits: ws_shell smoke (python -m py_compile PATH / go test ./pkg -short / node --check). Fix failures before done.
4. No stubs (pass / … / NotImplemented / TODO panic). Never add argparse --help.
5. End with STRICT JSON only:
{"status":"done","summary":"...","files_changed":["real/path.go"],"notes":"..."}
Never claim done without tool edits. Never end on a tool call.
`)
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

func formatReviewPrompt(t plan.Task) string {
	return fmt.Sprintf(`Review task %s (%s) role=@%s. Reply with JSON only — no tools.

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
`, t.ID, t.Title, t.Role, t.Acceptance, truncate(t.Output, 3500))
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func formatCorrectPrompt(t plan.Task, review plan.ReviewResult) string {
	return fmt.Sprintf(`Fix task %s after failed review.

## Original task
%s

## Previous output
%s

## Review issues
- %s

## Review summary
%s

Use ws_edit/ws_write on real files, then finish with STRICT JSON:
{"status":"done","summary":"...","files_changed":["..."],"notes":"..."}
`, t.ID, StripScopedPack(t.Description), truncate(t.Output, 2500), strings.Join(review.Issues, "\n- "), review.Summary)
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

func looksLikeToolJunk(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "<function") || strings.Contains(lower, "<tool_call>") ||
		strings.Contains(lower, "</tool_call>")
}

// stripPostSections removes harness-appended evidence/gate sections so JSON
// completeness checks look at the model answer, not smoke/claims appendices.
func stripPostSections(s string) string {
	for _, marker := range []string{
		"\n## Disk evidence\n",
		"\n## Deterministic smoke\n",
		"\n## Acceptance smoke\n",
		"\n## Static quality\n",
		"\n## Claims gate\n",
	} {
		if i := strings.Index(s, marker); i >= 0 {
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
	if looksLikeToolJunk(raw) {
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
	if alreadySatisfied(r.Root, t) {
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

func hasToolWriteEvidence(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "ws_write") || strings.Contains(lower, "ws_edit") ||
		strings.Contains(lower, "ws_patch") || strings.Contains(lower, "ws_mv") ||
		strings.Contains(lower, "ws_delete") ||
		strings.Contains(lower, "wrote ") || strings.Contains(lower, "edited ") ||
		strings.Contains(lower, "updated ") || strings.Contains(lower, "patched ") ||
		strings.Contains(lower, "moved ") || strings.Contains(lower, "deleted ") ||
		strings.Contains(lower, "dry-run: would write") || strings.Contains(lower, "dry-run: would edit") ||
		strings.Contains(lower, "dry-run: would mv") || strings.Contains(lower, "dry-run: would delete") ||
		strings.Contains(lower, "staged write") || strings.Contains(lower, "pending/") ||
		strings.Contains(lower, "## disk evidence")
}

func hasWriteEvidence(output string) bool {
	// Kept for compatibility; prefer hasToolWriteEvidence + disk checks.
	return hasToolWriteEvidence(output) ||
		(strings.Contains(strings.ToLower(output), `"files_changed":[`) &&
			!strings.Contains(strings.ToLower(output), `"files_changed":[]`))
}

func (r *Runner) hasRealWriteEvidence(t plan.Task, baseline map[string]string) bool {
	if renameOK(r.Root, t) {
		return true
	}
	if hasToolWriteEvidence(t.Output) {
		return true
	}
	if hasDiskEvidenceSection(t.Output) {
		return true
	}
	if r.Root == "" {
		return false
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
	// Ambiguous baseline (empty / missing / matches current after a late snapshot):
	// trust git dirty on focus files or disk evidence section already checked above.
	if baselineAmbiguous(baseline, t) {
		if focusGitDirty(changed, t.Files) {
			return true
		}
		// Focus file present + edit task with any write-looking output → trust disk.
		lower := strings.ToLower(t.Output)
		if looksLikeEditTask(t) && focusFilesPresent(r.Root, t.Files) &&
			(strings.Contains(lower, "edited ") ||
				strings.Contains(lower, "wrote ") ||
				strings.Contains(lower, "patched ") ||
				strings.Contains(lower, "moved ") ||
				strings.Contains(lower, "deleted ") ||
				strings.Contains(lower, "ws_mv") ||
				strings.Contains(lower, "dry-run: would")) {
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

func hasDiskEvidenceSection(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "## disk evidence") {
		return false
	}
	return strings.Contains(lower, "modified:") ||
		strings.Contains(lower, "created/present:") ||
		strings.Contains(lower, "renamed:") ||
		strings.Contains(lower, "deleted:") ||
		strings.Contains(lower, "git dirty:") ||
		strings.Contains(lower, "pending:")
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
		out[f] = fileFingerprint(filepath.Join(r.Root, f))
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
	for _, c := range r.gitChangedFiles() {
		lines = append(lines, "- git dirty: "+c)
		if len(lines) > 12 {
			break
		}
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

func fileFingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// lightweight fingerprint
	sum := 0
	for i, b := range data {
		sum = (sum*131 + int(b) + i) % 1000000007
	}
	return fmt.Sprintf("%d:%d", len(data), sum)
}

func alreadySatisfied(root string, t plan.Task) bool {
	if root == "" {
		return false
	}
	if renameOK(root, t) {
		return true
	}
	blob := strings.ToLower(t.Title + " " + StripScopedPack(t.Description) + " " + t.Acceptance)
	// Create/scaffold tasks: acceptance met when the task's declared files exist
	// AND pass static quality (no Placeholder stubs — TestSLMs regression).
	// Use t.Files only (not expandTaskFocus) so scaffold prefixes don't inflate "needed".
	// Keep this narrow — "implement"/"write"/"edit" still need real write evidence.
	if strings.Contains(blob, "create") || strings.Contains(blob, "scaffold") ||
		strings.Contains(blob, "initialize") || strings.Contains(blob, "greenfield") {
		// Implement/class-agent work is never "already satisfied" by mere existence.
		if strings.Contains(blob, "implement") || strings.Contains(blob, "class") ||
			strings.Contains(blob, "langgraph") || strings.Contains(blob, "langchain") {
			return false
		}
		targets := t.Files
		if len(targets) == 0 {
			targets = plan.InferCreateFiles(blob)
		}
		present, needed := 0, 0
		for _, f := range targets {
			if strings.HasSuffix(f, "/") || f == "src" || f == "tests" {
				continue
			}
			needed++
			if !plan.FileExists(root, f) {
				continue
			}
			info, err := os.Stat(filepath.Join(root, f))
			if err == nil && info.Size() > 0 {
				present++
			}
		}
		if needed > 0 && present >= needed {
			if len(quality.CheckStaticQuality(root, t)) > 0 {
				return false
			}
			return true
		}
	}
	if !(strings.Contains(blob, "doc comment") || strings.Contains(blob, "comment")) {
		return false
	}
	for _, f := range t.Files {
		if !plan.FileExists(root, f) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		body := string(data)
		if strings.Contains(body, "//") && (strings.Contains(strings.ToLower(body), "func ") || strings.Contains(body, "def ")) {
			if strings.Contains(body, "\n//") || strings.HasPrefix(strings.TrimSpace(body), "//") {
				return true
			}
		}
	}
	return false
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

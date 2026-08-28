package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// ContinueHandler collects continue/stop decisions (tests / custom UIs).
type ContinueHandler func(ctx context.Context, ask plan.ContinueAsk) (plan.ContinueAnswer, error)

// OnContinue registers a continue-ask callback.
func (o *Orchestrator) OnContinue(h ContinueHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onContinue = h
}

// needsContinueAsk is true when retries/QA are exhausted but work remains.
func needsContinueAsk(board *plan.Board, testerRejected, qaFailed bool, gaps []quality.PreciseGap) bool {
	if board == nil {
		return false
	}
	if len(gaps) > 0 {
		return true
	}
	if testerRejected || qaFailed {
		return true
	}
	if boardHasEscalated(board) {
		return true
	}
	if board.FailedCount() > 0 {
		return true
	}
	if board.AgentWorkRemaining() {
		return true
	}
	return false
}

func escalatedTaskIDs(board *plan.Board) []string {
	if board == nil {
		return nil
	}
	var ids []string
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column == plan.ColDone {
			continue
		}
		blob := strings.ToLower(t.Error + " " + t.Notes + " " + t.Review)
		if t.Column == plan.ColBlocked ||
			strings.Contains(blob, "escalated") || strings.Contains(blob, "needs human") ||
			strings.Contains(blob, "max retries") || strings.Contains(blob, "placeholder gap") ||
			strings.Contains(blob, "timeout") || strings.Contains(blob, "timed out") ||
			strings.Contains(blob, "deadline") ||
			(t.Column == plan.ColToScope && strings.TrimSpace(t.Error) != "") {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// runContinueAsk pauses (or auto-continues once) when work remains after
// exhausted retries/QA. Returns (shouldRunAnotherWave, updated board).
func (o *Orchestrator) runContinueAsk(ctx context.Context, query string, board *plan.Board, runner *loop.Runner, reason string, gaps []quality.PreciseGap, testerRejected, qaFailed bool) (bool, *plan.Board) {
	if o == nil || o.cfg == nil || board == nil {
		return false, board
	}
	if !needsContinueAsk(board, testerRejected, qaFailed, gaps) {
		return false, board
	}
	mode := plan.NormalizeContinueAsk(o.cfg.ContinueAsk)
	if o.cfg.AutoApprove {
		mode = plan.ContinueAskAuto
	}
	// Headless: one more corrective wave beats stopping silently at a question
	// nobody is there to read. Bounded — ContinueAskAuto runs exactly one.
	// An explicit --on-gate-timeout=stop keeps the ask (and the stop).
	mode = o.headlessGateMode(mode, plan.ContinueAskAsk, plan.ContinueAskAuto)
	if mode == plan.ContinueAskOff {
		o.emit("test", "continue_ask=off — finishing with remaining work flagged", "")
		return false, board
	}

	timeout := o.cfg.ContinueAskTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ask := plan.BuildContinueAsk(query, reason, escalatedTaskIDs(board), gapLines(gaps))
	ask.TimeoutS = int(timeout.Seconds())
	payload := plan.MarshalContinueAskJSON(ask)
	_ = o.store.Append(contextstore.DocScratch, "Continue ask",
		ask.Summary+"\nReason: "+reason+"\n"+strings.Join(ask.Gaps, "\n"))

	var action string
	if mode == plan.ContinueAskAuto {
		o.emitFull("test", stream.KindOutput, "continue", "",
			"auto-continue: one more corrective wave", "", truncate(payload, 800))
		o.emitLoop("test", LoopEvent{
			Action: "continue_wave",
			Reason: "auto-continue: one more corrective wave — " + reason,
			From:   "test",
			To:     "execute",
		})
		action = "continue"
	} else {
		if err := hitl.WriteAsk(o.cfg.SlmDir(), "continue", ask); err != nil {
			o.emitWarn("test", "continue ask unavailable — stopping with remaining work flagged", err.Error())
			o.emitLoop("test", LoopEvent{
				Action: "aborted",
				Reason: "continue ask could not be written — stopping with work flagged",
				From:   "test",
				To:     "done",
				Wave:   o.waveCounter,
			})
			return false, board
		}
		o.emitLoop("test", LoopEvent{
			Action:   "continue_pending",
			Reason:   reason,
			Failures: append([]string{}, ask.Gaps...),
			From:     "test",
			To:       "execute",
			Awaiting: true,
		})
		o.emitFull("test", stream.KindAsk, "continue", "",
			"retries exhausted — continue another wave? POST /api/continue/answer",
			"", payload)

		var ans plan.ContinueAnswer
		got := false
		o.mu.Lock()
		h := o.onContinue
		o.mu.Unlock()
		if h != nil {
			if a, err := h(ctx, ask); err == nil {
				a.AskID = ask.ID
				ans, got = a, true
			}
		}
		if !got {
			o.emit("test", fmt.Sprintf("waiting for continue decision (timeout %s)", timeout), "")
			ok, err := hitl.WaitAnswersForID(ctx, o.cfg.SlmDir(), "continue", ask.ID, timeout, &ans)
			if err != nil {
				hitl.Clear(o.cfg.SlmDir(), "continue")
				return false, board
			}
			got = ok
		}
		hitl.Clear(o.cfg.SlmDir(), "continue")
		if !got {
			o.emit("test", "continue ask timeout — stopping (work remains flagged)", "")
			o.emitLoop("test", LoopEvent{
				Action: "aborted",
				Reason: "continue ask timed out — stopping with work flagged",
				From:   "test",
				To:     "done",
				Wave:   o.waveCounter,
			})
			action = "stop"
		} else {
			action = plan.NormalizeContinueAction(ans.Action)
			o.emitFull("test", stream.KindOutput, "continue", "",
				"continue answer: "+action, "", strings.TrimSpace(ans.Notes))
		}
	}

	switch action {
	case "continue":
		reopenForContinue(board, gaps)
		o.persistBoard(board)
		if runner != nil && board.AgentWorkRemaining() {
			o.emit("execute", "user/auto continue — corrective wave", "")
			o.emitLoop("execute", LoopEvent{
				Action: "continue_wave",
				Reason: "continuing — another corrective execute wave",
				From:   "test",
				To:     "execute",
			})
			ran, err := runner.RunCorrectiveBoard(ctx, board)
			if !ran {
				o.emit("execute", "continue wave skipped — max_waves budget exhausted", "")
			}
			if err != nil && !isCancelErr(err) {
				o.emit("execute", "continue wave warning: "+err.Error(), "")
			}
			snap := o.boardStore.Snapshot()
			board = &snap
			o.persistBoard(board)
		}
		// One more placeholder fill after continue wave.
		if o.cfg.PlaceholderPass {
			_ = o.runPlaceholderPass(ctx, query, board, runner)
		}
		return true, board
	case "flag_only":
		flagPreciseGaps(board, gaps)
		o.persistBoard(board)
		o.emit("test", "flag_only — precise gaps kept for human fill; not continuing", "")
		o.emitLoop("test", LoopEvent{
			Action: "flag_only",
			Reason: "keeping precise gaps for human fill — not restarting loop",
			From:   "test",
			To:     "done",
			Wave:   o.waveCounter,
		})
		return false, board
	default:
		flagPreciseGaps(board, gaps)
		o.persistBoard(board)
		o.emitLoop("test", LoopEvent{
			Action: "aborted",
			Reason: "user aborted — finishing with remaining work flagged",
			From:   "test",
			To:     "done",
			Wave:   o.waveCounter,
		})
		return false, board
	}
}

func reopenForContinue(board *plan.Board, gaps []quality.PreciseGap) {
	if board == nil {
		return
	}
	if len(gaps) > 0 {
		reopenPlaceholderTasks(board, gaps)
	}
	for i := range board.Tasks {
		t := &board.Tasks[i]
		t.Normalize()
		blob := strings.ToLower(t.Error + " " + t.Notes + " " + t.Review)
		if t.Column == plan.ColToScope || t.Column == plan.ColBlocked ||
			strings.Contains(blob, "escalated") || strings.Contains(blob, "max retries") ||
			strings.Contains(blob, "qa_gate") || strings.Contains(blob, "review rejected") ||
			strings.Contains(blob, "timeout") || strings.Contains(blob, "timed out") ||
			strings.Contains(blob, "deadline") {
			if t.Role == plan.RoleWorker || t.Role == plan.RoleCorrector || t.Role == "deep" ||
				plan.IsTesterRole(t.Role) || t.Role == plan.RolePlaceholder {
				t.Error = ""
				t.Notes = strings.TrimSpace(t.Notes + "\nREOPENED: continue wave after exhausted retries")
				t.MoveTo(plan.ColReadyToDev)
				board.Tasks[i] = *t
			}
		}
	}
}

func flagPreciseGaps(board *plan.Board, gaps []quality.PreciseGap) {
	if board == nil || len(gaps) == 0 {
		return
	}
	for i := range board.Tasks {
		t := &board.Tasks[i]
		for _, g := range gaps {
			for _, f := range t.Files {
				if f == g.Path {
					loc := g.Path
					if g.Line > 0 {
						loc = fmt.Sprintf("%s:%d", g.Path, g.Line)
					}
					t.Notes = strings.TrimSpace(t.Notes + "\nTODO(precise): " + loc + " — " + g.Reason)
					board.Tasks[i] = *t
				}
			}
		}
	}
}

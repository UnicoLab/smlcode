package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// EscalateHandler collects escalate decisions (tests / custom UIs).
type EscalateHandler func(ctx context.Context, ask plan.EscalateAsk) (plan.EscalateAnswer, error)

// escalateAskMu serializes escalate HITL. See runEscalateAsk for why.
var escalateAskMu sync.Mutex

// OnEscalate registers an escalate-ask callback.
func (o *Orchestrator) OnEscalate(h EscalateHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onEscalate = h
}

// runEscalateAsk pauses the calling task until the user acts (or timeout/auto).
// On timeout, @escalate (or configured expert) decides the action.
// Mutates board in place and persists.
func (o *Orchestrator) runEscalateAsk(ctx context.Context, board *plan.Board, t plan.Task, detail string) plan.EscalateAnswer {
	ans := plan.EscalateAnswer{Action: plan.EscalateActionReScope}
	if o == nil || o.cfg == nil || board == nil {
		return ans
	}
	mode := plan.NormalizeEscalateAsk(o.cfg.EscalateAsk)
	if o.cfg.AutoApprove {
		mode = plan.EscalateAskAuto
	}
	timeout := o.escalateAskTimeout()
	timeoutSec := int(timeout / time.Second)
	ask := plan.BuildEscalateAskWithCap(t, detail, timeoutSec, o.maxGateRetries())
	payload := plan.MarshalEscalateAskJSON(ask)
	_ = o.store.Append(contextstore.DocScratch, "Escalate ask",
		ask.Summary+"\n"+ask.Detail)

	if mode == plan.EscalateAskOff {
		o.emit("execute", fmt.Sprintf("escalate_ask=off — %s left in to_scope", t.ID), "")
		ans.Action = plan.EscalateActionReScope
		o.applyEscalate(board, t.ID, ans.Action, "")
		o.persistBoard(board)
		return ans
	}
	if mode == plan.EscalateAskAuto {
		ans.Action = plan.EscalateActionRetry
		// The cap applies to auto exactly as it does to a human answer.
		// Unattended runs are precisely where an uncapped "retry" turned into
		// 200 rounds of identical work.
		applied := o.applyEscalate(board, t.ID, ans.Action, "auto escalate → retry")
		o.emitFull("execute", stream.KindOutput, "escalate", t.ID,
			"auto-escalate: "+applied+" "+t.ID+" "+retryBudgetSuffix(ask), "", truncate(payload, 600))
		o.emitLoop("execute", LoopEvent{
			Action: "escalate_auto",
			Reason: "auto-" + applied + " after escalate — " + t.ID,
			From:   "execute",
			To:     "execute",
			Wave:   o.waveCounter,
		})
		ans.Action = applied
		o.persistBoard(board)
		return ans
	}

	// hitl.WriteAsk stores ONE pending ask per KIND, and OnEscalate fires from
	// inside reviewAndCorrect, which runs in N goroutines under ReviewParallel.
	// Two tasks escalating in the same wave therefore overwrote each other's
	// ask file, each then waited on its own (now-absent) ask ID, and at most
	// one could be answered: the loser burned the full timeout and spent an
	// extra LLM call in escalateTimeoutDecide, while hitl.Clear from the winner
	// dropped the loser's pending state entirely.
	//
	// A human answers one ask at a time anyway, so the asks are serialized:
	// each task gets the single ask slot to itself, start to finish.
	escalateAskMu.Lock()
	defer escalateAskMu.Unlock()
	if err := ctx.Err(); err != nil {
		ans.Action = plan.EscalateActionReScope
		ans.Notes = "run canceled while queued for escalate"
		o.applyEscalate(board, t.ID, ans.Action, ans.Notes)
		o.persistBoard(board)
		return ans
	}

	if err := hitl.WriteAsk(o.cfg.SlmDir(), "escalate", ask); err != nil {
		o.emitWarn("execute", "escalate ask unavailable — re-scoping task", err.Error())
		ans.Action = plan.EscalateActionReScope
		ans.Notes = "escalate ask could not be written: " + err.Error()
		o.applyEscalate(board, t.ID, ans.Action, ans.Notes)
		o.persistBoard(board)
		return ans
	}
	o.emitLoop("execute", LoopEvent{
		Action:   "escalate_pending",
		Reason:   ask.Summary,
		From:     "execute",
		To:       "execute",
		Awaiting: true,
		Wave:     o.waveCounter,
	})
	o.emitFull("execute", stream.KindAsk, "escalate", t.ID,
		fmt.Sprintf("%s escalated — choose %s %s (timeout %s → @%s decides)",
			t.ID, strings.Join(ask.Options, " / "), retryBudgetSuffix(ask), timeout, o.escalateDecideRole()),
		"", payload)
	o.emitFull("execute", stream.KindIntervention, "harness", t.ID,
		ask.Summary+" — waiting for your decision",
		"escalate", ask.Detail)

	got := false
	o.mu.Lock()
	h := o.onEscalate
	o.mu.Unlock()
	if h != nil {
		if a, err := h(ctx, ask); err == nil {
			a.AskID = ask.ID
			ans, got = a, true
		}
	}
	if !got {
		o.emit("execute", fmt.Sprintf("waiting for escalate decision on %s (timeout %s)", t.ID, timeout), "")
		ok, err := hitl.WaitAnswersForID(ctx, o.cfg.SlmDir(), "escalate", ask.ID, timeout, &ans)
		if err != nil {
			hitl.Clear(o.cfg.SlmDir(), "escalate")
			// Context canceled (user stop) — leave in backlog, do not spend an LLM turn.
			ans.Action = plan.EscalateActionReScope
			o.applyEscalate(board, t.ID, ans.Action, "escalate wait canceled")
			o.persistBoard(board)
			return ans
		}
		got = ok
	}
	hitl.Clear(o.cfg.SlmDir(), "escalate")
	if !got {
		role := o.escalateDecideRole()
		o.emit("execute", fmt.Sprintf("escalate timeout on %s — @%s deciding", t.ID, role), "")
		o.emitLoop("execute", LoopEvent{
			Action: "escalate_timeout",
			Reason: t.ID + " escalate timed out → @" + role + " decides",
			From:   "execute",
			To:     "execute",
			Wave:   o.waveCounter,
		})
		ans = o.escalateTimeoutDecide(ctx, board, t, detail)
	} else {
		ans.Action = plan.NormalizeEscalateAction(ans.Action)
		o.emitFull("execute", stream.KindOutput, "escalate", t.ID,
			"escalate answer: "+ans.Action, "", strings.TrimSpace(ans.Notes))
	}
	applied := o.applyEscalate(board, t.ID, ans.Action, ans.Notes)
	if applied != ans.Action {
		o.emitWarn("execute", fmt.Sprintf(
			"%s: retry refused — the escalate retry cap (%d) is spent; re-scoping instead",
			t.ID, ask.MaxGateRetries),
			"Answering retry again cannot change the outcome: the task has already been reopened "+
				"the maximum number of times with the same scope and the same acceptance. "+
				"Narrow the acceptance or fix the blocking evidence, then promote it back to ready_to_dev.")
		ans.Action = applied
	}
	o.persistBoard(board)
	o.emitLoop("execute", LoopEvent{
		Action: "escalate_resolved",
		Reason: t.ID + " → " + applied,
		From:   "execute",
		To:     "execute",
		Wave:   o.waveCounter,
	})
	return ans
}

// applyEscalate applies an escalate answer under the gate-retry cap and returns
// the action that was actually applied.
func (o *Orchestrator) applyEscalate(board *plan.Board, taskID, action, notes string) string {
	return plan.ApplyEscalateActionCapped(board, taskID, action, notes, o.maxGateRetries())
}

// maxGateRetries is the escalate-gate retry cap in force for this run.
func (o *Orchestrator) maxGateRetries() int {
	if o != nil && o.cfg != nil && o.cfg.EscalateMaxRetries > 0 {
		return o.cfg.EscalateMaxRetries
	}
	return plan.DefaultMaxGateRetries
}

// retryBudgetSuffix renders "(retry 1 of 2)" for the gate card and its events.
func retryBudgetSuffix(ask plan.EscalateAsk) string {
	return fmt.Sprintf("(gate retries used %d of %d)", ask.GateRetries, ask.MaxGateRetries)
}

// escalateDecideRole picks the specialist that arbitrates escalate timeouts.
func (o *Orchestrator) escalateDecideRole() string {
	if o != nil && o.cfg != nil {
		if id := strings.TrimSpace(o.cfg.EscalateTimeoutAgent); id != "" {
			return id
		}
	}
	// Prefer dedicated @escalate, then quality expert @reviewer, then coordinators.
	for _, id := range []string{plan.RoleEscalate, plan.RoleReviewer, "coordinator", "orchestrator"} {
		if o != nil && o.factory != nil {
			if _, err := o.factory.Create(id); err == nil {
				return id
			}
		}
	}
	return plan.RoleEscalate
}

// escalateTimeoutDecide asks the SLM arbitrator; falls back to a safe heuristic.
func (o *Orchestrator) escalateTimeoutDecide(ctx context.Context, board *plan.Board, t plan.Task, detail string) plan.EscalateAnswer {
	if o != nil && o.cfg != nil && o.cfg.DryRun {
		ans := plan.HeuristicEscalateDecide(t, detail)
		o.emitFull("execute", stream.KindOutput, "escalate", t.ID,
			"dry-run escalate timeout — heuristic: "+ans.Action, "", ans.Notes)
		return ans
	}
	role := o.escalateDecideRole()
	prompt := formatEscalateDecidePrompt(t, detail)
	o.emitAgent("execute", role, t.ID, "escalate timeout — SLM deciding", strings.Join(t.Files, ", "), "")
	out, err := o.runRoleTracked(ctx, role, t.ID, prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		ans := plan.HeuristicEscalateDecide(t, detail)
		o.emitFull("execute", stream.KindOutput, role, t.ID,
			"escalate SLM unavailable — heuristic: "+ans.Action, "", ans.Notes)
		return ans
	}
	o.emitFull("execute", stream.KindOutput, role, t.ID, "escalate decide", "", truncate(out, 800))
	if d, ok := plan.ParseEscalateDecide(out); ok {
		notes := strings.TrimSpace(d.Reason)
		if notes == "" {
			notes = "timeout SLM (@" + role + ") → " + d.Action
		} else {
			notes = "timeout SLM (@" + role + "): " + notes
		}
		return plan.EscalateAnswer{Action: d.Action, Notes: notes}
	}
	ans := plan.HeuristicEscalateDecide(t, detail)
	ans.Notes = strings.TrimSpace(ans.Notes + " (unparseable SLM output)")
	return ans
}

func formatEscalateDecidePrompt(t plan.Task, detail string) string {
	var b strings.Builder
	b.WriteString("Human escalate HITL timed out. Decide the next action for this task.\n\n")
	b.WriteString("## Task\n")
	fmt.Fprintf(&b, "- id: %s\n- title: %s\n- role: %s\n", t.ID, t.Title, t.Role)
	if len(t.Files) > 0 {
		b.WriteString("- files: " + strings.Join(t.Files, ", ") + "\n")
	}
	if strings.TrimSpace(t.Acceptance) != "" {
		b.WriteString("- acceptance: " + truncate(t.Acceptance, 400) + "\n")
	}
	if strings.TrimSpace(t.Error) != "" {
		b.WriteString("- error: " + truncate(t.Error, 300) + "\n")
	}
	if strings.TrimSpace(t.Review) != "" {
		b.WriteString("- review: " + truncate(t.Review, 500) + "\n")
	}
	if strings.TrimSpace(detail) != "" {
		b.WriteString("\n## Escalate detail\n")
		b.WriteString(truncate(detail, 1200))
		b.WriteString("\n")
	}
	if strings.TrimSpace(t.Output) != "" {
		b.WriteString("\n## Last output (truncated)\n")
		b.WriteString(truncate(t.Output, 1500))
		b.WriteString("\n")
	}
	b.WriteString("\nReturn STRICT JSON: {\"action\":\"retry|re_scope|abort|mark_done\",\"reason\":\"…\",\"confidence\":0.0-1.0}\n")
	return b.String()
}

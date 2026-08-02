package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// EscalateHandler collects escalate decisions (tests / custom UIs).
type EscalateHandler func(ctx context.Context, ask plan.EscalateAsk) (plan.EscalateAnswer, error)

// OnEscalate registers an escalate-ask callback.
func (o *Orchestrator) OnEscalate(h EscalateHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onEscalate = h
}

// runEscalateAsk pauses the calling task until the user acts (or timeout/auto).
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
	timeout := o.cfg.EscalateAskTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timeoutSec := int(timeout / time.Second)
	ask := plan.BuildEscalateAsk(t, detail, timeoutSec)
	payload := plan.MarshalEscalateAskJSON(ask)
	_ = o.store.Append(contextstore.DocScratch, "Escalate ask",
		ask.Summary+"\n"+ask.Detail)

	if mode == plan.EscalateAskOff {
		o.emit("execute", fmt.Sprintf("escalate_ask=off — %s left in to_scope", t.ID), "")
		ans.Action = plan.EscalateActionReScope
		plan.ApplyEscalateAction(board, t.ID, ans.Action, "")
		o.persistBoard(board)
		return ans
	}
	if mode == plan.EscalateAskAuto {
		o.emitFull("execute", stream.KindOutput, "escalate", t.ID,
			"auto-escalate: retry "+t.ID, "", truncate(payload, 600))
		o.emitLoop("execute", LoopEvent{
			Action: "escalate_auto",
			Reason: "auto-retry after escalate — " + t.ID,
			From:   "execute",
			To:     "execute",
			Wave:   o.waveCounter,
		})
		ans.Action = plan.EscalateActionRetry
		plan.ApplyEscalateAction(board, t.ID, ans.Action, "auto escalate → retry")
		o.persistBoard(board)
		return ans
	}

	_ = hitl.WriteAsk(o.cfg.SlmDir(), "escalate", ask)
	o.emitLoop("execute", LoopEvent{
		Action:   "escalate_pending",
		Reason:   ask.Summary,
		From:     "execute",
		To:       "execute",
		Awaiting: true,
		Wave:     o.waveCounter,
	})
	o.emitFull("execute", stream.KindAsk, "escalate", t.ID,
		fmt.Sprintf("%s escalated — choose re-scope / retry / mark done / abort (timeout %s)",
			t.ID, timeout),
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
			ans, got = a, true
		}
	}
	if !got {
		o.emit("execute", fmt.Sprintf("waiting for escalate decision on %s (timeout %s)", t.ID, timeout), "")
		ok, err := hitl.WaitAnswers(ctx, o.cfg.SlmDir(), "escalate", timeout, &ans)
		if err != nil {
			hitl.Clear(o.cfg.SlmDir(), "escalate")
			ans.Action = plan.EscalateActionReScope
			plan.ApplyEscalateAction(board, t.ID, ans.Action, "escalate wait canceled")
			o.persistBoard(board)
			return ans
		}
		got = ok
	}
	hitl.Clear(o.cfg.SlmDir(), "escalate")
	if !got {
		o.emit("execute", fmt.Sprintf("escalate timeout on %s — re_scope", t.ID), "")
		o.emitLoop("execute", LoopEvent{
			Action: "escalate_timeout",
			Reason: t.ID + " escalate timed out → re_scope",
			From:   "execute",
			To:     "execute",
			Wave:   o.waveCounter,
		})
		ans.Action = plan.EscalateActionReScope
	} else {
		ans.Action = plan.NormalizeEscalateAction(ans.Action)
		o.emitFull("execute", stream.KindOutput, "escalate", t.ID,
			"escalate answer: "+ans.Action, "", strings.TrimSpace(ans.Notes))
	}
	plan.ApplyEscalateAction(board, t.ID, ans.Action, ans.Notes)
	o.persistBoard(board)
	o.emitLoop("execute", LoopEvent{
		Action: "escalate_resolved",
		Reason: t.ID + " → " + ans.Action,
		From:   "execute",
		To:     "execute",
		Wave:   o.waveCounter,
	})
	return ans
}

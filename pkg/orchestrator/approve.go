package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// PlanApproveHandler collects plan go/replan decisions (tests / custom UIs).
type PlanApproveHandler func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error)

// OnPlanApprove registers a plan approval callback.
func (o *Orchestrator) OnPlanApprove(h PlanApproveHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onPlanApprove = h
}

// runPlanApprovalGate pauses before execute when plan_approve=ask (unless auto_approve).
// Returns (continueExecute, error). false means user requested replan (caller should stop).
func (o *Orchestrator) runPlanApprovalGate(ctx context.Context, query string, board *plan.Board) (bool, error) {
	mode := plan.NormalizePlanApprove(o.cfg.PlanApprove)
	if o.cfg.AutoApprove || mode == plan.PlanApproveModeOff {
		return true, nil
	}
	ask := plan.BuildPlanApproveAsk(query, board)
	payload := plan.MarshalPlanAskJSON(ask)

	if mode == plan.PlanApproveModeAuto {
		o.emitFull("plan", stream.KindOutput, "plan-approve", "",
			"plan ready (auto-approved)", "", truncate(payload, 1200))
		return true, nil
	}

	// ask mode
	_ = hitl.WriteAsk(o.cfg.SlmDir(), "plan", ask)
	o.emitFull("plan", stream.KindAsk, "plan-approve", "",
		fmt.Sprintf("approve plan? %d tasks — POST /api/plan/approve", ask.TaskCount),
		"", payload)

	var ans plan.PlanApproveAnswer
	got := false

	o.mu.Lock()
	h := o.onPlanApprove
	o.mu.Unlock()
	if h != nil {
		if a, err := h(ctx, ask); err == nil {
			ans, got = a, true
		}
	}
	if !got {
		timeout := o.cfg.PlanApproveTimeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		o.emit("plan", fmt.Sprintf("waiting for plan approval (timeout %s)", timeout), "")
		ok, err := hitl.WaitAnswers(ctx, o.cfg.SlmDir(), "plan", timeout, &ans)
		if err != nil {
			hitl.Clear(o.cfg.SlmDir(), "plan")
			return false, err
		}
		got = ok
	}
	hitl.Clear(o.cfg.SlmDir(), "plan")

	if !got {
		o.emit("plan", "plan approval timeout — auto-approving", "")
		return true, nil
	}
	if plan.IsPlanReplan(ans) {
		note := strings.TrimSpace(ans.Notes)
		msg := "user requested replan"
		if note != "" {
			msg += ": " + note
		}
		o.emit("plan", msg, "")
		return false, fmt.Errorf("%s", msg)
	}
	if !plan.IsPlanApproved(ans) {
		o.emit("plan", "plan not approved — stopping before execute", "")
		return false, fmt.Errorf("plan not approved")
	}
	o.emit("plan", "plan approved — starting execute", "")
	return true, nil
}

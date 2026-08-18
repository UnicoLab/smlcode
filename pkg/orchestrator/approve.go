package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/composer"
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
func (o *Orchestrator) runPlanApprovalGate(ctx context.Context, query string, board *plan.Board, validation ...*plan.ScopeJudgeResult) (bool, error) {
	mode := plan.NormalizePlanApprove(o.cfg.PlanApprove)
	if o.cfg.AutoApprove || mode == plan.PlanApproveModeOff {
		return true, nil
	}
	ask := plan.BuildPlanApproveAsk(query, board)
	if len(validation) > 0 && validation[0] != nil {
		ask.Validation = validation[0]
	}
	o.mu.Lock()
	if o.dynamicComposition != nil {
		ask.Composition = planCompositionSnapshot(*o.dynamicComposition)
	}
	o.mu.Unlock()
	timeout := o.cfg.PlanApproveTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ask.TimeoutS = int(timeout.Seconds())
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
			a.AskID = ask.ID
			ans, got = a, true
		}
	}
	if !got {
		o.emit("plan", fmt.Sprintf("waiting for plan approval (timeout %s)", timeout), "")
		ok, err := hitl.WaitAnswersForID(ctx, o.cfg.SlmDir(), "plan", ask.ID, timeout, &ans)
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
	if note := strings.TrimSpace(ans.Notes); note != "" && board != nil {
		board.Plan.Assumptions = append(board.Plan.Assumptions, "User plan note: "+note)
		o.persistBoard(board)
		o.emit("plan", "plan approved with user notes", "")
	}
	o.emit("plan", "plan approved — starting execute", "")
	return true, nil
}

func planCompositionSnapshot(c composer.Composition) *plan.PlanComposition {
	c.Normalize()
	out := &plan.PlanComposition{
		Summary:  c.Summary,
		Strategy: c.Strategy,
		Handoff:  append([]string{}, c.Handoff...),
		Execute: plan.PlanCompositionExecute{
			DefaultRole: c.Execute.DefaultRole,
			Reviewer:    c.Execute.Reviewer,
			Corrector:   c.Execute.Corrector,
			MaxWaves:    c.Execute.MaxWaves,
		},
	}
	for _, p := range c.Phases {
		out.Phases = append(out.Phases, plan.PlanCompositionPhase{
			ID: p.ID, Agent: p.Agent, Enabled: p.Enabled, When: p.When,
		})
	}
	for _, m := range c.Team {
		out.Team = append(out.Team, plan.PlanCompositionTeam{
			Role:   m.Role,
			Skills: append([]string{}, m.Skills...),
		})
	}
	for _, s := range c.Slots {
		out.Slots = append(out.Slots, plan.PlanCompositionSlot{
			ID:        s.ID,
			Agent:     s.Agent,
			Title:     s.Title,
			Before:    s.Before,
			After:     s.After,
			Replace:   s.Replace,
			When:      s.When,
			PersistTo: s.PersistTo,
			FailMode:  s.FailMode,
		})
	}
	if out.Summary == "" && len(out.Phases) == 0 && len(out.Team) == 0 && len(out.Slots) == 0 {
		return nil
	}
	return out
}

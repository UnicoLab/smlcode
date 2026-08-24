package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// PlanApproveHandler collects plan go/replan decisions (tests / custom UIs).
type PlanApproveHandler func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error)

type planApprovalDecision struct {
	Approved bool
	Replan   bool
	Notes    string
}

// OnPlanApprove registers a plan approval callback.
func (o *Orchestrator) OnPlanApprove(h PlanApproveHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onPlanApprove = h
}

// runPlanApprovalGate pauses before execute when plan_approve=ask (unless auto_approve).
// Returns (continueExecute, error). false means user requested replan (caller should stop).
func (o *Orchestrator) runPlanApprovalGate(ctx context.Context, query string, board *plan.Board, validation ...*plan.ScopeJudgeResult) (bool, error) {
	decision, err := o.runPlanApprovalDecision(ctx, query, board, validation...)
	if err != nil {
		return false, err
	}
	if decision.Replan {
		msg := "user requested replan"
		if decision.Notes != "" {
			msg += ": " + decision.Notes
		}
		return false, fmt.Errorf("%s", msg)
	}
	return decision.Approved, nil
}

func (o *Orchestrator) runPlanApprovalDecision(ctx context.Context, query string, board *plan.Board, validation ...*plan.ScopeJudgeResult) (planApprovalDecision, error) {
	mode := plan.NormalizePlanApprove(o.cfg.PlanApprove)
	if o.cfg.AutoApprove || mode == plan.PlanApproveModeOff {
		return planApprovalDecision{Approved: true}, nil
	}
	ask := plan.BuildPlanApproveAsk(query, board)
	if len(validation) > 0 && validation[0] != nil {
		ask.Validation = validation[0]
	}
	o.mu.Lock()
	if o.dynamicComposition != nil {
		prof := config.ResolveModelProfile(o.cfg.ModelProfiles, o.cfg.Model)
		ask.Composition = planCompositionSnapshot(*o.dynamicComposition, o.cfg.DynamicPipeline, prof.ContextLimit)
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
		return planApprovalDecision{Approved: true}, nil
	}

	// ask mode, but nobody is attached who could answer.
	//
	// Decide HERE rather than build the ask, hand it to a handler that will
	// refuse it on the operator's behalf, and throw a finished plan away. The
	// same decision was already announced at run start by GatePreflight, so
	// this can never surprise anyone: a headless run either said "auto-
	// approving plan gate" at t=0 or refused to start at all.
	if o.headlessAutoApproves() {
		o.emitFull("plan", stream.KindOutput, "plan-approve", "",
			"no TTY: auto-approving plan gate (override with --on-gate-timeout=stop)",
			"", truncate(payload, 1200))
		return planApprovalDecision{Approved: true}, nil
	}

	// ask mode
	if err := hitl.WriteAsk(o.cfg.SlmDir(), "plan", ask); err != nil {
		return planApprovalDecision{}, fmt.Errorf("write plan approval ask: %w", err)
	}
	o.emitFull("plan", stream.KindAsk, "plan-approve", "",
		fmt.Sprintf("approve plan? %d tasks — POST /api/plan/approve", ask.TaskCount),
		"", payload)

	var ans plan.PlanApproveAnswer
	got := false

	o.mu.Lock()
	h := o.onPlanApprove
	o.mu.Unlock()
	// A subscriber is anything that COULD have answered: the CLI registers
	// OnPlanApprove/OnContinue/OnEscalate/OnAsk and intercepts before this
	// path, while the Studio/REST path relies on the pending-ask file plus an
	// attached event listener.
	subscribed := o.Subscribed()
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
			return planApprovalDecision{}, err
		}
		got = ok
	}
	hitl.Clear(o.cfg.SlmDir(), "plan")

	if !got {
		// P14: the gate used to fail OPEN — a timeout auto-approved the plan and
		// execute started with nobody having looked at it. That is only
		// defensible when there was no subscriber that COULD have answered.
		switch o.planApproveOnTimeout() {
		case PlanTimeoutApprove:
			o.emitWarn("plan", "plan approval timeout — auto-approving (plan_approve_on_timeout=approve)", "")
			return planApprovalDecision{Approved: true}, nil
		case PlanTimeoutReject:
			o.emitProblem("plan", "plan approval timeout — NOT approving (plan_approve_on_timeout=reject)", "")
			o.emitRetainedWork("plan", board)
			return planApprovalDecision{}, o.stoppedAtGateError(
				fmt.Sprintf("plan approval timed out after %s and plan_approve_on_timeout=reject", timeout))
		default: // PlanTimeoutAuto
			if subscribed {
				o.emitProblem("plan",
					"plan approval timeout with a listener attached — NOT auto-approving; re-run or set plan_approve=off", "")
				o.emitRetainedWork("plan", board)
				return planApprovalDecision{}, o.stoppedAtGateError(
					fmt.Sprintf("plan approval timed out after %s with no answer", timeout))
			}
			o.emitWarn("plan", "plan approval timeout with no listener attached — auto-approving", "")
			return planApprovalDecision{Approved: true}, nil
		}
	}
	if plan.IsPlanReplan(ans) {
		note := strings.TrimSpace(ans.Notes)
		msg := "user requested replan"
		if note != "" {
			msg += ": " + note
		}
		o.emit("plan", msg, "")
		return planApprovalDecision{Replan: true, Notes: note}, nil
	}
	if !plan.IsPlanApproved(ans) {
		o.emitProblem("plan", "plan not approved — stopping before execute", "")
		// Nothing is discarded: the board, PLAN.md and TASKS.md this run
		// produced are on disk and resumable. Say so, with the command.
		o.emitRetainedWork("plan", board)
		return planApprovalDecision{}, o.stoppedAtGateError("plan not approved")
	}
	if note := strings.TrimSpace(ans.Notes); note != "" && board != nil {
		board.Plan.Assumptions = append(board.Plan.Assumptions, "User plan note: "+note)
		o.persistBoard(board)
		o.emit("plan", "plan approved with user notes", "")
	}
	o.emit("plan", "plan approved — starting execute", "")
	return planApprovalDecision{Approved: true, Notes: strings.TrimSpace(ans.Notes)}, nil
}

func planCompositionSnapshot(c composer.Composition, dynamicEnabled bool, contextLimit int) *plan.PlanComposition {
	c.Normalize()
	out := &plan.PlanComposition{
		Summary:  c.Summary,
		Strategy: c.Strategy,
		Handoff:  append([]string{}, c.Handoff...),
		SLMFit:   composer.FitHints(c, dynamicEnabled, contextLimit),
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

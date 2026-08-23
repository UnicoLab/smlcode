package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Human-in-the-loop gates, wired to the terminal.
//
// The orchestrator has always exposed OnPlanApprove / OnContinue / OnEscalate /
// OnAsk, but nothing outside the tests registered them: the CLI's advice to a
// terminal user was literally "POST /api/plan/approve", and plan_approve_timeout
// then AUTO-APPROVED after two minutes. These handlers render each gate inline
// and, when a TTY is attached, block instead of timing out.

// gateHost is what a gate handler needs from the interactive session.
type gateHost interface {
	AskGate(ctx context.Context, g cli.Gate) (cli.GateAnswer, bool)
}

// nonInteractivePolicy resolves --on-gate-timeout for headless runs.
func nonInteractivePolicy() cli.GateTimeoutPolicy {
	p, ok := cli.ParseGateTimeoutPolicy(flagGateTimeout)
	if !ok {
		return cli.GateTimeoutStop
	}
	return p
}

// resolveHeadless picks an answer for a gate with no human attached.
func resolveHeadless(g cli.Gate) cli.GateAnswer {
	switch nonInteractivePolicy() {
	case cli.GateTimeoutApprove:
		for _, o := range g.Options {
			switch o.Value {
			case "approve", "continue", "retry":
				return cli.GateAnswer{Value: o.Value, Notes: "auto-approved (--on-gate-timeout=approve)"}
			}
		}
	case cli.GateTimeoutReject:
		for _, o := range g.Options {
			switch o.Value {
			case "reject", "replan", "abort", "stop":
				return cli.GateAnswer{Value: o.Value, Notes: "auto-rejected (--on-gate-timeout=reject)"}
			}
		}
	}
	return cli.GateAnswer{Value: g.NonTTYDefault, Notes: "no TTY — --on-gate-timeout=stop"}
}

// askGate routes a gate to the terminal when possible, otherwise to the policy.
func askGate(ctx context.Context, host gateHost, g cli.Gate) cli.GateAnswer {
	if host != nil && cli.IsInteractive() {
		if ans, ok := host.AskGate(ctx, g); ok {
			return ans
		}
		// Context canceled mid-gate: treat as a stop, never an approval.
		return cli.GateAnswer{Value: g.NonTTYDefault, Notes: "interrupted"}
	}
	return resolveHeadless(g)
}

// registerGates wires every HITL hook to the terminal for one harness.
func registerGates(h *harness.Harness, host gateHost) {
	if h == nil || h.Orchestrator == nil {
		return
	}
	o := h.Orchestrator

	o.OnPlanApprove(func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		g := cli.PlanGate(ask.ID, ask.Query, ask.Summary, ask.Goals, ask.Tasks, ask.TaskCount)
		ans := askGate(ctx, host, g)
		decision := ans.Value
		switch decision {
		case "approve", "replan":
		case "reject":
			decision = "reject" // IsPlanReplan treats reject as replan-ish; keep explicit
		default:
			decision = "replan"
		}
		return plan.PlanApproveAnswer{
			AskID:      ask.ID,
			Decision:   decision,
			Notes:      ans.Notes,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	})

	o.OnContinue(func(ctx context.Context, ask plan.ContinueAsk) (plan.ContinueAnswer, error) {
		g := cli.ContinueGate(ask.ID, ask.Reason, ask.Summary, ask.Gaps, ask.Escalated)
		ans := askGate(ctx, host, g)
		return plan.ContinueAnswer{
			AskID:      ask.ID,
			Action:     plan.NormalizeContinueAction(ans.Value),
			Notes:      ans.Notes,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	})

	o.OnEscalate(func(ctx context.Context, ask plan.EscalateAsk) (plan.EscalateAnswer, error) {
		g := cli.EscalateGate(ask.ID, ask.TaskID, ask.Title, ask.Detail, ask.Files)
		ans := askGate(ctx, host, g)
		return plan.EscalateAnswer{
			AskID:      ask.ID,
			Action:     plan.NormalizeEscalateAction(ans.Value),
			Notes:      ans.Notes,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	})

	o.OnAsk(func(ctx context.Context, ask plan.ScopeAsk) (plan.ScopeAnswers, error) {
		out := plan.ScopeAnswers{
			AskID:      ask.ID,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}
		if !cli.IsInteractive() || host == nil {
			out.UseAllRec = true
			out.Notes = "no TTY — recommended defaults applied"
			return out, nil
		}
		for i, q := range ask.Questions {
			labels := make([]string, 0, len(q.Options))
			for _, o := range q.Options {
				label := o.Label
				if o.Description != "" {
					label += " — " + o.Description
				}
				labels = append(labels, label)
			}
			rec := q.Recommended
			if rec == "" {
				for _, o := range q.Options {
					if o.Recommended {
						rec = o.Label
						break
					}
				}
			}
			id := q.ID
			if id == "" {
				id = fmt.Sprintf("q%d", i+1)
			}
			g := cli.ClarifyGate(ask.ID+":"+id, q.Question, labels, rec)
			ans := askGate(ctx, host, g)
			switch ans.Value {
			case "__recommended__", "":
				if rec != "" {
					out.Answers = append(out.Answers, plan.ScopeAnswer{QuestionID: id, Selected: []string{rec}})
				}
			case "__freeform__":
				out.Answers = append(out.Answers, plan.ScopeAnswer{QuestionID: id, Freeform: ans.Notes})
			default:
				// ClarifyGate values carry the "label — description" form.
				sel := ans.Value
				if i := strings.Index(sel, " — "); i >= 0 {
					sel = sel[:i]
				}
				out.Answers = append(out.Answers, plan.ScopeAnswer{
					QuestionID: id, Selected: []string{sel}, Comment: ans.Notes,
				})
			}
		}
		if len(out.Answers) == 0 {
			out.UseAllRec = true
		}
		return out, nil
	})
}

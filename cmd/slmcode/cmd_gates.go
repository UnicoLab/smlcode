package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
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

// prepareHeadlessGates resolves this run's HITL gate policy BEFORE the first
// model call, and refuses the run outright when a gate it is guaranteed to
// reach cannot be answered.
//
// This is the fix for the worst failure mode the CLI had: a headless
// `slmcode run` spent its ENTIRE budget on explore → compose → plan → split →
// scope-judge (9m17s in the reported case), reached the plan gate, resolved it
// to "not approved" instantly because there was no TTY, and printed one line —
// "plan not approved — stopping before execute". Nine minutes of compute, a
// green scope judge, a valid board, and zero code written.
//
// A run that cannot finish now costs zero seconds and says which flag to set.
func prepareHeadlessGates(cmd *cobra.Command, h *harness.Harness) error {
	gateTimeoutExplicit = flagChanged(cmd, "on-gate-timeout")
	if h == nil || h.Orchestrator == nil {
		return nil
	}
	// One TTY probe, in the layer that owns the terminal. The engine is told.
	h.Orchestrator.SetHeadlessGates(orchestrator.HeadlessGates{
		Headless: !cli.IsInteractive(),
		Policy:   orchestrator.NormalizeHeadlessPolicy(string(nonInteractivePolicy())),
		Explicit: gateTimeoutExplicit,
	})
	// The decisions themselves are emitted by the engine at run start, so they
	// land in the transcript (and events.jsonl) with everything else. Only the
	// refusal is rendered here — there is no run to attach it to.
	if _, err := h.Orchestrator.GatePreflight(); err != nil {
		var blocked *orchestrator.GateBlockedError
		if errors.As(err, &blocked) {
			fmt.Println(cli.Warn(blocked.Reason))
			fmt.Println()
			fmt.Println(cli.Dim("  pick one:"))
			for _, r := range blocked.Remedies {
				fmt.Println(cli.Dim("    " + r))
			}
			return failf(6, "refusing to start: the %s gate cannot be answered with no TTY attached", blocked.Gate)
		}
		return err
	}
	return nil
}

// flagChanged reports whether the operator actually passed a flag, checking the
// command's own set and the root's persistent set (--on-gate-timeout lives on
// the root, so a subcommand sees it only through inheritance).
func flagChanged(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return true
	}
	if root := cmd.Root(); root != nil {
		if f := root.PersistentFlags().Lookup(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}

// retainedRunHint names what a stopped run KEPT and the command that resumes it.
//
// Stopping at a gate is allowed; losing the work is not. pkg/session writes
// board.json, PLAN.md, TASKS.md and QUERY.md under .slmcode/queries/<runID>/ as
// the board is built — before any gate opens — and `slmcode session resume
// <runID>` continues from that checkpoint instead of replanning from zero. The
// CLI used to say "stopping before execute" and leave the user to guess.
func retainedRunHint(slmDir string) []string {
	turns, err := session.ListQueries(slmDir)
	if err != nil || len(turns) == 0 {
		return nil
	}
	t := turns[0]
	if len(t.Board.Tasks) == 0 {
		return nil
	}
	noun := "tasks"
	if len(t.Board.Tasks) == 1 {
		noun = "task"
	}
	return []string{
		fmt.Sprintf("nothing was discarded — %d planned %s are on disk and resumable:", len(t.Board.Tasks), noun),
		"  " + session.TurnDir(slmDir, t.ID) + cli.Dim("   board.json · PLAN.md · TASKS.md"),
		"  slmcode session resume " + t.ID + cli.Dim("   continue from the plan, no replanning"),
	}
}

// printRetainedRunHint renders retainedRunHint, or nothing when there is no
// board worth pointing at.
func printRetainedRunHint(slmDir string) {
	lines := retainedRunHint(slmDir)
	if len(lines) == 0 {
		return
	}
	fmt.Println()
	for _, l := range lines {
		fmt.Println(cli.Dim("  " + l))
	}
}

// gateTimeoutExplicit records whether the operator actually typed
// --on-gate-timeout, as opposed to inheriting its "stop" default.
//
// The distinction is the whole fix: the default must not punish the headless
// case (do all the work, then discard it), but an explicit choice is a choice
// and is never overridden. Set once by prepareHeadlessGates; a run that never
// calls it (the TUI, chat) keeps the old behavior because it has a human.
var gateTimeoutExplicit bool

// nonInteractivePolicy resolves the literal --on-gate-timeout value.
func nonInteractivePolicy() cli.GateTimeoutPolicy {
	p, ok := cli.ParseGateTimeoutPolicy(flagGateTimeout)
	if !ok {
		return cli.GateTimeoutStop
	}
	return p
}

// effectiveGatePolicy is what an unanswerable gate actually does.
//
// A headless invocation is BY DEFINITION one where nobody can answer, and the
// operator asked for work to be done — so the default resolves to "approve"
// rather than "stop". `--on-gate-timeout=stop` still stops.
func effectiveGatePolicy() cli.GateTimeoutPolicy {
	if gateTimeoutExplicit {
		return nonInteractivePolicy()
	}
	if !cli.IsInteractive() {
		return cli.GateTimeoutApprove
	}
	return nonInteractivePolicy()
}

// gateAudit records what happened to the gates in one run so the CLI can turn
// a headless refusal into a message that says what to do about it.
type gateAudit struct {
	unanswered  []string // gate kinds resolved by policy instead of a human
	interrupted bool     // the user aborted AT a gate (Ctrl-C / Esc)
	// overrides names the tasks a human force-marked done at the escalate
	// gate. Answering [d]one moves a task the evidence gate REFUSED into the
	// done column, and the run summary then reported "1/1 tasks done, 0
	// failed" with no trace that a human had waved it through. A summary that
	// cannot distinguish "the harness verified this" from "you told the
	// harness to stop asking" is not a summary of the run.
	overrides []string
}

// noteOverride records a human forcing one task done.
func (a *gateAudit) noteOverride(taskID string) {
	if a == nil || taskID == "" {
		return
	}
	for _, id := range a.overrides {
		if id == taskID {
			return
		}
	}
	a.overrides = append(a.overrides, taskID)
}

func (a *gateAudit) note(kind string) {
	if a == nil {
		return
	}
	for _, k := range a.unanswered {
		if k == kind {
			return
		}
	}
	a.unanswered = append(a.unanswered, kind)
}

// blocked reports whether any gate went unanswered.
func (a *gateAudit) blocked() bool { return a != nil && len(a.unanswered) > 0 }

// gateConfigKey maps a gate kind onto the config key that switches it off.
var gateConfigKey = map[string]string{
	"plan":     "plan_approve",
	"continue": "continue_ask",
	"escalate": "escalate_ask",
	"clarify":  "clarify_mode",
}

// hint is the actionable next step for a run that stopped at a gate.
func (a *gateAudit) hint() string {
	if !a.blocked() {
		return ""
	}
	noun := "gate"
	if len(a.unanswered) > 1 {
		noun = "gates"
	}
	lines := []string{
		"the " + strings.Join(a.unanswered, " and ") + " " + noun +
			" needed a human and none was attached (--on-gate-timeout=" +
			string(effectiveGatePolicy()) + ")",
		"run the same command on a terminal to answer inline, or choose a headless policy:",
		"  slmcode run --on-gate-timeout=approve \"…\"   answer every gate with yes",
	}
	for _, kind := range a.unanswered {
		if key, ok := gateConfigKey[kind]; ok {
			lines = append(lines, "  slmcode config set "+key+" auto"+
				strings.Repeat(" ", maxInt(1, 22-len(key)))+"stop asking (this project)")
		}
	}
	return strings.Join(lines, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// resolveHeadless picks an answer for a gate with no human attached.
//
// The "stop" policy must produce a decision the engine reads as STOP. It used
// to hand back the gate's NonTTYDefault, which for the plan gate is "reject" —
// and plan.IsPlanReplan counts "reject" as a replan request, so a headless run
// replanned three times and died with "plan replan limit reached", having spent
// three planner+splitter round-trips to reach a stop it could have taken at the
// first gate.
func resolveHeadless(audit *gateAudit, g cli.Gate) cli.GateAnswer {
	policy := effectiveGatePolicy()
	switch policy {
	case cli.GateTimeoutApprove:
		for _, o := range g.Options {
			switch o.Value {
			case "approve", "continue", "retry":
				note := "auto-approved (--on-gate-timeout=approve)"
				if !gateTimeoutExplicit {
					note = "no TTY: auto-approved (override with --on-gate-timeout=stop)"
				}
				return cli.GateAnswer{Value: o.Value, Notes: note}
			}
		}
	case cli.GateTimeoutReject:
		for _, o := range g.Options {
			switch o.Value {
			case "reject", "replan", "abort", "stop":
				audit.note(g.Kind)
				return cli.GateAnswer{Value: o.Value, Notes: "auto-rejected (--on-gate-timeout=reject)"}
			}
		}
	}
	audit.note(g.Kind)
	return cli.GateAnswer{
		Value: g.NonTTYDefault,
		Notes: "not answered (no terminal attached; --on-gate-timeout=" +
			string(policy) + ")",
	}
}

// askGate routes a gate to the terminal when possible, otherwise to the policy.
func askGate(ctx context.Context, audit *gateAudit, host gateHost, g cli.Gate) cli.GateAnswer {
	if host != nil && cli.IsInteractive() {
		if ans, ok := host.AskGate(ctx, g); ok {
			if ans.Value == cli.GateInterrupted {
				// A deliberate abort, not an unanswerable gate. Raw mode ate
				// the SIGINT, so this is the only signal we get.
				audit.interrupted = true
				return cli.GateAnswer{Value: g.NonTTYDefault, Notes: "interrupted at the gate"}
			}
			return ans
		}
		// Context canceled mid-gate: treat as a stop, never an approval. A real
		// interrupt is NOT an unanswered gate — recording it would turn a
		// Ctrl-C into exit 6 ("gate could not be answered") plus a page of
		// advice about --on-gate-timeout, when the honest answer is 130.
		if ctx.Err() == nil {
			audit.note(g.Kind)
		}
		return cli.GateAnswer{Value: g.NonTTYDefault, Notes: "interrupted"}
	}
	return resolveHeadless(audit, g)
}

// registerGates wires every HITL hook to the terminal for one harness.
//
// host is the live dashboard when there is one. A nil host on an interactive
// terminal is upgraded to a plain terminal prompt rather than falling through
// to the headless policy — `slmcode run` has no dashboard but does have a
// human, and telling that human "no TTY" was the worst lie in the CLI.
func registerGates(h *harness.Harness, host gateHost) *gateAudit {
	audit := &gateAudit{}
	if h == nil || h.Orchestrator == nil {
		return audit
	}
	if host == nil && cli.IsInteractive() {
		host = cli.NewTerminalGateHost()
	}
	o := h.Orchestrator

	o.OnPlanApprove(func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		g := cli.PlanGate(ask.ID, ask.Query, ask.Summary, ask.Goals, ask.Tasks, ask.TaskCount)
		ans := askGate(ctx, audit, host, g)
		// "no" and "replan" are two different answers and must stay different.
		// plan.IsPlanReplan() counts BOTH "reject" and "replan" as a replan
		// request, so forwarding "reject" made [n]o a synonym for [r]eplan and
		// looped the planner until the revision limit. Anything that is not an
		// approval or an explicit replan is forwarded as "stop", which the
		// orchestrator reads as "not approved" and acts on once.
		decision := planDecisionFor(ans.Value)
		return plan.PlanApproveAnswer{
			AskID:      ask.ID,
			Decision:   decision,
			Notes:      ans.Notes,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	})

	o.OnContinue(func(ctx context.Context, ask plan.ContinueAsk) (plan.ContinueAnswer, error) {
		g := cli.ContinueGate(ask.ID, ask.Reason, ask.Summary, ask.Gaps, ask.Escalated)
		ans := askGate(ctx, audit, host, g)
		return plan.ContinueAnswer{
			AskID:      ask.ID,
			Action:     plan.NormalizeContinueAction(ans.Value),
			Notes:      ans.Notes,
			AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	})

	o.OnEscalate(func(ctx context.Context, ask plan.EscalateAsk) (plan.EscalateAnswer, error) {
		g := cli.EscalateGate(ask.ID, ask.TaskID, ask.Title, ask.Detail, ask.Files)
		ans := askGate(ctx, audit, host, g)
		action := plan.NormalizeEscalateAction(ans.Value)
		notes := ans.Notes
		if action == plan.EscalateActionMarkDone {
			audit.noteOverride(ask.TaskID)
			// Stamp the board too, so `slmcode task show` and every later
			// `slmcode board` still say who closed this task — the audit
			// above only lives as long as this process.
			notes = strings.TrimSpace("HUMAN OVERRIDE: forced done at the escalate gate " +
				"(the evidence gate had refused this task)\n" + notes)
		}
		return plan.EscalateAnswer{
			AskID:      ask.ID,
			Action:     action,
			Notes:      notes,
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
			out.Notes = "no terminal attached — recommended defaults applied"
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
			ans := askGate(ctx, audit, host, g)
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

	return audit
}

// planDecisionFor maps a gate answer onto the decision the orchestrator reads.
//
// "no" and "replan" are two different answers and must stay different.
// plan.IsPlanReplan() counts BOTH "reject" and "replan" as a replan request,
// so forwarding "reject" made [n]o a synonym for [r]eplan and looped the
// planner until the revision limit. Anything that is not an approval or an
// explicit replan becomes "stop", which the orchestrator reads as "not
// approved" and acts on once.
func planDecisionFor(answer string) string {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "approve":
		return "approve"
	case "replan":
		return "replan"
	}
	return "stop"
}

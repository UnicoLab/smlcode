package orchestrator

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// Headless runs: what every human-in-the-loop gate does when there is no human.
//
// A headless run used to spend its ENTIRE budget planning and then throw the
// result away: plan_approve defaults to "ask", --on-gate-timeout defaults to
// "stop", and with no TTY the gate resolved to "not approved" the instant it
// opened. A live run spent 9m17s producing a green scope judge and a valid
// two-task board, wrote zero lines of code, and printed one line: "plan not
// approved — stopping before execute".
//
// Three rules fix that, and this file owns all three:
//
//  1. The decision is taken at RUN START, before the first model call. A run
//     that cannot possibly finish refuses immediately; a run that will resolve
//     its own gates says so up front.
//  2. Headless is not a reason to stop. Nobody can answer, and the operator
//     asked for work to be done, so a CONVENIENCE gate (plan / clarify /
//     continue / escalate) answers itself with "yes" and logs the decision. An
//     EXPLICIT --on-gate-timeout=stop|reject still stops — never override a
//     choice the operator made on purpose.
//  3. A SAFETY gate is not a convenience gate. shell_permission=ask exists to
//     keep an unattended agent from running arbitrary commands, so it never
//     auto-approves: it fails fast at run start instead.

// HeadlessPolicy is what an unanswerable gate does.
type HeadlessPolicy = string

// Headless gate policies. These are the --on-gate-timeout values.
const (
	// HeadlessApprove answers every convenience gate with "yes".
	HeadlessApprove HeadlessPolicy = "approve"
	// HeadlessReject answers every gate with "no".
	HeadlessReject HeadlessPolicy = "reject"
	// HeadlessStop stops the run at the first gate.
	HeadlessStop HeadlessPolicy = "stop"
)

// NormalizeHeadlessPolicy maps an operator string onto a policy.
func NormalizeHeadlessPolicy(s string) HeadlessPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approve", "yes", "auto":
		return HeadlessApprove
	case "reject", "no":
		return HeadlessReject
	default:
		return HeadlessStop
	}
}

// HeadlessGates is what the process driving this orchestrator knows about the
// human on the other end of a gate.
//
// Known distinguishes "the CLI looked at the terminal and told us" from "nobody
// said": an unset value falls back on Subscribed(), which is the engine's own
// long-standing notion of headless (see config.PlanTimeoutAuto — "approves only
// when NO event subscriber was attached"). There is exactly one TTY probe in
// this codebase, cli.IsInteractive, and it lives in the layer that owns the
// terminal; the engine is told the answer rather than probing a second time.
type HeadlessGates struct {
	// Known is true once SetHeadlessGates has been called.
	Known bool
	// Headless is true when no human can answer a gate in this process.
	Headless bool
	// Policy is the operator's non-interactive gate choice.
	Policy HeadlessPolicy
	// Explicit is true when the operator chose Policy on purpose
	// (--on-gate-timeout was passed) rather than inheriting the default.
	Explicit bool
}

// SetHeadlessGates records the caller's view of the human on the other end.
func (o *Orchestrator) SetHeadlessGates(g HeadlessGates) {
	if o == nil {
		return
	}
	g.Known = true
	g.Policy = NormalizeHeadlessPolicy(g.Policy)
	o.mu.Lock()
	o.headless = g
	o.mu.Unlock()
}

// HeadlessGates reports the effective headless view for this run.
func (o *Orchestrator) HeadlessGates() HeadlessGates {
	if o == nil {
		return HeadlessGates{Headless: true, Policy: HeadlessApprove}
	}
	o.mu.Lock()
	g := o.headless
	o.mu.Unlock()
	if g.Known {
		return g
	}
	// Nobody told us. Reuse the engine's own rule rather than inventing a
	// second one: a run with no subscriber at all has no UI that could have
	// answered, which is exactly what "headless" means here.
	return HeadlessGates{Headless: !o.Subscribed(), Policy: HeadlessApprove}
}

// headlessAutoApproves reports whether a CONVENIENCE gate should answer itself
// with "yes" instead of stranding the run.
//
// False for an attended run (a human is there to answer) and false when the
// operator explicitly asked for stop/reject.
func (o *Orchestrator) headlessAutoApproves() bool {
	g := o.HeadlessGates()
	if !g.Headless {
		return false
	}
	if o.conf().AutoApprove {
		return true
	}
	// An explicit stop|reject is a choice and is never overridden.
	return !g.Explicit || g.Policy == HeadlessApprove
}

// GateDecision is one row of the headless decision table, recorded at run start
// so the transcript says what WILL happen before the budget is spent.
type GateDecision struct {
	// Gate is the gate kind: plan | clarify | continue | escalate.
	Gate string
	// Behavior is what it will do: auto-approve | recommended | continue | retry.
	Behavior string
	// Detail is the one-line explanation shown to the operator.
	Detail string
}

// String renders one decision as the line the CLI and the event log print.
func (d GateDecision) String() string {
	return "no TTY: " + d.Detail
}

// GateBlockedError is a run refused at the door because a gate it is
// guaranteed to reach cannot be answered and must not be answered for you.
//
// It carries the remedy, because "plan not approved" with no next step is the
// defect this whole file exists to remove.
type GateBlockedError struct {
	// Gate is the gate kind that cannot be resolved.
	Gate string
	// Reason is what makes it unanswerable.
	Reason string
	// Remedies are the exact commands that make the run possible.
	Remedies []string
}

func (e *GateBlockedError) Error() string {
	var b strings.Builder
	b.WriteString(e.Reason)
	for _, r := range e.Remedies {
		b.WriteString("\n  ")
		b.WriteString(r)
	}
	return b.String()
}

// ExitCode maps a refusal onto the documented "gate could not be answered" code.
func (e *GateBlockedError) ExitCode() int { return 6 }

// GatePreflight resolves every human-in-the-loop gate against the headless
// policy BEFORE the first model call.
//
// It returns the decisions taken (to be logged) or an error naming the exact
// flag to set. Doing minutes of planning and THEN discovering the gate cannot
// be answered is the one behavior this function exists to make impossible.
func (o *Orchestrator) GatePreflight() ([]GateDecision, error) {
	if o == nil || o.cfg == nil {
		return nil, nil
	}
	g := o.HeadlessGates()
	if !g.Headless {
		// A human is attached: every gate is answerable, nothing to decide.
		return nil, nil
	}
	c := o.cfg

	// SAFETY GATE FIRST — and it never auto-approves.
	//
	// shell_permission=ask exists so an UNATTENDED agent cannot run arbitrary
	// commands. Headless is precisely the unattended case, so answering it with
	// "yes" would delete the gate rather than resolve it. The run also cannot
	// proceed: every ws_shell call would wait out shell_ask_timeout and come
	// back denied, so the agents would silently skip every build and test.
	if permissions.NormalizeShell(c.ShellPermission) == permissions.ShellAsk && !c.AutoApprove {
		return nil, &GateBlockedError{
			Gate: "shell",
			Reason: "no TTY attached and shell_permission=ask: every shell command would wait " +
				"out shell_ask_timeout and come back denied, so builds and tests would be " +
				"silently skipped. A shell-permission gate is a safety gate and is never " +
				"auto-approved — choose a policy instead of leaving it to a timeout",
			Remedies: []string{
				"slmcode config set shell_permission allow   let this project run commands",
				"slmcode config set shell_permission deny    refuse them, and say so in every task",
				"run the same command on a terminal to approve each command inline",
			},
		}
	}

	autoApprove := o.headlessAutoApproves()
	var out []GateDecision

	// PLAN GATE — the one that burned 9m17s of planning and discarded it.
	if plan.NormalizePlanApprove(c.PlanApprove) == plan.PlanApproveModeAsk && !c.AutoApprove {
		if !autoApprove {
			return nil, &GateBlockedError{
				Gate: "plan",
				Reason: fmt.Sprintf("no TTY attached, plan_approve=ask and --on-gate-timeout=%s: "+
					"this run would explore, plan and split for minutes and then stop at the plan "+
					"gate with nothing executed. Refusing now instead of spending the budget first",
					g.Policy),
				Remedies: []string{
					"slmcode run --on-gate-timeout=approve \"…\"   answer every gate with yes",
					"slmcode config set plan_approve auto        stop asking (this project)",
					"run the same command on a terminal to answer the gate inline",
				},
			}
		}
		out = append(out, GateDecision{
			Gate:     "plan",
			Behavior: "auto-approve",
			Detail:   "auto-approving plan gate (override with --on-gate-timeout=stop)",
		})
	}

	// CLARIFY GATE — never strands a run (its non-TTY answer is "use the
	// recommended option"), but headless it should not force an interview
	// round-trip the operator cannot answer either.
	if plan.NormalizeClarifyMode(c.ClarifyMode) == plan.ClarifyAsk && !c.AutoApprove {
		out = append(out, GateDecision{
			Gate:     "clarify",
			Behavior: "recommended",
			Detail:   "clarify gate answers itself with the recommended options (clarify_mode=ask)",
		})
	}

	// CONTINUE GATE — retries exhausted with work left. Headless "stop" is the
	// silent variant of the same defect: work done, nobody told why it stopped.
	if plan.NormalizeContinueAsk(c.ContinueAsk) == plan.ContinueAskAsk && !c.AutoApprove {
		if autoApprove {
			out = append(out, GateDecision{
				Gate:     "continue",
				Behavior: "continue",
				Detail:   "continue gate runs one more corrective wave instead of stopping (continue_ask=ask)",
			})
		} else {
			out = append(out, GateDecision{
				Gate:     "continue",
				Behavior: "stop",
				Detail: "continue gate stops with remaining work flagged (--on-gate-timeout=" +
					g.Policy + ")",
			})
		}
	}

	// ESCALATE GATE — per-task, and bounded by escalate_max_retries either way.
	if plan.NormalizeEscalateAsk(c.EscalateAsk) == plan.EscalateAskAsk && !c.AutoApprove {
		if autoApprove {
			out = append(out, GateDecision{
				Gate:     "escalate",
				Behavior: "retry",
				Detail: "escalate gate retries within the escalate_max_retries budget " +
					"instead of waiting for a human (escalate_ask=ask)",
			})
		} else {
			out = append(out, GateDecision{
				Gate:     "escalate",
				Behavior: "re_scope",
				Detail: "escalate gate parks each stuck task in to_scope (--on-gate-timeout=" +
					g.Policy + ")",
			})
		}
	}
	return out, nil
}

// preflightGates refuses a run that a gate is guaranteed to strand.
//
// Call it as early as a run can call anything: its whole value is that it costs
// nothing and happens before the first model call.
func (o *Orchestrator) preflightGates() error {
	if _, err := o.GatePreflight(); err != nil {
		// The refusal is a run-level problem, not a warning buried mid-phase.
		o.emitProblem("init", strings.SplitN(err.Error(), "\n", 2)[0], "")
		return err
	}
	return nil
}

// emitGateDecisions records what each gate will do.
//
// Called once the query turn exists, so the decisions land in
// .slmcode/queries/<runID>/events.jsonl — the file an operator reads when a
// run behaved in a way they did not expect. A decision only the terminal saw
// is a decision a piped run cannot audit afterwards.
func (o *Orchestrator) emitGateDecisions() {
	decisions, err := o.GatePreflight()
	if err != nil {
		return // already refused by preflightGates
	}
	for _, d := range decisions {
		o.emitWarn("init", d.String(), "")
	}
}

// headlessGateMode downgrades one gate's mode for a headless run.
//
// It returns the mode the gate should actually use, WITHOUT touching o.cfg:
// the config is shared with the harness and outlives the run, and a Studio
// process that ran one headless job must not inherit its gate policy forever.
func (o *Orchestrator) headlessGateMode(configured, ask, auto string) string {
	if configured != ask {
		return configured
	}
	if o.headlessAutoApproves() {
		return auto
	}
	return configured
}

// ── Retained work ──────────────────────────────────────────────────────────

// retainedWorkHint names the artifacts a stopped run KEPT and the command that
// picks them back up.
//
// A gate that stops a run after planning has not lost the plan: pkg/session
// wrote board.json, PLAN.md, TASKS.md and QUERY.md under
// .slmcode/queries/<runID>/ before the gate ever opened, and
// `slmcode session resume <runID>` continues from that checkpoint. Saying
// "stopping before execute" and nothing else taught users the work was gone.
func (o *Orchestrator) retainedWorkHint() string {
	if o == nil || o.cfg == nil {
		return ""
	}
	turn := o.turn()
	if turn == nil || turn.ID == "" {
		return ""
	}
	return "plan retained in " + session.TurnDir(o.cfg.SlmDir(), turn.ID) +
		" (board.json, PLAN.md, TASKS.md) — resume with: slmcode session resume " + turn.ID
}

// emitRetainedWork tells the operator, on the stop path, that nothing was lost.
func (o *Orchestrator) emitRetainedWork(phase string, board *plan.Board) {
	hint := o.retainedWorkHint()
	if hint == "" {
		return
	}
	n := 0
	if board != nil {
		n = len(board.Tasks)
	}
	o.emitWarn(phase, fmt.Sprintf("%d task(s) already planned are kept, not discarded — %s", n, hint), "")
}

// stoppedAtGateError is the error a stopped gate returns: the reason, plus what
// was retained and how to resume it.
func (o *Orchestrator) stoppedAtGateError(reason string) error {
	if hint := o.retainedWorkHint(); hint != "" {
		return fmt.Errorf("%s — %s", reason, hint)
	}
	return fmt.Errorf("%s", reason)
}

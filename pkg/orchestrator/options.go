package orchestrator

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Knobs this layer needs that pkg/config does not carry yet.
//
// pkg/config is owned by another agent, so every knob below resolves from an
// environment variable with a documented default. Each one is listed in the
// hand-off report with the config field name, type and default it should
// become; when the field lands, replace the body of the accessor with the
// config read and keep the env var as an override.
const (
	envArchitectEditor = "SLMCODE_ARCHITECT_EDITOR" // architect_editor bool, default false
	envEvolve          = "SLMCODE_EVOLVE"           // evolve bool, default true
	envDeterministic   = "SLMCODE_DETERMINISTIC"    // deterministic bool, default false
	//nolint:gosec // G101 false positive: an env var NAME, not a credential.
	envMemoryTokens       = "SLMCODE_MEMORY_TOKENS"        // memory_tokens int, default 300
	envMaxTaskCalls       = "SLMCODE_MAX_TASK_CALLS"       // max_task_calls int, default 6
	envReadWindowLines    = "SLMCODE_READ_WINDOW_LINES"    // read_window_lines int, default 0 (tool default)
	envMaxToolChars       = "SLMCODE_MAX_TOOL_CHARS"       // max_tool_chars int, default 0 (tool default)
	envShellTimeout       = "SLMCODE_SHELL_TIMEOUT"        // shell_timeout duration, default 0 (tool default)
	envDisableSyntaxCheck = "SLMCODE_DISABLE_SYNTAX_CHECK" // disable_syntax_check bool, default false
	envPlanApproveTimeout = "SLMCODE_PLAN_APPROVE_ON_TIMEOUT"
	envEscalateTimeout    = "SLMCODE_ESCALATE_ASK_TIMEOUT" // escalate_ask_timeout duration
	envRegressionChecks   = "SLMCODE_REGRESSION_CHECKS"    // regression_checks bool, default true
)

// DefaultEscalateAskTimeout is this layer's floor for the escalate HITL wait.
//
// pkg/config ships 30s, which is far too short for a mode literally named
// "ask": a human has to notice the prompt, read the task and choose. Every
// expiry costs an extra LLM call in escalateTimeoutDecide, so a short timeout
// is not even cheap. 5 minutes matches plan_approve/clarify.
const DefaultEscalateAskTimeout = 5 * time.Minute

// DefaultQAGateRounds is the round count the repair loop needs to be reachable.
// pkg/config ships 1, which makes round==max true on the first iteration.
const DefaultQAGateRounds = 3

// DefaultMemoryTokens is the budget for the injected memory block.
const DefaultMemoryTokens = 300

// DefaultMaxTaskCalls is the per-task LLM call budget handed to loop.Runner.
const DefaultMaxTaskCalls = 6

func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// architectEditorEnabled reports whether the describer→editor pipeline runs.
// Off by default: it doubles the LLM calls for a task and only pays off when
// the two halves are pointed at different models.
func (o *Orchestrator) architectEditorEnabled() bool {
	return envBool(envArchitectEditor, false)
}

// evolveEnabled reports whether the self-improvement engine is opened.
func (o *Orchestrator) evolveEnabled() bool {
	return envBool(envEvolve, true)
}

// deterministicMode disables bandit exploration (CI, --no-explore).
// DryRun implies it: a dry run must not move the policy.
func (o *Orchestrator) deterministicMode() bool {
	if o != nil && o.cfg != nil && o.cfg.DryRun {
		return true
	}
	return envBool(envDeterministic, false)
}

// memoryTokens is the token budget for the injected memory block.
func (o *Orchestrator) memoryTokens() int {
	if n := envInt(envMemoryTokens, DefaultMemoryTokens); n > 0 {
		return n
	}
	return DefaultMemoryTokens
}

// maxTaskCalls is the per-task LLM call budget.
func (o *Orchestrator) maxTaskCalls() int {
	if n := envInt(envMaxTaskCalls, DefaultMaxTaskCalls); n > 0 {
		return n
	}
	return DefaultMaxTaskCalls
}

// regressionChecksEnabled reports whether stored regression checks are replayed
// around the QA gate.
func (o *Orchestrator) regressionChecksEnabled() bool {
	return envBool(envRegressionChecks, true)
}

// escalateAskTimeout is the escalate HITL wait.
//
// config ships 30s, which is far too short for a mode literally named "ask":
// a human has to notice the prompt, read the task and choose, and every expiry
// costs an extra LLM call in escalateTimeoutDecide. So an UNSET value — zero,
// or exactly the shipped default, which are indistinguishable after
// config.Normalize — is raised to DefaultEscalateAskTimeout. Any other value
// is a deliberate choice and is honored exactly, including a shorter one.
func (o *Orchestrator) escalateAskTimeout() time.Duration {
	d := DefaultEscalateAskTimeout
	if o != nil && o.cfg != nil {
		switch cur := o.cfg.EscalateAskTimeout; {
		case cur <= 0, cur == config.DefaultEscalateAskTimeout:
			// unset / shipped default → raise
		default:
			d = cur
		}
	}
	return envDuration(envEscalateTimeout, d)
}

// qaGateRounds is the QA gate round budget, floored at DefaultQAGateRounds so
// the diagnose/fix pass is reachable under the shipped config default of 1.
func (o *Orchestrator) qaGateRounds() int {
	if o == nil || o.cfg == nil {
		return DefaultQAGateRounds
	}
	if o.cfg.QAGateMaxRounds > DefaultQAGateRounds {
		return o.cfg.QAGateMaxRounds
	}
	return DefaultQAGateRounds
}

// PlanApproveOnTimeout values.
const (
	// PlanTimeoutApprove auto-approves when nobody answers (legacy default).
	PlanTimeoutApprove = "approve"
	// PlanTimeoutReject stops before execute when nobody answers.
	PlanTimeoutReject = "reject"
	// PlanTimeoutAuto approves only when NO subscriber was attached — i.e. when
	// there was no UI that could have answered. This is the default.
	PlanTimeoutAuto = "auto"
)

// planApproveOnTimeout resolves the on-timeout policy for the plan gate.
func (o *Orchestrator) planApproveOnTimeout() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envPlanApproveTimeout)))
	switch v {
	case PlanTimeoutApprove, PlanTimeoutReject, PlanTimeoutAuto:
		return v
	default:
		return PlanTimeoutAuto
	}
}

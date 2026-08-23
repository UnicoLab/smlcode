package orchestrator

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Per-run knobs.
//
// Each accessor below resolves in one direction: config field first (which
// already carries the full defaults → user file → project file → env → flag
// chain from pkg/config), then the legacy SLMCODE_* variable as a last-resort
// override for the handful of names that predate the config fields. Keeping
// the env var live means a script or CI job that set one before these fields
// existed still works.
const (
	envArchitectEditor = "SLMCODE_ARCHITECT_EDITOR"
	envEvolve          = "SLMCODE_EVOLVE"
	envDeterministic   = "SLMCODE_DETERMINISTIC"
	//nolint:gosec // G101 false positive: an env var NAME, not a credential.
	envMemoryTokens       = "SLMCODE_MEMORY_TOKENS"
	envMaxTaskCalls       = "SLMCODE_MAX_TASK_CALLS"
	envReadWindowLines    = "SLMCODE_READ_WINDOW_LINES"
	envMaxToolChars       = "SLMCODE_MAX_TOOL_CHARS"
	envShellTimeout       = "SLMCODE_SHELL_TIMEOUT"
	envDisableSyntaxCheck = "SLMCODE_DISABLE_SYNTAX_CHECK"
	envPlanApproveTimeout = "SLMCODE_PLAN_APPROVE_ON_TIMEOUT"
	envEscalateTimeout    = "SLMCODE_ESCALATE_ASK_TIMEOUT"
	envRegressionChecks   = "SLMCODE_REGRESSION_CHECKS"
	envQABootstrap        = "SLMCODE_QA_BOOTSTRAP"
	envStructuredDecoding = "SLMCODE_STRUCTURED_DECODING"
)

// Defaults re-exported from pkg/config so callers in this package keep a single
// spelling. The values themselves live with the config field they belong to.
const (
	// DefaultEscalateAskTimeout is the escalate HITL wait.
	DefaultEscalateAskTimeout = config.DefaultEscalateAskTimeout
	// DefaultQAGateRounds is the round count the repair loop needs to be reachable.
	DefaultQAGateRounds = config.DefaultQAGateRounds
	// DefaultMemoryTokens is the budget for the injected memory block.
	DefaultMemoryTokens = config.DefaultMemoryTokens
	// DefaultMaxTaskCalls is the per-task LLM call budget handed to loop.Runner.
	DefaultMaxTaskCalls = config.DefaultMaxTaskCalls
)

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

// cfg returns the orchestrator's config, or a defaulted one when the
// orchestrator was built without it (tests, embedders).
func (o *Orchestrator) conf() *config.Config {
	if o != nil && o.cfg != nil {
		return o.cfg
	}
	return config.Default("")
}

// architectEditorEnabled reports whether the describer→editor pipeline runs.
// Off by default: it doubles the LLM calls for a task and only pays off when
// the two halves are pointed at different models.
func (o *Orchestrator) architectEditorEnabled() bool {
	return envBool(envArchitectEditor, o.conf().ArchitectEditor)
}

// evolveEnabled reports whether the self-improvement engine is opened.
func (o *Orchestrator) evolveEnabled() bool {
	return envBool(envEvolve, o.conf().Evolve)
}

// deterministicMode disables bandit exploration (CI, --no-explore).
// DryRun implies it: a dry run must not move the policy.
func (o *Orchestrator) deterministicMode() bool {
	c := o.conf()
	if c.DryRun {
		return true
	}
	return envBool(envDeterministic, c.Deterministic)
}

// memoryTokens is the token budget for the injected memory block.
func (o *Orchestrator) memoryTokens() int {
	if n := envInt(envMemoryTokens, o.conf().MemoryTokens); n > 0 {
		return n
	}
	return DefaultMemoryTokens
}

// maxTaskCalls is the per-task LLM call budget.
func (o *Orchestrator) maxTaskCalls() int {
	if n := envInt(envMaxTaskCalls, o.conf().MaxTaskCalls); n > 0 {
		return n
	}
	return DefaultMaxTaskCalls
}

// regressionChecksEnabled reports whether stored regression checks are replayed
// around the QA gate.
func (o *Orchestrator) regressionChecksEnabled() bool {
	return envBool(envRegressionChecks, o.conf().RegressionChecks)
}

// syntaxCheckDisabled reports whether post-edit syntax verification is off.
func (o *Orchestrator) syntaxCheckDisabled() bool {
	return envBool(envDisableSyntaxCheck, o.conf().DisableSyntaxCheck)
}

// readWindowLines is the ws_read window size (0 = the tool's own default).
func (o *Orchestrator) readWindowLines() int {
	return envInt(envReadWindowLines, o.conf().ReadWindowLines)
}

// maxToolChars caps a tool result (0 = the tool's own default).
func (o *Orchestrator) maxToolChars() int {
	return envInt(envMaxToolChars, o.conf().MaxToolChars)
}

// shellTimeout is the ws_shell per-command timeout (0 = the tool's default).
func (o *Orchestrator) shellTimeout() time.Duration {
	return envDuration(envShellTimeout, o.conf().ShellTimeout)
}

// StructuredDecoding reports the constrained-decoding policy (auto | off).
//
// "off" pins every role to prompt-only JSON; "auto" lets pkg/backends
// negotiate the strongest mechanism the endpoint confirms (json_schema,
// guided_json, GBNF). Exported because the enforcement point lives in
// pkg/backends' capability negotiation, not in this package.
func (o *Orchestrator) StructuredDecoding() string {
	v := strings.TrimSpace(os.Getenv(envStructuredDecoding))
	if v == "" {
		return config.NormalizeStructuredDecoding(o.conf().StructuredDecoding)
	}
	return config.NormalizeStructuredDecoding(v)
}

// QABootstrapMode reports whether the QA gate may run dependency installers
// against agent-authored manifests (off | ask | auto). Exported because the
// gate that consumes it lives in pkg/quality.
func (o *Orchestrator) QABootstrapMode() string {
	v := strings.TrimSpace(os.Getenv(envQABootstrap))
	if v == "" {
		return config.NormalizeQABootstrap(o.conf().QABootstrap)
	}
	return config.NormalizeQABootstrap(v)
}

// escalateAskTimeout is the escalate HITL wait.
func (o *Orchestrator) escalateAskTimeout() time.Duration {
	d := o.conf().EscalateAskTimeout
	if d <= 0 {
		d = DefaultEscalateAskTimeout
	}
	return envDuration(envEscalateTimeout, d)
}

// qaGateRounds is the QA gate round budget. Below 2 the diagnose/fix pass is
// unreachable (the loop compares round == max on entry), so a stored 1 — which
// every project written before the default changed still carries — is raised.
func (o *Orchestrator) qaGateRounds() int {
	if n := o.conf().QAGateMaxRounds; n > DefaultQAGateRounds {
		return n
	}
	return DefaultQAGateRounds
}

// PlanApproveOnTimeout values, re-exported from pkg/config.
const (
	// PlanTimeoutApprove auto-approves when nobody answers (legacy default).
	PlanTimeoutApprove = config.PlanTimeoutApprove
	// PlanTimeoutReject stops before execute when nobody answers.
	PlanTimeoutReject = config.PlanTimeoutReject
	// PlanTimeoutAuto approves only when NO subscriber was attached — i.e. when
	// there was no UI that could have answered. This is the default.
	PlanTimeoutAuto = config.PlanTimeoutAuto
)

// planApproveOnTimeout resolves the on-timeout policy for the plan gate.
func (o *Orchestrator) planApproveOnTimeout() string {
	if v := strings.TrimSpace(os.Getenv(envPlanApproveTimeout)); v != "" {
		return config.NormalizePlanApproveOnTimeout(v)
	}
	return config.NormalizePlanApproveOnTimeout(o.conf().PlanApproveOnTimeout)
}

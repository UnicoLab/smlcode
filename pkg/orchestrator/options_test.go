package orchestrator

import (
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// The knobs below used to resolve from environment variables only, so a
// config.yaml could not reach them at all. Each accessor now reads the config
// field first and keeps the legacy SLMCODE_* variable as an override.
func TestOptionAccessorsReadConfigWithEnvOverride(t *testing.T) {
	tests := []struct {
		name   string
		tune   func(*config.Config)
		env    map[string]string
		got    func(*Orchestrator) any
		want   any
		wantEv any // value with the env override applied
	}{
		{
			name: "evolve",
			tune: func(c *config.Config) { c.Evolve = false },
			env:  map[string]string{envEvolve: "true"},
			got:  func(o *Orchestrator) any { return o.evolveEnabled() },
			want: false, wantEv: true,
		},
		{
			name: "deterministic",
			tune: func(c *config.Config) { c.Deterministic = true },
			env:  map[string]string{envDeterministic: "false"},
			got:  func(o *Orchestrator) any { return o.deterministicMode() },
			want: true, wantEv: false,
		},
		{
			name: "architect_editor",
			tune: func(c *config.Config) { c.ArchitectEditor = true },
			env:  map[string]string{envArchitectEditor: "false"},
			got:  func(o *Orchestrator) any { return o.architectEditorEnabled() },
			want: true, wantEv: false,
		},
		{
			name: "regression_checks",
			tune: func(c *config.Config) { c.RegressionChecks = false },
			env:  map[string]string{envRegressionChecks: "true"},
			got:  func(o *Orchestrator) any { return o.regressionChecksEnabled() },
			want: false, wantEv: true,
		},
		{
			name: "memory_tokens",
			tune: func(c *config.Config) { c.MemoryTokens = 512 },
			env:  map[string]string{envMemoryTokens: "700"},
			got:  func(o *Orchestrator) any { return o.memoryTokens() },
			want: 512, wantEv: 700,
		},
		{
			name: "max_task_calls",
			tune: func(c *config.Config) { c.MaxTaskCalls = 9 },
			env:  map[string]string{envMaxTaskCalls: "3"},
			got:  func(o *Orchestrator) any { return o.maxTaskCalls() },
			want: 9, wantEv: 3,
		},
		{
			name: "read_window_lines",
			tune: func(c *config.Config) { c.ReadWindowLines = 40 },
			env:  map[string]string{envReadWindowLines: "80"},
			got:  func(o *Orchestrator) any { return o.readWindowLines() },
			want: 40, wantEv: 80,
		},
		{
			name: "max_tool_chars",
			tune: func(c *config.Config) { c.MaxToolChars = 6000 },
			env:  map[string]string{envMaxToolChars: "1200"},
			got:  func(o *Orchestrator) any { return o.maxToolChars() },
			want: 6000, wantEv: 1200,
		},
		{
			name: "shell_timeout",
			tune: func(c *config.Config) { c.ShellTimeout = 45 * time.Second },
			env:  map[string]string{envShellTimeout: "2m"},
			got:  func(o *Orchestrator) any { return o.shellTimeout() },
			want: 45 * time.Second, wantEv: 2 * time.Minute,
		},
		{
			name: "disable_syntax_check",
			tune: func(c *config.Config) { c.DisableSyntaxCheck = true },
			env:  map[string]string{envDisableSyntaxCheck: "false"},
			got:  func(o *Orchestrator) any { return o.syntaxCheckDisabled() },
			want: true, wantEv: false,
		},
		{
			name: "escalate_ask_timeout",
			tune: func(c *config.Config) { c.EscalateAskTimeout = 90 * time.Second },
			env:  map[string]string{envEscalateTimeout: "3m"},
			got:  func(o *Orchestrator) any { return o.escalateAskTimeout() },
			want: 90 * time.Second, wantEv: 3 * time.Minute,
		},
		{
			name: "plan_approve_on_timeout",
			tune: func(c *config.Config) { c.PlanApproveOnTimeout = PlanTimeoutReject },
			env:  map[string]string{envPlanApproveTimeout: PlanTimeoutApprove},
			got:  func(o *Orchestrator) any { return o.planApproveOnTimeout() },
			want: PlanTimeoutReject, wantEv: PlanTimeoutApprove,
		},
		{
			name: "structured_decoding",
			tune: func(c *config.Config) { c.StructuredDecoding = config.DecodingOff },
			env:  map[string]string{envStructuredDecoding: "auto"},
			got:  func(o *Orchestrator) any { return o.StructuredDecoding() },
			want: config.DecodingOff, wantEv: config.DecodingAuto,
		},
		{
			name: "qa_bootstrap",
			tune: func(c *config.Config) { c.QABootstrap = config.QABootstrapAuto },
			env:  map[string]string{envQABootstrap: "off"},
			got:  func(o *Orchestrator) any { return o.QABootstrapMode() },
			want: config.QABootstrapAuto, wantEv: config.QABootstrapOff,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			tc.tune(cfg)
			o := &Orchestrator{cfg: cfg}

			if got := tc.got(o); got != tc.want {
				t.Fatalf("config value not honored: got %v, want %v", got, tc.want)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := tc.got(o); got != tc.wantEv {
				t.Fatalf("env override not honored: got %v, want %v", got, tc.wantEv)
			}
		})
	}
}

// The repair loop compares round == max on entry, so a stored 1 leaves the
// diagnose/fix pass unreachable. Every project written before the default
// changed still carries that 1.
func TestQAGateRoundsFloorKeepsTheRepairLoopReachable(t *testing.T) {
	for _, tc := range []struct {
		stored int
		want   int
	}{
		{stored: 0, want: DefaultQAGateRounds},
		{stored: 1, want: DefaultQAGateRounds},
		{stored: 3, want: DefaultQAGateRounds},
		{stored: 7, want: 7},
	} {
		cfg := config.Default(t.TempDir())
		cfg.QAGateMaxRounds = tc.stored
		o := &Orchestrator{cfg: cfg}
		if got := o.qaGateRounds(); got != tc.want {
			t.Errorf("stored %d → %d, want %d", tc.stored, got, tc.want)
		}
	}
	if DefaultQAGateRounds < 2 {
		t.Fatal("a round budget below 2 makes the repair pass unreachable")
	}
}

// A dry run must never move the learned policy, whatever the config says.
func TestDryRunForcesDeterministic(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Deterministic = false
	cfg.DryRun = true
	o := &Orchestrator{cfg: cfg}
	if !o.deterministicMode() {
		t.Fatal("dry_run must imply deterministic")
	}
}

// Accessors are called on a zero orchestrator in a few paths; none may panic.
func TestAccessorsTolerateAMissingConfig(t *testing.T) {
	var o *Orchestrator
	if !o.evolveEnabled() {
		t.Error("evolve should default on")
	}
	if o.memoryTokens() != config.DefaultMemoryTokens {
		t.Error("memory tokens should fall back to the shipped default")
	}
	if o.qaGateRounds() != DefaultQAGateRounds {
		t.Error("qa gate rounds should fall back to the shipped default")
	}
	if o.planApproveOnTimeout() != PlanTimeoutAuto {
		t.Error("plan timeout policy should default to auto")
	}
}

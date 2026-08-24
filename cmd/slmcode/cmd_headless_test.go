package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// The CLI half of the headless-gate fix: which policy an unanswerable gate
// resolves to, when the run is refused at the door, and what a stopped run is
// told about the work it kept.

// The flag's "stop" DEFAULT must not punish the headless case. Nobody can
// answer, and the operator asked for work to be done, so an unset flag on a
// headless run resolves to approve — and says so.
func TestUnsetGateFlagAutoApprovesWhenHeadless(t *testing.T) {
	setGateFlag(t, "stop", false) // the cobra default, never typed

	if got := effectiveGatePolicy(); got != cli.GateTimeoutApprove {
		t.Fatalf("headless default policy = %v, want approve", got)
	}
	audit := &gateAudit{}
	ans := resolveHeadless(audit, cli.PlanGate("i", "q", "s", nil, []string{"T1"}, 1))
	if ans.Value != "approve" {
		t.Fatalf("headless default must approve the plan gate, got %+v", ans)
	}
	if audit.blocked() {
		t.Fatal("an auto-approved gate is not an unanswered gate")
	}
	if !strings.Contains(ans.Notes, "no TTY") ||
		!strings.Contains(ans.Notes, "--on-gate-timeout=stop") {
		t.Fatalf("the decision must be logged and name the override: %q", ans.Notes)
	}
}

// An EXPLICIT stop is a choice and is never overridden.
func TestExplicitStopBeatsTheHeadlessDefault(t *testing.T) {
	setGateFlag(t, "stop", true)

	if got := effectiveGatePolicy(); got != cli.GateTimeoutStop {
		t.Fatalf("explicit policy = %v, want stop", got)
	}
	audit := &gateAudit{}
	ans := resolveHeadless(audit, cli.PlanGate("i", "q", "s", nil, []string{"T1"}, 1))
	if ans.Value == "approve" {
		t.Fatalf("an explicit stop must not approve: %+v", ans)
	}
	if !audit.blocked() {
		t.Fatal("an explicit stop leaves the gate unanswered")
	}
}

// nonInteractivePolicy still reports the literal flag value — effectiveGatePolicy
// is the one that layers the headless default on top.
func TestEffectiveAndLiteralPolicyStaySeparate(t *testing.T) {
	setGateFlag(t, "stop", false)
	if nonInteractivePolicy() != cli.GateTimeoutStop {
		t.Fatal("nonInteractivePolicy must keep reporting the flag as written")
	}
}

// prepareHeadlessGates refuses a headless run whose explicit policy would stop
// it — at the door, with exit code 6 and an actionable message.
func TestPrepareHeadlessGatesRefusesExplicitStop(t *testing.T) {
	setGateFlag(t, "stop", false) // prepareHeadlessGates recomputes explicitness
	h, root, runCommand := gateTestHarness(t, func(c *config.Config) {
		c.PlanApprove = plan.PlanApproveModeAsk
	})
	if err := root.PersistentFlags().Set("on-gate-timeout", "stop"); err != nil {
		t.Fatal(err)
	}

	err := prepareHeadlessGates(runCommand, h)
	if err == nil {
		t.Fatal("an explicit --on-gate-timeout=stop on a headless run must refuse up front")
	}
	if code := exitCodeFor(err); code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Errorf("the refusal must say it refused to start: %v", err)
	}
	if !gateTimeoutExplicit {
		t.Error("prepareHeadlessGates must record that the flag was typed")
	}
}

// With the flag untouched, the same headless run is allowed to start and the
// engine is told to resolve its own gates.
func TestPrepareHeadlessGatesAllowsTheDefault(t *testing.T) {
	setGateFlag(t, "stop", true) // proves prepareHeadlessGates recomputes it
	h, _, runCommand := gateTestHarness(t, func(c *config.Config) {
		c.PlanApprove = plan.PlanApproveModeAsk
	})

	if err := prepareHeadlessGates(runCommand, h); err != nil {
		t.Fatalf("a default headless run must be allowed to start: %v", err)
	}
	if gateTimeoutExplicit {
		t.Error("an untouched flag is not explicit")
	}
	g := h.Orchestrator.HeadlessGates()
	if !g.Known {
		t.Fatal("the engine must be told about the terminal, not left to guess")
	}
	if g.Explicit {
		t.Error("HeadlessGates.Explicit must mirror the flag")
	}
	// `go test` runs with a piped stdout, so this process IS headless.
	if !g.Headless {
		t.Error("a piped run is headless")
	}
}

// The shell-permission gate fails fast and never auto-approves, even when the
// operator asked for --on-gate-timeout=approve.
func TestPrepareHeadlessGatesRefusesShellAskGate(t *testing.T) {
	setGateFlag(t, "approve", false)
	h, root, runCommand := gateTestHarness(t, func(c *config.Config) {
		c.ShellPermission = permissions.ShellAsk
		c.PlanApprove = plan.PlanApproveModeOff
	})
	if err := root.PersistentFlags().Set("on-gate-timeout", "approve"); err != nil {
		t.Fatal(err)
	}

	err := prepareHeadlessGates(runCommand, h)
	if err == nil {
		t.Fatal("shell_permission=ask must refuse a headless run, never auto-approve it")
	}
	if code := exitCodeFor(err); code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	if !strings.Contains(err.Error(), "shell gate") {
		t.Errorf("the refusal must name the shell gate: %v", err)
	}
}

// retainedRunHint names the artifacts a stopped run kept, and the exact command
// that resumes them — the "never discard completed work silently" rule.
func TestRetainedRunHintNamesArtifactsAndResumeCommand(t *testing.T) {
	slmDir := filepath.Join(t.TempDir(), ".slmcode")
	turn, err := session.BeginTurn(slmDir, "run-42", "implement the median function")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: "run-42",
		Query:   "implement the median function",
		Plan:    plan.Plan{Summary: "add Median"},
		Tasks:   []plan.Task{{ID: "T1", Title: "add Median"}, {ID: "T2", Title: "test it"}},
	}
	if err := session.SaveTurnBoard(slmDir, turn, board); err != nil {
		t.Fatal(err)
	}

	lines := cli.StripANSI(strings.Join(retainedRunHint(slmDir), "\n"))
	for _, want := range []string{
		"nothing was discarded",
		"2 planned tasks",
		session.TurnDir(slmDir, "run-42"),
		"board.json",
		"PLAN.md",
		"TASKS.md",
		"slmcode session resume run-42",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("retained hint is missing %q:\n%s", want, lines)
		}
	}
}

// No board, no claim: the hint must not promise artifacts that do not exist.
func TestRetainedRunHintIsSilentWithoutABoard(t *testing.T) {
	slmDir := filepath.Join(t.TempDir(), ".slmcode")
	if got := retainedRunHint(slmDir); got != nil {
		t.Fatalf("no queries dir at all → no hint, got %v", got)
	}
	if _, err := session.BeginTurn(slmDir, "run-empty", "q"); err != nil {
		t.Fatal(err)
	}
	if got := retainedRunHint(slmDir); got != nil {
		t.Fatalf("a turn with no tasks has nothing to resume, got %v", got)
	}
}

// flagChanged has to see a flag typed on the ROOT's persistent set, which is
// where --on-gate-timeout lives; a subcommand only inherits it.
func TestFlagChangedSeesInheritedPersistentFlags(t *testing.T) {
	_, root, sub := gateTestHarness(t, nil)
	if flagChanged(sub, "on-gate-timeout") {
		t.Fatal("an untouched flag is not changed")
	}
	if err := root.PersistentFlags().Set("on-gate-timeout", "approve"); err != nil {
		t.Fatal(err)
	}
	if !flagChanged(sub, "on-gate-timeout") {
		t.Fatal("a flag set on the root must read as changed from the subcommand")
	}
	if flagChanged(nil, "on-gate-timeout") {
		t.Fatal("flagChanged must be nil-safe")
	}
}

// gateTestHarness builds a harness plus the cobra command tree the run command
// lives in, so flag-explicitness can be exercised the way cobra reports it.
func gateTestHarness(t *testing.T, mutate func(*config.Config)) (*harness.Harness, *cobra.Command, *cobra.Command) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	if mutate != nil {
		mutate(cfg)
	}
	o, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	h := &harness.Harness{Config: cfg, Orchestrator: o}

	root := &cobra.Command{Use: "slmcode"}
	var gateFlag string
	root.PersistentFlags().StringVar(&gateFlag, "on-gate-timeout", "stop", "")
	sub := &cobra.Command{Use: "run"}
	root.AddCommand(sub)
	return h, root, sub
}

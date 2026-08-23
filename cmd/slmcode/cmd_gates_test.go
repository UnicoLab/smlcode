package main

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/cli"
)

func TestNonInteractivePolicyDefaultsToStop(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()

	flagGateTimeout = ""
	if got := nonInteractivePolicy(); got != cli.GateTimeoutStop {
		t.Fatalf("policy=%v want stop", got)
	}
	flagGateTimeout = "nonsense"
	if got := nonInteractivePolicy(); got != cli.GateTimeoutStop {
		t.Fatalf("an invalid policy must fall back to stop, got %v", got)
	}
}

// TestHeadlessPlanGateNeverAutoApproves is the regression for the old default:
// plan_approve_timeout expired after two minutes and AUTO-APPROVED the plan.
func TestHeadlessPlanGateNeverAutoApproves(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "stop"

	g := cli.PlanGate("id", "query", "summary", nil, []string{"T1: do a thing"}, 1)
	ans := resolveHeadless(g)
	if ans.Value == "approve" {
		t.Fatalf("a headless plan gate must not approve, got %+v", ans)
	}
	if ans.Value != "reject" {
		t.Fatalf("expected the gate's conservative default, got %+v", ans)
	}
}

func TestHeadlessPolicyApproveOptsIn(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "approve"

	if got := resolveHeadless(cli.PlanGate("i", "q", "s", nil, nil, 0)); got.Value != "approve" {
		t.Fatalf("--on-gate-timeout=approve should approve, got %+v", got)
	}
	if got := resolveHeadless(cli.ContinueGate("i", "r", "s", nil, nil)); got.Value != "continue" {
		t.Fatalf("continue gate under approve policy: %+v", got)
	}
}

func TestHeadlessPolicyReject(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "reject"

	if got := resolveHeadless(cli.EscalateGate("i", "T1", "t", "d", nil)); got.Value != "abort" {
		t.Fatalf("escalate gate under reject policy: %+v", got)
	}
}

func TestHeadlessAnswersCarryAnExplanation(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "stop"

	ans := resolveHeadless(cli.ContinueGate("i", "r", "s", nil, nil))
	if ans.Notes == "" {
		t.Fatal("a headless decision must say why it was made")
	}
}

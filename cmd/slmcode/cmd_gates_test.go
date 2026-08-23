package main

import (
	"strings"
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
	ans := resolveHeadless(&gateAudit{}, g)
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

	if got := resolveHeadless(&gateAudit{}, cli.PlanGate("i", "q", "s", nil, nil, 0)); got.Value != "approve" {
		t.Fatalf("--on-gate-timeout=approve should approve, got %+v", got)
	}
	if got := resolveHeadless(&gateAudit{}, cli.ContinueGate("i", "r", "s", nil, nil)); got.Value != "continue" {
		t.Fatalf("continue gate under approve policy: %+v", got)
	}
}

func TestHeadlessPolicyReject(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "reject"

	if got := resolveHeadless(&gateAudit{}, cli.EscalateGate("i", "T1", "t", "d", nil)); got.Value != "abort" {
		t.Fatalf("escalate gate under reject policy: %+v", got)
	}
}

func TestHeadlessAnswersCarryAnExplanation(t *testing.T) {
	old := flagGateTimeout
	defer func() { flagGateTimeout = old }()
	flagGateTimeout = "stop"

	ans := resolveHeadless(&gateAudit{}, cli.ContinueGate("i", "r", "s", nil, nil))
	if ans.Notes == "" {
		t.Fatal("a headless decision must say why it was made")
	}
}

// TestHeadlessPlanRejectionStopsInsteadOfReplanning is the regression for the
// replan loop: the CLI forwarded the plan gate's "reject" default straight to
// the engine, plan.IsPlanReplan() counted it as a replan request, and a
// headless run burned three planner+splitter round-trips before dying with
// "plan replan limit reached". Anything that is not an approval or an explicit
// replan must reach the engine as a stop.
func TestHeadlessPlanRejectionStopsInsteadOfReplanning(t *testing.T) {
	for _, in := range []string{"reject", "", "abort", "stop"} {
		if got := planDecisionFor(in); got != "stop" {
			t.Errorf("plan answer %q → decision %q, want stop", in, got)
		}
	}
	if got := planDecisionFor("approve"); got != "approve" {
		t.Errorf("approve → %q", got)
	}
	if got := planDecisionFor("replan"); got != "replan" {
		t.Errorf("replan → %q", got)
	}
}

func TestGateAuditHintNamesTheGateAndAWayOut(t *testing.T) {
	a := &gateAudit{}
	if a.blocked() {
		t.Fatal("a fresh audit is not blocked")
	}
	a.note("plan")
	a.note("plan") // deduplicated
	if !a.blocked() || len(a.unanswered) != 1 {
		t.Fatalf("audit = %+v", a)
	}
	hint := a.hint()
	for _, want := range []string{"plan gate", "--on-gate-timeout=approve", "plan_approve"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint is missing %q:\n%s", want, hint)
		}
	}
}

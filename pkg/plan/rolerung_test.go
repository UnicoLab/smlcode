package plan

import "testing"

// The predicates must see through an escalation rung, or they are wrong for
// exactly the tasks that had the most trouble — the ones that escalated.
//
// Leaving each caller to strip the rung first is how this bug keeps coming
// back: every private copy forgets, and the failure is silent because a role
// that stops matching just means the gate quietly does not run.
func TestPredicatesSeeThroughAnEscalationRung(t *testing.T) {
	for _, role := range []string{
		"worker" + EscalationSuffix + "1",
		"go-worker" + EscalationSuffix + "2",
		"corrector" + EscalationSuffix + "3",
		"react-corrector" + EscalationSuffix + "1",
	} {
		if !IsImplementerRole(role) {
			t.Errorf("IsImplementerRole(%q) = false", role)
		}
	}
	for _, role := range []string{
		"tester" + EscalationSuffix + "1",
		"go-tester" + EscalationSuffix + "2",
	} {
		if !IsTesterRole(role) {
			t.Errorf("IsTesterRole(%q) = false", role)
		}
	}
}

// A rung is a positive integer. Anything else is part of the name, or the
// separator would silently truncate a role somebody legitimately chose.
func TestOnlyARealRungIsStripped(t *testing.T) {
	for _, id := range []string{
		"worker" + EscalationSuffix,        // no rung
		"worker" + EscalationSuffix + "0",  // not positive
		"worker" + EscalationSuffix + "x",  // not a number
		"worker" + EscalationSuffix + "2a", // not a number
	} {
		if got := baseRoleID(id); got != id {
			t.Errorf("baseRoleID(%q) = %q, want it unchanged", id, got)
		}
	}
	if got := baseRoleID("go-worker" + EscalationSuffix + "12"); got != "go-worker" {
		t.Errorf("baseRoleID = %q, want go-worker", got)
	}
	if got := baseRoleID("go-worker"); got != "go-worker" {
		t.Errorf("baseRoleID = %q, want go-worker", got)
	}
}

// Escalating must never turn a tester into an implementer or the reverse — that
// swap is what hands a tester the worker finish contract.
func TestEscalationDoesNotChangeTheRoleFamily(t *testing.T) {
	if IsImplementerRole("tester" + EscalationSuffix + "2") {
		t.Error("an escalated tester reads as an implementer")
	}
	if IsTesterRole("go-worker" + EscalationSuffix + "2") {
		t.Error("an escalated worker reads as a tester")
	}
}

package loop

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The measured regression, and the reason this test is worth its place.
//
// Once tasks began being staffed from their own files, a Go task arrives with
// role "go-worker" — and acceptanceSmokeRole compared for equality, so it
// answered no and the acceptance-criteria gate quietly did not run. On a live
// board the criteria were emitted correctly, with runnable bare verify
// commands ("go test -run TestMedianEven ./..."), and were never verified once:
//
//	FAIL  go-bugfix: acceptance criteria were verified
//	      — no criteria evidence on the board
//
// Two features that each worked alone: routing switched off verification by
// renaming the role that performs it.
func TestSpecialistWorkersStillVerifyCriteria(t *testing.T) {
	for _, role := range []string{
		plan.RoleWorker, "deep", plan.RoleCorrector,
		"go-worker", "react-worker", "python-worker", "shadcn-worker",
		"go-corrector",
	} {
		if !acceptanceSmokeRole(role) {
			t.Errorf("%q does not verify criteria", role)
		}
	}
}

// Escalation rungs go through BaseRoleID, so a routed AND escalated worker is
// still a worker — the case that would otherwise break on the second rename.
func TestEscalatedSpecialistStillVerifiesCriteria(t *testing.T) {
	for _, role := range []string{
		"go-worker" + agents.EscalationSuffix + "2",
		plan.RoleWorker + agents.EscalationSuffix + "3",
	} {
		if !acceptanceSmokeRole(role) {
			t.Errorf("%q does not verify criteria", role)
		}
	}
}

// Roles that do not implement must stay out: the gate runs shell commands, and
// a splitter or reviewer has no edits to verify.
func TestNonImplementersDoNotRunTheCriteriaGate(t *testing.T) {
	for _, role := range []string{
		"splitter", plan.RoleTester, plan.RoleReviewer, plan.RoleExplorer,
	} {
		if acceptanceSmokeRole(role) {
			t.Errorf("%q unexpectedly runs the criteria gate", role)
		}
	}
}

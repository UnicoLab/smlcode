package contextstore

import (
	"strings"
	"testing"
)

func TestEmpiricalTeamworkRolesBudgetAndPackingGaps(t *testing.T) {
	// 1. RoleBudgetPercent for built-in teamwork roles
	t.Run("RoleBudgetPercent for teamwork roles", func(t *testing.T) {
		// Worker role must have 100% budget, not default fallback 75%
		if got := RoleBudgetPercent("teamwork-worker"); got != 100 {
			t.Errorf("RoleBudgetPercent(\"teamwork-worker\") = %d; want 100 (got fallback 75%%, starving SLM worker)", got)
		}
		if got := RoleBudgetPercent("teamwork_worker"); got != 100 {
			t.Errorf("RoleBudgetPercent(\"teamwork_worker\") = %d; want 100 (got fallback 75%%)", got)
		}

		// Adversarial review role must have 85% budget, not default fallback 75%
		if got := RoleBudgetPercent("teamwork-review"); got != 85 {
			t.Errorf("RoleBudgetPercent(\"teamwork-review\") = %d; want 85 (got fallback 75%%)", got)
		}
		if got := RoleBudgetPercent("teamwork_review"); got != 85 {
			t.Errorf("RoleBudgetPercent(\"teamwork_review\") = %d; want 85 (got fallback 75%%)", got)
		}

		// Spec role must have 60% budget, not default fallback 75%
		if got := RoleBudgetPercent("teamwork-spec"); got != 60 {
			t.Errorf("RoleBudgetPercent(\"teamwork-spec\") = %d; want 60 (got fallback 75%%)", got)
		}
		if got := RoleBudgetPercent("teamwork_spec"); got != 60 {
			t.Errorf("RoleBudgetPercent(\"teamwork_spec\") = %d; want 60 (got fallback 75%%)", got)
		}

		// Plan role must have 70% budget, not default fallback 75%
		if got := RoleBudgetPercent("teamwork-plan"); got != 70 {
			t.Errorf("RoleBudgetPercent(\"teamwork-plan\") = %d; want 70 (got fallback 75%%)", got)
		}
		if got := RoleBudgetPercent("teamwork_plan"); got != 70 {
			t.Errorf("RoleBudgetPercent(\"teamwork_plan\") = %d; want 70 (got fallback 75%%)", got)
		}
	})

	// 2. DefaultIdentifierOnlyRoles must contain teamwork spec roles
	t.Run("DefaultIdentifierOnlyRoles includes teamwork-spec", func(t *testing.T) {
		roles := DefaultIdentifierOnlyRoles()
		hasHyphen := false
		hasUnderscore := false
		for _, r := range roles {
			if strings.EqualFold(r, "teamwork-spec") {
				hasHyphen = true
			}
			if strings.EqualFold(r, "teamwork_spec") {
				hasUnderscore = true
			}
		}
		if !hasHyphen && !hasUnderscore {
			t.Errorf("DefaultIdentifierOnlyRoles() missing teamwork-spec and teamwork_spec: got %v", roles)
		}
	})

	// 3. isLeanRole for teamwork roles
	t.Run("isLeanRole for teamwork execution and review roles", func(t *testing.T) {
		if !isLeanRole("teamwork-worker") {
			t.Errorf("isLeanRole(\"teamwork-worker\") = false; want true so files are prioritized over docs")
		}
		if !isLeanRole("teamwork_worker") {
			t.Errorf("isLeanRole(\"teamwork_worker\") = false; want true")
		}
		if !isLeanRole("teamwork-review") {
			t.Errorf("isLeanRole(\"teamwork-review\") = false; want true")
		}
		if !isLeanRole("teamwork_review") {
			t.Errorf("isLeanRole(\"teamwork_review\") = false; want true")
		}
	})
}

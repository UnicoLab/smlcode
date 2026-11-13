package contextstore

import (
	"slices"
	"testing"
)

func TestRoleBudgetPercent(t *testing.T) {
	tests := []struct {
		role string
		want int
	}{
		// Implementation roles (100%)
		{"worker", 100},
		{"corrector", 100},
		{"deep", 100},
		{"placeholder", 100},

		// Review & verification roles (85%)
		{"reviewer", 85},
		{"tester", 85},
		{"adversarial_reviewer", 85},
		{"challenger", 85},
		{" Adversarial_Reviewer ", 85},
		{"CHALLENGER", 85},

		// Planning & architecture roles (70%)
		{"architect", 70},
		{"planner", 70},
		{"splitter", 70},

		// Exploration & specification roles (60%)
		{"explorer", 60},
		{"context", 60},
		{"docs", 60},
		{"interviewer", 60},
		{"spec", 60},
		{"specification", 60},
		{"  Interviewer  ", 60},
		{"SPEC", 60},
		{"Specification", 60},

		// High-level coordination (50%)
		{"coordinator", 50},
		{"memory", 50},

		// Default fallback (75%)
		{"unknown_role", 75},
		{"", 75},
	}

	for _, tc := range tests {
		t.Run(tc.role, func(t *testing.T) {
			got := RoleBudgetPercent(tc.role)
			if got != tc.want {
				t.Fatalf("RoleBudgetPercent(%q) = %d, want %d", tc.role, got, tc.want)
			}
		})
	}
}

func TestDefaultIdentifierOnlyRoles(t *testing.T) {
	roles := DefaultIdentifierOnlyRoles()
	required := []string{
		"explorer", "docs", "context", "architect", "coordinator", "memory",
		"interviewer", "specification", "spec",
	}

	for _, req := range required {
		if !slices.Contains(roles, req) {
			t.Errorf("DefaultIdentifierOnlyRoles() missing required role %q; got %v", req, roles)
		}
	}
}

func TestIsLeanRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"worker", true},
		{"reviewer", true},
		{"adversarial_reviewer", true},
		{"challenger", true},
		{" Adversarial_Reviewer ", true},
		{"CHALLENGER", true},
		{"interviewer", false},
		{"specification", false},
		{"spec", false},
		{"unknown", false},
	}

	for _, tc := range tests {
		t.Run(tc.role, func(t *testing.T) {
			got := isLeanRole(tc.role)
			if got != tc.want {
				t.Fatalf("isLeanRole(%q) = %v, want %v", tc.role, got, tc.want)
			}
		})
	}
}

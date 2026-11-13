package plan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// PlanMilestone defines a high-level milestone in a Teamwork plan.
type PlanMilestone struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Acceptance  string   `json:"acceptance"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// FileOwnership defines the responsible owner and disjoint file paths for a component.
type FileOwnership struct {
	Owner string   `json:"owner"`
	Paths []string `json:"paths"`
}

// PlanContract defines an interface boundary between components.
type PlanContract struct {
	InterfaceID string   `json:"interface_id"`
	Provider    string   `json:"provider"`
	Consumers   []string `json:"consumers,omitempty"`
	Spec        string   `json:"spec"`
}

// TeamworkPlan defines the structured decomposition produced by teamwork-plan.
type TeamworkPlan struct {
	Strategy      string          `json:"strategy"`
	Milestones    []PlanMilestone `json:"milestones"`
	FileOwnership []FileOwnership `json:"file_ownership"`
	Contracts     []PlanContract  `json:"contracts,omitempty"`
	Risks         []string        `json:"risks,omitempty"`
}

// RenderContractsMarkdown formats frozen component interface contracts into CONTRACT.md.
func RenderContractsMarkdown(contracts []PlanContract) string {
	if len(contracts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Interface Contracts\n\n")
	b.WriteString("Frozen interface contracts across components. Implementations must conform.\n\n")
	for _, c := range contracts {
		fmt.Fprintf(&b, "## %s\n\n", c.InterfaceID)
		fmt.Fprintf(&b, "- **Provider:** `%s`\n", c.Provider)
		if len(c.Consumers) > 0 {
			fmt.Fprintf(&b, "- **Consumers:** `%s`\n", strings.Join(c.Consumers, "`, `"))
		}
		b.WriteString("\n### Specification\n\n```\n")
		b.WriteString(strings.TrimSpace(c.Spec))
		b.WriteString("\n```\n\n")
	}
	return b.String()
}

// ParseTeamworkPlanJSON attempts to extract and parse a TeamworkPlan from model output.
func ParseTeamworkPlanJSON(raw string) (TeamworkPlan, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TeamworkPlan{}, false
	}
	extracted := repairRole(extractJSON(raw), schema.RoleTeamworkPlan)
	var tp TeamworkPlan
	if err := json.Unmarshal([]byte(extracted), &tp); err != nil {
		return TeamworkPlan{}, false
	}
	if tp.Strategy == "" && len(tp.Milestones) == 0 && len(tp.Contracts) == 0 {
		return TeamworkPlan{}, false
	}
	return tp, true
}

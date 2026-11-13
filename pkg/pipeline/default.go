package pipeline

import (
	"os"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Default returns the built-in full-engine pipeline graph.
func Default() Config {
	en := true
	return Config{
		Version: 1,
		Order: []string{
			"init", "skills", "context", "explore", "docs", "architect",
			"clarify", "plan", "split", "coord", "execute", "learn",
			"polish", "test", "memory", "done",
		},
		Groups: []GroupMeta{
			{ID: "prepare", Label: "Prepare", Steps: []string{"init", "skills", "context", "explore", "docs"}},
			{ID: "design", Label: "Design", Steps: []string{"architect", "clarify", "plan", "split"}},
			{ID: "build", Label: "Build", Steps: []string{"coord", "execute", "learn"}},
			{ID: "verify", Label: "Verify", Steps: []string{"polish", "test"}},
			{ID: "finish", Label: "Finish", Steps: []string{"memory", "done"}},
		},
		Phases: map[string]PhaseSpec{
			"init":      {Agent: "", Label: "Init", Tip: "Boot workspace + session", Group: "prepare", When: WhenAlways, Enabled: &en},
			"skills":    {Agent: "", Label: "Skills", Tip: "Load skills & knowledge packs", Group: "prepare", When: WhenAlways},
			"context":   {Agent: plan.RoleContext, Label: "Context", Tip: "Refresh CONTEXT / project memory", Group: "prepare", When: WhenAlways},
			"explore":   {Agent: plan.RoleExplorer, Label: "Explore", Tip: "Discover relevant files", Group: "prepare", When: WhenAuto},
			"docs":      {Agent: "docs", Label: "Docs", Tip: "Read docs & conventions", Group: "prepare", When: WhenAuto},
			"architect": {Agent: "architect", Label: "Architect", Tip: "Shape approach & components", Group: "design", When: WhenAuto},
			"clarify":   {Agent: "", Label: "Clarify", Tip: "Lock PRD / ask decisions", Group: "design", When: WhenAuto},
			"plan":      {Agent: plan.RolePlanner, Label: "Plan", Tip: "Write the execution plan", Group: "design", When: WhenAlways},
			"split":     {Agent: "splitter", Label: "Split", Tip: "Break into atomic tasks", Group: "design", When: WhenAlways},
			"coord":     {Agent: "coordinator", Label: "Coord", Tip: "Coordinate board & focus", Group: "build", When: WhenAlways},
			"execute":   {Agent: plan.RoleWorker, Label: "Execute", Tip: "Workers implement + review", Group: "build", When: WhenAlways},
			"learn":     {Agent: "memory", Label: "Learn", Tip: "Capture lessons mid-run", Group: "build", When: WhenAuto},
			"polish":    {Agent: plan.RolePlaceholder, Label: "Polish", Tip: "Fill placeholders / flag precise gaps", Group: "verify", When: WhenAlways},
			"test":      {Agent: plan.RoleTester, Label: "Test", Tip: "Tester + QA gate verification", Group: "verify", When: WhenAlways},
			"memory":    {Agent: "memory", Label: "Memory", Tip: "Distill long-term memory", Group: "finish", When: WhenAlways},
			"done":      {Agent: "", Label: "Done", Tip: "Run complete", Group: "finish", When: WhenAlways},
		},
		Execute: ExecuteLoop{
			DefaultRole: plan.RoleWorker,
			Reviewer:    plan.RoleReviewer,
			Corrector:   plan.RoleCorrector,
			MaxWaves:    2,
		},
		Slots: nil,
	}
}

// EnsureFile writes default pipeline.yaml when missing.
func EnsureFile(slmDir string) error {
	path := Path(slmDir)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	cfg := Default()
	return Save(slmDir, &cfg)
}

// Teamwork returns the built-in multi-agent Teamwork pipeline graph:
// Stage 1 (specification/interview) -> Stage 2 (planning/architecture) ->
// Stage 3 (worker execution waves) -> Stage 4 (adversarial review & verification).
func Teamwork() Config {
	en := true
	return Config{
		Version: 1,
		Order: []string{
			"init", "skills", "context", "explore", "docs", "clarify",
			"architect", "plan", "split", "coord", "execute", "learn",
			"polish", "test", "memory", "done",
		},
		Groups: []GroupMeta{
			{ID: GroupInterview, Label: "Specification & Interview", Steps: []string{"init", "skills", "context", "explore", "docs", "clarify"}},
			{ID: GroupDesign, Label: "Architecture & Planning", Steps: []string{"architect", "plan", "split"}},
			{ID: GroupExecute, Label: "Worker Execution Waves", Steps: []string{"coord", "execute", "learn"}},
			{ID: GroupReview, Label: "Adversarial Review & QA", Steps: []string{"polish", "test"}},
			{ID: GroupFinish, Label: "Long-term Memory & Completion", Steps: []string{"memory", "done"}},
		},
		Phases: map[string]PhaseSpec{
			"init":      {Agent: "", Label: "Init", Tip: "Boot workspace + session", Group: GroupInterview, When: WhenAlways, Enabled: &en},
			"skills":    {Agent: "", Label: "Skills", Tip: "Load skills & knowledge packs", Group: GroupInterview, When: WhenAlways},
			"context":   {Agent: plan.RoleContext, Label: "Context", Tip: "Refresh CONTEXT / project memory", Group: GroupInterview, When: WhenAlways},
			"explore":   {Agent: plan.RoleExplorer, Label: "Explore", Tip: "Discover relevant files & architecture", Group: GroupInterview, When: WhenAuto},
			"docs":      {Agent: "docs", Label: "Docs", Tip: "Read docs & project conventions", Group: GroupInterview, When: WhenAuto},
			"clarify":   {Agent: RoleTeamworkSpec, Label: "Clarify", Tip: "Lock PRD & specify requirements", Group: GroupInterview, When: WhenAlways},
			"architect": {Agent: "architect", Label: "Architect", Tip: "Shape approach & component contracts", Group: GroupDesign, When: WhenAuto},
			"plan":      {Agent: RoleTeamworkPlan, Label: "Plan", Tip: "Decompose PRD into milestones & contracts", Group: GroupDesign, When: WhenAlways},
			"split":     {Agent: "splitter", Label: "Split", Tip: "Break into atomic tasks with criteria", Group: GroupDesign, When: WhenAlways},
			"coord":     {Agent: "coordinator", Label: "Coord", Tip: "Coordinate board & focus guardrails", Group: GroupExecute, When: WhenAlways},
			"execute":   {Agent: RoleTeamworkWorker, Label: "Execute", Tip: "Workers implement within focus scope", Group: GroupExecute, When: WhenAlways},
			"learn":     {Agent: "memory", Label: "Learn", Tip: "Capture lessons mid-run", Group: GroupExecute, When: WhenAuto},
			"polish":    {Agent: plan.RolePlaceholder, Label: "Polish", Tip: "Fill placeholders & flag precise gaps", Group: GroupReview, When: WhenAlways},
			"test":      {Agent: plan.RoleTester, Label: "Test", Tip: "Deterministic gates & holistic verification", Group: GroupReview, When: WhenAlways},
			"memory":    {Agent: "memory", Label: "Memory", Tip: "Distill long-term memory", Group: GroupFinish, When: WhenAlways},
			"done":      {Agent: "", Label: "Done", Tip: "Run complete", Group: GroupFinish, When: WhenAlways},
		},
		Execute: ExecuteLoop{
			DefaultRole: RoleTeamworkWorker,
			Reviewer:    RoleTeamworkReview,
			Corrector:   plan.RoleCorrector,
			MaxWaves:    2,
		},
		Slots: nil,
	}
}

// TeamworkPipeline returns a pointer to a normalized and validated Teamwork pipeline.
func TeamworkPipeline() *Pipeline {
	cfg := Teamwork()
	cfg.Normalize()
	return &cfg
}

// EnsureTeamworkFile writes teamwork pipeline.yaml when missing.
func EnsureTeamworkFile(slmDir string) error {
	path := Path(slmDir)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	cfg := Teamwork()
	return Save(slmDir, &cfg)
}

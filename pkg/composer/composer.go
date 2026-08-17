// Package composer implements dynamic pipeline composition for SLMCode.
//
// A dedicated "composer" specialist inspects the incoming query and produces a
// Composition: which pipeline phases to run, which agents (the "team") are bound
// to those phases, which skills each specialist should load, and the execute-loop
// roles (worker / reviewer / corrector). Apply turns that Composition into a real
// pipeline.Config the orchestrator can run directly.
package composer

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/repair"
)

// RoleID is the built-in specialist id for the dynamic pipeline composer.
const RoleID = "composer"

// StructuralPhases are engine-managed phases that always run and cannot be
// disabled by a composition. They have no agent binding of their own.
var StructuralPhases = map[string]bool{
	"init":   true,
	"skills": true,
	"done":   true,
}

// PhaseChoice is one phase binding chosen by the composer.
type PhaseChoice struct {
	// ID is the phase key (context, explore, architect, clarify, plan, split,
	// coord, execute, learn, polish, test, memory).
	ID string `json:"id"`
	// Agent overrides the phase's default specialist (team selection).
	Agent string `json:"agent,omitempty"`
	// Enabled turns the phase on/off. Structural phases are always on.
	Enabled bool `json:"enabled"`
	// When optionally forces always|auto|never. When empty, enabling a phase
	// preserves the built-in heuristic (auto) or default (always).
	When string `json:"when,omitempty"`
}

// ExecuteChoice configures the board execute loop.
type ExecuteChoice struct {
	DefaultRole string `json:"default_role,omitempty"`
	Reviewer    string `json:"reviewer,omitempty"`
	Corrector   string `json:"corrector,omitempty"`
	MaxWaves    int    `json:"max_waves,omitempty"`
}

// TeamMember binds a specialist role to a set of skills to load for it.
type TeamMember struct {
	Role   string   `json:"role"`
	Skills []string `json:"skills,omitempty"`
}

// Composition is the composer's structured output: a full dynamic pipeline plan.
type Composition struct {
	// Summary is a one-line description of the assembled plan.
	Summary string `json:"summary"`
	// Strategy is the composer's reasoning (kept short for SLMs).
	Strategy string `json:"strategy,omitempty"`
	// Phases lists the phases to activate, in execution order.
	Phases []PhaseChoice `json:"phases"`
	// Execute configures the board worker/reviewer/corrector loop.
	Execute ExecuteChoice `json:"execute"`
	// Team lists specialists and the skills each should load.
	Team []TeamMember `json:"team,omitempty"`
	// Slots are extra insertable specialists around phase anchors.
	Slots []pipeline.Slot `json:"slots,omitempty"`
}

// Normalize lowercases and trims identifiers, drops empty entries, and applies
// safe defaults. It is idempotent.
func (c *Composition) Normalize() {
	if c == nil {
		return
	}
	c.Summary = strings.TrimSpace(c.Summary)
	c.Strategy = strings.TrimSpace(c.Strategy)

	var phases []PhaseChoice
	seen := map[string]bool{}
	for _, p := range c.Phases {
		p.ID = strings.ToLower(strings.TrimSpace(p.ID))
		p.Agent = strings.ToLower(strings.TrimSpace(p.Agent))
		p.When = strings.ToLower(strings.TrimSpace(p.When))
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if p.When != pipeline.WhenAlways && p.When != pipeline.WhenAuto && p.When != pipeline.WhenNever {
			p.When = ""
		}
		phases = append(phases, p)
	}
	c.Phases = phases

	c.Execute.DefaultRole = strings.ToLower(strings.TrimSpace(c.Execute.DefaultRole))
	c.Execute.Reviewer = strings.ToLower(strings.TrimSpace(c.Execute.Reviewer))
	c.Execute.Corrector = strings.ToLower(strings.TrimSpace(c.Execute.Corrector))
	if c.Execute.MaxWaves < 0 {
		c.Execute.MaxWaves = 0
	}

	var team []TeamMember
	seenRole := map[string]bool{}
	for _, t := range c.Team {
		t.Role = strings.ToLower(strings.TrimSpace(t.Role))
		if t.Role == "" || seenRole[t.Role] {
			continue
		}
		seenRole[t.Role] = true
		t.Skills = cleanList(t.Skills)
		team = append(team, t)
	}
	c.Team = team

	for i := range c.Slots {
		c.Slots[i].ID = strings.ToLower(strings.TrimSpace(c.Slots[i].ID))
		c.Slots[i].Agent = strings.ToLower(strings.TrimSpace(c.Slots[i].Agent))
	}
}

func cleanList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Parse extracts and decodes a composer JSON output (markdown fences and common
// SLM JSON mistakes tolerated).
func Parse(raw string) (Composition, error) {
	var c Composition
	if err := repair.RepairAndUnmarshal(raw, &c); err != nil {
		return Composition{}, fmt.Errorf("composer: %w", err)
	}
	c.Normalize()
	if strings.TrimSpace(c.Summary) == "" {
		c.Summary = "Dynamic pipeline composed for this task"
	}
	return c, nil
}

func boolPtr(v bool) *bool { return &v }

// Apply builds a concrete pipeline.Config from a Composition.
//
// It starts from the built-in default pipeline (so every phase keeps its
// canonical "when" heuristic and agent), disables phases the composer omitted,
// applies explicit agent/when overrides, sets the execute loop, and appends any
// composer-declared slots. Structural phases (init/skills/done) stay enabled.
func Apply(comp Composition) (pipeline.Config, error) {
	base := pipeline.Default()
	base.Normalize()

	chosen := map[string]PhaseChoice{}
	for _, p := range comp.Phases {
		chosen[p.ID] = p
	}

	for id, ps := range base.Phases {
		if StructuralPhases[id] {
			continue
		}
		pc, ok := chosen[id]
		if !ok || !pc.Enabled {
			ps.Enabled = boolPtr(false)
			ps.When = pipeline.WhenNever
			base.Phases[id] = ps
			continue
		}
		// Enabled: clear the explicit disable flag and preserve the built-in
		// heuristic unless the composer forced a specific "when".
		ps.Enabled = nil
		if pc.When != "" {
			ps.When = pc.When
		}
		if pc.Agent != "" {
			ps.Agent = pc.Agent
		}
		base.Phases[id] = ps
	}

	if comp.Execute.DefaultRole != "" {
		base.Execute.DefaultRole = comp.Execute.DefaultRole
	}
	if comp.Execute.Reviewer != "" {
		base.Execute.Reviewer = comp.Execute.Reviewer
	}
	if comp.Execute.Corrector != "" {
		base.Execute.Corrector = comp.Execute.Corrector
	}
	if comp.Execute.MaxWaves > 0 {
		base.Execute.MaxWaves = comp.Execute.MaxWaves
	}

	base.Slots = append(base.Slots, comp.Slots...)

	base.Normalize()
	if err := base.Validate(); err != nil {
		return pipeline.Config{}, fmt.Errorf("composer: %w", err)
	}
	return base, nil
}

// SkillsByRole flattens the composer's team into a role → skills map.
func (c Composition) SkillsByRole() map[string][]string {
	out := map[string][]string{}
	for _, t := range c.Team {
		if len(t.Skills) == 0 {
			continue
		}
		out[t.Role] = append([]string{}, t.Skills...)
	}
	return out
}

// AgentSet returns every distinct agent referenced by the composition (phases,
// execute loop, slots, team).
func (c Composition) AgentSet() map[string]bool {
	out := map[string]bool{}
	add := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out[id] = true
		}
	}
	for _, p := range c.Phases {
		add(p.Agent)
	}
	add(c.Execute.DefaultRole)
	add(c.Execute.Reviewer)
	add(c.Execute.Corrector)
	for _, t := range c.Team {
		add(t.Role)
	}
	for _, s := range c.Slots {
		add(s.Agent)
	}
	return out
}

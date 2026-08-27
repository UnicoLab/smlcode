// Package composer implements dynamic pipeline composition for SLMCode.
//
// A dedicated "composer" specialist inspects the incoming query and produces a
// Composition: which pipeline phases to run, which agents (the "team") are bound
// to those phases, which skills each specialist should load, and the execute-loop
// roles (worker / reviewer / corrector). Apply turns that Composition into a real
// pipeline.Config the orchestrator can run directly.
package composer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/repair"
	"github.com/UnicoLab/slmcode/pkg/schema"
)

// RoleID is the built-in specialist id for the dynamic pipeline composer.
const RoleID = "composer"

// DynamicFileName is the inspectable structured composition for the latest run.
const DynamicFileName = "composition.dynamic.json"

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
	//
	// LISTING A PHASE MEANS ENABLING IT. A composer that emits
	// {"id":"plan"} with no "enabled" key gets Enabled=true — see
	// UnmarshalJSON. Omitting a field is the single most common SLM JSON
	// slip, and Go's zero value made it mean "disabled": Apply then set
	// When=never and the run fell through to fallbackTasks with planning and
	// splitting silently dead. Only an EXPLICIT {"enabled":false} disables.
	Enabled bool `json:"enabled"`
	// When optionally forces always|auto|never. When empty, enabling a phase
	// preserves the built-in heuristic (auto) or default (always).
	When string `json:"when,omitempty"`
}

// UnmarshalJSON decodes a phase choice, defaulting a MISSING "enabled" key to
// true.
//
// The field stays a plain bool (rather than *bool) on purpose: it is read by
// pkg/orchestrator and mirrored into pkg/plan, and a pointer would push a
// nil-check into every consumer for a default that belongs at the parse
// boundary. Presence, not value, is what the decoder has to observe — and only
// a custom unmarshaler can see it.
func (p *PhaseChoice) UnmarshalJSON(data []byte) error {
	type phaseChoiceAlias PhaseChoice
	// The default applies to the whole object; an explicit "enabled" overwrites it.
	tmp := phaseChoiceAlias{Enabled: true}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*p = PhaseChoice(tmp)
	return nil
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
	// Complexity and Kind are the budget class: how much of the run's finite
	// budget this request is worth, and what sort of work it is. See
	// profiles.go. Empty values normalize to standard/task.
	Complexity string `json:"complexity,omitempty"`
	Kind       string `json:"kind,omitempty"`
	// Handoff is a compact shared contract every later specialist should see:
	// target files, non-goals, verification commands, and any sequencing notes.
	Handoff []string `json:"handoff,omitempty"`
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
	c.Handoff = cleanListPreserveCase(c.Handoff)
	c.Complexity = NormalizeComplexity(c.Complexity)
	c.Kind = NormalizeKind(c.Kind)

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

func cleanListPreserveCase(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// Parse extracts and decodes a composer JSON output (markdown fences and common
// SLM JSON mistakes tolerated).
func Parse(raw string) (Composition, error) {
	c, _, err := ParseTracked(raw)
	return c, err
}

// ParseTracked is Parse plus the name of the repair rung that fixed the output
// (repair.RungNone when the model produced clean JSON). A composition arriving
// via close_braces or truncated is a signal the composer's max_tokens is short.
func ParseTracked(raw string) (Composition, string, error) {
	var c Composition
	fixed, rung, err := repair.RepairRole(raw, schema.RoleComposition)
	if err != nil {
		return Composition{}, rung, fmt.Errorf("composer: %w", err)
	}
	if err := json.Unmarshal(fixed, &c); err != nil {
		return Composition{}, rung, fmt.Errorf("composer: %w", err)
	}
	c.Normalize()
	if strings.TrimSpace(c.Summary) == "" {
		c.Summary = "Dynamic pipeline composed for this task"
	}
	return c, rung, nil
}

// SaveDynamic persists the full latest composition for inspection/debugging.
// Unlike pipeline.dynamic.yaml, this keeps handoff/team/skill choices too.
func SaveDynamic(slmDir string, c *Composition) error {
	if strings.TrimSpace(slmDir) == "" || c == nil {
		return nil
	}
	if err := os.MkdirAll(slmDir, 0o750); err != nil { // project state dir, owner-only
		return err
	}
	cp := *c
	cp.Normalize()
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(slmDir, DynamicFileName), append(b, '\n'), 0o644)
}

// LoadDynamic reads the inspectable latest composition from slmDir.
func LoadDynamic(slmDir string) (Composition, bool, error) {
	var comp Composition
	if strings.TrimSpace(slmDir) == "" {
		return comp, false, nil
	}
	body, err := os.ReadFile(filepath.Join(slmDir, DynamicFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return comp, false, nil
		}
		return comp, false, err
	}
	if err := json.Unmarshal(body, &comp); err != nil {
		return comp, false, fmt.Errorf("read dynamic composition: %w", err)
	}
	comp.Normalize()
	return comp, true, nil
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

// Profile returns the budget class this composition declares. Safe on a
// zero-value Composition: unset fields normalize to the standard task budget.
func (c Composition) Profile() Profile { return ProfileFor(c.Complexity, c.Kind) }

// ApplyProfile fills in whatever the composition did not choose for itself.
//
// The precedence is deliberate and one-directional: an explicit choice ALWAYS
// wins over the class. The profile is a budget, not an override — a composer
// that named its phases has already reasoned about this specific request,
// which is strictly more information than a class derived from the query
// string. This only fills the gaps, and it is what makes the class useful on
// the heuristic path (where nothing else fills them) without overriding the
// LLM path (where something already did).
func (c *Composition) ApplyProfile(p Profile) {
	if c == nil {
		return
	}
	c.Complexity, c.Kind = p.Complexity, p.Kind
	if len(c.Phases) == 0 {
		c.Phases = make([]PhaseChoice, 0, len(p.Phases))
		for _, id := range p.Phases {
			c.Phases = append(c.Phases, PhaseChoice{ID: id, Enabled: true})
		}
	}
	if c.Execute.MaxWaves == 0 {
		c.Execute.MaxWaves = p.MaxWaves
	}
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

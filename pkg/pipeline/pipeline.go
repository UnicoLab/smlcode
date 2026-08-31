// Package pipeline defines the config-driven execution graph for SLMCode:
// phases, agent bindings, insertable slots, and execute-loop roles.
//
// Persisted as .slmcode/pipeline.yaml. Studio, orchestrator, and the progress
// header all read the same document so the UI stays dynamic.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

const FileName = "pipeline.yaml"

// DynamicFileName is the on-disk destination for a composer-assembled pipeline.
// Kept separate from pipeline.yaml so dynamic composition never clobbers the
// user's chosen static pipeline.
const DynamicFileName = "pipeline.dynamic.yaml"

// When constants for phase/slot activation.
const (
	WhenAlways = "always"
	WhenAuto   = "auto" // built-in heuristics (e.g. architect only when needed)
	WhenNever  = "never"
)

// Persist targets for slot output.
const (
	PersistNone    = "none"
	PersistScratch = "scratch"
	PersistContext = "context"
	PersistMemory  = "memory"
)

// FailMode for slots.
const (
	FailContinue = "continue"
	FailAbort    = "abort"
)

// PhaseSpec binds a pipeline stage to an agent (or disables it).
type PhaseSpec struct {
	Agent   string `yaml:"agent,omitempty" json:"agent,omitempty"`
	When    string `yaml:"when,omitempty" json:"when,omitempty"` // always|auto|never
	Label   string `yaml:"label,omitempty" json:"label,omitempty"`
	Tip     string `yaml:"tip,omitempty" json:"tip,omitempty"`
	Group   string `yaml:"group,omitempty" json:"group,omitempty"`
	Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// EnabledOrDefault is true unless explicitly disabled or when=never.
func (p PhaseSpec) EnabledOrDefault() bool {
	if p.Enabled != nil {
		return *p.Enabled
	}
	return !strings.EqualFold(strings.TrimSpace(p.When), WhenNever)
}

// ExecuteLoop configures board wave agents (reviewer/corrector defaults).
type ExecuteLoop struct {
	DefaultRole string `yaml:"default_role,omitempty" json:"default_role,omitempty"`
	Reviewer    string `yaml:"reviewer,omitempty" json:"reviewer,omitempty"`
	Corrector   string `yaml:"corrector,omitempty" json:"corrector,omitempty"`
	// MaxWaves caps corrective execute waves after tester reject (0 = engine default).
	MaxWaves int `yaml:"max_waves,omitempty" json:"max_waves,omitempty"`
}

// Slot is a user-inserted agent anywhere around a phase anchor.
type Slot struct {
	ID           string  `yaml:"id" json:"id"`
	Agent        string  `yaml:"agent" json:"agent"`
	Title        string  `yaml:"title,omitempty" json:"title,omitempty"`
	Before       string  `yaml:"before,omitempty" json:"before,omitempty"` // phase anchor
	After        string  `yaml:"after,omitempty" json:"after,omitempty"`
	Replace      string  `yaml:"replace,omitempty" json:"replace,omitempty"`             // run instead of phase agent
	When         string  `yaml:"when,omitempty" json:"when,omitempty"`                   // always|never|query_matches:<re>
	Input        string  `yaml:"input,omitempty" json:"input,omitempty"`                 // prompt template
	SystemPrompt string  `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"` // optional one-shot override note (prepended)
	Model        string  `yaml:"model,omitempty" json:"model,omitempty"`
	Provider     string  `yaml:"provider,omitempty" json:"provider,omitempty"`
	Temperature  float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens    int     `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	MaxIter      int     `yaml:"max_iter,omitempty" json:"max_iter,omitempty"`
	Multipass    bool    `yaml:"multipass,omitempty" json:"multipass,omitempty"`
	PersistTo    string  `yaml:"persist_to,omitempty" json:"persist_to,omitempty"`
	FailMode     string  `yaml:"fail_mode,omitempty" json:"fail_mode,omitempty"`
	Enabled      *bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// EnabledOrDefault for slots.
func (s Slot) EnabledOrDefault() bool {
	if s.Enabled != nil {
		return *s.Enabled
	}
	return !strings.EqualFold(strings.TrimSpace(s.When), WhenNever)
}

// GroupMeta describes a UI group of phases.
type GroupMeta struct {
	ID    string   `yaml:"id" json:"id"`
	Label string   `yaml:"label" json:"label"`
	Steps []string `yaml:"steps" json:"steps"`
}

// Config is the full pipeline document.
type Config struct {
	Version int                  `yaml:"version,omitempty" json:"version,omitempty"`
	Phases  map[string]PhaseSpec `yaml:"phases,omitempty" json:"phases,omitempty"`
	Order   []string             `yaml:"order,omitempty" json:"order,omitempty"` // display + progress order
	Groups  []GroupMeta          `yaml:"groups,omitempty" json:"groups,omitempty"`
	Execute ExecuteLoop          `yaml:"execute,omitempty" json:"execute,omitempty"`
	// Teams attaches virtual development teams (block kind "team") to this
	// pipeline.
	//
	// A pipeline is a shape of work, and for most shapes the shape implies the
	// org chart: a "fullstack" pipeline is one that always has a backend half
	// and a frontend half, whatever today's query says. Naming them here means
	// the run does not have to rediscover that from the query text — and, more
	// usefully, means a team the query would never have hinted at (infra, docs)
	// is still on the run because the pipeline says it belongs there.
	//
	// Ids that no longer resolve are dropped with a warning rather than failing
	// the pipeline: a shared preset outlives the library it was written
	// against, and a pipeline that refuses to load is a worse answer than one
	// that runs with one fewer team.
	Teams []string `yaml:"teams,omitempty" json:"teams,omitempty"`
	Slots []Slot   `yaml:"slots,omitempty" json:"slots,omitempty"`
}

// Path returns <slmDir>/pipeline.yaml.
func Path(slmDir string) string {
	return filepath.Join(slmDir, FileName)
}

// PathDynamic returns <slmDir>/pipeline.dynamic.yaml.
func PathDynamic(slmDir string) string {
	return filepath.Join(slmDir, DynamicFileName)
}

// Load reads pipeline.yaml or returns Default() when missing.
func Load(slmDir string) (*Config, error) {
	path := Path(slmDir)
	data, fromBackup, err := readPipelineBytes(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := Default()
			return &cfg, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		if fromBackup {
			return nil, fmt.Errorf("pipeline.yaml backup: %w", err)
		}
		return nil, fmt.Errorf("pipeline.yaml: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		if !fromBackup {
			if backup, bakErr := os.ReadFile(atomicfile.BackupPath(path)); bakErr == nil {
				var bak Config
				if yaml.Unmarshal(backup, &bak) == nil {
					bak.Normalize()
					if bak.Validate() == nil {
						return &bak, nil
					}
				}
			}
		}
		return nil, err
	}
	return &cfg, nil
}

func readPipelineBytes(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is our own <slmDir>/pipeline*.yaml, not external input
	if err == nil {
		var probe Config
		if yaml.Unmarshal(data, &probe) == nil {
			probe.Normalize()
			if probe.Validate() == nil {
				return data, false, nil
			}
		}
		if backup, bakErr := os.ReadFile(atomicfile.BackupPath(path)); bakErr == nil {
			return backup, true, nil
		}
		return data, false, nil
	}
	return nil, false, err
}

// Save writes pipeline.yaml (creates parent dirs).
func Save(slmDir string, cfg *Config) error {
	return saveFile(slmDir, FileName, cfg,
		"# SLMCode pipeline — phases, loop agents, and insertable slots.\n"+
			"# Edit via Studio → Pipeline or PUT /api/pipeline\n\n")
}

// SaveDynamic writes a composer-assembled pipeline to pipeline.dynamic.yaml.
func SaveDynamic(slmDir string, cfg *Config) error {
	return saveFile(slmDir, DynamicFileName, cfg,
		"# SLMCode dynamic pipeline — assembled by the composer specialist for this task.\n"+
			"# Inspect-only: reruns regenerate it. Your pipeline.yaml is unchanged.\n\n")
}

func saveFile(slmDir, fileName string, cfg *Config, header string) error {
	if cfg == nil {
		return fmt.Errorf("nil pipeline config")
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(slmDir, 0o750); err != nil { // project config dir, owner-only
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicfile.WriteWithBackup(filepath.Join(slmDir, fileName), append([]byte(header), data...), 0o644)
}

// Normalize fills defaults and cleans fields.
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	if c.Version <= 0 {
		c.Version = 1
	}
	def := Default()
	if c.Phases == nil {
		c.Phases = map[string]PhaseSpec{}
	}
	// Merge missing phase keys from default (keep user overrides).
	for id, ps := range def.Phases {
		cur, ok := c.Phases[id]
		if !ok {
			c.Phases[id] = ps
			continue
		}
		if cur.Agent == "" {
			cur.Agent = ps.Agent
		}
		if cur.When == "" {
			cur.When = ps.When
		}
		if cur.Label == "" {
			cur.Label = ps.Label
		}
		if cur.Tip == "" {
			cur.Tip = ps.Tip
		}
		if cur.Group == "" {
			cur.Group = ps.Group
		}
		c.Phases[id] = cur
	}
	if len(c.Order) == 0 {
		c.Order = append([]string{}, def.Order...)
	}
	if len(c.Groups) == 0 {
		c.Groups = append([]GroupMeta{}, def.Groups...)
	}
	if c.Execute.DefaultRole == "" {
		c.Execute.DefaultRole = def.Execute.DefaultRole
	}
	if c.Execute.Reviewer == "" {
		c.Execute.Reviewer = def.Execute.Reviewer
	}
	if c.Execute.Corrector == "" {
		c.Execute.Corrector = def.Execute.Corrector
	}
	for i := range c.Slots {
		s := &c.Slots[i]
		s.ID = strings.ToLower(strings.TrimSpace(s.ID))
		s.Agent = strings.ToLower(strings.TrimSpace(s.Agent))
		s.Before = strings.ToLower(strings.TrimSpace(s.Before))
		s.After = strings.ToLower(strings.TrimSpace(s.After))
		s.Replace = strings.ToLower(strings.TrimSpace(s.Replace))
		if s.When == "" {
			s.When = WhenAlways
		}
		if s.PersistTo == "" {
			s.PersistTo = PersistScratch
		}
		if s.FailMode == "" {
			s.FailMode = FailContinue
		}
		if s.Title == "" {
			s.Title = s.ID
		}
	}
	// Team ids are slugs like block ids. Deduped and lowercased here so a
	// hand-written preset and one saved from Studio produce the same run.
	if len(c.Teams) > 0 {
		seen := map[string]bool{}
		kept := make([]string, 0, len(c.Teams))
		for _, id := range c.Teams {
			id = strings.ToLower(strings.TrimSpace(id))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			kept = append(kept, id)
		}
		c.Teams = kept
	}
}

var slotIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// Validate checks structural rules (not whether agent ids exist — that needs the factory).
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil pipeline")
	}
	anchors := map[string]bool{}
	for _, id := range c.Order {
		anchors[id] = true
	}
	for id := range c.Phases {
		anchors[id] = true
	}
	seenSlot := map[string]bool{}
	for _, s := range c.Slots {
		if !slotIDRe.MatchString(s.ID) {
			return fmt.Errorf("slot %q: invalid id", s.ID)
		}
		if seenSlot[s.ID] {
			return fmt.Errorf("duplicate slot id %q", s.ID)
		}
		seenSlot[s.ID] = true
		if s.Agent == "" {
			return fmt.Errorf("slot %q: agent required", s.ID)
		}
		placed := s.Before != "" || s.After != "" || s.Replace != ""
		if !placed {
			return fmt.Errorf("slot %q: set before, after, or replace", s.ID)
		}
		for _, a := range []string{s.Before, s.After, s.Replace} {
			if a == "" {
				continue
			}
			if !anchors[a] {
				return fmt.Errorf("slot %q: unknown phase anchor %q", s.ID, a)
			}
		}
	}
	// Group checks: unique non-empty ids, steps reference known phases, and
	// no phase assigned to more than one group.
	seenGroup := map[string]bool{}
	stepOwner := map[string]string{} // phase id → group id
	for _, g := range c.Groups {
		if strings.TrimSpace(g.ID) == "" {
			return fmt.Errorf("group: empty id")
		}
		if seenGroup[g.ID] {
			return fmt.Errorf("group %q: duplicate group id", g.ID)
		}
		seenGroup[g.ID] = true
		for _, step := range g.Steps {
			if _, ok := c.Phases[step]; !ok {
				return fmt.Errorf("group %q: unknown phase %q in steps", g.ID, step)
			}
			if owner, ok := stepOwner[step]; ok && owner != g.ID {
				return fmt.Errorf("phase %q assigned to multiple groups", step)
			}
			stepOwner[step] = g.ID
		}
	}
	return nil
}

// PhaseAgent returns the agent id for a phase (defaultAgent if unset).
func (c *Config) PhaseAgent(phase, defaultAgent string) string {
	if c == nil {
		return defaultAgent
	}
	if ps, ok := c.Phases[phase]; ok && strings.TrimSpace(ps.Agent) != "" {
		return strings.TrimSpace(ps.Agent)
	}
	// Also allow looking up by default agent key (plan → planner).
	if ps, ok := c.Phases[defaultAgent]; ok && strings.TrimSpace(ps.Agent) != "" {
		return strings.TrimSpace(ps.Agent)
	}
	return defaultAgent
}

// PhaseEnabled reports whether a built-in phase should run.
func (c *Config) PhaseEnabled(phase string) bool {
	if c == nil {
		return true
	}
	ps, ok := c.Phases[phase]
	if !ok {
		return true
	}
	return ps.EnabledOrDefault()
}

// PhaseWhen returns always|auto|never.
func (c *Config) PhaseWhen(phase string) string {
	if c == nil {
		return WhenAlways
	}
	ps, ok := c.Phases[phase]
	if !ok || ps.When == "" {
		return WhenAlways
	}
	return strings.ToLower(strings.TrimSpace(ps.When))
}

// SlotsAt returns enabled slots for a position relative to a phase.
// position is "before", "after", or "replace".
func (c *Config) SlotsAt(phase, position string) []Slot {
	if c == nil {
		return nil
	}
	phase = strings.ToLower(strings.TrimSpace(phase))
	position = strings.ToLower(strings.TrimSpace(position))
	var out []Slot
	for _, s := range c.Slots {
		if !s.EnabledOrDefault() {
			continue
		}
		switch position {
		case "before":
			if s.Before == phase {
				out = append(out, s)
			}
		case "after":
			if s.After == phase {
				out = append(out, s)
			}
		case "replace":
			if s.Replace == phase {
				out = append(out, s)
			}
		}
	}
	return out
}

// HasReplace is true when a slot replaces the built-in phase agent.
func (c *Config) HasReplace(phase string) bool {
	return len(c.SlotsAt(phase, "replace")) > 0
}

// PublicView is the API/Studio payload (resolved + anchors).
type PublicView struct {
	Config   Config            `json:"config"`
	Anchors  []string          `json:"anchors"`
	Defaults map[string]string `json:"defaults"` // phase → default agent
}

// View builds the Studio/API response.
func View(cfg *Config) PublicView {
	if cfg == nil {
		d := Default()
		cfg = &d
	}
	cfg.Normalize()
	defaults := map[string]string{}
	for id, ps := range Default().Phases {
		defaults[id] = ps.Agent
	}
	return PublicView{
		Config:   *cfg,
		Anchors:  append([]string{}, cfg.Order...),
		Defaults: defaults,
	}
}

// SlotMatchesWhen evaluates slot.when against the query.
func SlotMatchesWhen(when, query string) bool {
	when = strings.TrimSpace(when)
	if when == "" || strings.EqualFold(when, WhenAlways) {
		return true
	}
	if strings.EqualFold(when, WhenNever) {
		return false
	}
	const prefix = "query_matches:"
	if strings.HasPrefix(strings.ToLower(when), prefix) {
		pat := strings.TrimSpace(when[len(prefix):])
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			return false
		}
		return re.MatchString(query)
	}
	return true
}

// RenderSlotInput expands {{query}}, {{exploration}}, {{plan}}, {{phase}} placeholders.
func RenderSlotInput(tmpl, query, exploration, planMD, phase string) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = "Run as a pipeline slot for phase {{phase}}.\n\nQuery:\n{{query}}\n"
	}
	r := strings.NewReplacer(
		"{{query}}", query,
		"{{exploration}}", exploration,
		"{{plan}}", planMD,
		"{{phase}}", phase,
	)
	return r.Replace(tmpl)
}

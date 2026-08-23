package blocks

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

// PipelineBlock is a named, versioned pipeline preset.
type PipelineBlock struct {
	Meta `yaml:",inline"`
	Spec pipeline.Config `yaml:"spec" json:"spec"`
}

// Normalize + Validate for pipeline blocks.
func (b *PipelineBlock) Normalize() {
	if b == nil {
		return
	}
	b.Kind = KindPipeline
	b.Meta.Normalize()
	b.Spec.Normalize()
}

func (b *PipelineBlock) Validate() error {
	if b == nil {
		return fmt.Errorf("nil pipeline block")
	}
	b.Normalize()
	if err := b.Meta.Validate(); err != nil {
		return err
	}
	return b.Spec.Validate()
}

// AgentBlock is a reusable agent definition (custom or language specialist).
type AgentBlock struct {
	Meta `yaml:",inline"`
	Spec agents.CustomSpec `yaml:"spec" json:"spec"`
}

func (b *AgentBlock) Normalize() {
	if b == nil {
		return
	}
	b.Kind = KindAgent
	b.Meta.Normalize()
	if strings.TrimSpace(b.Spec.ID) == "" {
		b.Spec.ID = b.ID
	}
	if strings.TrimSpace(b.Spec.Title) == "" {
		b.Spec.Title = b.Name
	}
	if strings.TrimSpace(b.Spec.Description) == "" {
		b.Spec.Description = b.Description
	}
	_ = agents.NormalizeCustom(&b.Spec)
}

func (b *AgentBlock) Validate() error {
	if b == nil {
		return fmt.Errorf("nil agent block")
	}
	b.Normalize()
	if err := b.Meta.Validate(); err != nil {
		return err
	}
	return agents.NormalizeCustom(&b.Spec)
}

// DetectSpec describes how to auto-select a quality pack for a workspace.
//
// Files are matched at the workspace ROOT only. An entry may be a plain name
// ("go.mod"), a glob ("*.csproj" — .NET has no fixed project filename) or a
// relative path ("src/main.rs").
//
// Contains maps a root file to substrings that prove the language: a
// package.json mentioning "react" is a React app, one that does not is a plain
// Node/TypeScript project. Marker files alone cannot separate those two, and
// getting it wrong hands the model the wrong tester and the wrong gate.
//
// Priority is a tiebreak added to a non-zero evidence score, not a score of its
// own. A negative Priority opts a pack out of auto-detection entirely.
type DetectSpec struct {
	Files      []string            `yaml:"files,omitempty" json:"files,omitempty"`
	Extensions []string            `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Contains   map[string][]string `yaml:"contains,omitempty" json:"contains,omitempty"`
	Priority   int                 `yaml:"priority,omitempty" json:"priority,omitempty"`
}

// CheckCmd is one shell check in a quality pack.
type CheckCmd struct {
	Cmd      string `yaml:"cmd" json:"cmd"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
	Label    string `yaml:"label,omitempty" json:"label,omitempty"`
}

// QualitySpec holds language-specific format/lint/test/build commands.
type QualitySpec struct {
	Detect       DetectSpec `yaml:"detect,omitempty" json:"detect,omitempty"`
	Format       []CheckCmd `yaml:"format,omitempty" json:"format,omitempty"`
	Lint         []CheckCmd `yaml:"lint,omitempty" json:"lint,omitempty"`
	Typecheck    []CheckCmd `yaml:"typecheck,omitempty" json:"typecheck,omitempty"`
	Test         []CheckCmd `yaml:"test,omitempty" json:"test,omitempty"`
	Build        []CheckCmd `yaml:"build,omitempty" json:"build,omitempty"`
	Smoke        string     `yaml:"smoke,omitempty" json:"smoke,omitempty"`
	QAGate       string     `yaml:"qa_gate,omitempty" json:"qa_gate,omitempty"`
	SafePrefixes []string   `yaml:"safe_prefixes,omitempty" json:"safe_prefixes,omitempty"`
	TesterHints  string     `yaml:"tester_hints,omitempty" json:"tester_hints,omitempty"`
}

// QualityBlock is a language quality / verify pack.
type QualityBlock struct {
	Meta `yaml:",inline"`
	Spec QualitySpec `yaml:"spec" json:"spec"`
}

func (b *QualityBlock) Normalize() {
	if b == nil {
		return
	}
	b.Kind = KindQuality
	b.Meta.Normalize()
	b.Spec.Smoke = strings.TrimSpace(b.Spec.Smoke)
	b.Spec.QAGate = strings.TrimSpace(b.Spec.QAGate)
	if b.Spec.QAGate == "" && b.Spec.Smoke != "" {
		b.Spec.QAGate = b.Spec.Smoke
	}
	for i := range b.Spec.Format {
		b.Spec.Format[i].Cmd = strings.TrimSpace(b.Spec.Format[i].Cmd)
	}
	for i := range b.Spec.Lint {
		b.Spec.Lint[i].Cmd = strings.TrimSpace(b.Spec.Lint[i].Cmd)
	}
	for i := range b.Spec.Typecheck {
		b.Spec.Typecheck[i].Cmd = strings.TrimSpace(b.Spec.Typecheck[i].Cmd)
	}
	for i := range b.Spec.Test {
		b.Spec.Test[i].Cmd = strings.TrimSpace(b.Spec.Test[i].Cmd)
	}
	for i := range b.Spec.Build {
		b.Spec.Build[i].Cmd = strings.TrimSpace(b.Spec.Build[i].Cmd)
	}
	var prefixes []string
	seen := map[string]bool{}
	for _, p := range b.Spec.SafePrefixes {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		prefixes = append(prefixes, p)
	}
	b.Spec.SafePrefixes = prefixes
}

func (b *QualityBlock) Validate() error {
	if b == nil {
		return fmt.Errorf("nil quality block")
	}
	b.Normalize()
	if err := b.Meta.Validate(); err != nil {
		return err
	}
	if b.Spec.QAGate == "" && len(b.Spec.Test) == 0 && b.Spec.Smoke == "" {
		return fmt.Errorf("quality %q: need qa_gate, smoke, or test commands", b.ID)
	}
	return nil
}

// PrimaryQAGate returns the preferred project verify command.
func (b *QualityBlock) PrimaryQAGate() string {
	if b == nil {
		return ""
	}
	if b.Spec.QAGate != "" {
		return b.Spec.QAGate
	}
	if len(b.Spec.Test) > 0 && strings.TrimSpace(b.Spec.Test[0].Cmd) != "" {
		return b.Spec.Test[0].Cmd
	}
	return b.Spec.Smoke
}

// PackSpec composes other blocks into a language (or domain) pack.
type PackSpec struct {
	Pipeline string   `yaml:"pipeline,omitempty" json:"pipeline,omitempty"`
	Quality  string   `yaml:"quality,omitempty" json:"quality,omitempty"`
	Agents   []string `yaml:"agents,omitempty" json:"agents,omitempty"`
	Skills   []string `yaml:"skills,omitempty" json:"skills,omitempty"`
	// PinSkills merges Skills into config.pinned_skills on apply.
	PinSkills bool `yaml:"pin_skills,omitempty" json:"pin_skills,omitempty"`
	// OverrideTester sets pipeline phases.test.agent to this agent id when set.
	OverrideTester string `yaml:"override_tester,omitempty" json:"override_tester,omitempty"`
	// OverrideWorker sets execute.default_role when set.
	OverrideWorker string `yaml:"override_worker,omitempty" json:"override_worker,omitempty"`
	// DeferPlanApprove forces plan_approve=ask for this pack (human must approve plan).
	DeferPlanApprove bool `yaml:"defer_plan_approve,omitempty" json:"defer_plan_approve,omitempty"`
	// DeferClarify forces clarify_mode=ask for this pack (pause for user decisions).
	DeferClarify bool `yaml:"defer_clarify,omitempty" json:"defer_clarify,omitempty"`
}

// PackBlock is a shareable language pack.
type PackBlock struct {
	Meta `yaml:",inline"`
	Spec PackSpec `yaml:"spec" json:"spec"`
}

func (b *PackBlock) Normalize() {
	if b == nil {
		return
	}
	b.Kind = KindPack
	b.Meta.Normalize()
	b.Spec.Pipeline = strings.ToLower(strings.TrimSpace(b.Spec.Pipeline))
	b.Spec.Quality = strings.ToLower(strings.TrimSpace(b.Spec.Quality))
	b.Spec.OverrideTester = strings.ToLower(strings.TrimSpace(b.Spec.OverrideTester))
	b.Spec.OverrideWorker = strings.ToLower(strings.TrimSpace(b.Spec.OverrideWorker))
	normList := func(in []string) []string {
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
	b.Spec.Agents = normList(b.Spec.Agents)
	b.Spec.Skills = normList(b.Spec.Skills)
}

func (b *PackBlock) Validate() error {
	if b == nil {
		return fmt.Errorf("nil pack block")
	}
	b.Normalize()
	if err := b.Meta.Validate(); err != nil {
		return err
	}
	if b.Spec.Pipeline == "" && b.Spec.Quality == "" && len(b.Spec.Agents) == 0 {
		return fmt.Errorf("pack %q: reference at least one pipeline, quality, or agent", b.ID)
	}
	return nil
}

package autoresearch

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Bounds on the surface itself. A surface is a search space, and an unbounded
// search space is not a search space.
const (
	// MaxPromptLen caps a rewritten system prompt. A proposer that returns a
	// novel is proposing a context-window problem, not an improvement.
	MaxPromptLen = 8000
	// MaxCandidates caps how many values one numeric domain enumerates.
	MaxCandidates = 64
	// MaxAgentFiles caps how many agent YAMLs one surface reflects over.
	MaxAgentFiles = 64
	// MaxKnobs caps the whole surface.
	MaxKnobs = 512
)

// Knob sources.
const (
	SourceAgent  = "agent"
	SourceConfig = "config"
)

// KnobKind is the shape of a knob's value.
type KnobKind string

// Knob kinds.
const (
	KnobFloat KnobKind = "float"
	KnobInt   KnobKind = "int"
	KnobEnum  KnobKind = "enum"
	KnobBool  KnobKind = "bool"
	KnobText  KnobKind = "text"
)

// Domain is the set of values a knob may take.
//
// Numeric domains are half-open grids: Min, Min+Step, … up to Max. Enumerating
// rather than sampling is what makes the deterministic proposer's exploration
// systematic and finite — a proposer that could pick any float in a range would
// never exhaust the space and could never report "surface exhausted" honestly.
type Domain struct {
	Kind   KnobKind `json:"kind"`
	Min    float64  `json:"min,omitempty"`
	Max    float64  `json:"max,omitempty"`
	Step   float64  `json:"step,omitempty"`
	Values []string `json:"values,omitempty"`
	MaxLen int      `json:"max_len,omitempty"`
}

// Candidates enumerates the domain in a stable order. A text domain has no
// enumeration and returns nil — that is what makes system_prompt reachable only
// by the LLM proposer.
func (d Domain) Candidates() []string {
	switch d.Kind {
	case KnobEnum, KnobBool:
		out := make([]string, len(d.Values))
		copy(out, d.Values)
		return out
	case KnobInt:
		step := d.Step
		if step <= 0 {
			step = 1
		}
		var out []string
		for i := 0; i < MaxCandidates; i++ {
			v := d.Min + float64(i)*step
			if v > d.Max+1e-9 {
				break
			}
			out = append(out, strconv.Itoa(int(math.Round(v))))
		}
		return out
	case KnobFloat:
		step := d.Step
		if step <= 0 {
			step = 0.05
		}
		var out []string
		for i := 0; i < MaxCandidates; i++ {
			v := d.Min + float64(i)*step
			if v > d.Max+1e-9 {
				break
			}
			out = append(out, formatFloat(v))
		}
		return out
	default:
		return nil
	}
}

// Allows reports whether value is inside the domain.
func (d Domain) Allows(value string) bool {
	switch d.Kind {
	case KnobEnum, KnobBool:
		for _, v := range d.Values {
			if v == value {
				return true
			}
		}
		return false
	case KnobInt:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return false
		}
		return float64(n) >= d.Min-1e-9 && float64(n) <= d.Max+1e-9
	case KnobFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return false
		}
		return f >= d.Min-1e-9 && f <= d.Max+1e-9
	case KnobText:
		limit := d.MaxLen
		if limit <= 0 {
			limit = MaxPromptLen
		}
		v := strings.TrimSpace(value)
		return v != "" && len(v) <= limit
	default:
		return false
	}
}

// String renders the domain for `--surface`.
func (d Domain) String() string {
	switch d.Kind {
	case KnobEnum, KnobBool:
		return strings.Join(d.Values, "|")
	case KnobInt:
		return fmt.Sprintf("%d..%d step %d", int(d.Min), int(d.Max), int(math.Max(d.Step, 1)))
	case KnobFloat:
		return fmt.Sprintf("%s..%s step %s", formatFloat(d.Min), formatFloat(d.Max), formatFloat(d.Step))
	case KnobText:
		limit := d.MaxLen
		if limit <= 0 {
			limit = MaxPromptLen
		}
		return fmt.Sprintf("free text, ≤ %d chars", limit)
	default:
		return string(d.Kind)
	}
}

// Knob is one mutable value on the surface.
type Knob struct {
	// ID is stable and human-readable: "agent:go-worker.temperature" or
	// "config:think_passes". The journal keys on it, so it must not encode a
	// filesystem path (those differ between machines and checkouts).
	ID string `json:"id"`
	// Source is SourceAgent or SourceConfig.
	Source string `json:"source"`
	// Owner is the agent id for an agent knob, empty for a config knob.
	Owner string `json:"owner,omitempty"`
	// Field is the YAML key this knob writes.
	Field string `json:"field"`
	// File is the absolute path of the file the knob lives in.
	File string `json:"file"`
	// Value is the current value, rendered exactly as the domain renders it.
	Value string `json:"value"`
	// Domain bounds what may be written.
	Domain Domain `json:"domain"`
	// InFile is false when the value came from a built-in default rather than
	// from the file — writing it will create the key.
	InFile bool `json:"in_file"`
}

// Options configures Reflect.
type Options struct {
	// Root is the project directory that holds .slmcode/.
	Root string
	// AgentsDir overrides <root>/.slmcode/agents.
	AgentsDir string
	// ConfigPath overrides <root>/.slmcode/config.yaml.
	ConfigPath string
	// NoAgents leaves agent YAMLs out of the surface.
	NoAgents bool
	// NoConfig leaves config knobs out of the surface.
	NoConfig bool
}

func (o Options) agentsDir() string {
	if o.AgentsDir != "" {
		return o.AgentsDir
	}
	return filepath.Join(o.Root, config.DirName, "agents")
}

func (o Options) configPath() string {
	if o.ConfigPath != "" {
		return o.ConfigPath
	}
	return filepath.Join(o.Root, config.DirName, "config.yaml")
}

// Surface is the declarative description of everything an experiment may
// mutate — and, by omission, everything it may not.
type Surface struct {
	root     string
	knobs    []Knob
	byID     map[string]int
	warnings []string
}

// ErrNotMutable is returned when a change names something outside the surface.
var ErrNotMutable = errors.New("autoresearch: knob is not on the mutable surface")

// Reflect builds the mutable surface for a project.
//
// Reflection is deterministic: agent files are walked in sorted filename order,
// fields in AgentFields() order, config keys in ConfigWhitelist() order. No map
// is ranged anywhere on this path, because the order this produces IS the
// experiment order.
// contextWindowFor resolves the model's context window for domain scaling, or
// 0 when nothing is known. An unmeasured model keeps the conservative
// small-model ranges, which is the safe direction.
func contextWindowFor(root string) int {
	cfg, err := config.Load(root)
	if err != nil || cfg == nil {
		// No readable project config: keep the conservative small-model ranges.
		return 0
	}
	return config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model).ContextLimit
}

func Reflect(opts Options) (*Surface, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, errors.New("autoresearch: Reflect needs a project root")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = root
	s := &Surface{root: root, byID: map[string]int{}}

	if !opts.NoAgents {
		if err := s.reflectAgents(opts.agentsDir()); err != nil {
			return nil, err
		}
	}
	if !opts.NoConfig {
		if err := s.reflectConfig(opts.configPath(), root); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(s.knobs, func(i, j int) bool { return s.knobs[i].ID < s.knobs[j].ID })
	if len(s.knobs) > MaxKnobs {
		s.warnings = append(s.warnings,
			fmt.Sprintf("surface capped at %d knobs (%d found)", MaxKnobs, len(s.knobs)))
		s.knobs = s.knobs[:MaxKnobs]
	}
	s.reindex()
	return s, nil
}

func (s *Surface) reindex() {
	s.byID = make(map[string]int, len(s.knobs))
	for i, k := range s.knobs {
		s.byID[k.ID] = i
	}
}

func (s *Surface) reflectAgents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // a project with no custom agents still has config knobs
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) > MaxAgentFiles {
		s.warnings = append(s.warnings,
			fmt.Sprintf("only the first %d agent file(s) of %d are on the surface", MaxAgentFiles, len(names)))
		names = names[:MaxAgentFiles]
	}

	for _, name := range names {
		path := filepath.Join(dir, name)
		doc, err := loadYAMLDoc(path)
		if err != nil {
			// One unparseable agent file must not cost the whole surface.
			s.warnings = append(s.warnings, err.Error())
			continue
		}
		spec := doc.spec()
		id := agentID(spec, name)
		for _, f := range agentFields {
			raw, present := scalarString(spec, f.Name)
			if !present {
				continue // absent field: nothing to read, nothing to tune
			}
			value := normalizeValue(raw, f.Domain)
			s.knobs = append(s.knobs, Knob{
				ID:     SourceAgent + ":" + id + "." + f.Name,
				Source: SourceAgent,
				Owner:  id,
				Field:  f.Name,
				File:   path,
				Value:  value,
				Domain: f.Domain,
				InFile: true,
			})
		}
	}
	return nil
}

func (s *Surface) reflectConfig(path, root string) error {
	doc, err := loadYAMLDoc(path)
	if err != nil {
		s.warnings = append(s.warnings, err.Error())
		return nil
	}
	m := doc.root()
	defaults := config.Default(root)
	window := contextWindowFor(root)
	for _, ck := range configWhitelist {
		// Defense in depth: the surface is built from the allow-list, and the
		// allow-list is re-checked on the way in. A knob that reaches this
		// slice by any other route does not exist.
		if !IsWhitelisted(ck.Key) {
			continue
		}
		// The RANGE follows the model too, not just the value: a domain written
		// for a 16K model actively pulls a 262K one back toward small-model
		// settings. See scale.go.
		ck.Domain = scaleDomain(ck.Key, ck.Domain, window)
		value, inFile := scalarString(m, ck.Key)
		if !inFile {
			d, ok := defaults.Get(ck.Key)
			if !ok {
				// The key is whitelisted but pkg/config no longer has it: say
				// so rather than offering a knob that writes nothing.
				s.warnings = append(s.warnings, "whitelisted config key not in this build: "+ck.Key)
				continue
			}
			value = renderDefault(d, ck.Domain)
		}
		s.knobs = append(s.knobs, Knob{
			ID:     SourceConfig + ":" + ck.Key,
			Source: SourceConfig,
			Field:  ck.Key,
			File:   path,
			Value:  normalizeValue(value, ck.Domain),
			Domain: ck.Domain,
			InFile: inFile,
		})
	}
	return nil
}

// Knobs returns the surface, sorted by ID.
func (s *Surface) Knobs() []Knob {
	out := make([]Knob, len(s.knobs))
	copy(out, s.knobs)
	return out
}

// Knob looks one up by ID.
func (s *Surface) Knob(id string) (Knob, bool) {
	i, ok := s.byID[id]
	if !ok {
		return Knob{}, false
	}
	return s.knobs[i], true
}

// Len is the number of knobs.
func (s *Surface) Len() int { return len(s.knobs) }

// Root is the project directory the surface was reflected from.
func (s *Surface) Root() string { return s.root }

// Files lists every file the surface can write, sorted and deduplicated. This
// is exactly what a snapshot must capture: no more (snapshotting the tree would
// be a rewind system, which is a different feature) and no less.
func (s *Surface) Files() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range s.knobs {
		if k.File == "" || seen[k.File] {
			continue
		}
		seen[k.File] = true
		out = append(out, k.File)
	}
	sort.Strings(out)
	return out
}

// Warnings reports non-fatal problems found while reflecting — an unparseable
// agent file, a capped surface, a whitelisted key this build no longer has.
func (s *Surface) Warnings() []string {
	out := make([]string, len(s.warnings))
	copy(out, s.warnings)
	return out
}

// Apply writes one change to disk and updates the in-memory value.
//
// Every guard the package has is re-asserted here, because this is the only
// function in the package that writes to a user's file:
//
//  1. the knob must be on the surface;
//  2. a config knob's key must still be whitelisted;
//  3. the new value must be inside the knob's domain.
//
// Failing any of them is an error, not a clamp. Silently writing a
// nearest-legal value would make the journal a work of fiction.
func (s *Surface) Apply(c Change) error {
	i, ok := s.byID[c.KnobID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotMutable, c.KnobID)
	}
	k := s.knobs[i]
	if k.Source == SourceConfig && !IsWhitelisted(k.Field) {
		return fmt.Errorf("%w: config key %q is not whitelisted", ErrNotMutable, k.Field)
	}
	if k.Source == SourceAgent && !IsAgentField(k.Field) {
		return fmt.Errorf("%w: agent field %q is not whitelisted", ErrNotMutable, k.Field)
	}
	value := normalizeValue(c.After, k.Domain)
	if !k.Domain.Allows(value) {
		return fmt.Errorf("autoresearch: %q is outside the domain of %s (%s)", c.After, k.ID, k.Domain)
	}

	doc, err := loadYAMLDoc(k.File)
	if err != nil {
		return err
	}
	target := doc.root()
	if k.Source == SourceAgent {
		target = doc.spec()
	}
	setScalar(target, k.Field, value, k.Domain.Kind)
	if err := doc.save(); err != nil {
		return err
	}
	s.knobs[i].Value = value
	s.knobs[i].InFile = true
	return nil
}

// SetValue updates the in-memory value without writing. The ratchet calls it
// after restoring a reverted trial, so the surface and the disk never disagree.
func (s *Surface) SetValue(id, value string) {
	if i, ok := s.byID[id]; ok {
		s.knobs[i].Value = value
	}
}

// normalizeValue renders a raw YAML scalar the way the domain renders it, so
// "0.20" and "0.2" are the same value and the "already tried" check works.
func normalizeValue(raw string, d Domain) string {
	v := strings.TrimSpace(raw)
	switch d.Kind {
	case KnobInt:
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return strconv.Itoa(int(math.Round(n)))
		}
	case KnobFloat:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return formatFloat(f)
		}
	case KnobBool:
		switch strings.ToLower(v) {
		case "true", "yes", "on":
			return "true"
		case "false", "no", "off":
			return "false"
		}
	case KnobEnum:
		return strings.ToLower(v)
	case KnobText:
		return strings.TrimSpace(raw)
	}
	return v
}

// renderDefault turns a config.Get value into the domain's rendering.
func renderDefault(v any, d Domain) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if d.Kind == KnobInt {
			return strconv.Itoa(int(math.Round(t)))
		}
		return formatFloat(t)
	default:
		return fmt.Sprint(v)
	}
}

// formatFloat renders a float without the accumulated noise of repeated
// addition: 0.05 added three times is 0.15000000000000002, and a knob whose
// value string drifts is a knob whose history can never match.
func formatFloat(f float64) string {
	return strconv.FormatFloat(math.Round(f*1e6)/1e6, 'f', -1, 64)
}

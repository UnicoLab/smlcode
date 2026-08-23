package evolve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Rule store layout and tuning.
const (
	// EvolveDirName is the directory evolve owns under .slmcode/ and ~/.slmcode/.
	EvolveDirName = "evolve"

	// MinApplyConfidence is the bar a rule must clear before the harness acts
	// on it automatically. Below it the rule is still reported, but only as a
	// suggestion for a model to consider.
	MinApplyConfidence = 0.45
	// RetireFloor is the confidence below which a rule stops being offered.
	RetireFloor = 0.18
	// RetireMinSamples guards against retiring a good rule on one bad sample.
	RetireMinSamples = 4

	// Beta priors. A seeded rule starts believed (it encodes a failure mode we
	// already understand); a synthesized rule starts skeptical and has to earn
	// its place.
	seededAlpha = 4.0
	seededBeta  = 1.0
	learnedAlph = 1.0
	learnedBeta = 2.0

	// MaxRules bounds the store.
	MaxRules = 400
	// MaxEvidence bounds per-rule evidence.
	MaxEvidence = 5
)

// Scope says where a rule lives and how far it generalizes.
type Scope string

const (
	// ScopeBuiltin rules ship with slmcode.
	ScopeBuiltin Scope = "builtin"
	// ScopeProject rules were learned here and stay here.
	ScopeProject Scope = "project"
	// ScopeUser rules generalize across projects for this model.
	ScopeUser Scope = "user"
)

// Trigger is the pattern a failure must match for a rule to fire. Empty fields
// are wildcards. A rule with a Fingerprint matches that fingerprint exactly;
// the Trigger is the fallback that lets a seeded rule fire the very first time
// a failure is seen, before any fingerprint has been recorded.
type Trigger struct {
	Class           Class    `json:"class,omitempty"`
	Tool            string   `json:"tool,omitempty"`
	Language        string   `json:"language,omitempty"`
	ModelFamily     string   `json:"model_family,omitempty"`
	MessageContains []string `json:"message_contains,omitempty"`
}

// Matches reports whether the trigger fires for a fingerprint and signal.
func (t Trigger) Matches(fp Fingerprint, sig Signal) bool {
	if t.Class != "" && t.Class != fp.Class {
		return false
	}
	if t.Tool != "" && !strings.EqualFold(t.Tool, fp.Tool) {
		return false
	}
	if t.Language != "" && !strings.EqualFold(t.Language, fp.Language) {
		return false
	}
	if t.ModelFamily != "" && !strings.EqualFold(t.ModelFamily, fp.ModelFamily) {
		return false
	}
	if len(t.MessageContains) > 0 {
		msg := strings.ToLower(sig.Message + " " + fp.Norm)
		for _, needle := range t.MessageContains {
			if !strings.Contains(msg, strings.ToLower(needle)) {
				return false
			}
		}
	}
	return true
}

// specificity ranks how narrow a trigger is, so a rule written for
// (class, tool, language) beats a generic (class) rule.
func (t Trigger) specificity() int {
	n := 0
	if t.Class != "" {
		n += 4
	}
	if t.Tool != "" {
		n += 3
	}
	if t.Language != "" {
		n += 2
	}
	if t.ModelFamily != "" {
		n += 2
	}
	n += len(t.MessageContains)
	return n
}

// Rule is one learned or seeded "when this fails, do that".
type Rule struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Trigger     Trigger   `json:"trigger"`
	Repair      Repair    `json:"repair"`
	Evidence    []string  `json:"evidence,omitempty"`
	Successes   int       `json:"successes"`
	Failures    int       `json:"failures"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsed    time.Time `json:"last_used,omitempty"`
	Scope       Scope     `json:"scope"`
	Seeded      bool      `json:"seeded,omitempty"`
	// Retired disables a rule permanently. Set it (by hand or automatically)
	// instead of deleting a seeded rule, which would simply come back.
	Retired bool `json:"retired,omitempty"`
	// Note is free-form, for humans.
	Note string `json:"note,omitempty"`
}

// Samples is how many outcomes back this rule.
func (r Rule) Samples() int { return r.Successes + r.Failures }

// Confidence is the Beta posterior mean of "applying this repair works".
func (r Rule) Confidence() float64 {
	a, b := learnedAlph, learnedBeta
	if r.Seeded {
		a, b = seededAlpha, seededBeta
	}
	return (a + float64(r.Successes)) / (a + b + float64(r.Samples()))
}

// Usable reports whether the rule may be offered at all.
func (r Rule) Usable() bool {
	if r.Retired {
		return false
	}
	if r.Repair.Validate() != nil {
		return false
	}
	// Guardrail: a rule is only retired once there is enough evidence to be
	// sure, so one unlucky early sample cannot silently kill a good repair.
	return !(r.Samples() >= RetireMinSamples && r.Confidence() < RetireFloor)
}

// Applicable reports whether the harness should apply this repair automatically.
func (r Rule) Applicable() bool { return r.Usable() && r.Confidence() >= MinApplyConfidence }

// Suggestion is what Lookup returns: the matched rule plus why it matched.
type Suggestion struct {
	Rule        Rule
	Fingerprint Fingerprint
	// Exact is true when the fingerprint matched a recorded rule fingerprint
	// rather than a trigger pattern.
	Exact bool
	// Confidence mirrors Rule.Confidence() for convenience.
	Confidence float64
	// Apply is true when the repair should be applied automatically.
	Apply bool
}

// Reason renders a human-readable explanation.
func (s Suggestion) Reason() string {
	how := "matched the failure pattern"
	if s.Exact {
		how = "seen this exact failure before"
	}
	origin := "learned here"
	switch s.Rule.Scope {
	case ScopeBuiltin:
		origin = "shipped with slmcode"
	case ScopeUser:
		origin = "learned on another project with this model"
	}
	return strings.Join([]string{
		how + " (" + origin + ")",
		"repair: " + s.Rule.Repair.String(),
		"confidence " + pctString(s.Confidence) + " over " + itoa(s.Rule.Samples()) + " outcome(s)",
	}, "; ")
}

type rulesFile struct {
	Version int       `json:"version"`
	Updated time.Time `json:"updated"`
	Rules   []Rule    `json:"rules"`
}

// Rules is the repair-rule store: seeded rules merged with project-scoped and
// user-scoped learned rules.
type Rules struct {
	mu          sync.RWMutex
	projectPath string
	userPath    string
	byID        map[string]*Rule
	order       []string
	dirty       bool
	warnings    []string
	now         func() time.Time
}

// RulesOptions configures OpenRules.
type RulesOptions struct {
	// Now is injectable for tests.
	Now func() time.Time
	// NoSeed skips the built-in rule set (for tests that want a blank slate).
	NoSeed bool
}

// OpenRules loads the rule store.
//
// projectDir is the project root (rules at <projectDir>/.slmcode/evolve/rules.json),
// userDir is the user home (rules at <userDir>/.slmcode/evolve/rules.json).
// Either may be empty, in which case that scope is in-memory only. Corrupt
// files are moved aside and reported, never fatal.
func OpenRules(projectDir, userDir string) (*Rules, error) {
	return OpenRulesWith(projectDir, userDir, RulesOptions{})
}

// OpenRulesWith is OpenRules with options.
func OpenRulesWith(projectDir, userDir string, opt RulesOptions) (*Rules, error) {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	r := &Rules{byID: map[string]*Rule{}, now: now}
	if projectDir != "" {
		r.projectPath = filepath.Join(projectDir, memory.SlmDirName, EvolveDirName, "rules.json")
	}
	if userDir != "" {
		r.userPath = filepath.Join(userDir, memory.SlmDirName, EvolveDirName, "rules.json")
	}
	if !opt.NoSeed {
		for _, rule := range SeedRules() {
			r.insert(rule)
		}
	}
	// Persisted rules overlay the seeds: stats, retirement and hand edits win.
	r.loadFile(r.userPath, ScopeUser)
	r.loadFile(r.projectPath, ScopeProject)
	return r, nil
}

func (r *Rules) loadFile(path string, scope Scope) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the caller's own state dir
	if err != nil {
		return
	}
	var rf rulesFile
	if err := json.Unmarshal(data, &rf); err != nil {
		r.warnings = append(r.warnings, filepath.Base(filepath.Dir(path))+"/rules.json unreadable; ignored")
		_ = os.Rename(path, path+".corrupt")
		return
	}
	for i := range rf.Rules {
		rule := rf.Rules[i]
		if rule.Scope == "" {
			rule.Scope = scope
		}
		if rule.ID == "" {
			rule.ID = RuleID(rule.Trigger, rule.Repair)
		}
		if existing, ok := r.byID[rule.ID]; ok {
			// Persisted state (counts, retirement, edited repair) wins over
			// the shipped seed, but the seeded flag is preserved so the rule
			// keeps its prior.
			seeded := existing.Seeded
			*existing = rule
			existing.Seeded = seeded || rule.Seeded
			continue
		}
		r.insert(rule)
	}
}

func (r *Rules) insert(rule Rule) {
	if rule.ID == "" {
		rule.ID = RuleID(rule.Trigger, rule.Repair)
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = r.now().UTC()
	}
	if _, dup := r.byID[rule.ID]; dup {
		return
	}
	cp := rule
	r.byID[cp.ID] = &cp
	r.order = append(r.order, cp.ID)
}

// RuleID is the stable identity of a rule: its trigger plus its repair.
func RuleID(t Trigger, rep Repair) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(t.Class), t.Tool, t.Language, t.ModelFamily,
		strings.Join(t.MessageContains, ","),
		string(rep.Kind), rep.Transform, rep.Tool, rep.EditFormat,
		rep.ConfigKey, rep.ConfigValue, rep.Command, rep.Action,
	}, "\x00")))
	return "rule_" + hex.EncodeToString(sum[:])[:12]
}

// Lookup finds the best repair for a failure.
//
// This is the "fail once" hot path: on a hit the harness applies a known fix
// with no LLM round-trip. Exact fingerprint matches win over trigger matches;
// within each group, higher confidence wins, then narrower triggers.
func (r *Rules) Lookup(sig Signal) (Suggestion, bool) {
	fp := Analyze(sig)
	if fp.Zero() {
		return Suggestion{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	var exact, pattern []*Rule
	for _, id := range r.order {
		rule, ok := r.byID[id]
		if !ok || !rule.Usable() {
			continue
		}
		switch {
		case rule.Fingerprint != "" && rule.Fingerprint == fp.ID:
			exact = append(exact, rule)
		case rule.Trigger.Matches(fp, sig):
			pattern = append(pattern, rule)
		}
	}
	best := pickBest(exact)
	isExact := best != nil
	if best == nil {
		best = pickBest(pattern)
	}
	if best == nil {
		return Suggestion{}, false
	}
	conf := best.Confidence()
	return Suggestion{
		Rule:        *best,
		Fingerprint: fp,
		Exact:       isExact,
		Confidence:  conf,
		Apply:       conf >= MinApplyConfidence,
	}, true
}

func pickBest(list []*Rule) *Rule {
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		ci, cj := list[i].Confidence(), list[j].Confidence()
		if ci != cj {
			return ci > cj
		}
		si, sj := list[i].Trigger.specificity(), list[j].Trigger.specificity()
		if si != sj {
			return si > sj
		}
		return list[i].ID < list[j].ID
	})
	return list[0]
}

// SuggestRepair is the string-only view of Lookup, so packages that must not
// depend on evolve's types (pkg/eval/metrics' offline replay, for one) can
// still consult the rule store through a tiny interface.
//
// It returns the guidance text, the transformed arguments when the repair is a
// deterministic argument transform (empty otherwise), and whether anything
// matched at all.
func (r *Rules) SuggestRepair(tool, message, language, modelFamily, args string) (guidance, newArgs string, ok bool) {
	s, found := r.Lookup(Signal{
		Tool: tool, Message: message, Language: language, ModelFamily: modelFamily,
	})
	if !found {
		return "", "", false
	}
	if s.Rule.Repair.Kind == RepairTransformArgs && args != "" {
		if out, changed := ApplyTransform(s.Rule.Repair.Transform, args); changed {
			return s.Rule.Repair.Guidance, out, true
		}
	}
	return s.Rule.Repair.Guidance, "", true
}

// Observe records the outcome of applying a rule. Unknown ids are ignored.
func (r *Rules) Observe(ruleID string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, found := r.byID[ruleID]
	if !found {
		return
	}
	if ok {
		rule.Successes++
	} else {
		rule.Failures++
	}
	rule.LastUsed = r.now().UTC()
	if rule.Samples() >= RetireMinSamples && rule.Confidence() < RetireFloor {
		rule.Retired = true
	}
	r.dirty = true
}

// BindFingerprint records that a rule handled a concrete fingerprint, so the
// next occurrence takes the exact-match fast path instead of re-scanning
// trigger patterns.
func (r *Rules) BindFingerprint(ruleID, fingerprint string) {
	if fingerprint == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule, ok := r.byID[ruleID]; ok && rule.Fingerprint == "" {
		rule.Fingerprint = fingerprint
		r.dirty = true
	}
}

// Resolution describes how a NEW failure was eventually fixed, so a rule can
// be synthesized from it.
type Resolution struct {
	// Repair is what actually worked. Its Guidance must be set.
	Repair Repair
	// Evidence is a short reference (episode id, task id) for the audit trail.
	Evidence string
	// Scope defaults to ScopeProject. Use ScopeUser for model-level lessons.
	Scope Scope
}

// Learn synthesizes a low-confidence candidate rule from a failure that was
// eventually resolved. If a rule with the same trigger and repair already
// exists it is credited with a success instead of being duplicated.
//
// Synthesized rules start at ~0.33 confidence: below MinApplyConfidence, so a
// guess is *offered* but not *applied* until it has proved itself twice.
func (r *Rules) Learn(sig Signal, res Resolution) (Rule, bool) {
	if err := res.Repair.Validate(); err != nil {
		return Rule{}, false
	}
	fp := Analyze(sig)
	if fp.Zero() {
		return Rule{}, false
	}
	scope := res.Scope
	if scope == "" {
		scope = ScopeProject
	}
	trigger := Trigger{Class: fp.Class, Tool: fp.Tool, Language: fp.Language}
	if scope == ScopeUser {
		trigger.ModelFamily = fp.ModelFamily
		trigger.Language = ""
	}
	id := RuleID(trigger, res.Repair)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirty = true
	if existing, ok := r.byID[id]; ok {
		existing.Successes++
		existing.LastUsed = r.now().UTC()
		if existing.Fingerprint == "" {
			existing.Fingerprint = fp.ID
		}
		existing.Evidence = boundedAppend(existing.Evidence, res.Evidence, MaxEvidence)
		existing.Retired = false
		return *existing, true
	}
	rule := Rule{
		ID:          id,
		Fingerprint: fp.ID,
		Trigger:     trigger,
		Repair:      res.Repair,
		Evidence:    boundedAppend(nil, res.Evidence, MaxEvidence),
		CreatedAt:   r.now().UTC(),
		LastUsed:    r.now().UTC(),
		Scope:       scope,
		Note:        "synthesized from a resolved failure: " + Describe(fp.Class),
	}
	r.insert(rule)
	r.enforceCapLocked()
	return rule, true
}

// Get returns one rule by id.
func (r *Rules) Get(id string) (Rule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.byID[id]
	if !ok {
		return Rule{}, false
	}
	return *rule, true
}

// All returns every rule, highest confidence first.
func (r *Rules) All() []Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rule, 0, len(r.order))
	for _, id := range r.order {
		if rule, ok := r.byID[id]; ok {
			out = append(out, *rule)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence() > out[j].Confidence() })
	return out
}

// Count returns how many rules are loaded.
func (r *Rules) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// Retire disables a rule without deleting it.
func (r *Rules) Retire(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, ok := r.byID[id]
	if !ok {
		return false
	}
	rule.Retired = true
	r.dirty = true
	return true
}

// RulePolicy bounds the rule store.
type RulePolicy struct {
	MaxRules   int
	MaxAge     time.Duration
	DropRetire bool // physically remove retired non-seeded rules
}

// DefaultRulePolicy is what a run applies at the end of a turn.
func DefaultRulePolicy() RulePolicy {
	return RulePolicy{MaxRules: MaxRules, MaxAge: 365 * 24 * time.Hour, DropRetire: true}
}

// Prune enforces the policy. Seeded rules are never removed (retiring one is
// how you disable it); the lowest-confidence learned rules go first.
func (r *Rules) Prune(policy RulePolicy) int {
	if policy.MaxRules == 0 {
		policy.MaxRules = MaxRules
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pruneLocked(policy)
}

func (r *Rules) pruneLocked(policy RulePolicy) int {
	now := r.now()
	before := len(r.order)
	keep := make([]string, 0, len(r.order))
	for _, id := range r.order {
		rule, ok := r.byID[id]
		if !ok {
			continue
		}
		if rule.Seeded {
			keep = append(keep, id)
			continue
		}
		if policy.DropRetire && rule.Retired {
			delete(r.byID, id)
			continue
		}
		if policy.MaxAge > 0 && !rule.CreatedAt.IsZero() && now.Sub(rule.CreatedAt) > policy.MaxAge && rule.Samples() == 0 {
			delete(r.byID, id)
			continue
		}
		keep = append(keep, id)
	}
	if policy.MaxRules > 0 && len(keep) > policy.MaxRules {
		learned := make([]string, 0, len(keep))
		var seeded []string
		for _, id := range keep {
			if r.byID[id].Seeded {
				seeded = append(seeded, id)
				continue
			}
			learned = append(learned, id)
		}
		sort.SliceStable(learned, func(i, j int) bool {
			return r.byID[learned[i]].Confidence() > r.byID[learned[j]].Confidence()
		})
		room := policy.MaxRules - len(seeded)
		if room < 0 {
			room = 0
		}
		if len(learned) > room {
			for _, id := range learned[room:] {
				delete(r.byID, id)
			}
			learned = learned[:room]
		}
		keep = keep[:0]
		for _, id := range r.order {
			if _, ok := r.byID[id]; ok {
				keep = append(keep, id)
			}
		}
	}
	r.order = keep
	if before != len(r.order) {
		r.dirty = true
	}
	return before - len(r.order)
}

func (r *Rules) enforceCapLocked() {
	if len(r.order) > MaxRules*2 {
		r.pruneLocked(DefaultRulePolicy())
	}
}

// Save persists project- and user-scoped rules to their respective files.
// Builtin rules are written too (with their live statistics) so the store is
// fully inspectable; deleting a seeded rule from the file resurrects it on the
// next load, which is why Retire exists.
func (r *Rules) Save() error {
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return nil
	}
	var project, user []Rule
	for _, id := range r.order {
		rule, ok := r.byID[id]
		if !ok {
			continue
		}
		if rule.Scope == ScopeUser {
			user = append(user, *rule)
			continue
		}
		project = append(project, *rule)
	}
	r.dirty = false
	now := r.now().UTC()
	r.mu.Unlock()

	if err := writeRules(r.projectPath, rulesFile{Version: 1, Updated: now, Rules: project}); err != nil {
		return err
	}
	return writeRules(r.userPath, rulesFile{Version: 1, Updated: now, Rules: user})
}

func writeRules(path string, rf rulesFile) error {
	if path == "" || len(rf.Rules) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600)
}

// Warnings returns non-fatal load problems.
func (r *Rules) Warnings() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.warnings...)
}

// Forget deletes the on-disk rule stores. Seeded rules come back on reload —
// that is the point.
func (r *Rules) Forget() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, p := range []string{r.projectPath, r.userPath} {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	r.byID = map[string]*Rule{}
	r.order = nil
	for _, rule := range SeedRules() {
		r.insert(rule)
	}
	r.dirty = false
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

func boundedAppend(in []string, v string, max int) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return in
	}
	for _, s := range in {
		if s == v {
			return in
		}
	}
	in = append(in, v)
	if max > 0 && len(in) > max {
		in = in[len(in)-max:]
	}
	return in
}

func pctString(f float64) string {
	return itoa(int(f*100+0.5)) + "%"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d [20]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		d[i] = '-'
	}
	return string(d[i:])
}

// shortHash is the shared short-id helper for this package.
func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}

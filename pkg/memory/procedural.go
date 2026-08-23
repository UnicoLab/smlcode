package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Procedural topics. A topic is a family of mutually exclusive options that
// the harness picks between.
const (
	TopicEditFormat  = "edit_format"  // search_replace | unified_diff | whole_file
	TopicRecovery    = "recovery"     // recipe for a failure class
	TopicPrompt      = "prompt"       // prompt variant
	TopicKnob        = "knob"         // a config knob value
	TopicToolChoice  = "tool_choice"  // which tool to reach for
	TopicThinkPasses = "think_passes" // multipass depth
)

// Procedural caps.
const (
	DefaultMaxProcedures = 400
	DefaultProceduralTk  = 200
	MinProcedureSamples  = 3
	renderMaxProcedures  = 8
)

// ProcKey namespaces a procedure. Model family and language are part of the
// key so a Python project's lessons never leak into a Go one, and a lesson
// about qwen2.5-coder never leaks into gpt-4o-mini.
type ProcKey struct {
	Topic       string `json:"topic"`
	Option      string `json:"option"`
	ModelFamily string `json:"model_family,omitempty"`
	Language    string `json:"language,omitempty"`
}

// Normalize lowercases and trims the key fields.
func (k ProcKey) Normalize() ProcKey {
	k.Topic = strings.ToLower(strings.TrimSpace(k.Topic))
	k.Option = strings.ToLower(strings.TrimSpace(k.Option))
	k.ModelFamily = strings.ToLower(strings.TrimSpace(k.ModelFamily))
	k.Language = NormalizeLanguage(k.Language)
	return k
}

// ID is the stable identity of a namespaced procedure.
func (k ProcKey) ID() string {
	k = k.Normalize()
	return hashID("p_", k.Topic, k.Option, k.ModelFamily, k.Language)
}

func (k ProcKey) String() string {
	k = k.Normalize()
	scope := k.ModelFamily
	if scope == "" {
		scope = "any-model"
	}
	lang := k.Language
	if lang == "" {
		lang = "any-language"
	}
	return fmt.Sprintf("%s/%s [%s, %s]", k.Topic, k.Option, scope, lang)
}

// Procedure is one cross-project outcome tally for a namespaced option.
type Procedure struct {
	ID        string    `json:"id"`
	Key       ProcKey   `json:"key"`
	Successes int       `json:"successes"`
	Failures  int       `json:"failures"`
	Note      string    `json:"note,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastUsed  time.Time `json:"last_used"`
}

// Samples is how many observations back this procedure.
func (p Procedure) Samples() int { return p.Successes + p.Failures }

// Rate is the posterior mean success rate under a Beta(1,1) prior — 0.5 with
// no evidence, never 0 or 1 from a single sample.
func (p Procedure) Rate() float64 {
	return float64(p.Successes+1) / float64(p.Samples()+2)
}

type proceduresFile struct {
	Version    int         `json:"version"`
	Updated    time.Time   `json:"updated"`
	Procedures []Procedure `json:"procedures"`
}

// Procedures is user-scoped, cross-project procedural memory.
type Procedures struct {
	mu       sync.RWMutex
	dir      string
	byID     map[string]*Procedure
	order    []string
	max      int
	dirty    bool
	warnings []string
	now      func() time.Time
	count    TokenCounter
}

func openProcedures(dir string, max int, now func() time.Time, count TokenCounter) *Procedures {
	if max <= 0 {
		max = DefaultMaxProcedures
	}
	if now == nil {
		now = time.Now
	}
	p := &Procedures{dir: dir, byID: map[string]*Procedure{}, max: max, now: now, count: count}
	p.load()
	return p
}

func (s *Procedures) path() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "procedures.json")
}

func (s *Procedures) mdPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "PROCEDURES.md")
}

func (s *Procedures) load() {
	path := s.path()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		return
	}
	var pf proceduresFile
	if err := json.Unmarshal(data, &pf); err != nil {
		s.warnings = append(s.warnings, "procedures.json unreadable; starting empty")
		_ = os.Rename(path, path+".corrupt")
		return
	}
	for i := range pf.Procedures {
		p := pf.Procedures[i]
		p.Key = p.Key.Normalize()
		if p.Key.Topic == "" || p.Key.Option == "" {
			continue
		}
		p.ID = p.Key.ID()
		if _, dup := s.byID[p.ID]; dup {
			continue
		}
		s.byID[p.ID] = &p
		s.order = append(s.order, p.ID)
	}
}

// Record folds one outcome into procedural memory.
func (s *Procedures) Record(key ProcKey, ok bool, note string) Procedure {
	key = key.Normalize()
	if key.Topic == "" || key.Option == "" {
		return Procedure{}
	}
	id := key.ID()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true

	p, exists := s.byID[id]
	if !exists {
		p = &Procedure{ID: id, Key: key, FirstSeen: now}
		s.byID[id] = p
		s.order = append(s.order, id)
	}
	if ok {
		p.Successes++
	} else {
		p.Failures++
	}
	p.LastUsed = now
	if n := clip(note, 200); n != "" {
		p.Note = n
	}
	s.enforceCapLocked()
	return *p
}

// Get returns one exact namespaced procedure.
func (s *Procedures) Get(key ProcKey) (Procedure, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[key.ID()]
	if !ok {
		return Procedure{}, false
	}
	return *p, true
}

// Rank returns the options recorded for a topic in the given namespace, best
// first. Namespace widening is deliberate and one-directional: an exact
// (family, language) match is preferred, then the same model in any language,
// then any model in the same language, and only then the fully generic entry.
// It never widens *across* languages before it has widened across models.
func (s *Procedures) Rank(topic, modelFamily, language string) []Procedure {
	topic = strings.ToLower(strings.TrimSpace(topic))
	modelFamily = strings.ToLower(strings.TrimSpace(modelFamily))
	language = NormalizeLanguage(language)

	scopes := []ProcKey{
		{ModelFamily: modelFamily, Language: language},
		{ModelFamily: modelFamily},
		{Language: language},
		{},
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, scope := range scopes {
		var out []Procedure
		for _, id := range s.order {
			p, ok := s.byID[id]
			if !ok || p.Key.Topic != topic {
				continue
			}
			if p.Key.ModelFamily != scope.ModelFamily || p.Key.Language != scope.Language {
				continue
			}
			out = append(out, *p)
		}
		if len(out) == 0 {
			continue
		}
		sort.SliceStable(out, func(i, j int) bool {
			ri, rj := out[i].Rate(), out[j].Rate()
			if ri != rj {
				return ri > rj
			}
			return out[i].Samples() > out[j].Samples()
		})
		return out
	}
	return nil
}

// Best returns the highest-rated option for a topic with enough evidence
// behind it. It returns false when nothing has been observed often enough —
// callers must have a sensible default and must not treat "no memory" as a
// reason to do something exotic.
func (s *Procedures) Best(topic, modelFamily, language string) (Procedure, bool) {
	for _, p := range s.Rank(topic, modelFamily, language) {
		if p.Samples() >= MinProcedureSamples {
			return p, true
		}
	}
	return Procedure{}, false
}

// Count returns how many procedures are stored.
func (s *Procedures) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// All returns every procedure, most-used first.
func (s *Procedures) All() []Procedure {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Procedure, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok {
			out = append(out, *p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key.Topic != out[j].Key.Topic {
			return out[i].Key.Topic < out[j].Key.Topic
		}
		return out[i].Rate() > out[j].Rate()
	})
	return out
}

func (s *Procedures) enforceCapLocked() {
	if s.max <= 0 || len(s.order) <= s.max*2 {
		return
	}
	s.pruneLocked(PrunePolicy{MaxProcedures: s.max})
}

// Render emits the injectable procedural block for one model family and
// language: only options with real evidence, phrased as advice.
func (s *Procedures) Render(modelFamily, language string, budgetTokens int) string {
	if budgetTokens <= 0 {
		budgetTokens = DefaultProceduralTk
	}
	topics := []string{TopicEditFormat, TopicToolChoice, TopicRecovery, TopicPrompt, TopicKnob, TopicThinkPasses}
	var lines []string
	for _, topic := range topics {
		ranked := s.Rank(topic, modelFamily, language)
		for _, p := range ranked {
			if p.Samples() < MinProcedureSamples {
				continue
			}
			line := fmt.Sprintf("- %s → `%s` (%.0f%% over %d runs)",
				topic, p.Key.Option, p.Rate()*100, p.Samples())
			if p.Note != "" {
				line += " — " + p.Note
			}
			lines = append(lines, line)
			if len(lines) >= renderMaxProcedures {
				break
			}
		}
		if len(lines) >= renderMaxProcedures {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	body := "## What works for this model\n\n" + strings.Join(lines, "\n") + "\n"
	return fitToTokens(body, budgetTokens, s.count)
}

// Flush persists procedures.json plus a Markdown mirror.
func (s *Procedures) Flush() error {
	s.mu.Lock()
	dirty := s.dirty
	dir := s.dir
	pf := proceduresFile{Version: 1, Updated: s.now().UTC()}
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok {
			pf.Procedures = append(pf.Procedures, *p)
		}
	}
	s.dirty = false
	s.mu.Unlock()

	if dir == "" || !dirty {
		return nil
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path(), append(data, '\n'), 0o600); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Procedural memory (cross-project)\n\n")
	b.WriteString("_What works for which model and language. Namespaced so lessons do not leak between stacks._\n\n")
	b.WriteString("| Topic | Option | Model family | Language | Success | Samples |\n")
	b.WriteString("|-------|--------|--------------|----------|---------|---------|\n")
	for _, p := range pf.Procedures {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %.0f%% | %d |\n",
			p.Key.Topic, p.Key.Option, orAny(p.Key.ModelFamily), orAny(p.Key.Language),
			p.Rate()*100, p.Samples())
	}
	return atomicfile.Write(s.mdPath(), []byte(b.String()), 0o600)
}

// Warnings returns non-fatal load problems.
func (s *Procedures) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

func orAny(s string) string {
	if strings.TrimSpace(s) == "" {
		return "*"
	}
	return s
}

// NormalizeLanguage folds common aliases so "Golang", "GO" and "go" share a
// namespace.
func NormalizeLanguage(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	switch l {
	case "golang":
		return "go"
	case "py", "python3":
		return "python"
	case "js", "node", "nodejs", "javascript":
		return "javascript"
	case "ts":
		return "typescript"
	case "rs":
		return "rust"
	case "c++", "cxx":
		return "cpp"
	default:
		return l
	}
}

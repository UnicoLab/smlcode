package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// On-disk layout.
const (
	// DirName is the directory memory owns under .slmcode/ and ~/.slmcode/.
	DirName = "memory"
	// SlmDirName is the per-project state directory memory lives inside.
	SlmDirName = ".slmcode"
)

// Store-wide defaults.
const (
	DefaultMaxEpisodes = 300
	DefaultPromptTk    = 1200
)

// Limits bound every layer of the store.
type Limits struct {
	MaxEpisodes      int
	MaxFacts         int
	MaxProcedures    int
	WorkingTokens    int
	SemanticTokens   int
	ProceduralTokens int
}

// DefaultLimits returns the shipped ceilings.
func DefaultLimits() Limits {
	return Limits{
		MaxEpisodes:      DefaultMaxEpisodes,
		MaxFacts:         DefaultMaxFacts,
		MaxProcedures:    DefaultMaxProcedures,
		WorkingTokens:    DefaultWorkingTk,
		SemanticTokens:   DefaultSemanticTk,
		ProceduralTokens: DefaultProceduralTk,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxEpisodes <= 0 {
		l.MaxEpisodes = d.MaxEpisodes
	}
	if l.MaxFacts <= 0 {
		l.MaxFacts = d.MaxFacts
	}
	if l.MaxProcedures <= 0 {
		l.MaxProcedures = d.MaxProcedures
	}
	if l.WorkingTokens <= 0 {
		l.WorkingTokens = d.WorkingTokens
	}
	if l.SemanticTokens <= 0 {
		l.SemanticTokens = d.SemanticTokens
	}
	if l.ProceduralTokens <= 0 {
		l.ProceduralTokens = d.ProceduralTokens
	}
	return l
}

// Options configures Open.
type Options struct {
	Limits Limits
	// Count is the token counter used for every budgeted rendering.
	// Defaults to contextstore.DefaultTokenCounter.
	Count TokenCounter
	// Now is injectable for deterministic tests.
	Now func() time.Time
	// ReadOnly disables all writes (inspection, CI).
	ReadOnly bool
}

// RunContext is what the store needs to know about the current run in order to
// recall and render relevant memory.
type RunContext struct {
	RunID       string
	Query       string
	Language    string
	Model       string
	ModelFamily string
	Provider    string
	Role        string
	Files       []string
	Tags        []string
}

// Store is the memory subsystem: one working memory, one episodic log, one
// semantic fact store (project-scoped) and one procedural store (user-scoped).
//
// A Store is always usable. Open never fails because of a corrupt or
// unwritable store — it degrades to in-memory operation and records the
// problem in Warnings.
type Store struct {
	mu sync.RWMutex

	projectDir string
	userDir    string
	memDir     string
	userMemDir string
	readOnly   bool

	limits Limits
	count  TokenCounter
	now    func() time.Time

	working    *Working
	episodes   *Episodes
	facts      *Facts
	procedures *Procedures

	run      RunContext
	warnings []string
}

// Open opens (or creates) the memory store.
//
// projectDir is the project root; project memory lives at
// <projectDir>/.slmcode/memory. userDir is the user's home; cross-project
// memory lives at <userDir>/.slmcode/memory. An empty userDir resolves to
// os.UserHomeDir(). An empty projectDir yields a fully in-memory store, which
// is the correct behavior for `slmcode` invoked outside a workspace.
func Open(projectDir, userDir string) (*Store, error) {
	return OpenWith(projectDir, userDir, Options{})
}

// OpenWith is Open with explicit options.
func OpenWith(projectDir, userDir string, opt Options) (*Store, error) {
	limits := opt.Limits.withDefaults()
	count := opt.Count
	if count == nil {
		count = contextstore.DefaultTokenCounter
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}

	s := &Store{
		projectDir: strings.TrimSpace(projectDir),
		userDir:    strings.TrimSpace(userDir),
		readOnly:   opt.ReadOnly,
		limits:     limits,
		count:      count,
		now:        now,
	}
	if s.userDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			s.userDir = home
		}
	}
	if s.projectDir != "" {
		s.memDir = filepath.Join(s.projectDir, SlmDirName, DirName)
		if err := s.ensure(s.memDir); err != nil {
			s.warnings = append(s.warnings, "project memory disabled: "+err.Error())
			s.memDir = ""
		}
	}
	if s.userDir != "" {
		s.userMemDir = filepath.Join(s.userDir, SlmDirName, DirName)
		if err := s.ensure(s.userMemDir); err != nil {
			s.warnings = append(s.warnings, "user memory disabled: "+err.Error())
			s.userMemDir = ""
		}
	}

	s.working = newWorking(limits.WorkingTokens, count, now)
	s.episodes = openEpisodes(s.memDir, limits.MaxEpisodes, now)
	s.facts = openFacts(s.memDir, limits.MaxFacts, now, count)
	s.procedures = openProcedures(s.userMemDir, limits.MaxProcedures, now, count)
	s.warnings = append(s.warnings, s.episodes.Warnings()...)
	s.warnings = append(s.warnings, s.facts.Warnings()...)
	s.warnings = append(s.warnings, s.procedures.Warnings()...)
	return s, nil
}

func (s *Store) ensure(dir string) error {
	if dir == "" || s.readOnly {
		return nil
	}
	return os.MkdirAll(dir, 0o750)
}

// Dir returns the project memory directory ("" when memory is in-process only).
func (s *Store) Dir() string { return s.memDir }

// UserDir returns the cross-project memory directory.
func (s *Store) UserDir() string { return s.userMemDir }

// Warnings returns non-fatal problems: a corrupt file, an unwritable directory.
// Callers should surface these but must never abort a run over them.
func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// Working returns short-term memory.
func (s *Store) Working() *Working { return s.working }

// Episodes returns the episodic store.
func (s *Store) Episodes() *Episodes { return s.episodes }

// Semantic returns the project fact store.
func (s *Store) Semantic() *Facts { return s.facts }

// Procedural returns the cross-project procedure store.
func (s *Store) Procedural() *Procedures { return s.procedures }

// SetRunContext tells the store what the current run is about. It also seeds
// working memory with the task and focus files.
func (s *Store) SetRunContext(rc RunContext) {
	rc.Language = NormalizeLanguage(rc.Language)
	if rc.ModelFamily == "" {
		rc.ModelFamily = ModelFamily(rc.Model)
	}
	s.mu.Lock()
	s.run = rc
	s.mu.Unlock()
	if rc.Query != "" {
		s.working.Start(rc.RunID, rc.Query, rc.Role)
	}
	if len(rc.Files) > 0 {
		s.working.Focus(rc.Files...)
	}
}

// RunContext returns the current run context.
func (s *Store) RunContext() RunContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.run
}

// RecordEpisode appends one completed task/turn to episodic memory.
func (s *Store) RecordEpisode(e Episode) error {
	if s.readOnly {
		return nil
	}
	rc := s.RunContext()
	if e.RunID == "" {
		e.RunID = rc.RunID
	}
	if e.Language == "" {
		e.Language = rc.Language
	}
	if e.Model == "" {
		e.Model = rc.Model
	}
	if e.Provider == "" {
		e.Provider = rc.Provider
	}
	if e.Query == "" {
		e.Query = rc.Query
	}
	_, err := s.episodes.Append(e)
	return err
}

// RecallEpisodes returns up to n past episodes most similar to q.
func (s *Store) RecallEpisodes(q Query, n int) []Episode {
	if q.Text == "" && len(q.Files) == 0 && len(q.Tags) == 0 {
		rc := s.RunContext()
		q.Text = rc.Query
		q.Files = rc.Files
		q.Tags = rc.Tags
		if q.Language == "" {
			q.Language = rc.Language
		}
	}
	return s.episodes.RecallEpisodes(q, n)
}

// RenderForPrompt builds the memory block to inject for a specialist role,
// costing at most budgetTokens. Sections are assembled in a role-dependent
// priority order and each is hard-capped; leftover budget from an empty
// section flows to the next one, never past the total.
//
// Returns "" when there is nothing useful to say. That is the desired output
// far more often than not: for a small model, an irrelevant memory is worse
// than no memory.
func (s *Store) RenderForPrompt(role string, budgetTokens int) string {
	if budgetTokens <= 0 {
		budgetTokens = DefaultPromptTk
	}
	rc := s.RunContext()
	shares := promptShares(role)
	remaining := budgetTokens

	type section struct {
		share float64
		build func(budget int) string
	}
	sections := []section{
		{shares.working, func(b int) string { return s.working.Render(b) }},
		{shares.semantic, func(b int) string { return s.facts.Render(b) }},
		{shares.episodic, func(b int) string { return s.renderEpisodes(rc, b) }},
		{shares.procedural, func(b int) string {
			return s.procedures.Render(rc.ModelFamily, rc.Language, b)
		}},
	}

	var parts []string
	for _, sec := range sections {
		if remaining <= 0 {
			break
		}
		want := int(float64(budgetTokens) * sec.share)
		if want > remaining {
			want = remaining
		}
		if want <= 0 {
			continue
		}
		body := sec.build(want)
		if strings.TrimSpace(body) == "" {
			continue
		}
		parts = append(parts, strings.TrimRight(body, "\n"))
		remaining -= countTokens(s.count, body)
	}
	if len(parts) == 0 {
		return ""
	}
	return fitToTokens(strings.Join(parts, "\n\n")+"\n", budgetTokens, s.count)
}

type shareSet struct{ working, semantic, episodic, procedural float64 }

// promptShares splits the memory budget by what a role actually needs.
// Implementation roles live in the present (working memory, the exact state of
// the files); planning roles live in the past (what this project is like and
// what happened last time).
func promptShares(role string) shareSet {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "worker", "corrector", "deep", "placeholder", "go-worker", "python-worker", "react-worker":
		return shareSet{working: 0.45, semantic: 0.25, episodic: 0.18, procedural: 0.12}
	case "tester", "go-tester", "python-tester", "react-tester":
		return shareSet{working: 0.35, semantic: 0.35, episodic: 0.20, procedural: 0.10}
	case "reviewer":
		return shareSet{working: 0.35, semantic: 0.30, episodic: 0.25, procedural: 0.10}
	case "planner", "architect", "splitter", "coordinator", "composer":
		return shareSet{working: 0.15, semantic: 0.40, episodic: 0.35, procedural: 0.10}
	case "explorer", "context", "docs", "memory":
		return shareSet{working: 0.20, semantic: 0.45, episodic: 0.30, procedural: 0.05}
	default:
		return shareSet{working: 0.30, semantic: 0.35, episodic: 0.25, procedural: 0.10}
	}
}

// renderEpisodes formats the recalled episodes as short, actionable lines.
func (s *Store) renderEpisodes(rc RunContext, budgetTokens int) string {
	if budgetTokens <= 0 {
		return ""
	}
	recalled := s.episodes.RecallEpisodes(Query{
		Text:     rc.Query,
		Files:    rc.Files,
		Tags:     rc.Tags,
		Language: rc.Language,
	}, 3)
	if len(recalled) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Similar work done before\n\n")
	for _, e := range recalled {
		verdict := "✓"
		if !e.Success {
			verdict = "✗"
		}
		fmt.Fprintf(&b, "- %s %s", verdict, firstLine(e.Query, 120))
		if len(e.FilesChanged) > 0 {
			fmt.Fprintf(&b, " → touched `%s`", strings.Join(dedupe(e.FilesChanged, 4), "`, `"))
		}
		b.WriteString("\n")
		for _, f := range e.Failures {
			if !f.Resolved() {
				continue
			}
			fmt.Fprintf(&b, "  - hit: %s → fixed by %s\n", firstLine(f.Message, 100), firstLine(orElse(f.Resolution, f.ResolvedBy), 80))
		}
	}
	return fitToTokens(b.String(), budgetTokens, s.count)
}

// Flush persists every dirty layer. Safe to call often.
func (s *Store) Flush() error {
	if s.readOnly {
		return nil
	}
	return errors.Join(
		s.episodes.Flush(),
		s.facts.Flush(),
		s.procedures.Flush(),
		s.dumpWorking(),
	)
}

// dumpWorking writes the current short-term state as Markdown so a user can
// see exactly what the harness believed mid-run. Purely for inspection.
func (s *Store) dumpWorking() error {
	if s.memDir == "" {
		return nil
	}
	body := s.working.Render(4000)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	header := fmt.Sprintf("# Working memory\n\n_Snapshot from %s. Regenerated every run; safe to delete._\n\n",
		s.now().UTC().Format(time.RFC3339))
	return atomicfile.Write(filepath.Join(s.memDir, "WORKING.md"), []byte(header+body), 0o600)
}

// Close flushes and releases the store.
func (s *Store) Close() error { return s.Flush() }

// ModelFamily reduces a concrete model id to the family that behavioral
// lessons actually generalize across: quantization, parameter count, and
// serving-format suffixes are dropped, the rest is kept.
//
//	"Qwen3-Coder-30B-A3B-Instruct-MLX-4bit" → "qwen3-coder"
//	"qwen2.5-coder:14b"                     → "qwen2.5-coder"
//	"deepseek-chat"                         → "deepseek"
//	"gpt-4o-mini"                           → "gpt-4o-mini"
func ModelFamily(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:] // strip provider prefixes like "ollama/…"
	}
	m = strings.ReplaceAll(m, ":", "-")
	m = strings.ReplaceAll(m, "_", "-")
	parts := strings.Split(m, "-")
	var kept []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		// The first serving-detail token ends the family: everything after a
		// size or quantization marker describes the build, not the model.
		if isNoiseToken(p) {
			break
		}
		kept = append(kept, p)
		if len(kept) >= 3 {
			break
		}
	}
	if len(kept) == 0 {
		return m
	}
	return strings.Join(kept, "-")
}

var noiseTokens = map[string]bool{
	"instruct": true, "chat": true, "it": true, "mlx": true, "gguf": true,
	"awq": true, "gptq": true, "bit": true, "bf16": true, "fp16": true, "f16": true,
	"int4": true, "int8": true, "quantized": true, "latest": true, "hf": true,
}

func isNoiseToken(p string) bool {
	if noiseTokens[p] {
		return true
	}
	// "30b", "3b", "a3b", "q4", "q4km", "4bit", "v1"
	if len(p) >= 2 {
		switch {
		case p[len(p)-1] == 'b' && allDigits(p[:len(p)-1]):
			return true
		case p[0] == 'q' && len(p) >= 2 && p[1] >= '0' && p[1] <= '9':
			return true
		case strings.HasSuffix(p, "bit") && allDigits(strings.TrimSuffix(p, "bit")):
			return true
		case p[0] == 'a' && strings.HasSuffix(p, "b") && allDigits(p[1:len(p)-1]):
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

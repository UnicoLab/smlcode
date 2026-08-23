package evolve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Reflection bounds.
const (
	MaxReflectionFailures = 12
	MaxReflectionBytes    = 16 * 1024
)

// FailureEvent is one failure a run hit and what became of it.
type FailureEvent struct {
	Signal Signal
	// RuleID is the repair rule that fixed it ("" if none did).
	RuleID string
	// ResolvedBy is "rule:<id>", "llm", "retry", "human" or "" (unresolved).
	ResolvedBy string
	// Resolution describes the fix in one line.
	Resolution string
	// Repair, when set, is what actually worked — the raw material for a new
	// rule when this fingerprint has never been seen before.
	Repair *Repair
	// Attempts is how many tries the recovery took.
	Attempts int
	// Recheck is an optional cheap way to prove the failure has not returned.
	Recheck *Check
}

// Resolved reports whether the failure was fixed.
func (f FailureEvent) Resolved() bool { return strings.TrimSpace(f.ResolvedBy) != "" }

// FromMemory reports whether the fix came from a stored rule rather than a
// fresh LLM round-trip.
func (f FailureEvent) FromMemory() bool {
	return strings.HasPrefix(f.ResolvedBy, "rule:") || f.RuleID != ""
}

// GateResult is one quality gate outcome.
type GateResult struct {
	Name   string
	Passed bool
	Detail string
}

// DecisionRecord is one bandit choice a run made and how it turned out.
type DecisionRecord struct {
	Key     Key
	Arm     string
	Outcome Outcome
}

// RunReport is everything a finished run knows about itself. The orchestrator
// fills it in; Reflect turns it into episodes, rules, rewards and prose.
type RunReport struct {
	RunID     string
	StartedAt time.Time
	EndedAt   time.Time

	Query    string
	Language string
	Model    string
	Provider string
	Role     string

	PlannedTasks   int
	CompletedTasks int
	BlockedTasks   int

	Gates     []GateResult
	Failures  []FailureEvent
	Decisions []DecisionRecord

	Retries        int
	LLMCalls       int
	TokensIn       int
	TokensOut      int
	ToolCalls      int
	ToolErrors     int
	RedundantCalls int

	FilesChanged   []string
	ToolsUsed      []string
	Commands       []memory.Command
	EditFormat     string
	EditsAttempted int
	EditsApplied   int

	Summary string
	Tags    []string
}

// WallMS is the run's wall time in milliseconds.
func (r RunReport) WallMS() int64 {
	if r.StartedAt.IsZero() || r.EndedAt.IsZero() || !r.EndedAt.After(r.StartedAt) {
		return 0
	}
	return r.EndedAt.Sub(r.StartedAt).Milliseconds()
}

// Success is the deterministic verdict: every planned task done, no blocked
// task, and every gate that ran passed.
func (r RunReport) Success() bool {
	if r.BlockedTasks > 0 {
		return false
	}
	if r.PlannedTasks > 0 && r.CompletedTasks < r.PlannedTasks {
		return false
	}
	for _, g := range r.Gates {
		if !g.Passed {
			return false
		}
	}
	return r.PlannedTasks > 0 || r.CompletedTasks > 0 || len(r.FilesChanged) > 0
}

// Reflection is the deterministic output of comparing intent with outcome.
type Reflection struct {
	// Episode is ready to hand to memory.Store.RecordEpisode.
	Episode memory.Episode
	// Candidates are new rules synthesized from failures that were resolved
	// for the first time.
	Candidates []candidateRule
	// Rewards are the bandit updates this run earned.
	Rewards []DecisionRecord
	// Checks are the regression checks this run contributed.
	Checks []Check
	// RuleOutcomes credits or debits the rules the run actually used.
	RuleOutcomes map[string]bool
	// Markdown is the human-readable report.
	Markdown string
	// ResolvedFromMemory / ResolvedFromLLM / Unresolved partition the failures.
	ResolvedFromMemory int
	ResolvedFromLLM    int
	Unresolved         int
}

type candidateRule struct {
	Signal     Signal
	Resolution Resolution
}

// Reflect compares intent with outcome. It is pure and deterministic: same
// report in, same reflection out, no I/O and no model.
func Reflect(r RunReport) Reflection {
	ref := Reflection{RuleOutcomes: map[string]bool{}}
	now := r.EndedAt
	if now.IsZero() {
		now = time.Now()
	}

	failures := r.Failures
	if len(failures) > MaxReflectionFailures {
		failures = failures[:MaxReflectionFailures]
	}

	var notes []memory.FailureNote
	for _, f := range failures {
		fp := Analyze(f.Signal)
		resolvedBy := f.ResolvedBy
		if resolvedBy == "" && f.RuleID != "" {
			resolvedBy = "rule:" + f.RuleID
		}
		notes = append(notes, memory.FailureNote{
			Fingerprint: fp.ID,
			Class:       string(fp.Class),
			Tool:        fp.Tool,
			Message:     firstLineOf(f.Signal.Message, 240),
			Resolution:  f.Resolution,
			ResolvedBy:  resolvedBy,
			Attempts:    f.Attempts,
		})

		switch {
		case !f.Resolved():
			ref.Unresolved++
		case f.FromMemory():
			ref.ResolvedFromMemory++
		default:
			ref.ResolvedFromLLM++
		}

		if f.RuleID != "" {
			// A rule was applied: credit it if the failure ended up resolved.
			if prev, seen := ref.RuleOutcomes[f.RuleID]; !seen || prev {
				ref.RuleOutcomes[f.RuleID] = f.Resolved()
			}
		}
		// Synthesize a candidate rule only for failures that were fixed
		// WITHOUT an existing rule — those are the ones we do not yet know.
		if f.Resolved() && f.RuleID == "" && f.Repair != nil && f.Repair.Validate() == nil {
			ref.Candidates = append(ref.Candidates, candidateRule{
				Signal: f.Signal,
				Resolution: Resolution{
					Repair:   *f.Repair,
					Evidence: r.RunID,
					Scope:    ScopeProject,
				},
			})
		}
		if f.Recheck != nil {
			c := *f.Recheck
			c.Fingerprint = fp.ID
			c.Class = fp.Class
			if c.Description == "" {
				c.Description = Describe(fp.Class) + ": " + firstLineOf(f.Signal.Message, 160)
			}
			c.RuleID = f.RuleID
			c.Normalize(now)
			ref.Checks = append(ref.Checks, c)
		}
	}

	var gates []memory.GateOutcome
	for _, g := range r.Gates {
		gates = append(gates, memory.GateOutcome{Name: g.Name, Passed: g.Passed, Detail: g.Detail})
	}

	ref.Episode = memory.Episode{
		At:             now,
		RunID:          r.RunID,
		Query:          r.Query,
		Summary:        r.Summary,
		Language:       r.Language,
		Model:          r.Model,
		Provider:       r.Provider,
		Role:           r.Role,
		FilesChanged:   r.FilesChanged,
		ToolsUsed:      r.ToolsUsed,
		Commands:       r.Commands,
		Failures:       notes,
		Gates:          gates,
		Tags:           r.Tags,
		EditFormat:     r.EditFormat,
		EditsAttempted: r.EditsAttempted,
		EditsApplied:   r.EditsApplied,
		LLMCalls:       r.LLMCalls,
		TokensIn:       r.TokensIn,
		TokensOut:      r.TokensOut,
		WallMS:         r.WallMS(),
		Retries:        r.Retries,
		Success:        r.Success(),
	}
	ref.Episode.Plan = append(ref.Episode.Plan, r.PlanTitles()...)
	// Normalize here rather than at record time so every consumer — the
	// metrics record, a UI, a test — sees the same bounded episode.
	ref.Episode.Normalize(now)
	ref.Rewards = append(ref.Rewards, r.Decisions...)
	ref.Markdown = renderReflection(r, ref)
	return ref
}

// PlanTitles derives plan lines from the counts when the caller did not supply
// task titles. Kept as a method so callers can override by setting Tags.
func (r RunReport) PlanTitles() []string {
	if r.PlannedTasks <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("%d task(s) planned, %d completed, %d blocked",
		r.PlannedTasks, r.CompletedTasks, r.BlockedTasks)}
}

// Apply commits a reflection: it records the episode, credits or debits the
// rules that were used, learns candidate rules, updates the bandit and stores
// the regression checks. Every step is best-effort; a memory failure never
// fails a run.
func (ref Reflection) Apply(mem *memory.Store, rules *Rules, bandit *Bandit, regs *Regressions) error {
	var errs []error
	if mem != nil {
		if err := mem.RecordEpisode(ref.Episode); err != nil {
			errs = append(errs, err)
		}
	}
	if rules != nil {
		for _, id := range sortedBoolKeys(ref.RuleOutcomes) {
			rules.Observe(id, ref.RuleOutcomes[id])
		}
		for _, c := range ref.Candidates {
			rules.Learn(c.Signal, c.Resolution)
		}
		if err := rules.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if bandit != nil {
		for _, d := range ref.Rewards {
			bandit.Update(d.Key, d.Arm, d.Outcome)
		}
		if err := bandit.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if regs != nil {
		for _, c := range ref.Checks {
			regs.Add(c)
		}
		if err := regs.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if mem != nil {
		// Procedural memory: which edit format actually applied, for this
		// model and language, across projects.
		if ref.Episode.EditFormat != "" && ref.Episode.EditsAttempted > 0 {
			mem.Procedural().Record(memory.ProcKey{
				Topic:       memory.TopicEditFormat,
				Option:      ref.Episode.EditFormat,
				ModelFamily: memory.ModelFamily(ref.Episode.Model),
				Language:    ref.Episode.Language,
			}, ref.Episode.EditsApplied == ref.Episode.EditsAttempted,
				fmt.Sprintf("%d/%d hunks applied", ref.Episode.EditsApplied, ref.Episode.EditsAttempted))
		}
		if err := mem.Flush(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WriteReflection saves REFLECTION.md under <projectDir>/.slmcode/memory/.
func WriteReflection(projectDir string, ref Reflection) error {
	if projectDir == "" || strings.TrimSpace(ref.Markdown) == "" {
		return nil
	}
	path := filepath.Join(projectDir, memory.SlmDirName, memory.DirName, "REFLECTION.md")
	body := ref.Markdown
	if len(body) > MaxReflectionBytes {
		body = body[:MaxReflectionBytes] + "\n\n_[truncated]_\n"
	}
	return atomicfile.Write(path, []byte(body), 0o600)
}

// Enrich optionally adds an LLM-written paragraph to the reflection. It is
// strictly additive: an error, a timeout or an empty answer leaves the
// deterministic report exactly as it was.
func Enrich(ctx context.Context, ref *Reflection, summarize memory.Summarizer) {
	if ref == nil || summarize == nil {
		return
	}
	prompt := "In at most three sentences, state the single most useful lesson from this run report. " +
		"Only use facts present below. No preamble.\n\n" + ref.Markdown
	out, err := summarize(ctx, prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	if len(out) > 800 {
		out = out[:800]
	}
	ref.Markdown = strings.TrimRight(ref.Markdown, "\n") +
		"\n\n## Model commentary\n\n_Advisory only — the sections above are computed._\n\n" +
		strings.TrimSpace(out) + "\n"
}

func renderReflection(r RunReport, ref Reflection) string {
	var b strings.Builder
	stamp := r.EndedAt
	if stamp.IsZero() {
		stamp = time.Now()
	}
	b.WriteString("# Reflection\n\n")
	fmt.Fprintf(&b, "_Run `%s` — %s_\n\n", orUnset(r.RunID), stamp.UTC().Format(time.RFC3339))
	if r.Query != "" {
		fmt.Fprintf(&b, "**Task:** %s\n\n", firstLineOf(r.Query, 200))
	}

	b.WriteString("## Intent vs outcome\n\n")
	b.WriteString("| Measure | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Tasks planned → done | %d → %d |\n", r.PlannedTasks, r.CompletedTasks)
	if r.BlockedTasks > 0 {
		fmt.Fprintf(&b, "| Tasks blocked | %d |\n", r.BlockedTasks)
	}
	passed, total := 0, len(r.Gates)
	for _, g := range r.Gates {
		if g.Passed {
			passed++
		}
	}
	fmt.Fprintf(&b, "| Gates passed | %d/%d |\n", passed, total)
	if r.EditsAttempted > 0 {
		fmt.Fprintf(&b, "| Edits applied | %d/%d (%d%%) |\n",
			r.EditsApplied, r.EditsAttempted, percent(r.EditsApplied, r.EditsAttempted))
	}
	fmt.Fprintf(&b, "| Retries | %d |\n", r.Retries)
	fmt.Fprintf(&b, "| LLM calls | %d |\n", r.LLMCalls)
	fmt.Fprintf(&b, "| Tokens in/out | %d / %d |\n", r.TokensIn, r.TokensOut)
	if r.ToolCalls > 0 {
		fmt.Fprintf(&b, "| Tool calls (errors, repeats) | %d (%d, %d) |\n", r.ToolCalls, r.ToolErrors, r.RedundantCalls)
	}
	fmt.Fprintf(&b, "| Wall time | %s |\n", time.Duration(r.WallMS())*time.Millisecond)
	fmt.Fprintf(&b, "| Verdict | %s |\n\n", verdictWord(r.Success()))

	if len(r.Failures) > 0 {
		b.WriteString("## Failures and how each was handled\n\n")
		for i, f := range r.Failures {
			if i >= MaxReflectionFailures {
				fmt.Fprintf(&b, "- _…and %d more_\n", len(r.Failures)-MaxReflectionFailures)
				break
			}
			fp := Analyze(f.Signal)
			how := "**unresolved**"
			switch {
			case f.FromMemory():
				how = "fixed from memory (" + orUnset(f.RuleID) + ")"
			case f.Resolved():
				how = "fixed by " + f.ResolvedBy
			}
			fmt.Fprintf(&b, "- `%s` %s — %s → %s\n", fp.ID, fp.Class, firstLineOf(f.Signal.Message, 120), how)
			if f.Resolution != "" {
				fmt.Fprintf(&b, "  - %s\n", firstLineOf(f.Resolution, 160))
			}
		}
		fmt.Fprintf(&b, "\nResolved from memory: %d · from a fresh LLM round-trip: %d · unresolved: %d\n\n",
			ref.ResolvedFromMemory, ref.ResolvedFromLLM, ref.Unresolved)
	}

	if len(ref.Candidates) > 0 {
		b.WriteString("## New repair rules learned\n\n")
		for _, c := range ref.Candidates {
			fp := Analyze(c.Signal)
			fmt.Fprintf(&b, "- %s → %s\n", fp.Class, c.Resolution.Repair.String())
			fmt.Fprintf(&b, "  - %s\n", firstLineOf(c.Resolution.Repair.Guidance, 200))
		}
		b.WriteString("\n_New rules start at low confidence and are suggested, not applied, until they prove themselves._\n\n")
	}

	if len(r.Decisions) > 0 {
		b.WriteString("## Choices made and what they earned\n\n")
		b.WriteString("| Decision | Chose | Reward |\n|---|---|---|\n")
		for _, d := range r.Decisions {
			fmt.Fprintf(&b, "| %s | %s | %.2f |\n", d.Key.Decision, d.Arm, d.Outcome.Reward())
		}
		b.WriteString("\n")
	}

	if len(ref.Checks) > 0 {
		b.WriteString("## Regression checks added\n\n")
		for _, c := range ref.Checks {
			switch c.Kind {
			case CheckCommand:
				fmt.Fprintf(&b, "- `%s` — %s\n", c.Command, c.Description)
			case CheckNone:
				fmt.Fprintf(&b, "- (no cheap re-check) — %s\n", c.Description)
			default:
				fmt.Fprintf(&b, "- %s `%s` — %s\n", c.Kind, c.Path, c.Description)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func verdictWord(ok bool) string {
	if ok {
		return "success"
	}
	return "incomplete"
}

func percent(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

func orUnset(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unset"
	}
	return s
}

func firstLineOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > n {
		s = strings.TrimSpace(s[:n]) + "…"
	}
	return s
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package autoresearch

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"
)

// Change origins.
const (
	// OriginDeterministic is the seeded coordinate-descent proposer.
	OriginDeterministic = "deterministic"
	// OriginLLM is the optional prompt-rewriting proposer.
	OriginLLM = "llm"
)

// ErrNoProposal means the proposer has nothing left to try. It is a normal
// end of run, not a failure: the ratchet reports "surface exhausted" and
// returns the best artifact it found.
var ErrNoProposal = errors.New("autoresearch: no further proposal available")

// Change is ONE knob moving to ONE new value.
//
// There is no plural form of this type anywhere in the package, and that is the
// design: a trial that changes two things at once produces a measurement that
// cannot be attributed to either, and a sequence of such trials is a random
// walk with a changelog.
type Change struct {
	KnobID string `json:"knob_id"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// IsZero reports whether the change is empty.
func (c Change) IsZero() bool { return c.KnobID == "" }

// String renders the change for the CLI.
func (c Change) String() string {
	return fmt.Sprintf("%s: %s → %s", c.KnobID, clip(c.Before, 40), clip(c.After, 40))
}

// History is what the ratchet has already tried this run.
type History struct {
	Trials []Trial
}

// Tried reports whether a knob has already been set to a value.
func (h History) Tried(knobID, value string) bool {
	for _, t := range h.Trials {
		if t.KnobID == knobID && t.After == value {
			return true
		}
	}
	return false
}

// Len is the number of recorded trials.
func (h History) Len() int { return len(h.Trials) }

// Proposer picks the next single change to try.
type Proposer interface {
	Propose(ctx context.Context, s *Surface, h History) (Change, error)
}

// DeterministicProposer explores the surface systematically, one knob at a
// time, from a seed.
//
// CYCLIC coordinate descent, not random search and not an exhaustive line
// search per coordinate. It fixes a knob order and a per-knob value order (both
// seeded permutations rather than draws, so the space is enumerated exactly
// once), then always moves the LEAST-tried knob next. The cyclic part matters
// more than it looks: a run has a budget of a dozen experiments and a surface
// of twenty-odd knobs, so a proposer that exhausted temperature's twenty-one
// values first would spend the entire budget without ever touching the other
// nineteen knobs.
//
// Three properties fall out of this, all of them required:
//
//   - the same seed produces the same experiment sequence, so a run replays;
//   - the space is finite, so "surface exhausted" is a fact the proposer can
//     report instead of looping forever;
//   - a small budget still sees breadth.
//
// It holds no state between calls. The sequence is a pure function of
// (seed, surface, history), which is why replay survives a restarted process.
type DeterministicProposer struct {
	seed int64
}

// NewDeterministicProposer builds the seeded proposer.
func NewDeterministicProposer(seed int64) *DeterministicProposer {
	return &DeterministicProposer{seed: seed}
}

// Seed returns the seed, for the journal.
func (p *DeterministicProposer) Seed() int64 { return p.seed }

// Propose returns the next untried (knob, value) pair.
func (p *DeterministicProposer) Propose(ctx context.Context, s *Surface, h History) (Change, error) {
	if err := ctx.Err(); err != nil {
		return Change{}, err
	}
	if s == nil || s.Len() == 0 {
		return Change{}, ErrNoProposal
	}
	knobs := s.Knobs() // sorted by ID — the permutation below needs a fixed input order

	// #nosec G404 -- a seeded, reproducible exploration order. Cryptographic
	// randomness would defeat the point: the sequence must replay exactly.
	perm := rand.New(rand.NewSource(p.seed)).Perm(len(knobs))

	// rank[i] is knob i's position in the seeded cycle; tries[i] is how often
	// this run has already moved it. Ordering by (tries, rank) is what makes
	// the walk cyclic: every knob gets its first value before any knob gets its
	// second.
	type slot struct{ index, rank, tries int }
	slots := make([]slot, len(knobs))
	for rank, ki := range perm {
		slots[ki] = slot{index: ki, rank: rank}
	}
	for _, t := range h.Trials {
		for i := range knobs {
			if knobs[i].ID == t.KnobID {
				slots[i].tries++
				break
			}
		}
	}
	sort.SliceStable(slots, func(a, b int) bool {
		if slots[a].tries != slots[b].tries {
			return slots[a].tries < slots[b].tries
		}
		return slots[a].rank < slots[b].rank
	})

	for _, sl := range slots {
		k := knobs[sl.index]
		cands := k.Domain.Candidates()
		if len(cands) == 0 {
			continue // text knobs are the LLM proposer's territory
		}
		// #nosec G404 -- see above; mixing the knob id keeps two knobs from
		// sharing a value order without making the sequence unpredictable.
		vperm := rand.New(rand.NewSource(p.seed ^ hash64(k.ID))).Perm(len(cands))
		for _, vi := range vperm {
			v := cands[vi]
			if v == k.Value || h.Tried(k.ID, v) {
				continue
			}
			return Change{
				KnobID: k.ID,
				Before: k.Value,
				After:  v,
				Origin: OriginDeterministic,
				Reason: fmt.Sprintf("systematic sweep of %s (%s)", k.Field, k.Domain),
			}, nil
		}
	}
	return Change{}, ErrNoProposal
}

// Rewriter is an optional text-generating model, shaped exactly like
// memory.Summarizer so an orchestrator's existing adapter fits with no glue.
type Rewriter func(ctx context.Context, prompt string) (string, error)

// LLMProposer rewrites a system_prompt with a model — and is strictly optional.
//
// "Optional" is enforced rather than documented: with a nil Rewriter it IS the
// fallback proposer, and every failure mode of the model (an error, an empty
// answer, an answer that is unchanged, or one that busts the domain) also falls
// back. There is no path through this type on which a misbehaving small model
// can stop a run or write nonsense to a file — which is the repo's standing
// rule for LLM assistance: deterministic core, optional model, never dependent
// on a small model getting it right.
type LLMProposer struct {
	rewrite  Rewriter
	fallback Proposer
	// every asks the model on one proposal in N; the rest are deterministic.
	// Prompt rewrites are expensive to evaluate and easy to overfit, so the
	// numeric sweep stays the backbone.
	every int
}

// NewLLMProposer wraps a deterministic fallback with optional prompt rewriting.
// A nil rewrite (no model configured) makes this a pass-through.
func NewLLMProposer(rewrite Rewriter, fallback Proposer) *LLMProposer {
	if fallback == nil {
		fallback = NewDeterministicProposer(0)
	}
	return &LLMProposer{rewrite: rewrite, fallback: fallback, every: 3}
}

// Every sets how often the model is asked (1 = every proposal).
func (p *LLMProposer) Every(n int) *LLMProposer {
	if n > 0 {
		p.every = n
	}
	return p
}

// Propose asks the model for a prompt rewrite, or falls back.
func (p *LLMProposer) Propose(ctx context.Context, s *Surface, h History) (Change, error) {
	if p.rewrite == nil || s == nil {
		return p.fallback.Propose(ctx, s, h)
	}
	if p.every > 1 && h.Len()%p.every != 0 {
		return p.fallback.Propose(ctx, s, h)
	}
	if c, ok := p.proposePrompt(ctx, s, h); ok {
		return c, nil
	}
	return p.fallback.Propose(ctx, s, h)
}

// proposePrompt is the whole model-facing surface of this package. Everything
// it returns is validated before it can reach a file.
func (p *LLMProposer) proposePrompt(ctx context.Context, s *Surface, h History) (Change, bool) {
	for _, k := range s.Knobs() { // sorted: the first text knob is a stable choice
		if k.Domain.Kind != KnobText {
			continue
		}
		out, err := p.rewrite(ctx, rewritePrompt(k, h))
		if err != nil {
			return Change{}, false
		}
		next := sanitizePrompt(out, k.Domain)
		if next == "" || next == k.Value {
			return Change{}, false
		}
		if h.Tried(k.ID, next) {
			return Change{}, false
		}
		return Change{
			KnobID: k.ID,
			Before: k.Value,
			After:  next,
			Origin: OriginLLM,
			Reason: "model-proposed rewrite of " + k.Field,
		}, true
	}
	return Change{}, false
}

// rewritePrompt builds the instruction sent to the model. It carries the recent
// trial outcomes so the model is revising against evidence rather than taste.
func rewritePrompt(k Knob, h History) string {
	var b strings.Builder
	b.WriteString("You are tuning ONE system prompt used by a small coding model.\n")
	b.WriteString("Rewrite it to be more precise and easier for a 7B-32B model to follow.\n")
	b.WriteString("Keep every hard requirement and every output-format instruction intact.\n")
	b.WriteString("Reply with the rewritten prompt only — no preamble, no code fence, no commentary.\n\n")
	if n := len(h.Trials); n > 0 {
		b.WriteString("Recent experiments on this harness:\n")
		from := n - 5
		if from < 0 {
			from = 0
		}
		for _, t := range h.Trials[from:] {
			outcome := "reverted"
			if t.Kept {
				outcome = "kept"
			}
			fmt.Fprintf(&b, "- %s → %s: %s (%s)\n", t.KnobID, clip(t.After, 30), outcome, t.Reason)
		}
		b.WriteString("\n")
	}
	b.WriteString("CURRENT PROMPT (agent " + k.Owner + "):\n")
	b.WriteString(k.Value)
	b.WriteString("\n")
	return b.String()
}

// sanitizePrompt makes a model answer safe to write, or rejects it.
func sanitizePrompt(out string, d Domain) string {
	s := strings.TrimSpace(out)
	// Models fence prose about half the time no matter what the instruction
	// said. Unfence rather than reject: the content is usually fine.
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !d.Allows(s) {
		return ""
	}
	return s
}

func hash64(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	// Mask the sign bit: a negative seed is legal but makes the value order
	// depend on two's-complement details nobody should have to reason about.
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// clip renders a possibly multi-line value on one line, bounded.
func clip(s string, n int) string {
	return clipRaw(strings.ReplaceAll(strings.TrimSpace(s), "\n", "⏎"), n)
}

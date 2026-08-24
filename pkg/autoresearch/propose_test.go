package autoresearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// sequence drives a proposer for n rounds without applying anything, so the
// order it WOULD explore is observable on its own.
func sequence(t *testing.T, p Proposer, root string, n int) []string {
	t.Helper()
	s := mustReflect(t, root)
	var (
		h   History
		out []string
	)
	for i := 0; i < n; i++ {
		c, err := p.Propose(context.Background(), s, h)
		if errors.Is(err, ErrNoProposal) {
			break
		}
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		out = append(out, c.KnobID+"="+c.After)
		h.Trials = append(h.Trials, Trial{KnobID: c.KnobID, After: c.After})
	}
	return out
}

// TestDeterministicProposerReplaysExactlyFromASeed is the replay guarantee: a
// seed fixes the entire experiment sequence, including across a fresh proposer
// (the sequence is a pure function of seed, surface and history, so it survives
// a restarted process).
func TestDeterministicProposerReplaysExactlyFromASeed(t *testing.T) {
	root := newTestProject(t)

	first := sequence(t, NewDeterministicProposer(42), root, 25)
	second := sequence(t, NewDeterministicProposer(42), root, 25)
	if !equalStrings(first, second) {
		t.Fatalf("seed 42 produced two different sequences:\n%v\n%v", first, second)
	}
	if len(first) != 25 {
		t.Fatalf("expected 25 proposals, got %d", len(first))
	}

	// And a different seed must actually explore differently, or "seeded" would
	// be a decorative word.
	if other := sequence(t, NewDeterministicProposer(43), root, 25); equalStrings(other, first) {
		t.Fatal("seed 43 produced the same sequence as seed 42")
	}
}

// TestDeterministicProposerCyclesAcrossKnobs: with a budget far smaller than
// one knob's domain, the walk must still see breadth. An exhaustive line search
// per coordinate would spend all twelve experiments on `temperature` and never
// look at anything else.
func TestDeterministicProposerCyclesAcrossKnobs(t *testing.T) {
	root := newTestProject(t)
	seq := sequence(t, NewDeterministicProposer(5), root, 12)
	seen := map[string]bool{}
	for _, s := range seq {
		seen[strings.SplitN(s, "=", 2)[0]] = true
	}
	if len(seen) != len(seq) {
		t.Fatalf("12 proposals touched only %d distinct knobs: %v", len(seen), seq)
	}
	// The second pass revisits, in the same cyclic order.
	long := sequence(t, NewDeterministicProposer(5), root, 24)
	if !equalStrings(long[:12], seq) {
		t.Fatal("the first pass is not a prefix of a longer run — the walk is not cyclic")
	}
}

func TestDeterministicProposerChangesOneKnobAtATime(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	var h History
	for i := 0; i < 15; i++ {
		c, err := NewDeterministicProposer(7).Propose(context.Background(), s, h)
		if errors.Is(err, ErrNoProposal) {
			break
		}
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		k, ok := s.Knob(c.KnobID)
		if !ok {
			t.Fatalf("proposed a knob that is not on the surface: %s", c.KnobID)
		}
		if c.Before != k.Value {
			t.Errorf("Before = %q but the knob currently holds %q", c.Before, k.Value)
		}
		if c.After == c.Before {
			t.Errorf("proposed a no-op change on %s", c.KnobID)
		}
		if !k.Domain.Allows(c.After) {
			t.Errorf("%s = %q is outside its domain %s", c.KnobID, c.After, k.Domain)
		}
		if c.Origin != OriginDeterministic {
			t.Errorf("Origin = %q", c.Origin)
		}
		h.Trials = append(h.Trials, Trial{KnobID: c.KnobID, After: c.After})
	}
}

func TestDeterministicProposerExhaustsTheSurface(t *testing.T) {
	// A surface small enough to enumerate: one enum knob with two values.
	root := t.TempDir()
	agents := root + "/.slmcode/agents"
	mkdirAll(t, agents)
	writeFile(t, agents+"/tiny.yaml", "id: tiny\ntemperature: 0.2\n")
	s, err := Reflect(Options{Root: root, NoConfig: true})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}

	p := NewDeterministicProposer(3)
	var h History
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		c, err := p.Propose(context.Background(), s, h)
		if errors.Is(err, ErrNoProposal) {
			// 21 values in the domain, minus the one it already holds.
			if len(seen) != 20 {
				t.Fatalf("exhausted after %d distinct values, want 20", len(seen))
			}
			return
		}
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if seen[c.After] {
			t.Fatalf("proposed %q twice", c.After)
		}
		seen[c.After] = true
		h.Trials = append(h.Trials, Trial{KnobID: c.KnobID, After: c.After})
	}
	t.Fatal("proposer never reported ErrNoProposal — the search space is not finite")
}

func TestDeterministicProposerSkipsTextKnobs(t *testing.T) {
	root := newTestProject(t)
	for _, id := range sequence(t, NewDeterministicProposer(11), root, 60) {
		if strings.HasPrefix(id, "agent:worker.system_prompt=") {
			t.Fatal("the deterministic proposer proposed a prompt rewrite")
		}
	}
}

// TestLLMProposerIsStrictlyOptional: with no model configured the engine must
// run entirely on the deterministic proposer, and every way a small model can
// misbehave must fall back rather than fail or write nonsense.
func TestLLMProposerIsStrictlyOptional(t *testing.T) {
	root := newTestProject(t)
	det := sequence(t, NewDeterministicProposer(5), root, 12)

	t.Run("nil rewriter", func(t *testing.T) {
		got := sequence(t, NewLLMProposer(nil, NewDeterministicProposer(5)), root, 12)
		if !equalStrings(got, det) {
			t.Fatalf("a nil rewriter changed the sequence:\n%v\n%v", det, got)
		}
	})

	misbehaviors := []struct {
		name string
		rw   Rewriter
	}{
		{"error", func(context.Context, string) (string, error) { return "", errors.New("model down") }},
		{"empty", func(context.Context, string) (string, error) { return "   \n ", nil }},
		{"unchanged", func(context.Context, string) (string, error) { return "Do the task.\nKeep diffs small.", nil }},
		{"too long", func(context.Context, string) (string, error) { return strings.Repeat("x", MaxPromptLen+1), nil }},
	}
	for _, mb := range misbehaviors {
		t.Run(mb.name, func(t *testing.T) {
			p := NewLLMProposer(mb.rw, NewDeterministicProposer(5)).Every(1)
			got := sequence(t, p, root, 12)
			if !equalStrings(got, det) {
				t.Fatalf("a %s model answer was not fully ignored:\n%v\n%v", mb.name, det, got)
			}
		})
	}
}

func TestLLMProposerRewritesAPromptAndUnfencesIt(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	rw := func(ctx context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "Keep diffs small.") {
			return "", fmt.Errorf("the current prompt was not shown to the model")
		}
		return "```\nDo the task precisely.\nKeep diffs small and reviewable.\n```", nil
	}
	c, err := NewLLMProposer(rw, NewDeterministicProposer(1)).Every(1).
		Propose(context.Background(), s, History{})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if c.KnobID != "agent:worker.system_prompt" {
		t.Fatalf("rewrote %s", c.KnobID)
	}
	if c.Origin != OriginLLM {
		t.Errorf("Origin = %q, want %q", c.Origin, OriginLLM)
	}
	if strings.Contains(c.After, "```") {
		t.Errorf("the code fence was written into the prompt: %q", c.After)
	}
	if want := "Do the task precisely.\nKeep diffs small and reviewable."; c.After != want {
		t.Errorf("After = %q, want %q", c.After, want)
	}
	// The rewrite must still be applicable — a proposal that Apply refuses is
	// not a proposal.
	if err := s.Apply(c); err != nil {
		t.Fatalf("Apply of an LLM proposal: %v", err)
	}
}

func TestHistoryTriedMatchesOnKnobAndValue(t *testing.T) {
	h := History{Trials: []Trial{
		{KnobID: "config:think_passes", After: "2"},
		{KnobID: "agent:worker.temperature", After: "0.35"},
	}}
	if !h.Tried("config:think_passes", "2") {
		t.Error("Tried missed an exact match")
	}
	if h.Tried("config:think_passes", "3") {
		t.Error("Tried matched a different value")
	}
	if h.Tried("config:max_retries", "2") {
		t.Error("Tried matched a different knob")
	}
}

package evolve

import (
	"strings"
	"testing"
)

// The three refusal shapes pkg/workspace's FocusGuard emits, verbatim. They are
// duplicated here on purpose: this package must not depend on pkg/workspace,
// and pkg/loop/outofscope_test.go asserts the real guard still produces text
// these fixtures represent.
const (
	roRefusal = "out-of-scope write blocked: stats.go — the explorer role does not edit files at all.\n" +
		"This role reads and reports; a later task makes the change. Rewording this call cannot make it work.\n" +
		"Next action: stop calling edit/write tools and finish now — put the file, the symbol and the " +
		"change that is needed in your answer"

	focusRefusal = "out-of-scope write blocked: internal/api/handler_v2.go — the worker role writes only inside " +
		"the task focus files and their packages.\n" +
		"The path is the problem, not the call: rewording it cannot make it work.\n" +
		"Next action: make the change in a focus file. If it genuinely belongs in internal/api/handler_v2.go, " +
		"say so in your answer so the planner can rescope — do not repeat this call"

	reviewRefusal = "out-of-scope files_changed: main.go, cmd/run.go — edit only the task focus files. " +
		"If the change genuinely belongs elsewhere, say so in your answer so the planner can rescope."
)

func TestOutOfScopeWriteIsClassified(t *testing.T) {
	for _, msg := range []string{roRefusal, focusRefusal, reviewRefusal} {
		if got := Classify(Signal{Tool: "ws_edit", Message: msg}); got != ClassOutOfScopeWrite {
			t.Errorf("Classify = %q, want %q for:\n%s", got, ClassOutOfScopeWrite, msg)
		}
	}
}

// ClassOutOfScopeWrite is structural: the path, the role and which of the three
// refusal texts fired are presentational, so every out-of-scope write in a
// project collapses to ONE fingerprint and therefore to one stored repair.
func TestOutOfScopeWriteFingerprintsStructurally(t *testing.T) {
	a := Analyze(Signal{Tool: "ws_edit", Message: roRefusal, Language: "go", Model: "qwen2.5-coder:9b"})
	b := Analyze(Signal{Tool: "ws_edit", Message: focusRefusal, Language: "go", Model: "qwen2.5-coder:9b"})
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("two out-of-scope refusals produced different fingerprints:\n  %s (%s)\n  %s (%s)",
			a.ID, a.Norm, b.ID, b.Norm)
	}
	if a.Salient != "" {
		t.Errorf("structural class must exclude the message from the hash, got salient %q", a.Salient)
	}
	// The message is still kept for humans and for rule evidence.
	if a.Norm == "" {
		t.Error("normalized message dropped; REFLECTION.md would have nothing to show")
	}
	// Tool still participates: ws_write is a different call to repair.
	c := Analyze(Signal{Tool: "ws_write", Message: focusRefusal, Language: "go", Model: "qwen2.5-coder:9b"})
	if c.ID == a.ID {
		t.Error("fingerprint ignored the tool; ws_edit and ws_write must stay distinguishable")
	}
}

// The false-positive guard, in the spirit of the `waveTimeout` note on
// hasNeedle: a class that fires on a loose word sends the wrong repair to a
// real failure. Every needle here is a full phrase for that reason.
func TestOutOfScopeClassDoesNotSwallowUnrelatedMessages(t *testing.T) {
	cases := []struct {
		msg  string
		want Class // "" = only assert it is NOT ClassOutOfScopeWrite
	}{
		{msg: "./pkg/a/b.go:12:6: cannot use scope as type Scope in argument to Check", want: ClassCompileError},
		{msg: "./pkg/loop/wave.go:88:2: declared and not used: outOfScopeWrites", want: ClassCompileError},
		{msg: "that refactor is out of scope for this release"},
		{msg: "write /dev/stdout: broken pipe"},
		{msg: "the request was blocked by permission mode", want: ClassPermissionDenied},
		{msg: "./pkg/loop/wave.go:31:9: undefined: waveTimeout", want: ClassCompileError},
		{msg: "old_str not found in pkg/a/b.go", want: ClassEditNotFound},
	}
	for _, tc := range cases {
		got := Classify(Signal{Tool: "ws_edit", Message: tc.msg})
		if got == ClassOutOfScopeWrite {
			t.Errorf("unrelated message swallowed by %q: %s", ClassOutOfScopeWrite, tc.msg)
			continue
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q — the new classifier changed an existing verdict",
				tc.msg, got, tc.want)
		}
	}
}

// The whole point of the class: a shipped rule fires on the FIRST occurrence,
// with confidence above the apply bar, so the harness never rediscovers this.
func TestSeededRuleRepairsOutOfScopeWrite(t *testing.T) {
	r := newRules(t)
	for _, msg := range []string{roRefusal, focusRefusal, reviewRefusal} {
		s, ok := r.Lookup(Signal{Tool: "ws_edit", Message: msg, Language: "go"})
		if !ok {
			t.Fatalf("no seeded rule matched an out-of-scope write:\n%s", msg)
		}
		if s.Fingerprint.Class != ClassOutOfScopeWrite {
			t.Fatalf("matched on class %q", s.Fingerprint.Class)
		}
		if s.Confidence < MinApplyConfidence {
			t.Errorf("confidence %.2f is below the %.2f apply bar — the rule would never fire",
				s.Confidence, MinApplyConfidence)
		}
		if !s.Apply {
			t.Error("seeded rule is not applied automatically")
		}
		rep := s.Rule.Repair
		// An action costs no LLM round-trip and names a recovery the harness
		// understands; guidance would only describe the same thing.
		if rep.Kind != RepairAction || rep.Action != ActionForceDifferent {
			t.Errorf("repair = %s, want action:%s", rep.String(), ActionForceDifferent)
		}
		if rep.Retry {
			t.Error("the repair must not re-issue the refused write")
		}
		if !strings.Contains(rep.Guidance, "focus files") ||
			!strings.Contains(rep.Guidance, "do not edit files at all") {
			t.Errorf("guidance does not state either role contract:\n%s", rep.Guidance)
		}
		if !s.Rule.Seeded || s.Rule.Scope != ScopeBuiltin {
			t.Errorf("rule is not a shipped seed: %+v", s.Rule)
		}
	}
}

// Exactly one seed targets the class. A second, role-specific seed could be
// bound to the SAME structural fingerprint by BindFingerprint and would then
// be free to hand a worker the explorer's advice.
func TestOneSeedPerStructuralOutOfScopeFingerprint(t *testing.T) {
	n := 0
	for _, rule := range SeedRules() {
		if rule.Trigger.Class == ClassOutOfScopeWrite {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d seeded rules target %q; a structural class must have exactly one", n, ClassOutOfScopeWrite)
	}
}

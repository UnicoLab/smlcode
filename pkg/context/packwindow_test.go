package contextstore

import "testing"

// The capacity/demand split.
//
// ContextLimitTokens answers "how much can this model hold?" and must stay the
// model's real window — overflow checks and compaction thresholds are wrong
// otherwise. Available() answers "how much should we SEND on this call?", and
// using the first as the second means the packer fills whatever window it is
// given.
//
// MEASURED, 2026-08-25, Qwen3-Coder-30B (262,144-token window): sizing the
// budgets from the measured window cost on both scenarios tried —
// respects-scope 130,255 -> 435,296 prompt tokens and implement-from-tests
// 119,340 -> 164,504 — because every pack grows to whatever window it is
// given, and injected material is re-sent on every call.
//
// So the PACK is bounded and the WINDOW is not.

// TestPackWindowIsBoundedAboveTheTunedSize is the bound itself: past
// MaxPackWindowTokens, a bigger model does not get a bigger pack.
func TestPackWindowIsBoundedAboveTheTunedSize(t *testing.T) {
	tuned := DefaultBudget(MaxPackWindowTokens).Available("coder")
	huge := DefaultBudget(262144).Available("coder")
	if huge != tuned {
		t.Fatalf("a 262,144-token window packs %d tokens and a %d-token window packs %d; "+
			"the pack must not grow past the size it was tuned against",
			huge, MaxPackWindowTokens, tuned)
	}
}

// TestSmallWindowsAreUntouched is the boundary that keeps this from being a
// global cap. Every model at or below the bound must pack exactly as before —
// this change is about refusing extra room, never about taking room away.
func TestSmallWindowsAreUntouched(t *testing.T) {
	for _, window := range []int{4096, 8192, 16384, MaxPackWindowTokens} {
		got := DefaultBudget(window).Available("coder")
		// Recompute the unbounded arithmetic directly: if the clamp were
		// applying here it would show up as a smaller number.
		reserved := DefaultReserveSystemTokens + DefaultReserveToolTokens + DefaultReserveResponseTokens
		want := (window - reserved) * (100 - DefaultSlackPercent) / 100 * RoleBudgetPercent("coder") / 100
		if want < MinPackTokens {
			want = MinPackTokens
		}
		if got != want {
			t.Fatalf("window %d packs %d tokens, want %d — the bound must not affect "+
				"a model at or below it", window, got, want)
		}
	}
}

// TestBoundDoesNotShrinkTheDeclaredWindow pins the half of the split that is
// easy to lose: Available() must not reach back and change the capacity every
// other subsystem reads.
func TestBoundDoesNotShrinkTheDeclaredWindow(t *testing.T) {
	b := DefaultBudget(262144)
	_ = b.Available("coder")
	if b.ContextLimitTokens != 262144 {
		t.Fatalf("ContextLimitTokens became %d; overflow detection and compaction "+
			"thresholds read this field and are wrong if it is not the real window",
			b.ContextLimitTokens)
	}
}

// TestBoundedPackStillRespectsTheFloor: a role with a tiny share must not be
// pushed under MinPackTokens by the clamp.
func TestBoundedPackStillRespectsTheFloor(t *testing.T) {
	for _, role := range []string{"coder", "reviewer", "planner", "unknown-role"} {
		if got := DefaultBudget(262144).Available(role); got < MinPackTokens {
			t.Fatalf("role %q got %d tokens, below the %d floor", role, got, MinPackTokens)
		}
	}
}

package workspace

import (
	"fmt"
	"strings"
	"testing"
)

// bigFile is a stand-in for real source: many lines, ~40 bytes each.
func bigFile(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "\tsomeCall(argument%04d, other%04d)\n", i, i)
	}
	return b.String()
}

// TestReadBudgetFollowsTheModelWindow is the regression guard for context the
// harness was throwing away.
//
// THE DEFECT: the read guard sized `ws_read` from MaxContextKB — a PROMPT BYTE
// budget defaulting to 16 — rather than the model's real context window. That
// is the same conflation compact.WindowTokensFromKB is marked Deprecated for,
// and which the packer was migrated off with the note that it "silently capped
// a 32K model at ~3.2K tokens ... the single biggest context regression in the
// harness". The read guard was never migrated.
//
// The damage scales with how good the model is. On a 262,144-token window the
// legacy budget hands back the same ~80 lines it gives a 4K model, discarding
// over 99% of what the model could hold — so the better the model, the more the
// harness wasted.
func TestReadBudgetFollowsTheModelWindow(t *testing.T) {
	const lines = 6000
	text := bigFile(lines)

	legacy := &Workspace{MaxContextKB: 16}
	small := &Workspace{MaxContextKB: 16, ContextLimitTokens: 32768}
	huge := &Workspace{MaxContextKB: 16, ContextLimitTokens: 262144}

	gotLegacy := legacy.readBudgetLines(text, lines)
	got32k := small.readBudgetLines(text, lines)
	got262k := huge.readBudgetLines(text, lines)

	if got32k <= gotLegacy {
		t.Fatalf("a 32K model reads %d lines, no more than the legacy byte budget's %d — "+
			"the real window is being ignored", got32k, gotLegacy)
	}
	if got262k <= got32k {
		t.Fatalf("a 262K model reads %d lines, no more than a 32K model's %d — "+
			"the budget is not scaling with the window", got262k, got32k)
	}
	// The point of the fix, stated as a number: a large-context model must get
	// back a working slice of a real file, not a peephole.
	if got262k < 2000 {
		t.Fatalf("a 262K-token model is still capped at %d lines of a %d-line file",
			got262k, lines)
	}
	t.Logf("read budget — legacy=%d  32K=%d  262K=%d (of %d lines)",
		gotLegacy, got32k, got262k, lines)
}

// TestReadBudgetFallsBackWhenTheWindowIsUnknown: a run with no model profile
// must keep the old conservative behavior. A wrong-but-SMALL read is safe; a
// wrong-but-large one overflows the prompt, so the fallback direction matters.
func TestReadBudgetFallsBackWhenTheWindowIsUnknown(t *testing.T) {
	const lines = 6000
	text := bigFile(lines)

	unknown := &Workspace{MaxContextKB: 16} // no ContextLimitTokens
	got := unknown.readBudgetLines(text, lines)

	if got <= 0 {
		t.Fatalf("no budget at all without a profile: %d", got)
	}
	if got > 200 {
		t.Fatalf("an unknown window produced a %d-line budget; with nothing measured "+
			"the guard must stay conservative", got)
	}
	// And a zero/absent legacy budget must not produce a zero cap either.
	bare := &Workspace{}
	if b := bare.readBudgetLines(text, lines); b <= 0 {
		t.Fatalf("a bare workspace produced a non-positive budget: %d", b)
	}
}

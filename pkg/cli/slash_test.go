package cli

import (
	"strings"
	"testing"
)

func testRegistry() *SlashRegistry {
	return NewSlashRegistry([]SlashCommand{
		{Name: "/run", Args: "<query>", Help: "run the pipeline", Group: "run"},
		{Name: "/resume", Args: "[id]", Help: "continue an interrupted run", Group: "run"},
		{Name: "/permission", Args: "auto|review", Help: "permission modes", Group: "config"},
		{Name: "/provider", Args: "<name>", Help: "switch provider", Group: "config"},
		{Name: "/stop", Help: "cancel the run", Group: "run", LiveOK: true},
		{Name: "/q", Aliases: []string{"/quit", "/exit"}, Help: "quit", Group: "inspect"},
	})
}

func TestSlashLookupAlias(t *testing.T) {
	r := testRegistry()
	c, ok := r.Lookup("/quit")
	if !ok || c.Name != "/q" {
		t.Fatalf("alias lookup failed: %+v ok=%v", c, ok)
	}
	if _, ok := r.Lookup("/nope"); ok {
		t.Fatal("unknown command must not resolve")
	}
}

func TestSlashFindExactBeatsPrefix(t *testing.T) {
	r := testRegistry()
	hits := r.Find("run")
	if len(hits) == 0 || hits[0].Name != "/run" {
		t.Fatalf("hits=%v", names(hits))
	}
}

func TestSlashFindPrefix(t *testing.T) {
	r := testRegistry()
	hits := r.Find("/re")
	if len(hits) == 0 || hits[0].Name != "/resume" {
		t.Fatalf("hits=%v", names(hits))
	}
}

func TestSlashFindSubsequence(t *testing.T) {
	r := testRegistry()
	hits := r.Find("prm")
	found := false
	for _, h := range hits {
		if h.Name == "/permission" {
			found = true
		}
	}
	if !found {
		t.Fatalf("subsequence match missing: %v", names(hits))
	}
}

func TestSlashFindEmptyReturnsAll(t *testing.T) {
	r := testRegistry()
	if len(r.Find("")) != len(r.All()) {
		t.Fatal("empty query should list everything")
	}
}

func TestSlashCompleteUniqueCompletesFully(t *testing.T) {
	r := testRegistry()
	got, cands := r.Complete("/sto")
	if got != "/stop " {
		t.Fatalf("completed=%q cands=%v", got, names(cands))
	}
}

func TestSlashCompleteAmbiguousExtendsToCommonPrefix(t *testing.T) {
	r := testRegistry()
	got, cands := r.Complete("/p")
	if len(cands) < 2 {
		t.Fatalf("expected several candidates, got %v", names(cands))
	}
	// /permission and /provider share "/p" only, so the line stays put and the
	// caller shows the picker.
	if !strings.HasPrefix(got, "/p") {
		t.Fatalf("completed=%q", got)
	}
}

func TestSlashCompleteLeavesArgumentsAlone(t *testing.T) {
	r := testRegistry()
	got, cands := r.Complete("/run add jwt")
	if got != "/run add jwt" || cands != nil {
		t.Fatalf("completed=%q cands=%v", got, names(cands))
	}
}

func TestSlashRenderPickerShowsArgsAndHelp(t *testing.T) {
	SetColorMode(ColorNever)
	r := testRegistry()
	out := r.RenderPicker("p", 100, 10)
	if !strings.Contains(out, "/permission") || !strings.Contains(out, "auto|review") ||
		!strings.Contains(out, "permission modes") {
		t.Fatalf("picker=%q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if VisibleWidth(line) > 100 {
			t.Fatalf("picker line too wide: %q", line)
		}
	}
}

func TestSlashRenderPickerNoMatch(t *testing.T) {
	SetColorMode(ColorNever)
	out := testRegistry().RenderPicker("zzzz", 80, 10)
	if !strings.Contains(out, "no command matches") {
		t.Fatalf("picker=%q", out)
	}
}

func TestSlashRenderHelpGroupsAndKeys(t *testing.T) {
	SetColorMode(ColorNever)
	out := testRegistry().RenderHelp(100)
	for _, want := range []string{"Run & steer", "Configuration", "/run", "Esc", "Ctrl-R"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
}

func TestSlashLiveMarker(t *testing.T) {
	SetColorMode(ColorNever)
	out := testRegistry().RenderPicker("stop", 100, 10)
	if !strings.Contains(out, "·live") {
		t.Fatalf("expected the live marker: %q", out)
	}
}

func names(cs []SlashCommand) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestSlashCompleteSolePrefixMatchWins(t *testing.T) {
	r := testRegistry()
	// "/pro" is a literal prefix of /provider only; /permission merely matches
	// it as a subsequence, which must not block completion.
	got, _ := r.Complete("/pro")
	if got != "/provider " {
		t.Fatalf("completed=%q want %q", got, "/provider ")
	}
}

func TestSlashCompleteAmbiguousPrefixDoesNotGuess(t *testing.T) {
	r := NewSlashRegistry([]SlashCommand{
		{Name: "/plan"}, {Name: "/planner"},
	})
	got, cands := r.Complete("/plan")
	if len(cands) != 2 {
		t.Fatalf("cands=%v", names(cands))
	}
	// Two literal prefix matches — the CLI must show both, not pick one.
	if got != "/plan" {
		t.Fatalf("completed=%q — an ambiguous prefix must not be guessed", got)
	}
}

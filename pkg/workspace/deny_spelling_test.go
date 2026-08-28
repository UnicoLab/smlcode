package workspace

import (
	"strings"
	"testing"
)

// ── The ownership boundary must not be spellable around ──────────────────
//
// pkg/squads turns "the frontend owns web/**" into a FocusGuard deny list, and
// that deny list is the ONLY thing that actually stops a backend worker writing
// the frontend's files — the charter in the prompt is a suggestion a stuck
// model talks itself out of.
//
// So the question is not whether "web/src/App.tsx" is denied. It is whether
// every OTHER spelling of that same file is denied too, because a model that
// has been told to stay out of web/ and wants to write there will not
// necessarily type the canonical form.

func TestADenyListSurvivesEverySpellingOfThePath(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("web/**")

	// Every one of these resolves to the same file the pattern names.
	for _, spelling := range []string{
		"web/src/App.tsx",
		"./web/src/App.tsx",
		"web//src/App.tsx",
		"web/./src/App.tsx",
		"web/src/../src/App.tsx",
		"web/other/../src/App.tsx",
		"web/src/App.tsx/",
	} {
		if !g.IsProtected(spelling) {
			t.Errorf("deny list did not hold for %q — the ownership boundary is spellable around", spelling)
		}
	}
}

// A backslash spelling is deliberately NOT denied, and this records why so the
// gap is not "fixed" into a real bug.
//
// On a POSIX host `web\src\App.tsx` is a legal single filename, not a path:
// the workspace writes it as one file named that, at the root, and it never
// reaches the frontend's lane at all. Denying it would be denying a different
// file — and converting backslashes to separators, which is the other tempting
// fix, would make a legitimate (if rare) filename unwritable.
func TestABackslashSpellingIsADifferentFileNotAnEscape(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("web/**")
	if g.IsProtected(`web\src\App.tsx`) {
		t.Error("a backslash filename was denied as if it were inside web/")
	}
	// The jail is what proves it goes nowhere dangerous; see
	// adversarial_jail_test.go, which drives the same spelling through resolve.
}

// The boundary must also hold for the directory itself and for a file directly
// inside it, not only for something two levels down.
func TestADenyListCoversTheDirectoryAndItsImmediateChildren(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("web/**")
	for _, p := range []string{"web/index.html", "web/a/b/c/d/e.tsx"} {
		if !g.IsProtected(p) {
			t.Errorf("deny list did not cover %q", p)
		}
	}
}

// And it must not over-reach: a sibling directory that merely starts with the
// same letters is a different team's lane.
func TestADenyListDoesNotSwallowASiblingWithASharedPrefix(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("web/**")
	for _, p := range []string{"website/index.html", "webhooks/handler.go", "cmd/web.go"} {
		if g.IsProtected(p) {
			t.Errorf("deny list swallowed %q, which is not inside web/", p)
		}
	}
}

// A pattern naming one file must behave the same way.
func TestALiteralDenyEntryAlsoResistsRespelling(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("go.mod")
	for _, spelling := range []string{"go.mod", "./go.mod", ".//go.mod", "a/../go.mod"} {
		if !g.IsProtected(spelling) {
			t.Errorf("deny list did not hold for %q", spelling)
		}
	}
}

// Paths that escape the root are not this guard's job — resolve() refuses them
// before a write — but IsProtected must not crash or silently answer "allowed"
// for something it cannot reason about.
func TestIsProtectedIsSafeOnPathsItCannotResolve(t *testing.T) {
	g := NewFocusGuard()
	g.Protect("web/**")
	for _, p := range []string{"", ".", "/", "..", "../web/src/App.tsx", "/abs/web/x", strings.Repeat("a/", 500) + "x"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("IsProtected panicked on %.40q: %v", p, r)
				}
			}()
			_ = g.IsProtected(p)
		}()
	}
}

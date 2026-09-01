package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectWith(t *testing.T, dir, scripts string) string {
	t.Helper()
	root := t.TempDir()
	at := filepath.Join(root, dir)
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"x","scripts":` + scripts + `}`
	if err := os.WriteFile(filepath.Join(at, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The measured case: the shipped frontend team wants a build, the scaffold has
// no script by that name, and the half goes unproved in every run.
func TestAHalfIsProvedByTheScriptTheProjectActuallyHas(t *testing.T) {
	root := projectWith(t, "web", `{"compile":"tsc -b","dev":"vite"}`)

	got, note := ResolveScriptCommand(root, "npm --prefix web run build")

	if got != "npm --prefix web run compile" {
		t.Fatalf("resolved to %q", got)
	}
	if !strings.Contains(note, "compile") || !strings.Contains(note, "build") {
		t.Errorf("the substitution must name both scripts: %q", note)
	}
}

// A project that has what was asked for is left completely alone.
func TestAnExistingScriptIsNotSubstituted(t *testing.T) {
	root := projectWith(t, "web", `{"build":"vite build","test":"vitest"}`)

	got, note := ResolveScriptCommand(root, "npm --prefix web run build")

	if got != "npm --prefix web run build" || note != "" {
		t.Fatalf("got %q, note %q", got, note)
	}
}

// npm's default test script FAILS by design. Substituting into it would turn a
// half that was merely unproved into a red one — inventing the exact failure
// this package exists to prevent.
func TestThePlaceholderTestScriptIsNeverSubstitutedInto(t *testing.T) {
	root := projectWith(t, "web",
		`{"test":"echo \"Error: no test specified\" && exit 1","dev":"vite"}`)

	got, note := ResolveScriptCommand(root, "npm --prefix web run build")

	if got != "npm --prefix web run build" || note != "" {
		t.Fatalf("substituted into a script that always fails: %q (%q)", got, note)
	}
}

// With nothing usable the command is left to fail, and CheckDidNotRun reports
// UNVERIFIED. An honest grey beats proving something nobody asked for.
func TestNothingUsableLeavesTheCommandAlone(t *testing.T) {
	for name, scripts := range map[string]string{
		"only unrelated scripts": `{"dev":"vite","start":"node ."}`,
		"no scripts at all":      `{}`,
	} {
		root := projectWith(t, "web", scripts)
		got, note := ResolveScriptCommand(root, "npm --prefix web run build")
		if got != "npm --prefix web run build" || note != "" {
			t.Errorf("%s: got %q, note %q", name, got, note)
		}
	}
}

// Best proof first: a build type-checks, resolves every import and fails on a
// broken component, so it wins over a lint that would pass either way.
func TestTheStrongestAvailableProofWins(t *testing.T) {
	root := projectWith(t, "web", `{"lint":"eslint .","test":"vitest","build":"vite build"}`)

	got, _ := ResolveScriptCommand(root, "npm --prefix web run typecheck")

	if got != "npm --prefix web run build" {
		t.Fatalf("resolved to %q, want the build", got)
	}
}

// Commands this must not touch, each because rewriting it could change what is
// being proved into something else entirely.
func TestOnlyAnExplicitRunFormIsRewritten(t *testing.T) {
	root := projectWith(t, "", `{"build":"tsc"}`)
	for name, cmd := range map[string]string{
		"a bare subcommand":     "npm test",
		"a joined command":      "npm run build && npm run lint",
		"not a package runner":  "go test ./...",
		"a runner with no args": "npm",
	} {
		got, note := ResolveScriptCommand(root, cmd)
		if got != cmd || note != "" {
			t.Errorf("%s: rewrote %q to %q (%q)", name, cmd, got, note)
		}
	}
}

// No project, or an unreadable one, is not an invitation to guess.
func TestAMissingOrBrokenProjectChangesNothing(t *testing.T) {
	root := t.TempDir()
	if got, note := ResolveScriptCommand(root, "npm --prefix web run build"); note != "" {
		t.Errorf("no package.json: got %q (%q)", got, note)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, note := ResolveScriptCommand(root, "npm run build"); note != "" {
		t.Errorf("broken package.json: got %q (%q)", got, note)
	}
}

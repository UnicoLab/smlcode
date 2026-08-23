package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, frontmatter, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\n" + frontmatter + "---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func scopeLoader(t *testing.T) *Loader {
	t.Helper()
	root := t.TempDir()
	writeSkill(t, root, "rust-errors",
		"description: rust error handling\ntriggers: error, result\npaths: \"**/*.rs, **/Cargo.toml\"\n",
		"Use thiserror.")
	writeSkill(t, root, "python-typing",
		"description: python typing\ntriggers: error, typing\npaths: \"**/*.py, **/*.pyi\"\n",
		"Annotate everything.")
	writeSkill(t, root, "commit-style",
		"description: commit messages\ntriggers: error, commit\n",
		"Imperative mood.")
	return NewLoader(root)
}

func skillNames(list []Skill) string {
	var out []string
	for _, s := range list {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

func TestPathsFrontmatterIsParsed(t *testing.T) {
	l := scopeLoader(t)
	sk, ok := l.Get("rust-errors")
	if !ok {
		t.Fatal("skill not found")
	}
	if len(sk.Paths) != 2 || sk.Paths[0] != "**/*.rs" || sk.Paths[1] != "**/Cargo.toml" {
		t.Fatalf("paths = %#v", sk.Paths)
	}
	if plain, _ := l.Get("commit-style"); len(plain.Paths) != 0 {
		t.Fatalf("ungated skill has paths %#v", plain.Paths)
	}
}

// REGRESSION: every bundled skill carries accurate `paths:` frontmatter and the
// docs promise path-gated loading, but the resolver ignored the field — a Rust
// skill loaded into a Python project on a keyword collision alone.
func TestScopeGatesSkillsByPaths(t *testing.T) {
	l := scopeLoader(t)
	pyScope := []string{"src/app.py", "tests/test_app.py"}

	got := skillNames(l.ResolveForRunScoped("fix the error handling", "worker", nil, 6, pyScope))
	if strings.Contains(got, "rust-errors") {
		t.Fatalf("RUST SKILL LOADED IN A PYTHON PROJECT: %s", got)
	}
	if !strings.Contains(got, "python-typing") {
		t.Errorf("in-scope skill dropped: %s", got)
	}
	if !strings.Contains(got, "commit-style") {
		t.Errorf("ungated skill dropped: %s", got)
	}

	rsScope := []string{"src/main.rs", "Cargo.toml"}
	got = skillNames(l.ResolveForRunScoped("fix the error handling", "worker", nil, 6, rsScope))
	if !strings.Contains(got, "rust-errors") {
		t.Errorf("rust skill missing for a rust scope: %s", got)
	}
	if strings.Contains(got, "python-typing") {
		t.Errorf("PYTHON SKILL LOADED IN A RUST PROJECT: %s", got)
	}
}

// An empty scope must disable gating, so every existing call site is unchanged.
func TestEmptyScopeDisablesGating(t *testing.T) {
	l := scopeLoader(t)
	unscoped := skillNames(l.ResolveForRun("fix the error handling", "worker", nil, 6))
	explicitNil := skillNames(l.ResolveForRunScoped("fix the error handling", "worker", nil, 6, nil))
	empty := skillNames(l.ResolveForRunScoped("fix the error handling", "worker", nil, 6, []string{}))
	if unscoped != explicitNil || unscoped != empty {
		t.Fatalf("scope-less results differ: %q / %q / %q", unscoped, explicitNil, empty)
	}
	for _, want := range []string{"rust-errors", "python-typing", "commit-style"} {
		if !strings.Contains(unscoped, want) {
			t.Errorf("unscoped resolve dropped %s: %s", want, unscoped)
		}
	}
}

// The operator naming a skill outranks the path heuristic.
func TestExplicitRefBypassesPathGate(t *testing.T) {
	l := scopeLoader(t)
	pyScope := []string{"src/app.py"}

	got := skillNames(l.ResolveForRunScoped("@skill:rust-errors help", "worker", nil, 6, pyScope))
	if !strings.Contains(got, "rust-errors") {
		t.Errorf("explicit @skill: reference gated out: %s", got)
	}
	got = skillNames(l.ResolveForRunScoped("help", "worker", []string{"rust-errors"}, 6, pyScope))
	if !strings.Contains(got, "rust-errors") {
		t.Errorf("pinned skill gated out: %s", got)
	}
	// …and the explicit match is still marked explicit, so it expands.
	matches := l.ResolveMatchesScoped("@skill:rust-errors help", "worker", nil, 6, pyScope)
	var found bool
	for _, m := range matches {
		if m.Skill.Name == "rust-errors" {
			found = true
			if !m.Explicit || !m.ShouldExpand() {
				t.Errorf("explicit ref lost its tier: %+v", m)
			}
		}
	}
	if !found {
		t.Error("explicit ref missing from ResolveMatchesScoped")
	}
}

func TestMatchesScopeGlobForms(t *testing.T) {
	cases := []struct {
		paths []string
		scope []string
		want  bool
	}{
		{nil, []string{"a.py"}, true},                           // ungated
		{[]string{"**/*.go"}, nil, true},                        // scope unknown
		{[]string{"**/*.go"}, []string{"pkg/x/y.go"}, true},     // ** across segments
		{[]string{"**/*.go"}, []string{"main.go"}, true},        // ** matching zero segments
		{[]string{"**/*.go"}, []string{"./main.go"}, true},      // leading ./
		{[]string{"web/**"}, []string{"web/src/App.tsx"}, true}, // prefix glob
		{[]string{"web"}, []string{"web/src/App.tsx"}, true},    // bare dir prefix
		{[]string{"**/*.rs"}, []string{"src/app.py"}, false},    // no match
		{[]string{"**/Dockerfile*"}, []string{"ops/Dockerfile"}, true},
		{[]string{"**/*.rs", "**/*.py"}, []string{"src/app.py"}, true}, // any-of
	}
	for _, tc := range cases {
		got := Skill{Paths: tc.paths}.MatchesScope(tc.scope)
		if got != tc.want {
			t.Errorf("MatchesScope(paths=%v, scope=%v) = %v, want %v", tc.paths, tc.scope, got, tc.want)
		}
	}
}

// The bundled packs must all carry usable frontmatter — a typo there silently
// disables a skill everywhere.
func TestBundledSkillsDeclareUsablePaths(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeBundled(dest); err != nil {
		t.Fatal(err)
	}
	list, err := NewLoader(dest).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Skip("no bundled skills embedded")
	}
	gated := 0
	for _, s := range list {
		for _, g := range s.Paths {
			if strings.TrimSpace(g) == "" || strings.Contains(g, ",") {
				t.Errorf("skill %s has a malformed path glob %q", s.Name, g)
			}
		}
		if len(s.Paths) > 0 {
			gated++
		}
	}
	if gated == 0 {
		t.Fatal("no bundled skill declares paths: — the frontmatter is not being parsed")
	}
	// Sanity: a Rust-only pack must not be selected for a Python tree.
	for _, s := range list {
		if s.Name != "rust-errors" {
			continue
		}
		if s.MatchesScope([]string{"src/app.py", "pyproject.toml"}) {
			t.Errorf("bundled rust-errors matches a python scope: paths=%v", s.Paths)
		}
		if !s.MatchesScope([]string{"src/main.rs"}) {
			t.Errorf("bundled rust-errors does not match a rust scope: paths=%v", s.Paths)
		}
	}
}

// Ranking must be reproducible: it decides which bodies are inlined, and a
// map-iteration tie-break made the same query produce a different prompt run
// to run.
func TestResolveOrderIsDeterministic(t *testing.T) {
	l := scopeLoader(t)
	first := skillNames(l.ResolveForRun("fix the error handling", "worker", nil, 6))
	for i := 0; i < 40; i++ {
		if got := skillNames(l.ResolveForRun("fix the error handling", "worker", nil, 6)); got != first {
			t.Fatalf("nondeterministic ranking: %q vs %q", first, got)
		}
	}
}

package plan

import (
	"reflect"
	"testing"
)

// The roster used to be alphabetical, which is the worst possible order for
// this decision: sorted by name, `corrector` and `deep` come before `go-worker`
// and `python-corrector`, so the generics sit at the top of the list a small
// model reads first — and it takes one, even though its own prompt told it to
// prefer a specialist for the language of the files.
func TestTheRosterLeadsWithTheTasksOwnLanguage(t *testing.T) {
	roster := []string{"corrector", "go-corrector", "python-worker", "worker", "go-worker"}
	got := RankRoster(roster, []string{"internal/http/todo.go"})

	ids := make([]string, len(got))
	for i, a := range got {
		ids[i] = a.ID
	}
	if want := []string{"go-corrector", "go-worker", "python-worker", "corrector", "worker"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("RankRoster = %v, want %v", ids, want)
	}
	if !got[0].Specialist || got[0].Note != "Go specialist" {
		t.Errorf("first entry = %+v, want the Go specialist labeled as one", got[0])
	}
	// Another language's expert is still a specialist, just not this task's.
	if got[2].Specialist || got[2].Note != "Python specialist" {
		t.Errorf("python entry = %+v", got[2])
	}
	if got[3].Note != "generic" {
		t.Errorf("generic entry = %+v", got[3])
	}
}

// Nothing is removed. A defect whose fix lives outside the file's language is
// exactly the case that needs an agent the ranking would have buried, and a
// manager that cannot reach one has no more choice than the router it replaced.
func TestRankingKeepsEverybodyOnTheRoster(t *testing.T) {
	roster := []string{"corrector", "go-worker", "react-worker", "worker"}
	got := RankRoster(roster, []string{"internal/http/todo.go"})
	if len(got) != len(roster) {
		t.Fatalf("RankRoster returned %d of %d agents", len(got), len(roster))
	}
	seen := map[string]int{}
	for _, a := range got {
		seen[a.ID]++
	}
	for _, id := range roster {
		if seen[id] != 1 {
			t.Errorf("%q appears %d times, want once", id, seen[id])
		}
	}
}

func TestRankingIsStableWithNoLanguageToGoOn(t *testing.T) {
	roster := []string{"go-worker", "corrector", "worker"}
	got := RankRoster(roster, nil)
	// No task language: specialists still sort above generics, and the input
	// order breaks ties, so the same board always produces the same list.
	ids := make([]string, len(got))
	for i, a := range got {
		ids[i] = a.ID
	}
	if want := []string{"go-worker", "corrector", "worker"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("RankRoster = %v, want %v", ids, want)
	}
	if got := RankRoster(nil, []string{"a.go"}); len(got) != 0 {
		t.Errorf("an empty roster ranked to %v", got)
	}
}

// A team prefix is somebody's naming convention, not a language.
func TestATeamPrefixIsNotASpecialist(t *testing.T) {
	got := RankRoster([]string{"backend-worker", "go-worker"}, []string{"a.go"})
	if got[0].ID != "go-worker" || !got[0].Specialist {
		t.Errorf("ranked %+v first, want the real Go specialist", got[0])
	}
	if got[1].Note != "generic" {
		t.Errorf("backend-worker = %+v, want it treated as generic", got[1])
	}
}

// ── Enforcing the preference the prompt only asks for ────────────────────

func TestAGenericPickIsUpgradedToTheLanguageSpecialist(t *testing.T) {
	roster := []string{"corrector", "go-corrector", "go-worker", "worker"}
	got, changed := PreferSpecialist("corrector", "go-worker", []string{"internal/http/todo.go"}, roster)
	if !changed || got != "go-corrector" {
		t.Errorf("PreferSpecialist = %q (changed=%v), want the Go corrector", got, changed)
	}
}

// The corrector before the worker: its whole prompt is "somebody else's code is
// failing, fix it", which is what a rejected delivery is.
func TestTheLanguageCorrectorIsPreferredOverItsWorker(t *testing.T) {
	roster := []string{"go-worker", "go-corrector", "worker"}
	if got, _ := PreferSpecialist("worker", "", []string{"a.go"}, roster); got != "go-corrector" {
		t.Errorf("PreferSpecialist = %q, want go-corrector", got)
	}
	// With no corrector registered, the language worker is still better than a
	// generic one.
	if got, _ := PreferSpecialist("worker", "", []string{"a.go"}, []string{"go-worker", "worker"}); got != "go-worker" {
		t.Errorf("PreferSpecialist = %q, want go-worker", got)
	}
}

// A manager that deliberately reached for another language's expert has a
// reason the file extensions cannot see.
func TestASpecialistPickIsNeverOverridden(t *testing.T) {
	roster := []string{"go-corrector", "react-worker", "worker"}
	got, changed := PreferSpecialist("react-worker", "go-worker", []string{"a.go"}, roster)
	if changed || got != "react-worker" {
		t.Errorf("PreferSpecialist = %q (changed=%v), want the manager's pick kept", got, changed)
	}
}

func TestUpgradingNeverPicksTheAgentThatJustFailed(t *testing.T) {
	roster := []string{"go-corrector", "worker"}
	got, changed := PreferSpecialist("worker", "go-corrector", []string{"a.go"}, roster)
	if changed || got != "worker" {
		t.Errorf("PreferSpecialist = %q (changed=%v), want no upgrade to the failed agent", got, changed)
	}
}

// The roster is what the harness can dispatch. Naming an agent outside it
// produces a task that never starts.
func TestUpgradingOnlyUsesAgentsOnTheRoster(t *testing.T) {
	got, changed := PreferSpecialist("corrector", "", []string{"a.go"}, []string{"corrector", "worker"})
	if changed || got != "corrector" {
		t.Errorf("PreferSpecialist = %q (changed=%v), want the pick kept", got, changed)
	}
}

func TestUpgradingIsInertWithNothingToGoOn(t *testing.T) {
	roster := []string{"go-corrector", "worker"}
	for _, tc := range []struct {
		name, pick string
		files      []string
	}{
		{"no pick", "", []string{"a.go"}},
		{"no files", "worker", nil},
		{"unknown extension", "worker", []string{"README.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, changed := PreferSpecialist(tc.pick, "", tc.files, roster); changed || got != tc.pick {
				t.Errorf("PreferSpecialist = %q (changed=%v), want %q unchanged", got, changed, tc.pick)
			}
		})
	}
}

// languageNames mirrors langOf's output set. When they drift, an agent named
// for a real language stops being recognized as a specialist.
func TestEveryLanguagePrefixHasALabel(t *testing.T) {
	for _, ext := range []string{
		".go", ".tsx", ".jsx", ".ts", ".mts", ".cts", ".py", ".rs", ".java", ".kt", ".kts",
		".rb", ".php", ".swift", ".cs", ".c", ".h", ".cc", ".cpp", ".hpp", ".sh", ".bash",
		".html", ".css", ".scss",
	} {
		lang := langOf("file" + ext)
		if lang == "" {
			t.Fatalf("langOf(%q) returned nothing — fix the test, not the map", ext)
		}
		if languageLabel(lang) == "" {
			t.Errorf("langOf maps %s to %q, which has no label — a %s-worker would be called generic",
				ext, lang, lang)
		}
	}
}

// ── Manifests have a language too ────────────────────────────────────────
//
// An extension cannot say: `requirements.txt` is a .txt and `Gemfile` has none
// at all, so both routed to whatever the run picked as its default specialist —
// a Go worker editing a Python dependency list in a mixed repo. Manifests come
// up constantly in real builds ("add the dependency", every greenfield
// scaffold), so getting them wrong is not an edge case.

func TestAManifestRoutesByItsName(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"requirements.txt", "python"},
		{"etl/requirements.txt", "python"},
		{"pyproject.toml", "python"},
		{"conftest.py", "python"},
		{"go.mod", "go"},
		{"go.sum", "go"},
		{"Cargo.toml", "rust"},
		{"Gemfile", "ruby"},
		{"pom.xml", "java"},
		{"composer.json", "php"},
	} {
		if got := langOf(tc.path); got != tc.want {
			t.Errorf("langOf(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// package.json and tsconfig.json are genuinely ambiguous between a TypeScript
// and a React lane, and the file-language rung OUTRANKS the squad rung — so
// claiming them would override the frontend team's own choice of worker with a
// guess. Leaving them unmapped lets the better signal win.
func TestTheAmbiguousWebManifestsAreLeftToTheTeam(t *testing.T) {
	for _, path := range []string{"package.json", "web/package.json", "tsconfig.json"} {
		if got := langOf(path); got != "" {
			t.Errorf("langOf(%q) = %q, want the squad rung to decide", path, got)
		}
	}
}

func TestAManifestPicksTheRightSpecialist(t *testing.T) {
	has := func(id string) bool {
		return id == "python-worker" || id == "go-worker" || id == RoleWorker
	}
	if got := SpecialistFor([]string{"requirements.txt"}, has); got != "python-worker" {
		t.Errorf("SpecialistFor(requirements.txt) = %q, want the Python specialist", got)
	}
	if got := SpecialistFor([]string{"go.mod"}, has); got != "go-worker" {
		t.Errorf("SpecialistFor(go.mod) = %q, want the Go specialist", got)
	}
}

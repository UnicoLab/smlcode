package agents

import "testing"

// all is a registry where every specialist exists — the shape of a real
// factory, which registers the whole language pack.
func all(string) bool { return true }

// only builds a registry containing exactly the named ids.
func only(ids ...string) func(string) bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func TestSpecialistForFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"go backend", []string{"cmd/server/main.go"}, "go-worker"},
		{"python", []string{"src/app/cli.py"}, "python-worker"},
		{"react component", []string{"web/src/TaskList.jsx"}, "react-worker"},
		{"plain web", []string{"web/index.html", "web/app.js"}, "web-worker"},
		{"rust", []string{"src/lib.rs"}, "rust-worker"},
		{"no extension", []string{"Makefile"}, ""},
		{"unknown extension", []string{"notes.txt"}, ""},
		{"no files", nil, ""},
		// The multi-language case this exists for: a task is routed by the
		// language it actually touches, not by the run's single default_role.
		{"majority wins", []string{"a.go", "b.go", "x.py"}, "go-worker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpecialistForFiles(tc.files, all); got != tc.want {
				t.Errorf("SpecialistForFiles(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}

// .tsx belongs to both ts-worker and react-worker. The tie must be broken the
// same way every time, never by map iteration order.
func TestSpecialistTieIsDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		if got := SpecialistForFiles([]string{"web/src/App.tsx"}, all); got != "react-worker" {
			t.Fatalf("iteration %d: got %q, want react-worker", i, got)
		}
	}
}

// Routing to an agent the factory never built is a hard task failure, not a
// degraded run — so an unregistered specialist is skipped.
func TestSpecialistMustBeRegistered(t *testing.T) {
	if got := SpecialistForFiles([]string{"main.go"}, only("worker", "tester")); got != "" {
		t.Errorf("got %q, want \"\" when go-worker is not registered", got)
	}
	if got := SpecialistForFiles([]string{"main.go"}, only("go-worker")); got != "go-worker" {
		t.Errorf("got %q, want go-worker when it IS registered", got)
	}
}

// A caller that cannot say which agents exist has not proved this one does.
func TestNilRegistryRefusesEverySpecialist(t *testing.T) {
	if got := SpecialistForFiles([]string{"main.go"}, nil); got != "" {
		t.Errorf("got %q, want \"\" — a nil registry must refuse, not accept", got)
	}
}

// Every id in the priority list must be a real key in the table, or a specialist
// silently stops being reachable when someone renames one.
func TestSpecialistPriorityCoversTheTable(t *testing.T) {
	inPriority := map[string]bool{}
	for _, id := range specialistPriority {
		inPriority[id] = true
		if _, ok := SpecialistExtensions[id]; !ok {
			t.Errorf("priority lists %q, which is not in SpecialistExtensions", id)
		}
	}
	for id := range SpecialistExtensions {
		if !inPriority[id] {
			t.Errorf("%q is in SpecialistExtensions but not in the priority order — it can never win a tie", id)
		}
	}
}

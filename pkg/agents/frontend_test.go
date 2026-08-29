package agents

import (
	"strings"
	"testing"
)

// bothAssemblers is the default roster: both assembler agents ship bundled, so
// blocks.Load registers them without anyone applying a pack.
func bothAssemblers(id string) bool {
	return id == ShadcnWorker || id == UntitledUIWorker || id == "worker" || id == "react-worker"
}

// 1. An explicit request is an answer, not evidence to weigh.
func TestQueryNamesTheLibrary(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"build the settings page with shadcn", ShadcnWorker},
		{"use shadcn/ui for the dashboard", ShadcnWorker},
		{"build it with Untitled UI", UntitledUIWorker},
		{"use untitledui components", UntitledUIWorker},
	} {
		got := ChooseFrontend(tc.query, nil, true, bothAssemblers)
		if got.Worker != tc.want {
			t.Errorf("%q → %q, want %q", tc.query, got.Worker, tc.want)
		}
		if !got.FromQuery {
			t.Errorf("%q should be marked FromQuery", tc.query)
		}
	}
}

// Opting out has to work, and has to beat every other signal — including a
// project that already uses a library.
func TestFromScratchOptsOut(t *testing.T) {
	inv := []string{"components.json", "src/components/ui/button.tsx"}
	for _, q := range []string{
		"build a settings page from scratch",
		"write the components from scratch, no component library",
		"hand-write the table component",
		"build it with vanilla react",
	} {
		got := ChooseFrontend(q, inv, false, bothAssemblers)
		if got.Worker != "" {
			t.Errorf("%q → %q, want hand-written", q, got.Worker)
		}
		if !got.FromQuery {
			t.Errorf("%q should be marked FromQuery", q)
		}
	}
}

// 2. A project that already committed to a library keeps it — adding a
// hand-written Button beside twenty installed ones is a duplicate, not a style.
func TestProjectMarkersDecide(t *testing.T) {
	for _, tc := range []struct {
		name string
		inv  []string
		want string
	}{
		{"shadcn at root", []string{"components.json", "components/ui/button.tsx"}, ShadcnWorker},
		{"shadcn under src", []string{"src/components/ui/dialog.tsx"}, ShadcnWorker},
		{"untitled ui", []string{"untitledui.json", "components/base/buttons/button.tsx"}, UntitledUIWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ChooseFrontend("add a settings page", tc.inv, false, bothAssemblers)
			if got.Worker != tc.want {
				t.Errorf("→ %q, want %q (%s)", got.Worker, tc.want, got.Why)
			}
			if got.FromQuery {
				t.Error("a project marker is not a query choice")
			}
		})
	}
}

// 3. Greenfield assembles by default — this is the acceleration the feature
// exists for, and it must need no setup.
func TestGreenfieldDefaultsToAssembling(t *testing.T) {
	got := ChooseFrontend("build a task manager UI", nil, true, bothAssemblers)
	if got.Worker != DefaultAssembler {
		t.Fatalf("→ %q, want %q", got.Worker, DefaultAssembler)
	}
	if !strings.Contains(got.Why, "from scratch") {
		t.Errorf("the default must say how to opt out, got %q", got.Why)
	}
}

// 4. An existing React app with no library markers keeps its house patterns.
// Introducing a component library there is a migration nobody asked for.
func TestExistingAppWithoutMarkersKeepsWritingByHand(t *testing.T) {
	inv := []string{"package.json", "src/App.tsx", "src/components/Header.tsx"}
	got := ChooseFrontend("add a settings page", inv, false, bothAssemblers)
	if got.Worker != "" {
		t.Errorf("→ %q, want hand-written (%s)", got.Worker, got.Why)
	}
}

// An assembler that is not registered must never be selected, and a nil
// registry proves nothing about what exists.
func TestUnregisteredAssemblerIsNeverChosen(t *testing.T) {
	none := func(string) bool { return false }
	if got := ChooseFrontend("build it with shadcn", nil, true, none); got.Worker != "" {
		t.Errorf("→ %q, want empty when no assembler is registered", got.Worker)
	}
	if got := ChooseFrontend("build a UI", nil, true, nil); got.Worker != "" {
		t.Errorf("→ %q, want empty for a nil registry", got.Worker)
	}
}

func TestHasReactFiles(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  bool
	}{
		{[]string{"src/App.tsx"}, true},
		{[]string{"src/App.jsx"}, true},
		{[]string{"cmd/server/main.go"}, false},
		// .ts is a hook, a store or an api client — nothing a component
		// library installs.
		{[]string{"src/lib/api.ts"}, false},
		{nil, false},
	} {
		if got := HasReactFiles(tc.files); got != tc.want {
			t.Errorf("HasReactFiles(%v) = %v, want %v", tc.files, got, tc.want)
		}
	}
}

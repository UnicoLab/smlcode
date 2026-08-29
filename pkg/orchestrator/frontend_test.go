package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The whole chain, with no model in it: bundled blocks register the assembler,
// ChooseFrontend picks it from the workspace, buildRunner carries it, and
// execAgentFor routes a React task to it.
//
// Every link is somewhere the feature can go quietly inert — and one of them
// did: runner.HasRole used to be set only inside the escalation branch, so on
// any install without a model_escalation ladder the assembler could never be
// selected and nothing said so.
func TestFrontendAssemblerReachesTheTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "components.json", `{"aliases":{"ui":"@/components/ui"}}`)
	writeFile(t, root, "package.json", `{"dependencies":{"react":"^19.0.0","class-variance-authority":"^0.7.1"}}`)
	writeFile(t, root, "src/components/ui/button.tsx", "export function Button() { return null }\n")
	writeFile(t, root, "src/App.tsx", "export default function App() { return null }\n")

	cfg := config.Default(root)
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	runner := o.buildRunner("add a settings page", "run-test", "")
	if runner.HasRole == nil {
		t.Fatal("HasRole is nil — specialist routing and the assembler are both disabled")
	}
	if !runner.HasRole(agents.ShadcnWorker) {
		t.Fatalf("%s is not registered — bundled agent blocks should register without applying a pack",
			agents.ShadcnWorker)
	}
	if runner.FrontendAssembler != agents.ShadcnWorker {
		t.Fatalf("FrontendAssembler = %q, want %q for a project with shadcn markers",
			runner.FrontendAssembler, agents.ShadcnWorker)
	}
	// Routing a task from here to that assembler is pkg/loop's half of the
	// chain, covered by TestMixedLanguageBoardRoutesToBothSpecialists and
	// friends; this test owns everything up to handing it over.
}

// A plain React project with no library markers keeps writing components by
// hand. Introducing a component library into an existing app is a migration
// nobody asked for, and doing it silently would be worse.
func TestPlainReactProjectGetsNoAssembler(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"dependencies":{"react":"^19.0.0","react-dom":"^19.0.0"}}`)
	writeFile(t, root, "src/App.tsx", "export default function App() { return null }\n")
	writeFile(t, root, "src/components/Header.tsx", "export const Header = () => null\n")

	cfg := config.Default(root)
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	runner := o.buildRunner("add a settings page", "run-test", "")
	if runner.FrontendAssembler != "" {
		t.Fatalf("FrontendAssembler = %q, want none for an existing app with no markers",
			runner.FrontendAssembler)
	}
}

// Naming the other library in the request beats the project's own markers: an
// explicit instruction is an answer, not evidence to weigh.
func TestRequestOverridesProjectMarkers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "components.json", `{}`)
	writeFile(t, root, "package.json", `{"dependencies":{"class-variance-authority":"^0.7.1"}}`)
	writeFile(t, root, "src/App.tsx", "export default function App() { return null }\n")

	cfg := config.Default(root)
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	if got := o.buildRunner("build the page from scratch", "r", "").FrontendAssembler; got != "" {
		t.Errorf("\"from scratch\" → %q, want no assembler", got)
	}
	if got := o.buildRunner("build it with Untitled UI", "r", "").FrontendAssembler; got != agents.UntitledUIWorker {
		t.Errorf("naming Untitled UI → %q, want %q", got, agents.UntitledUIWorker)
	}
}

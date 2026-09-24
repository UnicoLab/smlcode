package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/teams"
)

// The team library, end to end over the real packages: a user authors a team,
// it is discovered, it is preselected for a request WITHOUT a model call, and
// it composes into the org chart the harness executes.
//
// The only thing not exercised here is the model, because the whole point of
// this path is that it does not need one.

// fullStackRepo writes a workspace that genuinely has two halves.
func fullStackRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod":              "module demo\n\ngo 1.23\n",
		"cmd/server/main.go":  "package main\n\nfunc main() {}\n",
		"internal/store/s.go": "package store\n",
		"web/package.json":    `{"name":"web"}`,
		"web/src/App.tsx":     "export default function App() { return null }\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func workspaceFiles(t *testing.T, root string) []string {
	t.Helper()
	files := plan.ListWorkspaceFiles(root, 2000)
	if len(files) == 0 {
		t.Fatal("the fixture workspace listed no files")
	}
	return files
}

// The shipped library has to cover the case the whole feature exists for,
// straight out of the box: a Go API and a React SPA, with no configuration.
func TestBuiltinLibraryPreselectsBothHalvesOfAFullStackRequest(t *testing.T) {
	root := fullStackRepo(t)
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "build a todo app: a Go backend serving a React frontend",
		Files: workspaceFiles(t, root),
	}, teams.Options{})

	if !sel.Enabled() {
		t.Fatalf("the builtin library must cover this out of the box, got %v", sel.IDs())
	}
	ids := sel.IDs()
	if len(ids) != 2 || ids[0] != "backend-go" || ids[1] != "frontend-react" {
		t.Fatalf("selected = %v", ids)
	}

	// The plan it composes must be one the harness can actually run — that is
	// the only thing that makes preselection worth having.
	p := teams.Compose(sel, "todo app")
	for _, pr := range p.Validate() {
		if pr.Severity == squads.SeverityError {
			t.Fatalf("composed plan does not validate: %s", pr)
		}
	}
	if owner, ok := p.Owner("cmd/server/main.go"); !ok || owner != "backend-go" {
		t.Errorf("cmd/server/main.go → %q,%v", owner, ok)
	}
	if owner, ok := p.Owner("web/src/App.tsx"); !ok || owner != "frontend-react" {
		t.Errorf("web/src/App.tsx → %q,%v", owner, ok)
	}
}

// A single-domain request is the common case, and paying the contract and
// integration overhead for one team buys nothing.
func TestLibraryLeavesASingleDomainRequestAsOneStream(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "add a doc comment to the Hello function",
		Files: plan.ListWorkspaceFiles(root, 2000),
	}, teams.Options{})

	if sel.Enabled() {
		t.Fatalf("a single-domain request must not assemble teams, got %v", sel.IDs())
	}
}

// A team authored by the user must shadow the builtin of the same id, and the
// override is what the run then uses. Without this, "I fixed my team's globs"
// is a change the run never sees.
func TestAProjectTeamOverridesTheBuiltinItShadows(t *testing.T) {
	root := fullStackRepo(t)

	override := &blocks.TeamBlock{
		Meta: blocks.Meta{Kind: blocks.KindTeam, ID: "backend-go", Name: "Our Go Backend"},
		Spec: teams.Team{
			ID:         "backend-go",
			Name:       "Our Go Backend",
			Owns:       []string{"cmd/**", "internal/**"},
			Acceptance: "make test",
			Worker:     "go-worker",
			Match:      teams.Match{Keywords: []string{"backend"}, Files: []string{"go.mod"}},
		},
	}
	path, err := blocks.Save(root, override)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(path), ".slmcode/blocks/teams/backend-go.yaml") {
		t.Fatalf("override landed at %s", path)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reg.GetTeam("backend-go")
	if !ok {
		t.Fatal("the override is not discoverable")
	}
	if got.Source != blocks.SourceProject {
		t.Fatalf("source = %q, want the project override to win", got.Source)
	}
	if got.Spec.Acceptance != "make test" {
		t.Fatalf("acceptance = %q — the override did not take", got.Spec.Acceptance)
	}

	// And the override is what preselection composes.
	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "a Go backend and a React frontend",
		Files: workspaceFiles(t, root),
	}, teams.Options{})
	p := teams.Compose(sel, "")
	backend, found := p.Squad("backend-go")
	if !found || backend.Acceptance != "make test" {
		t.Fatalf("composed squad = %+v", backend)
	}

	// Deleting the override reveals the builtin again rather than losing the
	// team — the whole reason a builtin cannot be deleted.
	if removed, derr := blocks.Delete(root, blocks.KindTeam, "backend-go"); derr != nil || !removed {
		t.Fatalf("delete override: removed=%v err=%v", removed, derr)
	}
	reg, err = blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := reg.GetTeam("backend-go")
	if !ok || back.Source != blocks.SourceBuiltin {
		t.Fatalf("after deleting the override: ok=%v source=%q", ok, back.Source)
	}
}

// A user's own team, for a domain no builtin covers, has to work the same way
// the shipped ones do — that is what makes the library a library.
func TestAUserAuthoredTeamIsSelectedAndComposed(t *testing.T) {
	root := fullStackRepo(t)
	for rel, body := range map[string]string{
		"billing/invoice.go": "package billing\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := blocks.Save(root, &blocks.TeamBlock{
		Meta: blocks.Meta{Kind: blocks.KindTeam, ID: "payments", Name: "Payments"},
		Spec: teams.Team{
			ID:         "payments",
			Owns:       []string{"billing/**"},
			Acceptance: "go test ./billing/...",
			Match:      teams.Match{Keywords: []string{"billing", "invoice", "payments"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "add invoice totals to the billing service and show them in the React frontend",
		Files: workspaceFiles(t, root),
	}, teams.Options{})

	ids := sel.IDs()
	if !hasTeam(ids, "payments") || !hasTeam(ids, "frontend-react") {
		t.Fatalf("selected = %v, want the authored team alongside the frontend", ids)
	}
	p := teams.Compose(sel, "")
	for _, pr := range p.Validate() {
		if pr.Severity == squads.SeverityError {
			t.Fatalf("composed plan does not validate: %s", pr)
		}
	}
}

// Pinning is how "run this with these teams" works from `--team`, the run
// setup, and a pipeline's attachment. It is an instruction, not a hypothesis:
// it selects on no evidence at all.
func TestPinnedTeamsComposeWithoutAnyEvidence(t *testing.T) {
	root := fullStackRepo(t)
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// `docs` ships with a negative priority — never automatic, always pinnable.
	sel := teams.Select(reg.TeamRoster(), teams.Signals{Query: "anything at all"}, teams.Options{
		Pinned: []string{"docs", "frontend-react"},
		Max:    2,
	})
	if got := sel.IDs(); len(got) != 2 || got[0] != "docs" || got[1] != "frontend-react" {
		t.Fatalf("pinned selection = %v", got)
	}

	p := teams.Compose(sel, "docs + frontend")
	if err := squads.Save(filepath.Join(root, ".slmcode"), p); err != nil {
		t.Fatal(err)
	}
	// The saved plan is what the NEXT run inherits, and the contract file is
	// what the agents read. Writing one without the other is the worst failure
	// this package has.
	back, ok, lerr := squads.Load(filepath.Join(root, ".slmcode"))
	if lerr != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, lerr)
	}
	if got := back.IDs(); len(got) != 2 {
		t.Fatalf("reloaded = %v", got)
	}
	if _, serr := os.Stat(filepath.Join(root, ".slmcode", squads.ContractFile)); serr != nil {
		t.Fatalf("CONTRACT.md must be written alongside the plan: %v", serr)
	}
}

// Staffing a team with an agent this harness cannot dispatch produces a team
// that looks fine and starts nothing. The drop has to be visible.
func TestComposedPlanDropsStaffingTheHarnessCannotDispatch(t *testing.T) {
	root := fullStackRepo(t)
	if _, err := blocks.Save(root, &blocks.TeamBlock{
		Meta: blocks.Meta{Kind: blocks.KindTeam, ID: "backend-go", Name: "Backend"},
		Spec: teams.Team{
			ID: "backend-go", Owns: []string{"cmd/**"}, Acceptance: "go test ./...",
			Worker: "an-agent-that-was-uninstalled",
			Match:  teams.Match{Files: []string{"go.mod"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "a Go backend and a React frontend", Files: workspaceFiles(t, root),
	}, teams.Options{})
	p := teams.Compose(sel, "")

	notes := teams.StaffCheck(&p, func(id string) bool { return id != "an-agent-that-was-uninstalled" })
	if len(notes) == 0 {
		t.Fatal("a dropped agent must be reported — an idle team looks nothing like its cause")
	}
	if !strings.Contains(strings.Join(notes, " "), "not a registered agent") {
		t.Fatalf("notes = %v", notes)
	}
	backend, _ := p.Squad("backend-go")
	if backend.Worker != "" {
		t.Fatalf("worker = %q, want the pipeline default", backend.Worker)
	}
}

// pythonServiceRepo writes an established Python service: enough files that a
// deep dive has something to read.
func pythonServiceRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"pyproject.toml":  "[project]\nname = \"svc\"\n",
		"README.md":       "# svc\n",
		"docs/usage.md":   "run it\n",
		"tests/test_a.py": "def test_a():\n    assert True\n",
	}
	for i := 0; i < 12; i++ {
		files[filepath.Join("app", "mod"+string(rune('a'+i))+".py")] = "x = 1\n"
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// "What's going on in this repo" is a team of its own out of the box: the
// deep dive joins the language team that owns the code, and the two compose
// into a plan that validates — the report files are nobody else's territory.
func TestBuiltinInsightTeamJoinsAnEstablishedRepo(t *testing.T) {
	root := pythonServiceRepo(t)
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "deep dive on the current state of this service and recommend maintenance",
		Files: workspaceFiles(t, root),
	}, teams.Options{})
	ids := sel.IDs()
	if !hasTeam(ids, "repo-insight") || !hasTeam(ids, "backend-python") {
		t.Fatalf("selected = %v, want the deep dive beside the Python team", ids)
	}
	p := teams.Compose(sel, "deep dive")
	for _, pr := range p.Validate() {
		if pr.Severity == squads.SeverityError {
			t.Fatalf("composed plan does not validate: %s", pr)
		}
	}
	if owner, ok := p.Owner("ARCHITECTURE.md"); !ok || owner != "repo-insight" {
		t.Errorf("ARCHITECTURE.md → %q,%v", owner, ok)
	}

	// The same words in an empty directory staff no deep dive.
	empty := t.TempDir()
	sel = teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "deep dive on the current state of this service",
		Files: plan.ListWorkspaceFiles(empty, 2000),
	}, teams.Options{})
	if hasTeam(sel.IDs(), "repo-insight") {
		t.Fatalf("a deep dive of nothing was staffed: %v", sel.IDs())
	}
}

// A non-DevOps request for OpenShift specs reaches the OpenShift team from the
// words alone, plural and all, and a plain API request does not.
func TestBuiltinOpenShiftTeamAnswersDeploymentRequests(t *testing.T) {
	root := pythonServiceRepo(t)
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sel := teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "set up OpenShift for this: two pods, their configmaps and a persistent volume",
		Files: workspaceFiles(t, root),
	}, teams.Options{})
	if !hasTeam(sel.IDs(), "openshift") {
		t.Fatalf("selected = %v, want the OpenShift team", sel.IDs())
	}

	sel = teams.Select(reg.TeamRoster(), teams.Signals{
		Query: "add a route that returns the JWT secret expiry",
		Files: workspaceFiles(t, root),
	}, teams.Options{})
	if hasTeam(sel.IDs(), "openshift") {
		t.Fatalf("an API request staffed the platform team: %v", sel.IDs())
	}
}

func hasTeam(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

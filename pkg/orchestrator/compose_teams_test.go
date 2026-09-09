package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// fullstackRoot is a workspace the builtin library reads as two halves.
func fullstackRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.23\n")
	write("cmd/server/main.go", "package main\n\nfunc main() {}\n")
	write("web/package.json", `{"name":"web"}`)
	write("web/src/App.tsx", "export default function App() { return null }\n")
	return root
}

// The complaint: the composition and the charter phase decided teams from two
// different inputs and never agreed. A two-domain request now carries both
// teams on the composition, with the manager each will answer to.
func TestCompositionCarriesTheTeamsTheRunWillUse(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	comp := o.PreviewComposition("add a Go API endpoint and the React page that calls it")

	if comp.TeamMode != composer.TeamModeParallel {
		t.Fatalf("mode=%q note=%q teams=%+v", comp.TeamMode, comp.TeamNote, comp.Teams)
	}
	ids := strings.Join(comp.TeamIDs(), ",")
	if !strings.Contains(ids, "backend-go") || !strings.Contains(ids, "frontend-react") {
		t.Fatalf("teams=%s", ids)
	}
	for _, tc := range comp.Teams {
		if tc.Manager == "" {
			t.Fatalf("team %s has no effective manager", tc.ID)
		}
		if !tc.ManagerDefault {
			t.Fatalf("builtin %s names no manager, so the run default should be marked: %+v", tc.ID, tc)
		}
		if tc.Reason == "" {
			t.Fatalf("team %s carries no reason", tc.ID)
		}
	}
	if !strings.Contains(comp.TeamNote, "in parallel") {
		t.Fatalf("note=%q", comp.TeamNote)
	}
	joined := strings.Join(comp.Handoff, "\n")
	if !strings.Contains(joined, "Teams build in parallel") || !strings.Contains(joined, "CONTRACT.md") {
		t.Fatalf("handoff does not brief the teams: %v", comp.Handoff)
	}
	// The people on each team are on the composition's roster too, so the
	// "Team" panel shows who will actually work.
	roles := map[string]bool{}
	for _, m := range comp.Team {
		roles[m.Role] = true
	}
	for _, want := range []string{"go-worker", "react-worker", "triage"} {
		if !roles[want] {
			t.Fatalf("roster lacks %s: %+v", want, comp.Team)
		}
	}
}

// "Send this request to the backend team": one pinned team staffs the run —
// its worker and tester take the loop even when the language hint would have
// picked the same specialists for a different reason, and its charter rides
// in the handoff.
func TestOnePinnedTeamStaffsTheRun(t *testing.T) {
	// A React-only workspace, so no second team scores on evidence and the
	// pinned team is the whole selection.
	root := t.TempDir()
	for rel, body := range map[string]string{
		"web/package.json": `{"name":"web"}`,
		"web/src/App.tsx":  "export default function App() { return null }\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default(root)
	cfg.Teams = []string{"frontend-react"}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The query names no team at all; the pin is the user's instruction.
	comp := o.PreviewComposition("tidy the module manifest")

	if comp.TeamMode != composer.TeamModeSingle || len(comp.Teams) != 1 || comp.Teams[0].ID != "frontend-react" {
		t.Fatalf("mode=%q teams=%+v note=%q", comp.TeamMode, comp.Teams, comp.TeamNote)
	}
	if !comp.Teams[0].Pinned {
		t.Fatalf("pinned team not marked: %+v", comp.Teams[0])
	}
	if comp.Execute.DefaultRole != "react-worker" {
		t.Fatalf("worker=%q — the pinned team's worker should staff the loop", comp.Execute.DefaultRole)
	}
	for _, p := range comp.Phases {
		switch p.ID {
		case "execute":
			if p.Agent != "react-worker" {
				t.Fatalf("execute agent=%q", p.Agent)
			}
		case "test":
			if p.Agent != "react-tester" {
				t.Fatalf("test agent=%q", p.Agent)
			}
		}
	}
	joined := strings.Join(comp.Handoff, "\n")
	if !strings.Contains(joined, "Team frontend-react staffs this run") {
		t.Fatalf("handoff=%v", comp.Handoff)
	}
	if !strings.Contains(joined, "Team territory: web/**") {
		t.Fatalf("handoff does not name the territory: %v", comp.Handoff)
	}
	if !strings.Contains(comp.TeamNote, "staffs this run") {
		t.Fatalf("note=%q", comp.TeamNote)
	}
}

// A request no team matches says so on the composition, so "why did my teams
// not run" is answered on the panel rather than in a log.
func TestNoTeamMatchIsExplained(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	comp := PreviewCompositionForConfig(cfg, "rewrite the poem")
	if len(comp.Teams) != 0 || comp.TeamMode != "" {
		t.Fatalf("teams=%+v mode=%q", comp.Teams, comp.TeamMode)
	}
	if !strings.Contains(comp.TeamNote, "no team matched") {
		t.Fatalf("note=%q", comp.TeamNote)
	}
}

// Teams switched off is not the same as no teams: the panel has to say which,
// or the user goes looking for a library problem that is a config flag.
func TestTeamsOffIsExplainedNotHidden(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	cfg.Squads = false
	comp := PreviewCompositionForConfig(cfg, "add a Go API endpoint and the React page that calls it")
	if comp.TeamMode != "" {
		t.Fatalf("mode=%q with squads off", comp.TeamMode)
	}
	if len(comp.Teams) < 2 {
		t.Fatalf("the matched teams should still be listed: %+v", comp.Teams)
	}
	if !strings.Contains(comp.TeamNote, "squads: false") {
		t.Fatalf("note=%q", comp.TeamNote)
	}

	cfg.Squads, cfg.TeamLibrary = true, false
	comp = PreviewCompositionForConfig(cfg, "add a Go API endpoint")
	if len(comp.Teams) != 0 || !strings.Contains(comp.TeamNote, "team_library: false") {
		t.Fatalf("teams=%+v note=%q", comp.Teams, comp.TeamNote)
	}
}

// A manager that cannot answer the triage contract is not a manager. The
// composition names who will actually decide, and says it is the default.
func TestEffectiveManagerFallsBackToTheRunDefault(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m, def := o.effectiveManager("go-worker"); m != "triage" || !def {
		t.Fatalf("a worker cannot triage: got %q default=%v", m, def)
	}
	if m, def := o.effectiveManager("triage"); m != "triage" || def {
		t.Fatalf("the builtin manager is not a fallback: got %q default=%v", m, def)
	}
	if m, def := o.effectiveManager(""); m != "triage" || !def {
		t.Fatalf("empty → default: got %q default=%v", m, def)
	}
}

// The composer model is told which teams are on the run, and told it may not
// change them — so the roles it picks agree with the charter phase.
func TestComposerPromptNamesTheTeams(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	query := "add a Go API endpoint and the React page that calls it"
	inventory := []string{"go.mod", "web/package.json"}
	prompt := o.buildComposerPrompt(query, inventory, "", "", o.decideTeams(query, inventory, nil))
	if !strings.Contains(prompt, "## Teams on this run") {
		t.Fatalf("prompt lacks the teams section:\n%s", prompt)
	}
	if !strings.Contains(prompt, "backend-go") || !strings.Contains(prompt, "manager=triage") {
		t.Fatalf("prompt does not name the teams and their managers:\n%s", prompt)
	}
}

func TestCompositionMarkdownAndBriefRenderTeams(t *testing.T) {
	c := composer.Composition{
		Summary:  "two halves",
		TeamMode: composer.TeamModeParallel,
		TeamNote: "2 teams build in parallel",
		Teams: []composer.TeamChoice{
			{ID: "backend-go", Worker: "go-worker", Manager: "triage", ManagerDefault: true, Owns: []string{"cmd/**"}},
			{ID: "frontend-react", Worker: "react-worker", Manager: "fe-triage", Owns: []string{"web/**"}},
		},
	}
	md := compositionMarkdown(c)
	if !strings.Contains(md, "## Teams") || !strings.Contains(md, "manager=triage (run default)") ||
		!strings.Contains(md, "manager=fe-triage") {
		t.Fatalf("markdown:\n%s", md)
	}
	brief := compositionBrief(c)
	if !strings.Contains(brief, "Teams on this run (in parallel") || !strings.Contains(brief, "frontend-react manager=fe-triage") {
		t.Fatalf("brief:\n%s", brief)
	}
}

// A resumed run used to execute with no org chart at all: the plan lived on
// disk and the orchestrator's handle died with the process. The board decides
// whether the saved plan belongs to this run.
func TestResumeRestoresTheSquadPlanTheBoardWasBuiltUnder(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := squads.Plan{
		Summary: "Go API + React SPA",
		Squads: []squads.Squad{
			{ID: "backend-go", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
			{ID: "frontend-react", Owns: []string{"web/**"}, Acceptance: "npm test"},
		},
	}
	if err := squads.Save(cfg.SlmDir(), p); err != nil {
		t.Fatal(err)
	}

	// A board no team touched: the plan on disk is some other run's.
	foreign := &plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "x", Files: []string{"README.md"}}}}
	o.restoreSquadPlan(foreign)
	if o.squadPlan != nil {
		t.Fatalf("a board with no team stamps must not adopt the saved plan")
	}

	stamped := &plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "x", Squad: "backend-go", Files: []string{"cmd/main.go"}}}}
	o.restoreSquadPlan(stamped)
	if o.squadPlan == nil || len(o.squadPlan.Squads) != 2 {
		t.Fatalf("plan not restored: %+v", o.squadPlan)
	}

	// Teams switched off: the saved file is ignored whatever the board says.
	o.squadPlan = nil
	o.cfg.Squads = false
	o.restoreSquadPlan(stamped)
	if o.squadPlan != nil {
		t.Fatalf("squads: false must not restore a plan")
	}
}

// The library's "one team" is a decision the charter phase keeps: it does not
// ask the model to invent a second team behind the composition's back.
func TestOneLibraryTeamIsDecidedNotHandedToTheModel(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"web/package.json": `{"name":"web"}`,
		"web/src/App.tsx":  "export default function App() { return null }\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default(root)
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, decided := o.teamsFromLibrary(context.Background(), "add a dark mode toggle to the dashboard", nil, "", "")
	if p != nil || !decided {
		t.Fatalf("one matched team: plan=%v decided=%v — the library decided, the model must not be asked", p, decided)
	}

	// Nothing matched and nothing pinned: the question stays open.
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "notes.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o2, err := New(config.Default(empty))
	if err != nil {
		t.Fatal(err)
	}
	if p, decided := o2.teamsFromLibrary(context.Background(), "rewrite the poem", nil, "", ""); p != nil || decided {
		t.Fatalf("no match: plan=%v decided=%v — the model may still be asked", p, decided)
	}
}

// On a single-team run the team's own manager is the one asked about a
// rejected delivery, with the team's people first in the roster — not the run
// default, which is what a run with no squad plan used to fall back to.
func TestSingleTeamManagerStaffsTriage(t *testing.T) {
	cfg := config.Default(fullstackRoot(t))
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	o.singleTeam = &composer.TeamChoice{
		ID: "frontend-react", Worker: "react-worker", Reviewer: "react-reviewer", Tester: "react-tester",
		Manager: "triage", Agents: []string{"worker"},
	}
	st := o.singleTeamStaffing()
	if st.Squad != "frontend-react" || st.Manager != "triage" {
		t.Fatalf("staffing=%+v", st)
	}
	if got := strings.Join(st.Members, ","); !strings.HasPrefix(got, "react-worker,react-reviewer,react-tester,worker") {
		t.Fatalf("members=%v", st.Members)
	}
	// The staffing plan the loop's helpers read is the single team's when
	// there is no squad plan, and the squad plan when there is one.
	if p := o.staffingPlan(); p == nil || len(p.Squads) != 1 || p.Squads[0].ID != "frontend-react" {
		t.Fatalf("staffingPlan=%+v", p)
	}
	o.squadPlan = &squads.Plan{Squads: []squads.Squad{{ID: "a"}, {ID: "b"}}}
	if p := o.staffingPlan(); p == nil || len(p.Squads) != 2 {
		t.Fatalf("staffingPlan with a squad plan=%+v", p)
	}
	o.singleTeam = nil
	o.squadPlan = nil
	if o.staffingPlan() != nil || o.singleTeamStaffing().Squad != "" {
		t.Fatalf("no team: staffing should be empty")
	}
}

// A preview with pins goes through the factory like the run does: a seat the
// run would clear is shown cleared, and a manager it would replace is shown
// replaced.
func TestPreviewWithPinsAppliesTheFactoryChecks(t *testing.T) {
	root := fullstackRoot(t)
	teamsDir := filepath.Join(root, ".slmcode", "blocks", "teams")
	if err := os.MkdirAll(teamsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	team := "api_version: blocks/v1\nkind: team\nid: ghost\nname: Ghost\nspec:\n  id: ghost\n" +
		"  owns: [ghost/**]\n  worker: no-such-worker\n  manager: go-worker\n"
	if err := os.WriteFile(filepath.Join(teamsDir, "ghost.yaml"), []byte(team), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := New(config.Default(root))
	if err != nil {
		t.Fatal(err)
	}
	comp := o.PreviewCompositionWithTeams("tidy the module manifest", []string{"ghost"})
	tc, ok := comp.TeamChoiceFor("ghost")
	if !ok {
		t.Fatalf("pinned team not on the composition: %+v", comp.Teams)
	}
	if !tc.Pinned {
		t.Fatalf("pin not marked: %+v", tc)
	}
	if tc.Worker != "" {
		t.Fatalf("an unregistered worker must be cleared, got %q", tc.Worker)
	}
	if tc.Manager != "triage" || !tc.ManagerDefault {
		t.Fatalf("a worker cannot manage; want the run default, got %+v", tc)
	}
	// The configured pins are untouched by a preview with an override.
	if len(o.cfg.Teams) != 0 {
		t.Fatalf("preview leaked pins into the config: %v", o.cfg.Teams)
	}
}

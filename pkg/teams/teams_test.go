package teams

import (
	"reflect"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/squads"
)

func TestNormalizeCanonicalizesEveryField(t *testing.T) {
	tm := Team{
		ID:      "  Backend Go  ",
		Charter: "  own the API  ",
		Owns:    []string{"./cmd/", "cmd", "internal//pkg", ""},
		Worker:  "  Go-Worker ",
		Skills:  []string{"Atomic-Coding", "atomic-coding", " "},
		Match: Match{
			Keywords:   []string{"API", "api", ""},
			Extensions: []string{"go", ".GO", "tsx"},
			Files:      []string{"./go.mod", "go.mod"},
		},
	}
	tm.Normalize()

	if tm.ID != "backend-go" {
		t.Fatalf("id=%q — spaces and case must slug, or the same team saved twice lands in two files", tm.ID)
	}
	if tm.Name != "backend-go" {
		t.Fatalf("name=%q — an unnamed team must fall back to its id rather than render blank", tm.Name)
	}
	if !reflect.DeepEqual(tm.Owns, []string{"cmd", "internal/pkg"}) {
		t.Fatalf("owns=%v — ./cmd/ and cmd are one path, and a duplicate glob doubles the ownership check", tm.Owns)
	}
	if tm.Worker != "go-worker" {
		t.Fatalf("worker=%q", tm.Worker)
	}
	if !reflect.DeepEqual(tm.Skills, []string{"atomic-coding"}) {
		t.Fatalf("skills=%v", tm.Skills)
	}
	if !reflect.DeepEqual(tm.Match.Keywords, []string{"api"}) {
		t.Fatalf("keywords=%v", tm.Match.Keywords)
	}
	if !reflect.DeepEqual(tm.Match.Extensions, []string{".go", ".tsx"}) {
		t.Fatalf("extensions=%v — a dotless suffix must gain its dot or it matches nothing", tm.Match.Extensions)
	}
	if !reflect.DeepEqual(tm.Match.Files, []string{"go.mod"}) {
		t.Fatalf("files=%v", tm.Match.Files)
	}
}

// A team owning nothing can never receive a task. Catching it here is the
// difference between "this team is misconfigured" and a team that silently
// sits idle for a whole run while its half goes unbuilt.
func TestValidateRejectsATeamNoTaskCouldReach(t *testing.T) {
	tm := Team{ID: "ghost"}
	err := tm.Validate()
	if err == nil {
		t.Fatal("a team owning no paths must not validate")
	}
	if !strings.Contains(err.Error(), "owns no paths") {
		t.Fatalf("error must say why: %v", err)
	}

	if err := (&Team{Owns: []string{"web/**"}}).Validate(); err == nil {
		t.Fatal("a team with no id must not validate — nothing could be assigned to it")
	}
}

func TestSquadRoundTripKeepsStaffingAndOwnership(t *testing.T) {
	tm := Team{
		ID: "backend", Name: "Backend", Charter: "the API",
		Owns: []string{"cmd/**"}, Acceptance: "go test ./...",
		Worker: "go-worker", Reviewer: "go-reviewer", Tester: "go-tester", Manager: "triage",
		Agents: []string{"go-corrector", "deep"},
		Skills: []string{"go-table-tests"},
	}
	tm.Normalize()

	sq := tm.Squad()
	back := FromSquad(sq)
	if !reflect.DeepEqual(back, tm) {
		t.Fatalf("round trip lost data:\n team=%+v\n back=%+v", tm, back)
	}
}

// The squad a team renders must not alias the team's own slices: a run that
// edits a squad's globs would otherwise rewrite the library entry it came from,
// on disk, without anyone asking for it.
func TestSquadDoesNotAliasTheLibraryEntry(t *testing.T) {
	tm := Team{ID: "backend", Owns: []string{"cmd/**"}, Skills: []string{"a"}}
	tm.Normalize()

	sq := tm.Squad()
	sq.Owns[0] = "web/**"
	sq.Skills[0] = "b"

	if tm.Owns[0] != "cmd/**" || tm.Skills[0] != "a" {
		t.Fatalf("editing the squad mutated the library team: %+v", tm)
	}
}

// Compose has to produce something squads.Validate accepts, or the library is a
// menu of plans that cannot run.
func TestComposeProducesARunnablePlan(t *testing.T) {
	sel := Selection{Teams: []Team{
		{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm --prefix web run build"},
	}}
	p := Compose(sel, "two halves")

	if p.Summary != "two halves" {
		t.Fatalf("summary=%q", p.Summary)
	}
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"backend", "frontend"}) {
		t.Fatalf("ids=%v — Compose must keep rank order", got)
	}
	for _, pr := range p.Validate() {
		if pr.Severity == squads.SeverityError {
			t.Fatalf("composed plan does not validate: %s", pr)
		}
	}
	// The contract is deliberately left to the model: it describes THIS
	// request's seam and cannot be stored in a library.
	if len(p.Contract.Interfaces) != 0 {
		t.Fatalf("Compose must not invent a contract: %+v", p.Contract)
	}
}

// A library outlives the agent registry it was written against. A worker id
// that is no longer registered must fall back to the pipeline default, loudly:
// left in place it produces a team that never dispatches anything.
func TestStaffCheckClearsAgentsTheHarnessCannotDispatch(t *testing.T) {
	p := squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"cmd/**"}, Worker: "go-worker", Reviewer: "ghost-reviewer"},
		{ID: "frontend", Owns: []string{"web/**"}, Manager: "nobody"},
	}}
	registered := func(id string) bool { return id == "go-worker" }

	notes := StaffCheck(&p, registered)

	if p.Squads[0].Worker != "go-worker" {
		t.Fatalf("a registered agent must survive: %q", p.Squads[0].Worker)
	}
	if p.Squads[0].Reviewer != "" || p.Squads[1].Manager != "" {
		t.Fatalf("unregistered staffing must be cleared: %+v", p.Squads)
	}
	if len(notes) != 2 {
		t.Fatalf("every cleared field must be reported, got %v", notes)
	}
	for _, n := range notes {
		if !strings.Contains(n, "not a registered agent") {
			t.Fatalf("note must say why: %q", n)
		}
	}
	if StaffCheck(nil, registered) != nil || StaffCheck(&p, nil) != nil {
		t.Fatal("StaffCheck must tolerate a nil plan and a nil registry")
	}
}

func TestSortIsStableAndByID(t *testing.T) {
	list := []Team{{ID: "z"}, {ID: "a"}, {ID: "m"}}
	Sort(list)
	if list[0].ID != "a" || list[1].ID != "m" || list[2].ID != "z" {
		t.Fatalf("unsorted: %+v", list)
	}
}

// A team is "these people", and how many there are is its author's business.
// Four fixed seats is a shape the harness dispatches, not a shape a team has to
// be — so an arbitrary roster has to survive normalization and reach the squad.
func TestATeamCarriesAsManyAgentsAsItsAuthorWants(t *testing.T) {
	tm := Team{
		ID: "platform", Owns: []string{"platform/**"},
		Worker: "go-worker",
		Agents: []string{"Go-Corrector", "deep", "go-corrector", " ", "python-worker"},
		Skills: []string{"a", "b", "c", "a"},
	}
	tm.Normalize()

	if !reflect.DeepEqual(tm.Agents, []string{"go-corrector", "deep", "python-worker"}) {
		t.Fatalf("roster=%v — deduped and lowercased, in the order its author wrote it", tm.Agents)
	}
	if !reflect.DeepEqual(tm.Skills, []string{"a", "b", "c"}) {
		t.Fatalf("skills=%v", tm.Skills)
	}
	if got := tm.Squad().Agents; !reflect.DeepEqual(got, tm.Agents) {
		t.Fatalf("the squad lost the roster: %v", got)
	}
}

// A roster member the harness cannot dispatch is a name on a team that never
// does any work, and the symptom — a manager offering an agent the loop then
// refuses — costs a full model call to discover.
func TestStaffCheckDropsRosterMembersTheHarnessCannotDispatch(t *testing.T) {
	p := squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"cmd/**"}, Worker: "go-worker",
			Agents: []string{"go-corrector", "uninstalled", "deep"}},
		{ID: "frontend", Owns: []string{"web/**"}},
	}}
	registered := func(id string) bool { return id != "uninstalled" }

	notes := StaffCheck(&p, registered)

	if got := p.Squads[0].Agents; !reflect.DeepEqual(got, []string{"go-corrector", "deep"}) {
		t.Fatalf("roster=%v — the ghost must go, the rest must stay in order", got)
	}
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "roster member \"uninstalled\"") {
		t.Fatalf("the drop must be reported: %v", notes)
	}
}

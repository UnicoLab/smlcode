package squads

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// goReactPlan is the plan this whole feature exists to make work: a Go API and
// a React SPA, built at the same time, meeting at a frozen HTTP contract.
func goReactPlan() Plan {
	p := Plan{
		Summary: "Todo app: Go API + React SPA",
		Contract: Contract{
			Summary: "The SPA talks to the API over JSON on /api.",
			Interfaces: []Interface{
				{
					ID:        "GET /api/todos",
					Provider:  "backend",
					Consumers: []string{"frontend"},
					Spec:      "200 -> [{\"id\":string,\"title\":string,\"done\":bool}]",
				},
				{
					ID:        "POST /api/todos",
					Provider:  "backend",
					Consumers: []string{"frontend"},
					Spec:      "{\"title\":string} -> 201 {\"id\":string,\"title\":string,\"done\":bool}",
				},
			},
		},
		Squads: []Squad{
			{
				ID: "backend", Name: "Backend · Go API",
				Charter:    "net/http server exposing the todo API and serving the built SPA.",
				Owns:       []string{"cmd/**", "internal/**", "go.mod", "go.sum"},
				Acceptance: "go build ./... && go test ./...",
				Worker:     "go-worker",
			},
			{
				ID: "frontend", Name: "Frontend · React SPA",
				Charter:    "Vite + React client for the todo API.",
				Owns:       []string{"web/**"},
				Acceptance: "npm --prefix web run build",
				Worker:     "react-worker",
			},
		},
		Integration: Integration{
			Acceptance: "go build ./... && npm --prefix web run build && go test ./...",
			Notes:      []string{"The API serves web/dist at / via embed."},
		},
	}
	p.Normalize()
	return p
}

func TestValidateAcceptsARealTwoTeamPlan(t *testing.T) {
	p := goReactPlan()
	problems := p.Validate()
	if problems.Errors() {
		t.Fatalf("a legitimate Go+React plan must validate, got:\n%s",
			strings.Join(problems.Strings(), "\n"))
	}
	if len(problems) != 0 {
		t.Errorf("expected no warnings either, got:\n%s", strings.Join(problems.Strings(), "\n"))
	}
	if !p.Enabled() {
		t.Error("two squads must count as enabled")
	}
}

// The safety property. Overlapping ownership means two agents writing one file
// at the same time and one of the two edits silently disappearing.
func TestValidateRejectsOverlappingOwnership(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []string
		overlaps bool
	}{
		{"identical globs", []string{"api/**"}, []string{"api/**"}, true},
		{"identical literals", []string{"go.mod"}, []string{"go.mod"}, true},
		{"subtree inside subtree", []string{"web/**"}, []string{"web/src/**"}, true},
		{"literal inside glob", []string{"web/**"}, []string{"web/package.json"}, true},
		{"bare dir vs its glob", []string{"api"}, []string{"api/**"}, true},
		{"catch-all vs anything", []string{"**"}, []string{"web/**"}, true},
		{"catch-all star vs anything", []string{"*"}, []string{"api/**"}, true},
		{"leading wildcard reaches everywhere", []string{"**/*.go"}, []string{"web/**"}, true},
		{"parent dir vs child file", []string{"internal"}, []string{"internal/store/db.go"}, true},

		{"clean split", []string{"api/**"}, []string{"web/**"}, false},
		{"clean literals", []string{"go.mod"}, []string{"package.json"}, false},
		{"sibling subtrees", []string{"cmd/server/**"}, []string{"cmd/cli/**"}, false},
		{"different roots with wildcards", []string{"api/*.go"}, []string{"web/*.ts"}, false},
		{"deep siblings", []string{"src/backend/**"}, []string{"src/frontend/**"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Plan{Squads: []Squad{
				{ID: "a", Owns: tc.a, Acceptance: "x"},
				{ID: "b", Owns: tc.b, Acceptance: "y"},
			}}
			p.Normalize()
			got := p.Validate()
			hasOverlap := false
			for _, pr := range got {
				if pr.Severity == SeverityError && strings.Contains(pr.Message, "both claim") {
					hasOverlap = true
				}
			}
			if hasOverlap != tc.overlaps {
				t.Fatalf("overlap(%v, %v) = %v, want %v\nfindings:\n%s",
					tc.a, tc.b, hasOverlap, tc.overlaps, strings.Join(got.Strings(), "\n"))
			}
		})
	}
}

func TestValidateCatchesStructuralHoles(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*Plan)
		wantErr  bool
		contains string
	}{
		{"single squad is not a team", func(p *Plan) { p.Squads = p.Squads[:1] }, true, "at least 2 squads"},
		{"squad owning nothing", func(p *Plan) { p.Squads[1].Owns = nil }, true, "owns no paths"},
		{"interface with unknown provider", func(p *Plan) {
			p.Contract.Interfaces[0].Provider = "mobile"
		}, true, "not a squad in this plan"},
		{"interface with unknown consumer", func(p *Plan) {
			p.Contract.Interfaces[0].Consumers = []string{"ios"}
		}, true, "not a squad in this plan"},
		{"no integration command is a warning", func(p *Plan) {
			p.Integration.Acceptance = ""
		}, false, "integration acceptance"},
		{"no contract is a warning", func(p *Plan) {
			p.Contract.Interfaces = nil
		}, false, "invent their own version of the seam"},
		{"squad without acceptance is a warning", func(p *Plan) {
			p.Squads[0].Acceptance = ""
		}, false, "cannot be proven green"},
		{"interface without a spec is a warning", func(p *Plan) {
			p.Contract.Interfaces[0].Spec = ""
		}, false, "named but not specified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := goReactPlan()
			tc.mutate(&p)
			got := p.Validate()
			if got.Errors() != tc.wantErr {
				t.Fatalf("Errors() = %v, want %v\n%s", got.Errors(), tc.wantErr,
					strings.Join(got.Strings(), "\n"))
			}
			joined := strings.Join(got.Strings(), "\n")
			if !strings.Contains(joined, tc.contains) {
				t.Fatalf("expected a finding mentioning %q, got:\n%s", tc.contains, joined)
			}
		})
	}
}

func TestOwnsPath(t *testing.T) {
	s := Squad{ID: "backend", Owns: []string{"cmd/**", "internal/**", "go.mod"}}
	owned := []string{
		"cmd/server/main.go", "cmd", "cmd/server",
		"internal/store/db.go", "internal", "go.mod",
		"./cmd/server/main.go", // normalized
	}
	for _, p := range owned {
		if !s.OwnsPath(p) {
			t.Errorf("expected %q to be owned", p)
		}
	}
	notOwned := []string{"web/src/App.tsx", "package.json", "", "commander/x.go", "go.sum"}
	for _, p := range notOwned {
		if s.OwnsPath(p) {
			t.Errorf("expected %q NOT to be owned", p)
		}
	}
}

func TestAssignRoutesWorkToOneSquad(t *testing.T) {
	p := goReactPlan()
	cases := []struct {
		name      string
		files     []string
		squad     string
		straddles []string
		unowned   []string
	}{
		{"pure backend", []string{"cmd/server/main.go", "internal/store/db.go"}, "backend", nil, nil},
		{"pure frontend", []string{"web/src/App.tsx"}, "frontend", nil, nil},
		{"normalizes before routing", []string{"./web/src/App.tsx"}, "frontend", nil, nil},
		// The seam itself must NOT land on one team: handing it to "frontend"
		// is precisely how a frontend task ends up rewriting the API.
		{"straddling work is unassigned", []string{"cmd/server/main.go", "web/src/api.ts"},
			"", []string{"backend", "frontend"}, nil},
		{"unowned files are reported", []string{"docs/readme.md"}, "", nil, []string{"docs/readme.md"}},
		{"empty input", nil, "", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.Assign(tc.files)
			if got.Squad != tc.squad {
				t.Errorf("Squad = %q, want %q", got.Squad, tc.squad)
			}
			if !reflect.DeepEqual(got.Straddles, tc.straddles) {
				t.Errorf("Straddles = %v, want %v", got.Straddles, tc.straddles)
			}
			if !reflect.DeepEqual(got.Unowned, tc.unowned) {
				t.Errorf("Unowned = %v, want %v", got.Unowned, tc.unowned)
			}
		})
	}
}

// Straddle reporting must not depend on which file happened to come first, or
// the same task reports a different owner on a re-run.
func TestAssignIsDeterministicRegardlessOfFileOrder(t *testing.T) {
	p := goReactPlan()
	a := p.Assign([]string{"web/src/api.ts", "cmd/server/main.go"})
	b := p.Assign([]string{"cmd/server/main.go", "web/src/api.ts"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("assignment depends on file order: %+v vs %+v", a, b)
	}
	if !reflect.DeepEqual(a.Straddles, []string{"backend", "frontend"}) {
		t.Fatalf("straddles should come back in plan order, got %v", a.Straddles)
	}
}

func TestCoverageFindsThePlansHoles(t *testing.T) {
	p := goReactPlan()
	missing := p.Coverage([]string{
		"cmd/server/main.go", "web/src/App.tsx",
		"Makefile", "docs/api.md", "Makefile", // duplicate must collapse
	})
	if !reflect.DeepEqual(missing, []string{"Makefile", "docs/api.md"}) {
		t.Fatalf("Coverage = %v, want [Makefile docs/api.md]", missing)
	}
}

func TestNormalizeCleansWhatASmallModelEmits(t *testing.T) {
	p := Plan{
		Summary: "  spaced  ",
		Squads: []Squad{
			{ID: "  Back End ", Owns: []string{"api/**", "api/**", " ", "./api/**"}},
			{ID: "back-end", Owns: []string{"x/**"}},   // duplicate id after slugging
			{ID: "", Owns: []string{"nowhere/**"}},     // unusable
			{ID: "frontend", Owns: []string{"web/**"}}, //
		},
		Contract: Contract{Interfaces: []Interface{
			{ID: "GET /a", Provider: "Back End", Consumers: []string{"frontend", "Back End", "frontend"}},
			{ID: "GET /a", Provider: "frontend"}, // duplicate id
			{ID: "  ", Provider: "frontend"},     // unusable
		}},
	}
	p.Normalize()

	if got := p.IDs(); !reflect.DeepEqual(got, []string{"back-end", "frontend"}) {
		t.Fatalf("IDs = %v, want [back-end frontend]", got)
	}
	if p.Summary != "spaced" {
		t.Errorf("Summary = %q", p.Summary)
	}
	back, _ := p.Squad("back-end")
	if !reflect.DeepEqual(back.Owns, []string{"api/**"}) {
		t.Errorf("owns should dedupe and normalize, got %v", back.Owns)
	}
	if back.Name != "back-end" {
		t.Errorf("Name should default to the id, got %q", back.Name)
	}
	if len(p.Contract.Interfaces) != 1 {
		t.Fatalf("expected 1 interface after dedupe, got %d", len(p.Contract.Interfaces))
	}
	in := p.Contract.Interfaces[0]
	if in.Provider != "back-end" {
		t.Errorf("provider should be slugged, got %q", in.Provider)
	}
	// A provider listed as its own consumer reads as a circular dependency.
	if !reflect.DeepEqual(in.Consumers, []string{"frontend"}) {
		t.Errorf("consumers = %v, want [frontend]", in.Consumers)
	}
}

func TestParseToleratesModelWrapping(t *testing.T) {
	body := `{"squads":[{"id":"backend","owns":["api/**"]},{"id":"frontend","owns":["web/**"]}]}`
	cases := []string{
		body,
		"Here is the plan:\n```json\n" + body + "\n```\nHope that helps!",
		"```\n" + body + "\n```",
		"  \n\n" + body,
	}
	for i, raw := range cases {
		p, err := Parse(raw)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := p.IDs(); !reflect.DeepEqual(got, []string{"backend", "frontend"}) {
			t.Fatalf("case %d: IDs = %v", i, got)
		}
	}
}

// A charter containing a brace must not truncate the object.
func TestParseHandlesBracesInsideStrings(t *testing.T) {
	raw := `{"summary":"use {mustache} templates","squads":[
	  {"id":"a","charter":"emit {\"json\":true}","owns":["a/**"]},
	  {"id":"b","owns":["b/**"]}]}`
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Summary != "use {mustache} templates" {
		t.Errorf("Summary = %q", p.Summary)
	}
	if len(p.Squads) != 2 {
		t.Fatalf("expected 2 squads, got %d", len(p.Squads))
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "no json here", "{unclosed", "[1,2,3]"} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("expected an error for %q", raw)
		}
	}
}

func TestBriefTellsASquadItsBoundaryAndItsObligations(t *testing.T) {
	p := goReactPlan()
	b := p.Brief("backend")

	for _, want := range []string{
		"backend",
		"cmd/**",         // what it owns
		"web/**",         // what it must not touch
		"do not edit",    // the boundary, stated
		"go build ./...", // its own acceptance
		"You PROVIDE",    // its obligations to the other squad
		"GET /api/todos", //
		ContractFile,     // where the full text lives
	} {
		if !strings.Contains(b, want) {
			t.Errorf("backend brief is missing %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "You CONSUME") {
		t.Errorf("backend consumes nothing; brief should not claim otherwise:\n%s", b)
	}

	f := p.Brief("frontend")
	if !strings.Contains(f, "You CONSUME") {
		t.Errorf("frontend brief should list what it consumes:\n%s", f)
	}
	if strings.Contains(f, "You PROVIDE") {
		t.Errorf("frontend provides nothing:\n%s", f)
	}
	if p.Brief("nope") != "" {
		t.Error("an unknown squad must brief to nothing")
	}
}

func TestRenderContractIsSelfContained(t *testing.T) {
	got := RenderContract(goReactPlan())
	for _, want := range []string{
		"# Interface contract", "FROZEN",
		"Backend · Go API", "Frontend · React SPA",
		"GET /api/todos", "POST /api/todos",
		"Provided by: `backend`", "Consumed by: `frontend`",
		"## Integration", "npm --prefix web run build",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("contract is missing %q:\n%s", want, got)
		}
	}
}

func TestSaveLoadRoundTripAndWritesBothArtifacts(t *testing.T) {
	dir := t.TempDir()
	want := goReactPlan()
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Both files, always: a plan without its contract is agents building
	// against text that describes something else.
	for _, name := range []string{PlanFile, ContractFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s on disk: %v", name, err)
		}
	}

	got, ok, err := Load(dir)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the plan:\n got %+v\nwant %+v", got, want)
	}

	Clear(dir)
	if _, ok, _ := Load(dir); ok {
		t.Error("Clear should remove the plan")
	}
	if _, err := os.Stat(filepath.Join(dir, ContractFile)); !os.IsNotExist(err) {
		t.Error("Clear should remove the contract too")
	}
}

func TestLoadMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Load(dir); ok || err != nil {
		t.Fatalf("missing plan: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(dir, PlanFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Error("a corrupt plan must be an error, not a silently empty plan")
	}
	if _, ok, _ := Load(""); ok {
		t.Error("no slmDir must not report a plan")
	}
}

func TestEnabledNeedsTwoSquads(t *testing.T) {
	var nilPlan *Plan
	if nilPlan.Enabled() {
		t.Error("a nil plan is not enabled")
	}
	one := Plan{Squads: []Squad{{ID: "a", Owns: []string{"a/**"}}}}
	if one.Enabled() {
		t.Error("one squad is the normal pipeline, not a team structure")
	}
	two := goReactPlan()
	if !two.Enabled() {
		t.Error("two squads is enabled")
	}
}

func TestSummarizeIsSafeOnAnEmptyPlan(t *testing.T) {
	var nilPlan *Plan
	if got := nilPlan.Summarize(); got != "no squads" {
		t.Errorf("Summarize() = %q", got)
	}
	full := goReactPlan()
	if got := full.Summarize(); !strings.Contains(got, "2 squads") {
		t.Errorf("Summarize() = %q", got)
	}
}

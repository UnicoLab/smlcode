// Package teams is the persistent library of virtual development teams.
//
// # WHY THIS EXISTS
//
// pkg/squads models the teams of ONE RUN: the manager specialist reads the
// query, invents an org chart, and that org chart dies with the run that
// produced it. That is the right lifetime for the CONTRACT between two halves —
// it describes this feature's seam and nothing else — and the wrong lifetime for
// everything around it.
//
// A team is not a per-run fact. "The backend is Go, it lives under cmd/ and
// internal/, go-worker writes it, `go test ./...` proves it" is true of the
// repository, on every run, forever. Re-deriving it from a model on each run
// costs a planning call to rediscover something the user already knows, and —
// on a 7–32B model, which is what this harness is for — gets it subtly wrong a
// meaningful fraction of the time: a glob that overlaps, a worker id that was
// never registered, an acceptance command for a language the repo does not use.
// Every one of those failures downgrades the run to a single stream, silently.
//
// So the library holds teams the USER authored, and the run uses them:
//
//   - a Team is a squad template plus the evidence that says when it applies
//     (Match — query keywords, marker files, extensions);
//   - Select turns "this query, this workspace" into a set of teams with NO
//     model call at all, which is the only part of squad assembly that a small
//     model was ever load-bearing for and the part it was worst at;
//   - Compose turns that set into a squads.Plan the rest of the harness already
//     knows how to execute.
//
// The model is still allowed to assemble teams — a library that does not cover
// the query falls back to it — but it is now the fallback rather than the only
// path. See orchestrator.assembleSquads.
package teams

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// Team is one reusable virtual development team.
//
// The staffing fields mirror squads.Squad on purpose: a Team is what a squad is
// BEFORE a run picks it up, and a field that exists here but not there is a
// setting the user can edit and the harness then ignores.
type Team struct {
	// ID is the stable handle ("backend"), used on tasks and in events.
	ID string `yaml:"id" json:"id"`
	// Name is the human label ("Backend · Go API").
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Charter is the one-line mission injected into every task pack for this
	// team. It is what keeps a worker from drifting into another team's half.
	Charter string `yaml:"charter,omitempty" json:"charter,omitempty"`
	// Owns lists path globs (`**` supported) this team may write to. Two teams
	// selected together may never own the same path — Select enforces it before
	// the plan is built, squads.Validate enforces it again before it runs.
	Owns []string `yaml:"owns,omitempty" json:"owns,omitempty"`
	// Acceptance is the command that proves THIS team's half works alone.
	Acceptance string `yaml:"acceptance,omitempty" json:"acceptance,omitempty"`

	// Worker / Reviewer / Tester / Manager staff the team. Empty means the
	// pipeline's default for that role, which is always a valid answer.
	Worker   string `yaml:"worker,omitempty" json:"worker,omitempty"`
	Reviewer string `yaml:"reviewer,omitempty" json:"reviewer,omitempty"`
	Tester   string `yaml:"tester,omitempty" json:"tester,omitempty"`
	Manager  string `yaml:"manager,omitempty" json:"manager,omitempty"`

	// Agents is the team's ROSTER — every agent the user wants on this team,
	// beyond the named seats above and in whatever number they choose.
	//
	// Four seats is a shape the harness happens to dispatch, not a shape a team
	// has to be. A real team is "these people", and the number is the user's
	// business. The roster is load-bearing in three places:
	//
	//   - the project manager triaging this team's rejected work sees its own
	//     people FIRST (squads.Colleagues) — the whole reason a per-team manager
	//     beats a run-wide one;
	//   - the plan editor offers them first for this team's tasks;
	//   - an agent the harness cannot dispatch is dropped with a reason rather
	//     than producing a team member nothing can staff.
	Agents []string `yaml:"agents,omitempty" json:"agents,omitempty"`
	// Skills are loaded into this team's task packs. As many as the team needs.
	Skills []string `yaml:"skills,omitempty" json:"skills,omitempty"`

	// Match is the evidence that selects this team for a request. A team with
	// no Match is never auto-selected — it is still available to pick by hand,
	// which is the correct behavior for a team whose applicability only its
	// author knows.
	Match Match `yaml:"match,omitempty" json:"match,omitempty"`

	// Source / Path are runtime provenance, filled by the loader, never
	// authored. They are what lets the UI say "builtin, edit to override".
	Source string `yaml:"-" json:"source,omitempty"`
	Path   string `yaml:"-" json:"path,omitempty"`
}

// Match is the deterministic evidence that a team applies to a request.
//
// Three independent kinds, because they fail independently: a query mentioning
// "the React frontend" is evidence even in an empty directory, and a repository
// containing web/package.json is evidence even when the query says only "add
// dark mode". Requiring both would miss half the real cases; scoring them
// together is what makes a one-word query and a bare repo both work.
type Match struct {
	// Keywords are matched against the query, case-insensitively, on word
	// boundaries — "api" must not fire on "rapids".
	Keywords []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	// Files are marker paths or globs looked for in the workspace inventory
	// ("go.mod", "web/package.json", "**/pyproject.toml").
	Files []string `yaml:"files,omitempty" json:"files,omitempty"`
	// Extensions are file suffixes whose presence in the workspace is evidence
	// (".go", ".tsx"). Written with or without the dot.
	Extensions []string `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	// Priority breaks ties between teams with equal evidence, and — when
	// negative — opts a team out of automatic selection entirely while leaving
	// it selectable by hand.
	Priority int `yaml:"priority,omitempty" json:"priority,omitempty"`
}

// Empty reports whether this team can never be auto-selected.
func (m Match) Empty() bool {
	return len(m.Keywords) == 0 && len(m.Files) == 0 && len(m.Extensions) == 0
}

// ── Normalize / Validate ─────────────────────────────────────────────────

// Normalize fills defaults and canonicalizes every field.
//
// Called before persisting and after loading, per the repo's Normalize →
// Validate convention: a team edited in Studio, one hand-written in YAML and
// one shipped as a builtin all have to arrive at the same bytes, or the same
// team from two sources compares unequal and the override never takes.
func (t *Team) Normalize() {
	if t == nil {
		return
	}
	t.ID = slug(t.ID)
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		t.Name = t.ID
	}
	t.Charter = strings.TrimSpace(t.Charter)
	t.Acceptance = strings.TrimSpace(t.Acceptance)
	t.Worker = agentID(t.Worker)
	t.Reviewer = agentID(t.Reviewer)
	t.Tester = agentID(t.Tester)
	t.Manager = agentID(t.Manager)
	t.Owns = dedupePaths(t.Owns)
	t.Agents = dedupeFold(t.Agents)
	t.Skills = dedupeFold(t.Skills)
	t.Match.Keywords = dedupeFold(t.Match.Keywords)
	t.Match.Files = dedupePaths(t.Match.Files)

	exts := make([]string, 0, len(t.Match.Extensions))
	for _, e := range t.Match.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts = append(exts, e)
	}
	t.Match.Extensions = dedupeFold(exts)
}

// Validate reports why this team could not be run.
//
// Deliberately narrower than squads.Validate: a single team cannot violate the
// disjoint-ownership rule, which needs a second team to break. What it CAN do
// is be unroutable — a team owning nothing can never receive a task and would
// sit idle for a whole run while its half of the work goes unbuilt.
func (t *Team) Validate() error {
	if t == nil {
		return fmt.Errorf("nil team")
	}
	t.Normalize()
	if t.ID == "" {
		return fmt.Errorf("team: id is required")
	}
	if len(t.Owns) == 0 {
		return fmt.Errorf("team %q: owns no paths, so no task could ever be routed to it", t.ID)
	}
	return nil
}

// Squad renders this team as the squad the harness executes.
func (t Team) Squad() squads.Squad {
	return squads.Squad{
		ID:         t.ID,
		Name:       t.Name,
		Charter:    t.Charter,
		Owns:       append([]string(nil), t.Owns...),
		Acceptance: t.Acceptance,
		Worker:     t.Worker,
		Reviewer:   t.Reviewer,
		Tester:     t.Tester,
		Manager:    t.Manager,
		Agents:     append([]string(nil), t.Agents...),
		Skills:     append([]string(nil), t.Skills...),
	}
}

// FromSquad captures a run's squad as a reusable team.
//
// This is how "the org chart the model just invented was right — keep it" turns
// into a library entry, without the user retyping four globs and three agent
// ids they can already see on screen.
func FromSquad(s squads.Squad) Team {
	t := Team{
		ID:         s.ID,
		Name:       s.Name,
		Charter:    s.Charter,
		Owns:       append([]string(nil), s.Owns...),
		Acceptance: s.Acceptance,
		Worker:     s.Worker,
		Reviewer:   s.Reviewer,
		Tester:     s.Tester,
		Manager:    s.Manager,
		Agents:     append([]string(nil), s.Agents...),
		Skills:     append([]string(nil), s.Skills...),
	}
	t.Normalize()
	return t
}

// ── Helpers shared with the loader ───────────────────────────────────────

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// agentID canonicalizes an agent reference without slugging it: agent ids may
// legitimately contain characters slug() strips, and rewriting one produces a
// team staffed by an agent that does not exist.
func agentID(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func dedupeFold(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupePaths(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = normalizePath(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

// matchPath answers whether one marker glob covers a workspace path, with the
// same "a bare directory owns its subtree" rule pkg/squads uses. Two different
// answers to "does backend own api/server.go" across two packages is a bug
// nobody would find until a task came back unassigned.
func matchPath(glob, rel string) bool {
	glob, rel = normalizePath(glob), normalizePath(rel)
	if glob == "" || rel == "" {
		return false
	}
	if workspace.MatchGlob(glob, rel) {
		return true
	}
	if !strings.ContainsAny(glob, "*?[") {
		// A bare marker matches the file anywhere in the tree as well as at the
		// root: an inventory listing `services/api/go.mod` is still evidence of
		// a Go project, and demanding `**/go.mod` from every author is a rule
		// they would forget once and debug for an hour.
		return rel == glob || strings.HasPrefix(rel, glob+"/") || strings.HasSuffix(rel, "/"+glob)
	}
	if trimmed := strings.TrimSuffix(strings.TrimSuffix(glob, "**"), "/"); trimmed != "" && trimmed != glob {
		return rel == trimmed || strings.HasPrefix(rel, trimmed+"/")
	}
	return false
}

// Sort orders a roster deterministically for any output a human or a test
// reads. Rosters are assembled by merging several directories through maps, and
// map iteration must never reach a rendered list.
func Sort(list []Team) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
}

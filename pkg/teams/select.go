package teams

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/squads"
)

// ── Picking the teams for a request ──────────────────────────────────────
//
// This is the part of squad assembly that never needed a model.
//
// "Which teams does this request involve" is answered by three pieces of
// evidence that are all sitting in front of us before any call is made: the
// words in the query, the marker files in the workspace, and the extensions of
// the files that are actually there. A 30B model asked the same question reads
// the same evidence and then has to also emit valid JSON, non-overlapping
// globs, and agent ids that exist — three extra ways to fail at a question that
// was already answered.
//
// So Select is deterministic, allocation-free of model calls, and testable
// without one. What is left for the model is the part that genuinely needs
// judgment: the CONTRACT between the teams it picked.

// Weights for each kind of evidence. Marker files outrank keywords because a
// go.mod is a fact about the repository and "backend" is a word someone typed;
// extensions outrank nothing because a repo containing one .go file in a script
// directory is the weakest signal of the three.
const (
	weightKeyword   = 3
	weightMarker    = 4
	weightExtension = 2
	weightOwnsHit   = 1
	// maxOwnsBonus caps the "this team's territory exists" bonus. A team owning
	// twelve globs must not out-score a better-matched team by breadth alone —
	// that rewards writing more globs rather than writing the right ones.
	maxOwnsBonus = 3
	// defaultMinScore is the qualifying bar. One piece of evidence is enough:
	// a repo with a go.mod and a query that never says "Go" still has a Go half.
	defaultMinScore = 1
	// defaultMaxTeams bounds a selection. Past a handful, teams stop being
	// parallel halves and start being a partition nobody asked for — and every
	// extra team is another acceptance command run on every wave.
	defaultMaxTeams = 4
)

// Signals is everything known about a request before any model call.
type Signals struct {
	// Query is the user's request, verbatim.
	Query string
	// Files is the workspace inventory: repo-relative paths that exist now.
	Files []string
}

// Evidence is why one team scored what it scored.
//
// Reasons are carried, not just the number, because a preselection the user
// disagrees with is unfixable otherwise: "frontend was chosen" tells them
// nothing, "frontend: web/package.json, .tsx files, 'dashboard'" tells them
// exactly which of the three to edit.
type Evidence struct {
	TeamID  string   `json:"team_id"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
	// Selected is false for a team that qualified on evidence and then lost an
	// ownership conflict, or fell outside the cap.
	Selected bool `json:"selected"`
	// Conflict names the team that took a path this one also claimed.
	Conflict string `json:"conflict,omitempty"`
	// Pinned marks a team the user chose by hand rather than one that scored.
	Pinned bool `json:"pinned,omitempty"`
}

// Selection is the outcome of preselecting teams for a request.
type Selection struct {
	// Teams are the chosen teams in rank order (pinned first, then by score).
	Teams []Team
	// Evidence covers every team that scored at all, selected or not.
	Evidence []Evidence
}

// IDs lists the selected team ids in rank order.
func (s Selection) IDs() []string {
	out := make([]string, 0, len(s.Teams))
	for _, t := range s.Teams {
		out = append(out, t.ID)
	}
	return out
}

// Enabled reports whether this selection is worth running as teams at all.
//
// One team is the normal single-stream pipeline wearing a hat: it pays the
// contract and integration overhead and buys no parallelism. The bar is the
// same one squads.Plan.Enabled applies, checked here so the caller can skip the
// whole path before building a plan.
func (s Selection) Enabled() bool { return len(s.Teams) >= 2 }

// Options tune a selection.
type Options struct {
	// Pinned are team ids the user chose explicitly. They are selected
	// regardless of evidence and keep the order given — an explicit choice is
	// not a hypothesis to be scored.
	Pinned []string
	// Max caps the number of selected teams (0 → defaultMaxTeams).
	Max int
	// Min is the qualifying score (0 → defaultMinScore).
	Min int
}

// Select preselects the teams a request involves.
//
// Deterministic for a given roster, signals and options: ties break on team id,
// and no map iteration reaches the result.
func Select(roster []Team, sig Signals, opts Options) Selection {
	maxTeams := opts.Max
	if maxTeams <= 0 {
		maxTeams = defaultMaxTeams
	}
	// The cap exists to stop AUTOMATIC selection running away. A user who
	// listed five teams by hand has already made that call, and silently
	// dropping the fifth would be the worst kind of no-op: the page says five,
	// the run has four, and nothing says which one went.
	if len(opts.Pinned) > maxTeams {
		maxTeams = len(opts.Pinned)
	}
	minScore := opts.Min
	if minScore <= 0 {
		minScore = defaultMinScore
	}

	byID := make(map[string]Team, len(roster))
	for _, t := range roster {
		t.Normalize()
		if t.ID == "" {
			continue
		}
		byID[t.ID] = t
	}

	lowerQuery := strings.ToLower(sig.Query)
	inv := newInventory(sig.Files)

	pinned := map[string]bool{}
	var out Selection
	var accepted []Team

	// accept adds a team when its territory is free, and records why not when
	// it is not. Overlap is resolved HERE rather than by squads.Validate for a
	// reason: Validate can only reject the whole plan, and a library that holds
	// both `backend-go` and `backend-node` — as any real one will — would then
	// make every mixed repository fall back to a single stream. Dropping the
	// weaker claimant keeps the run parallel and says what it dropped.
	accept := func(t Team, ev *Evidence) {
		if len(accepted) >= maxTeams {
			return
		}
		for _, a := range accepted {
			if ga, gb, ok := overlap(a, t); ok {
				ev.Conflict = a.ID
				ev.Reasons = append(ev.Reasons, fmt.Sprintf(
					"not selected: %q already claims %s (this team claims %s)", a.ID, ga, gb))
				return
			}
		}
		accepted = append(accepted, t)
		ev.Selected = true
	}

	// Pinned first: an explicit choice outranks every scored one, and takes the
	// contested paths when it collides with one.
	for _, id := range opts.Pinned {
		id = slug(id)
		t, ok := byID[id]
		if !ok || pinned[id] {
			continue
		}
		pinned[id] = true
		ev := Evidence{TeamID: id, Pinned: true, Reasons: []string{"selected by hand"}}
		accept(t, &ev)
		out.Evidence = append(out.Evidence, ev)
	}

	scored := make([]Evidence, 0, len(byID))
	seen := make(map[string]bool, len(byID))
	for _, t := range roster {
		t.Normalize()
		// A roster with two entries under one id would otherwise score the same
		// team twice and report the second as losing a territory conflict with
		// ITSELF — a message nobody could act on.
		if t.ID == "" || pinned[t.ID] || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		// A negative priority opts a team out of automatic selection while
		// leaving it pinnable — the escape hatch for a team whose applicability
		// only its author can judge.
		if t.Match.Priority < 0 || t.Match.Empty() {
			continue
		}
		score, reasons := score(t, lowerQuery, inv)
		if score < minScore {
			continue
		}
		scored = append(scored, Evidence{TeamID: t.ID, Score: score + t.Match.Priority, Reasons: reasons})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].TeamID < scored[j].TeamID
	})

	for i := range scored {
		accept(byID[scored[i].TeamID], &scored[i])
		out.Evidence = append(out.Evidence, scored[i])
	}
	out.Teams = accepted
	return out
}

// score rates one team against the evidence, returning the reasons alongside.
func score(t Team, lowerQuery string, inv inventory) (int, []string) {
	total := 0
	var reasons []string

	var hits []string
	for _, kw := range t.Match.Keywords {
		if containsWord(lowerQuery, kw) {
			hits = append(hits, kw)
		}
	}
	if len(hits) > 0 {
		total += weightKeyword * len(hits)
		reasons = append(reasons, "query mentions "+quoteList(hits))
	}

	hits = nil
	for _, marker := range t.Match.Files {
		if inv.hasPath(marker) {
			hits = append(hits, marker)
		}
	}
	if len(hits) > 0 {
		total += weightMarker * len(hits)
		reasons = append(reasons, "workspace has "+quoteList(hits))
	}

	hits = nil
	for _, ext := range t.Match.Extensions {
		if inv.hasExt(ext) {
			hits = append(hits, ext)
		}
	}
	if len(hits) > 0 {
		total += weightExtension * len(hits)
		reasons = append(reasons, "workspace contains "+quoteList(hits)+" files")
	}

	// Territory that already exists is weak corroboration — it says the half is
	// real, not that this request touches it. Capped so breadth cannot win.
	owns := 0
	for _, g := range t.Owns {
		if inv.hasPath(g) {
			owns++
		}
	}
	if owns > 0 {
		bonus := owns * weightOwnsHit
		if bonus > maxOwnsBonus {
			bonus = maxOwnsBonus
		}
		total += bonus
		reasons = append(reasons, fmt.Sprintf("owns %d existing path(s)", owns))
	}
	return total, reasons
}

// overlap reports the first pair of globs by which two teams claim one path.
func overlap(a, b Team) (string, string, bool) {
	for _, ga := range a.Owns {
		for _, gb := range b.Owns {
			if squads.GlobsIntersect(ga, gb) {
				return ga, gb, true
			}
		}
	}
	return "", "", false
}

// ── Composing the selection into a runnable plan ─────────────────────────

// Compose renders a selection as the squad plan the harness executes.
//
// It deliberately leaves the CONTRACT empty. The interfaces between two teams
// describe THIS request's seam — a route, a signature, a file format — and are
// the one part of an org chart that genuinely cannot be stored in a library
// because they are different every time. Filling them is the model's job (see
// orchestrator.contractFor), and a plan without them still runs: it warns, it
// does not fail.
func Compose(sel Selection, summary string) squads.Plan {
	p := squads.Plan{Summary: strings.TrimSpace(summary)}
	for _, t := range sel.Teams {
		p.Squads = append(p.Squads, t.Squad())
	}
	p.Normalize()
	return p
}

// StaffCheck drops staffing this harness cannot dispatch.
//
// A library outlives the agent registry that was installed when it was written:
// a team naming `go-worker` on a machine where the Go pack was never installed
// produces tasks that never start, and the symptom — a team that is simply
// idle — looks nothing like the cause. Clearing the field falls back to the
// pipeline default, which always exists.
//
// Returns the notes to surface, one per cleared field, so the drop is visible
// rather than silent.
func StaffCheck(p *squads.Plan, registered func(string) bool) []string {
	if p == nil || registered == nil {
		return nil
	}
	var notes []string
	for i := range p.Squads {
		s := &p.Squads[i]
		for _, f := range []struct {
			role string
			ptr  *string
		}{
			{"worker", &s.Worker},
			{"reviewer", &s.Reviewer},
			{"tester", &s.Tester},
			{"manager", &s.Manager},
		} {
			id := strings.TrimSpace(*f.ptr)
			if id == "" || registered(id) {
				continue
			}
			notes = append(notes, fmt.Sprintf(
				"team %s: %s %q is not a registered agent — using the pipeline default", s.ID, f.role, id))
			*f.ptr = ""
		}
		// The open roster gets the same treatment, one entry at a time: a member
		// nothing can dispatch is a name on a team that never does any work, and
		// the symptom — an agent the manager offers and the loop then refuses —
		// costs a full model call to discover.
		if len(s.Agents) > 0 {
			kept := make([]string, 0, len(s.Agents))
			for _, id := range s.Agents {
				if id = strings.TrimSpace(id); id == "" {
					continue
				}
				if !registered(id) {
					notes = append(notes, fmt.Sprintf(
						"team %s: roster member %q is not a registered agent — dropped", s.ID, id))
					continue
				}
				kept = append(kept, id)
			}
			s.Agents = kept
		}
	}
	return notes
}

// ── Inventory + text helpers ─────────────────────────────────────────────

type inventory struct {
	paths []string
	exts  map[string]bool
}

func newInventory(files []string) inventory {
	inv := inventory{exts: map[string]bool{}}
	for _, f := range files {
		rel := normalizePath(f)
		if rel == "" {
			continue
		}
		inv.paths = append(inv.paths, rel)
		if i := strings.LastIndexByte(rel, '.'); i > 0 && !strings.ContainsRune(rel[i:], '/') {
			inv.exts[strings.ToLower(rel[i:])] = true
		}
	}
	return inv
}

func (inv inventory) hasPath(glob string) bool {
	for _, p := range inv.paths {
		if matchPath(glob, p) {
			return true
		}
	}
	return false
}

func (inv inventory) hasExt(ext string) bool { return inv.exts[strings.ToLower(ext)] }

// containsWord matches a keyword on word boundaries.
//
// Substring matching turns "api" into a hit on "rapids" and "go" into a hit on
// almost every English sentence — and a preselection that fires on noise is
// worse than none, because the user now has to notice and undo it.
func containsWord(haystack, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	// A multi-word keyword ("dark mode") is matched as a phrase.
	from := 0
	for {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		if boundary(haystack, i-1) && boundary(haystack, i+len(needle)) {
			return true
		}
		from = i + 1
		if from >= len(haystack) {
			return false
		}
	}
}

func boundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	return (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9')
}

func quoteList(in []string) string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, `"`+s+`"`)
	}
	return strings.Join(out, ", ")
}

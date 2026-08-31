package plan

import (
	"sort"
	"strings"
)

// ── Per-task specialist routing ──────────────────────────────────────────
//
// The composer picks ONE language specialist for a run, from the project's
// detected language and the query (see orchestrator/langpick.go). That is the
// right answer for a single-language repository and the wrong one for every
// repository this harness now targets: in a Go API with a React SPA, a run-level
// `go-worker` is wrong for every task under web/, and a run-level `react-worker`
// is wrong for every task under cmd/.
//
// Routing therefore has to happen PER TASK, and the task's own files are the
// strongest evidence available — stronger than the run-level pick, for the same
// reason langpick.go gives for preferring the repository over the query: a file
// extension is a fact, a run-level default is a summary.
//
// What routing never does is override a choice somebody already made
// deliberately. A task the splitter, the manager or a human assigned to a named
// specialist keeps it.

// RoutePolicy is what routing may choose from.
type RoutePolicy struct {
	// Available reports whether an agent id is registered. Naming an agent that
	// does not exist fails to dispatch, which is worse than a slightly less apt
	// specialist doing the work, so every candidate is checked.
	Available func(string) bool
	// SquadWorker / SquadReviewer / SquadTester return a squad's preferred
	// agent for that seat, or "".
	//
	// All three exist for the same reason: every one of them is editable on the
	// approval card and on the Teams page, and a seat the UI offers and routing
	// ignores is worse than one the UI never showed — the user sets it, nothing
	// changes, and nothing says why.
	SquadWorker   func(squadID string) string
	SquadReviewer func(squadID string) string
	SquadTester   func(squadID string) string
	// DefaultWorker is the run-level pick, used when a task's files say nothing.
	DefaultWorker string
	// DefaultReviewer / DefaultTester are the run-level equivalents.
	DefaultReviewer string
	DefaultTester   string
}

func (p RoutePolicy) has(id string) bool {
	if id == "" {
		return false
	}
	if p.Available == nil {
		return false
	}
	return p.Available(id)
}

// Routing is one task's resolved staffing, with the reason for each choice so
// the decision is auditable in the event stream rather than mysterious.
type Routing struct {
	Role     string
	Reason   string
	Reviewer string
	Tester   string
	// Changed is true when Role differs from the task's incoming role.
	Changed bool
}

// isImplementerRole reports whether a role writes code, and is therefore worth
// routing to a language specialist. Planning, exploration and scoping are prose
// and gain nothing from one.
//
// The language packs register their roles as `<lang>-worker` /
// `<lang>-corrector`, so the suffix — not the whole id — is what identifies the
// family. Matching only the bare ids classified `go-worker` as a NON-implementer,
// which made the explicit-specialist rung below unreachable: every named
// specialist fell into the "leave it alone" branch and got there by accident
// rather than by the rule that was supposed to protect it.
func isImplementerRole(role string) bool { return IsImplementerRole(role) }

// IsTesterRole reports whether role names a verification agent.
//
// Suffix-aware for the same reason IsImplementerRole is: per-task routing puts
// `go-tester` / `python-tester` on a verification task whenever a language pack
// is active, which is most runs. Every exact-id check then stops recognizing it
// — and the consequences are not cosmetic. The worst was the finish contract: a
// tester handed the WORKER contract answers {"status":"done","files_changed":…}
// while the gate parses for {"passed":…,"failures":…}, so a passing
// verification reads as a malformed one and the run rewrites a plan that was
// fine.
// EscalationSuffix separates a base role id from its rung number.
//
// '@' is deliberate: it appears in no built-in or custom role id, and — unlike
// '-' — it cannot collide with a legitimately hyphenated name such as
// reviewer-strict or go-tester.
//
// It lives here, below agents, because the role PREDICATES live here: a
// predicate that did not strip the rung was wrong for every escalated task, and
// leaving each caller to remember to strip it first is how the bug keeps coming
// back. agents.EscalationSuffix aliases this.
const EscalationSuffix = "@esc"

// baseRoleID drops a trailing escalation rung: "go-worker@esc2" → "go-worker".
//
// A rung must be a positive integer, so a role that legitimately contains the
// separator keeps its whole name.
func baseRoleID(id string) string {
	i := strings.LastIndex(id, EscalationSuffix)
	if i < 0 {
		return id
	}
	rung := id[i+len(EscalationSuffix):]
	if rung == "" {
		return id
	}
	for _, c := range rung {
		if c < '0' || c > '9' {
			return id
		}
	}
	if strings.TrimLeft(rung, "0") == "" {
		return id
	}
	return id[:i]
}

func IsTesterRole(role string) bool {
	role = baseRoleID(strings.ToLower(strings.TrimSpace(role)))
	if role == RoleTester {
		return true
	}
	if i := strings.LastIndex(role, "-"); i >= 0 {
		return role[i+1:] == RoleTester
	}
	return false
}

// IsImplementerRole reports whether role names an agent that WRITES code and
// may therefore be re-routed to a language specialist.
//
// It matches the suffix, not just the bare id. Every private copy of this
// predicate that checked only `worker` and `corrector` has had the same bug:
// once per-task routing puts `go-worker` on a task, a check for the bare id
// says that task has no implementer — and whatever the check was gating
// silently stops happening for every task on a squad run, which is all of them.
func IsImplementerRole(role string) bool {
	role = baseRoleID(strings.ToLower(strings.TrimSpace(role)))
	switch role {
	case "", RoleWorker, RoleCorrector:
		return true
	}
	if i := strings.LastIndex(role, "-"); i >= 0 {
		switch role[i+1:] {
		case RoleWorker, RoleCorrector:
			return true
		}
	}
	return false
}

// RouteTask resolves which specialist should execute a task.
//
// Precedence, each rung earning its place over the one below it:
//
//  1. a specialist the task ALREADY names, when it is registered — somebody
//     chose it on purpose and routing is not entitled to second-guess them;
//  2. a non-implementer role — a tester or reviewer task is not a worker task
//     and must not be turned into one;
//  3. the LANGUAGE OF ITS OWN FILES — the per-task adaptation this exists for;
//  4. its SQUAD's preferred worker — the manager's choice for that half;
//  5. the run-level default;
//  6. the generic worker.
//
// Rungs 3 and 4 are in that order deliberately. When a squad says `go-worker`
// and the task's files are all `.tsx`, the files are right and the squad label
// is stale — the same reasoning langpick.go uses to prefer the repository over
// a word in the query.
func RouteTask(t Task, p RoutePolicy) Routing {
	incoming := strings.ToLower(strings.TrimSpace(t.Role))

	// 1 + 2: an explicit, registered specialist, or a non-implementer role.
	if !isImplementerRole(incoming) {
		return Routing{
			Role:     t.Role,
			Reason:   "kept: " + incoming + " is not an implementer role",
			Reviewer: p.reviewerFor(t, ""),
			Tester:   p.testerFor(t, ""),
		}
	}
	if incoming != "" && incoming != RoleWorker && p.has(incoming) {
		lang := LanguageOf(t.Files)
		return Routing{
			Role:     t.Role,
			Reason:   "kept: task already names the registered specialist " + incoming,
			Reviewer: p.reviewerFor(t, lang),
			Tester:   p.testerFor(t, lang),
		}
	}

	lang := LanguageOf(t.Files)
	choose := func(id, reason string) Routing {
		return Routing{
			Role:     id,
			Reason:   reason,
			Reviewer: p.reviewerFor(t, lang),
			Tester:   p.testerFor(t, lang),
			Changed:  !strings.EqualFold(id, t.Role),
		}
	}

	// 3: the language of the files this task actually touches.
	if lang != "" {
		if id := lang + "-worker"; p.has(id) {
			return choose(id, "files are "+lang)
		}
	}
	// 4: the squad's own preferred worker.
	if t.Squad != "" && p.SquadWorker != nil {
		if id := strings.TrimSpace(p.SquadWorker(t.Squad)); p.has(id) {
			return choose(id, "squad "+t.Squad+" staffs "+id)
		}
	}
	// 5: the run-level pick.
	if p.has(p.DefaultWorker) {
		return choose(p.DefaultWorker, "run default")
	}
	// 6.
	return choose(RoleWorker, "no specialist registered for this task")
}

// reviewerFor picks a language-matched reviewer when one exists.
//
// A reviewer judging TypeScript with a Go reviewer's prompt reads the diff for
// the wrong hazards — that is the same mismatch as the worker's, one step later
// and harder to notice because the verdict still looks like a verdict.
func (p RoutePolicy) reviewerFor(t Task, lang string) string {
	if lang == "" {
		lang = LanguageOf(t.Files)
	}
	if lang != "" {
		if id := lang + "-reviewer"; p.has(id) {
			return id
		}
	}
	// The squad's own choice, one rung below the language of the files — the
	// same order the worker rungs use, and for the same reason: a squad label
	// can be stale about a file, an extension cannot.
	if id := p.squadSeat(t, p.SquadReviewer); id != "" {
		return id
	}
	if p.has(p.DefaultReviewer) {
		return p.DefaultReviewer
	}
	return ""
}

// squadSeat resolves one of the squad staffing lookups, checking that whatever
// it names can actually be dispatched.
func (p RoutePolicy) squadSeat(t Task, lookup func(string) string) string {
	if t.Squad == "" || lookup == nil {
		return ""
	}
	id := strings.TrimSpace(lookup(t.Squad))
	if !p.has(id) {
		return ""
	}
	return id
}

// testerFor picks a language-matched tester when one exists.
func (p RoutePolicy) testerFor(t Task, lang string) string {
	if lang == "" {
		lang = LanguageOf(t.Files)
	}
	if lang != "" {
		if id := lang + "-tester"; p.has(id) {
			return id
		}
	}
	if id := p.squadSeat(t, p.SquadTester); id != "" {
		return id
	}
	if p.has(p.DefaultTester) {
		return p.DefaultTester
	}
	return ""
}

// LanguageOf returns the language-pack prefix these files belong to, or "".
//
// Majority wins, so a task touching four .tsx files and one .go file routes to
// the frontend specialist rather than to whichever path was listed first. Ties
// break by name so the same task always routes the same way — a router whose
// answer depends on map iteration produces a different team on every run.
func LanguageOf(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		if l := langOf(f); l != "" {
			counts[l]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	langs := make([]string, 0, len(counts))
	for l := range counts {
		langs = append(langs, l)
	}
	sort.Slice(langs, func(i, j int) bool {
		if counts[langs[i]] != counts[langs[j]] {
			return counts[langs[i]] > counts[langs[j]]
		}
		return langs[i] < langs[j]
	})
	return langs[0]
}

// RouteBoard routes every task on a board and returns a per-role tally for the
// event stream.
//
// Mutates Role in place. Reviewer and tester picks are returned rather than
// stamped: they are per-wave decisions the loop makes when it dispatches, and
// writing them onto the task would freeze a choice that the corrector ladder
// legitimately varies.
func RouteBoard(tasks []Task, p RoutePolicy) (map[string]int, map[string]Routing) {
	tally := map[string]int{}
	byTask := make(map[string]Routing, len(tasks))
	for i := range tasks {
		r := RouteTask(tasks[i], p)
		if r.Role != "" {
			tasks[i].Role = r.Role
		}
		byTask[tasks[i].ID] = r
		tally[r.Role]++
	}
	return tally, byTask
}

// TallyLine renders a routing tally as one deterministic event line.
func TallyLine(tally map[string]int) string {
	if len(tally) == 0 {
		return "no tasks routed"
	}
	roles := make([]string, 0, len(tally))
	for r := range tally {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	parts := make([]string, 0, len(roles))
	for _, r := range roles {
		parts = append(parts, r+"="+itoa(tally[r]))
	}
	return strings.Join(parts, " ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

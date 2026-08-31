package squads

import "strings"

// ── Who answers for a team ───────────────────────────────────────────────
//
// A rejected delivery raises one question: who takes it next, and what do they
// need to know that the last attempt did not. That is a staffing decision, and
// staffing decisions belong to somebody who knows the people.
//
// Run-wide, there is nobody who does. The manager sees a flat roster of every
// implementer the factory registered and a task id, and has to guess which of
// them is the team that owns the file. On a two-squad run it guesses wrong in
// the obvious way: a failing Go handler goes to whoever sounds most competent,
// which is as likely to be the React half's worker as the Go one.
//
// A squad knows its own people. These helpers are how the triage step asks it.

// Staffing is one squad's people, as far as reassignment is concerned.
type Staffing struct {
	// Squad is the id these people belong to; "" when the task is unassigned.
	Squad string
	// Manager triages this squad's rejected work. "" falls back to the run's
	// default manager.
	Manager string
	// Members are the agent ids this squad is staffed with, most-specific
	// first: its named seats (worker, reviewer, tester), then the rest of the
	// roster its author put on it, in the order they wrote them.
	Members []string
}

// StaffingFor reports who answers for a squad.
//
// An unknown id is not an error: tasks routed before the squads were assembled,
// and tasks in a single-stream run, legitimately carry no squad. The zero value
// means "the run's defaults decide", which is what the caller wants.
func StaffingFor(p *Plan, squadID string) Staffing {
	squadID = slug(squadID)
	if p == nil || squadID == "" {
		return Staffing{}
	}
	for _, s := range p.Squads {
		if s.ID != squadID {
			continue
		}
		out := Staffing{Squad: s.ID, Manager: strings.TrimSpace(s.Manager)}
		// Named seats first — they are the most specific statement about who
		// does what — then the open roster, in the order its author wrote it.
		roster := append([]string{s.Worker, s.Reviewer, s.Tester}, s.Agents...)
		for _, m := range roster {
			if m = strings.TrimSpace(m); m != "" && !containsFold(out.Members, m) {
				out.Members = append(out.Members, m)
			}
		}
		return out
	}
	return Staffing{}
}

// Colleagues orders a roster so the task's own team comes first.
//
// It never REMOVES anybody. A squad's worker is the obvious first choice for
// that squad's failing work, but it is not always the right one — the whole
// reason the delivery was rejected may be that this team lacks the skill the
// fix needs, and a manager forbidden from looking outside its own team could
// only pick between agents that have already failed.
//
// So this is an ordering, not a filter: in-team staff first, in the order the
// squad lists them, then everybody else in their original order. What the
// manager sees first is its own people; what it can still reach is everyone.
func Colleagues(p *Plan, squadID string, roster []string) []string {
	staff := StaffingFor(p, squadID)
	if len(staff.Members) == 0 || len(roster) == 0 {
		return roster
	}
	out := make([]string, 0, len(roster))
	taken := make(map[string]bool, len(roster))
	for _, m := range staff.Members {
		for _, id := range roster {
			if strings.EqualFold(id, m) && !taken[id] {
				taken[id] = true
				out = append(out, id)
			}
		}
	}
	for _, id := range roster {
		if !taken[id] {
			out = append(out, id)
		}
	}
	return out
}

func containsFold(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}

// LaneOf reports the single squad that owns every one of these paths.
//
// It answers a narrower question than Assign: not "who should do this work" but
// "is this defect entirely inside one team's territory". The answer is "" the
// moment the paths straddle two teams or land somewhere nobody owns, because a
// defect on the seam belongs to both halves and one that lands nowhere belongs
// to whoever the board says.
//
// # WHY THIS EXISTS
//
// A tester failure names files. The reopen heuristics that decide which tasks
// to reopen from those files are text matches — acceptance snippets, basenames,
// task ids in the failure blob — and text matches leak across teams. The
// frozen contract makes it worse rather than better: it is attached as
// acceptance criteria to BOTH halves, so one clause of shared text is enough
// for a backend compile error to reopen the frontend's finished work.
//
// The frontend team then re-runs, fails at a defect it does not own and cannot
// see, and the run ends reporting "frontend 0/1 working" over a half that was
// correct and complete. That is the same failure the write deny list exists to
// prevent, arriving through the board instead of through the tool layer.
func LaneOf(p *Plan, paths []string) string {
	if p == nil || len(paths) == 0 {
		return ""
	}
	lane := ""
	for _, path := range paths {
		owner, ok := p.Owner(path)
		if !ok {
			// Unowned: it could belong to anybody, so it cannot narrow the
			// defect to one team.
			return ""
		}
		if lane == "" {
			lane = owner
			continue
		}
		if lane != owner {
			return ""
		}
	}
	return lane
}

// SeamOwner names who owes the interfaces an integration failure implicates,
// and which clauses those are.
//
// # WHY THIS EXISTS
//
// An integration failure is the most specific defect a two-team run can
// produce: every squad's own acceptance is green and the assembled application
// is still broken, which means the seam between them is wrong. That is the
// exact failure the frozen contract exists to prevent, and the one where a
// generic "the gate is red" ticket is least useful — nobody owns it, no
// specialist is implied, and the agent that picks it up has to rediscover which
// half is lying about its own interface.
//
// The provider owes the clause. A consumer built against text it was handed;
// the provider is the one that either implemented that text or drifted from it.
// When several teams provide interfaces, the failure output usually names one
// of their lanes, and that is the tiebreak. When it names none, the answer is
// "" and the ticket stays unassigned rather than landing on a guess.
func SeamOwner(p *Plan, output string) (squad string, clauses []string) {
	if p == nil || len(p.Contract.Interfaces) == 0 {
		return "", nil
	}
	byProvider := map[string][]string{}
	var providers []string
	for _, in := range p.Contract.Interfaces {
		id := strings.TrimSpace(in.Provider)
		if id == "" {
			continue
		}
		if _, seen := byProvider[id]; !seen {
			providers = append(providers, id)
		}
		byProvider[id] = append(byProvider[id], in.ID)
	}
	switch len(providers) {
	case 0:
		return "", nil
	case 1:
		return providers[0], byProvider[providers[0]]
	}
	// Several providers: the failure text decides, by naming a squad or a path
	// inside its lane.
	lower := strings.ToLower(output)
	for _, id := range providers {
		if strings.Contains(lower, strings.ToLower(id)) {
			return id, byProvider[id]
		}
	}
	for _, path := range PathsIn(output) {
		if owner, ok := p.Owner(path); ok {
			if clause, mine := byProvider[owner]; mine {
				return owner, clause
			}
		}
	}
	return "", nil
}

// PathsIn pulls repo-relative-looking paths out of command output.
//
// Deliberately conservative: a path is something with a slash and an extension,
// which is what a compiler, a bundler and a test runner all print. Over-matching
// here would put a ticket on files nobody touched.
func PathsIn(output string) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(output, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '"' || r == '\'' || r == '(' || r == ')' || r == '`'
	}) {
		field = strings.TrimRight(strings.Trim(field, ",;"), ":")
		// Trim a trailing `:12:34` line/column suffix.
		for {
			trimmed := trimLineRef(field)
			if trimmed == field {
				break
			}
			field = trimmed
		}
		if !strings.Contains(field, "/") || !strings.Contains(filepathBase(field), ".") {
			continue
		}
		if strings.HasPrefix(field, "/") || strings.Contains(field, "://") || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// trimLineRef removes one trailing `:<digits>` from a path reference.
func trimLineRef(s string) string {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return s
	}
	for _, r := range s[i+1:] {
		if r < '0' || r > '9' {
			return s
		}
	}
	return s[:i]
}

func filepathBase(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

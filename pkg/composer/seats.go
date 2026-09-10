package composer

import "strings"

// ── Filling a team's seats from the pipeline ─────────────────────────────
//
// A team is written once and a pipeline is chosen per run, so the two do not
// have to agree: the pipeline has a test phase and the team names no tester;
// the team is a worker and a manager and nothing else. Routing already copes
// — a seat the team leaves empty falls to the run's default for that role —
// but silently, and a user who built a team without a tester and then watched
// "the tester" reject its work had no way to know who that was or why.
//
// FillSeats is the same fallback, written down: for each of the four seats,
// who will actually sit in it on this run and where they came from. It is
// what the composition, the run setup panel, the Teams page and the floor all
// show, so the answer is the same everywhere.

// Seat sources.
const (
	// SeatFromTeam is the team's own choice.
	SeatFromTeam = "team"
	// SeatFromPipeline is the pipeline's agent for that role — the execute
	// loop's worker or reviewer, or the test phase's agent.
	SeatFromPipeline = "pipeline"
	// SeatFromDefault is the harness default when neither named one.
	SeatFromDefault = "default"
)

// SeatFill is one seat, staffed.
type SeatFill struct {
	Role   string `json:"role"`
	Agent  string `json:"agent"`
	Source string `json:"source"`
}

// Borrowed reports whether this seat is filled from outside the team.
func (s SeatFill) Borrowed() bool { return s.Source != SeatFromTeam }

// SeatDefaults are the pipeline's agents for the three working seats.
type SeatDefaults struct {
	Worker   string
	Reviewer string
	Tester   string
}

// FillSeats staffs the four seats of a team.
//
// managerDefault says the manager given is the run default rather than the
// team's own (see agents.ResolveManager); it is reported as such rather than
// as a pipeline pick, because a manager is not a pipeline role.
func FillSeats(worker, reviewer, tester, manager string, managerDefault bool, d SeatDefaults) []SeatFill {
	pick := func(role, own, pipe, fallback string) SeatFill {
		if own = strings.TrimSpace(own); own != "" {
			return SeatFill{Role: role, Agent: own, Source: SeatFromTeam}
		}
		if pipe = strings.TrimSpace(pipe); pipe != "" {
			return SeatFill{Role: role, Agent: pipe, Source: SeatFromPipeline}
		}
		return SeatFill{Role: role, Agent: fallback, Source: SeatFromDefault}
	}
	out := []SeatFill{
		pick("worker", worker, d.Worker, "worker"),
		pick("reviewer", reviewer, d.Reviewer, "reviewer"),
		pick("tester", tester, d.Tester, "tester"),
	}
	m := SeatFill{Role: "manager", Agent: strings.TrimSpace(manager), Source: SeatFromTeam}
	if m.Agent == "" {
		m.Agent = "triage"
	}
	if managerDefault {
		m.Source = SeatFromDefault
	}
	return append(out, m)
}

// Gaps says, in words, which seats a team did not fill itself — one line per
// borrowed working seat, in the form the charter phase emits and the panels
// show. The manager is left out: every team has one by construction.
func Gaps(teamID string, seats []SeatFill) []string {
	var out []string
	for _, s := range seats {
		if s.Role == "manager" || !s.Borrowed() {
			continue
		}
		from := "the pipeline's"
		if s.Source == SeatFromDefault {
			from = "the default"
		}
		out = append(out, "team "+teamID+" names no "+s.Role+" — "+from+" "+s.Agent+" takes its "+s.Role+" seat")
	}
	return out
}

// TesterAgent finds the agent bound to the test phase of a composition, or "".
func (c Composition) TesterAgent() string {
	for _, p := range c.Phases {
		if p.ID == "test" && p.Enabled && p.When != "never" {
			return p.Agent
		}
	}
	return ""
}

// SeatDefaultsOf reads the pipeline seats a composition settled on.
func (c Composition) SeatDefaultsOf() SeatDefaults {
	return SeatDefaults{
		Worker:   c.Execute.DefaultRole,
		Reviewer: c.Execute.Reviewer,
		Tester:   c.TesterAgent(),
	}
}

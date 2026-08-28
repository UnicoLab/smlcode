package squads

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Status is one squad's live state, derived from the board.
type Status struct {
	ID       string
	Name     string
	Total    int
	Done     int
	Blocked  int
	InFlight int
	// Complete is true when the squad has work and all of it finished.
	Complete bool
	// Stuck is true when nothing is in flight and something is blocked — the
	// squad cannot make progress on its own.
	Stuck bool
}

func (s Status) String() string {
	state := "working"
	switch {
	case s.Total == 0:
		state = "idle"
	case s.Complete:
		state = "done"
	case s.Stuck:
		state = "stuck"
	}
	return fmt.Sprintf("%s %d/%d %s", s.ID, s.Done, s.Total, state)
}

// Progress computes each squad's state from the board, in plan order.
//
// This is the reporting a manager needs and a single-stream run never did: with
// two teams running at once, "12 of 20 tasks done" hides that one squad
// finished twenty minutes ago and the other has been blocked since.
func Progress(p *Plan, tasks []plan.Task) []Status {
	if p == nil {
		return nil
	}
	byID := make(map[string]*Status, len(p.Squads))
	out := make([]Status, 0, len(p.Squads))
	for _, s := range p.Squads {
		out = append(out, Status{ID: s.ID, Name: s.Name})
	}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	for _, t := range tasks {
		st, ok := byID[strings.TrimSpace(t.Squad)]
		if !ok {
			continue
		}
		st.Total++
		switch {
		case t.Column == plan.ColDone || t.Status == plan.StatusDone:
			st.Done++
		case t.Column == plan.ColBlocked || t.Status == plan.StatusFailed:
			st.Blocked++
		default:
			st.InFlight++
		}
	}
	for i := range out {
		s := &out[i]
		s.Complete = s.Total > 0 && s.Done == s.Total
		s.Stuck = s.Total > 0 && s.InFlight == 0 && s.Blocked > 0
	}
	return out
}

// Stall is a squad held up by another squad's unfinished obligation.
type Stall struct {
	// Squad is the waiting consumer.
	Squad string
	// Interface is the contract clause it needs.
	Interface string
	// Provider is the squad that owes it.
	Provider string
}

func (s Stall) String() string {
	return fmt.Sprintf("%s is waiting on %s to deliver %q", s.Squad, s.Provider, s.Interface)
}

// WaitingOn reports consumers stalled on an undelivered interface.
//
// This is the cross-team failure a single board cannot express and the reason
// a manager role exists at all. A consumer squad with no work left in flight,
// something blocked, and a provider that has not finished is not a task-level
// failure any reviewer can fix — retrying its tasks forever is the wrong
// response, and without this the board just spins.
//
// Deliberately narrow: a consumer that is still making progress is not
// stalled, however far behind its provider is.
func WaitingOn(p *Plan, tasks []plan.Task) []Stall {
	if p == nil || len(p.Contract.Interfaces) == 0 {
		return nil
	}
	state := map[string]Status{}
	for _, s := range Progress(p, tasks) {
		state[s.ID] = s
	}
	var out []Stall
	for _, in := range p.Contract.Interfaces {
		prov, ok := state[in.Provider]
		if !ok || prov.Complete {
			continue
		}
		for _, c := range in.Consumers {
			cons, ok := state[c]
			if !ok || cons.Complete || !cons.Stuck {
				continue
			}
			out = append(out, Stall{Squad: c, Interface: in.ID, Provider: in.Provider})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Squad != out[j].Squad {
			return out[i].Squad < out[j].Squad
		}
		return out[i].Interface < out[j].Interface
	})
	return out
}

// IntegrationGate is the manager's decision about whether the halves may be
// joined yet.
type IntegrationGate struct {
	// Ready is true when every squad with work has finished it.
	Ready bool
	// Reason explains a false Ready in one line.
	Reason string
	// Command is the integration acceptance to run when Ready.
	Command string
}

// ReadyForIntegration decides whether to run the join step.
//
// Requiring EVERY squad to be complete is the point: integration exists to
// catch the halves not fitting together, and running it against a half that is
// still being written produces failures about missing files rather than about
// the seam — noise that trains everyone to ignore the one gate that matters.
func ReadyForIntegration(p *Plan, tasks []plan.Task) IntegrationGate {
	if p == nil || len(p.Squads) == 0 {
		return IntegrationGate{Reason: "no squads"}
	}
	var pending []string
	worked := 0
	for _, s := range Progress(p, tasks) {
		if s.Total == 0 {
			continue
		}
		worked++
		if !s.Complete {
			pending = append(pending, fmt.Sprintf("%s (%d/%d)", s.ID, s.Done, s.Total))
		}
	}
	switch {
	case worked == 0:
		return IntegrationGate{Reason: "no squad has any work"}
	case len(pending) > 0:
		return IntegrationGate{Reason: "still building: " + strings.Join(pending, ", ")}
	}
	return IntegrationGate{Ready: true, Command: p.Integration.Acceptance}
}

// ProgressLine renders every squad's state as one event line.
func ProgressLine(p *Plan, tasks []plan.Task) string {
	st := Progress(p, tasks)
	if len(st) == 0 {
		return ""
	}
	parts := make([]string, 0, len(st))
	for _, s := range st {
		parts = append(parts, s.String())
	}
	return strings.Join(parts, " · ")
}

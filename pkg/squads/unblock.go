package squads

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// ── Letting the halves run at the same time ──────────────────────────────
//
// # WHY THIS EXISTS
//
// Freezing the interface contract has one purpose beyond agreement: it is what
// lets the two halves be built AT ONCE. The frontend does not need the API to
// exist, only its shape, and the shape is the thing that was frozen and handed
// to both sides as acceptance criteria.
//
// Planners write the dependency anyway. Measured live, with the contract frozen
// and attached:
//
//	task T1  squad=backend-go     files=[cmd/server/main.go]
//	task T2  squad=frontend-react files=[web/src/App.tsx]  after=T1
//
//	wave 1: T1(backend-go) — teams live: backend-go
//	wave 2: T2(frontend-react) — teams live: frontend-react
//
// Two tasks, disjoint files, different teams, run one after the other. Every
// mechanism worked: the org chart, the ownership fence, the frozen seam. The
// teams still took twice the wall clock they needed, on a local model where
// wall clock is the budget that runs out — and nothing reported it, because a
// serialized run and a parallel one produce the same board.
//
// The edge is dropped only where the contract actually replaced it: the task
// being waited ON belongs to a squad that PROVIDES an interface, and the task
// waiting belongs to one that CONSUMES it. That pairing is the contract's own
// statement that the seam between those two teams is specified. Anything else
// is a real dependency and stays.

// UnblockAcrossTeams removes dependencies that the frozen contract has already
// answered, and returns one line per removal.
//
// Four things it never touches, each because the wait is real:
//
//   - a dependency INSIDE one squad — ordinary ordering, nothing to do with the
//     seam;
//   - a dependency involving an unassigned task — the seam is integration and
//     genuinely comes after the halves it joins;
//   - a dependency between two squads with NO frozen interface between them —
//     nothing was agreed, so the consumer really would be guessing;
//   - anything at all when the contract is empty.
func UnblockAcrossTeams(p *Plan, tasks []plan.Task) []string {
	if p == nil || len(p.Squads) < 2 || len(tasks) == 0 || len(p.Contract.Interfaces) == 0 {
		return nil
	}
	squadOf := map[string]string{}
	for _, t := range tasks {
		squadOf[key(t.ID)] = strings.TrimSpace(t.Squad)
	}
	// specified[provider][consumer] is the interface that named the pair — the
	// contract's own statement that this seam is agreed.
	specified := map[string]map[string]string{}
	for _, iface := range p.Contract.Interfaces {
		prov := strings.TrimSpace(iface.Provider)
		if prov == "" {
			continue
		}
		if specified[prov] == nil {
			specified[prov] = map[string]string{}
		}
		for _, c := range iface.Consumers {
			c = strings.TrimSpace(c)
			if c == "" || c == prov {
				continue
			}
			if _, already := specified[prov][c]; !already {
				specified[prov][c] = strings.TrimSpace(iface.ID)
			}
		}
	}

	var dropped []string
	for i := range tasks {
		waiter := strings.TrimSpace(tasks[i].Squad)
		if waiter == "" || len(tasks[i].DependsOn) == 0 {
			continue
		}
		kept := make([]string, 0, len(tasks[i].DependsOn))
		for _, d := range tasks[i].DependsOn {
			provider := squadOf[key(d)]
			iface, agreed := "", false
			if provider != "" && provider != waiter {
				iface, agreed = specified[provider][waiter]
			}
			if !agreed {
				kept = append(kept, d)
				continue
			}
			name := iface
			if name == "" {
				name = "the frozen contract"
			}
			dropped = append(dropped, tasks[i].ID+" no longer waits on "+strings.TrimSpace(d)+
				" — "+waiter+" builds against "+name+", which "+provider+
				" froze, rather than against its code")
		}
		if len(kept) != len(tasks[i].DependsOn) {
			tasks[i].DependsOn = kept
		}
	}
	return dropped
}

// key normalizes a task id for comparison.
func key(id string) string { return strings.ToUpper(strings.TrimSpace(id)) }

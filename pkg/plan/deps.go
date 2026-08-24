package plan

import (
	"fmt"
	"sort"
	"strings"
)

// Dependency resolution for the kanban board.
//
// Readiness used to treat `blocked` as satisfying a dependency, on the theory
// that one failed locate task must not freeze the board. It did not freeze the
// board — it made the freeze invisible: the dependent ran anyway, on top of
// work that was never done, and its own failure read as a fresh bug rather than
// as the second casualty of the first one. Only `done` satisfies a dependency
// now, and the two shapes that rule would otherwise strand — a dependent whose
// upstream failed, and a dependency cycle — are moved to `blocked` here with
// the reason attached, so they surface to the escalation path instead of
// sitting in ready_to_dev forever while the stall detector spins.

// PropagateBlocked moves every ready_to_dev task that can never run into the
// blocked column, annotating it with why.
//
// Two causes, both terminal: an upstream dependency that ended blocked (a
// failure this task's work was going to be built on), and a dependency cycle
// (no execution order exists at all). It returns the IDs it moved, sorted, so a
// caller can log or escalate them; an empty result means the board was already
// consistent.
//
// Only ready_to_dev is touched. A task in to_scope/scoped is human-owned and a
// task in in_progress is mid-flight, and blocking either would be this function
// fighting whoever owns it; ready_to_dev is exactly the set readiness considers,
// which is the set that can be silently stranded.
func (b *Board) PropagateBlocked() []string {
	boardMu.Lock()
	defer boardMu.Unlock()
	return b.propagateBlockedLocked()
}

func (b *Board) propagateBlockedLocked() []string {
	idx, order := b.taskIndexLocked()

	// why[id] is set for every task THIS pass decides to block. Deciding first
	// and mutating afterwards keeps the fixpoint below reading one consistent
	// view of the board, and makes the whole pass a no-op when nothing changed.
	why := map[string]string{}

	// Cycles first. A cycle is the one shape the dependency fixpoint cannot
	// resolve on its own: every member waits for another member, so no member
	// is ever ready-with-satisfied-deps and no member is ever blocked either —
	// they simply stop being scheduled, with nothing anywhere saying so. Naming
	// them here also lets the fixpoint carry the blockage downstream to their
	// dependents like any other failure.
	for _, cycle := range b.dependencyCyclesLocked() {
		reason := fmt.Sprintf(
			"dependency cycle between %s — no execution order exists, so none of them can ever run. "+
				"Remove one depends_on link to break it.", strings.Join(cycle, ", "))
		for _, id := range cycle {
			if i, ok := idx[id]; ok && b.Tasks[i].Column == ColReadyToDev {
				why[id] = reason
			}
		}
	}

	// Transitive fixpoint: a task blocked because its upstream failed is itself
	// a failed upstream for whatever depends on it. Iterating to a fixpoint
	// rather than once is what makes a three-deep chain collapse in one call.
	// It terminates because every pass that sets `progress` also blocks at
	// least one previously unblocked task, and the board is finite.
	for progress := true; progress; {
		progress = false
		for _, id := range order {
			t := &b.Tasks[idx[id]]
			if t.Column != ColReadyToDev || why[id] != "" {
				continue
			}
			for _, dep := range t.DependsOn {
				j, known := idx[dep]
				// A dependency on an ID that is not on the board is left alone
				// on purpose: boards are assembled task by task, so a dangling
				// dep is far more often "not added yet" than "will never
				// exist", and blocking on it would fail tasks mid-construction.
				if !known {
					continue
				}
				if b.Tasks[j].Column != ColBlocked && why[dep] == "" {
					continue
				}
				why[id] = fmt.Sprintf(
					"dependency %s is blocked, so the work this task builds on was never completed. "+
						"Fix or re-scope %s first — running on top of a failed dependency is how one "+
						"failure becomes a wave of them.", dep, dep)
				progress = true
				break
			}
		}
	}

	moved := make([]string, 0, len(why))
	for _, id := range order { // order is sorted, so moved is deterministic
		reason, ok := why[id]
		if !ok {
			continue
		}
		t := b.Tasks[idx[id]]
		t.MoveTo(ColBlocked)
		t.Error = reason
		t.Notes = strings.TrimSpace(t.Notes + "\nBLOCKED: " + reason)
		b.Tasks[idx[id]] = t
		moved = append(moved, id)
	}
	return moved
}

// DependencyCycles reports every dependency cycle on the board, each as the
// sorted IDs of the tasks caught in it.
//
// The grouping is by mutual reachability, not by distinct loop: a graph can
// hold exponentially many distinct cycles but only ever N groups, and "these
// tasks can never be ordered relative to each other" is the fact an operator
// has to act on either way. A task that merely DEPENDS on a cycle is not
// listed — it is reached by the ordinary blocked-upstream propagation once the
// cycle's members are blocked, which gives it a message naming the neighbor it
// actually waits on.
//
// Both the members within a cycle and the cycles themselves come back sorted,
// so two calls on the same board report the same thing in the same order.
func (b *Board) DependencyCycles() [][]string {
	boardMu.RLock()
	defer boardMu.RUnlock()
	return b.dependencyCyclesLocked()
}

func (b *Board) dependencyCyclesLocked() [][]string {
	ids, deps := b.depGraphLocked()
	n := len(ids)

	// Transitive closure of depends_on, Floyd–Warshall over a bit matrix.
	//
	// A linear SCC algorithm would be asymptotically better, but a board is a
	// handful of tasks and this one has the property that actually matters
	// here: three counted loops, no recursion and no worklist, so the input
	// that motivated the check — a cycle — cannot make it spin.
	reach := make([][]bool, n)
	for i := range reach {
		reach[i] = make([]bool, n)
		for _, j := range deps[i] {
			reach[i][j] = true
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !reach[i][k] {
				continue
			}
			for j := 0; j < n; j++ {
				if reach[k][j] {
					reach[i][j] = true
				}
			}
		}
	}

	// A task in a cycle can reach itself; two tasks in the SAME cycle can reach
	// each other. ids is sorted, so scanning by index yields sorted members and
	// cycles ordered by their smallest member.
	var out [][]string
	grouped := make([]bool, n)
	for i := 0; i < n; i++ {
		if grouped[i] || !reach[i][i] {
			continue
		}
		var cycle []string
		for j := 0; j < n; j++ {
			if reach[i][j] && reach[j][i] {
				grouped[j] = true
				cycle = append(cycle, ids[j])
			}
		}
		out = append(out, cycle)
	}
	return out
}

// depGraphLocked flattens the board into a sorted ID list plus index-addressed
// adjacency. Edges to IDs that are not on the board are dropped: a dangling
// dependency is a stalled task, not a cycle, and the two want different
// answers.
func (b *Board) depGraphLocked() (ids []string, deps [][]int) {
	pos := make(map[string]int, len(b.Tasks))
	ids = make([]string, 0, len(b.Tasks))
	for _, t := range b.Tasks {
		if _, dup := pos[t.ID]; dup || t.ID == "" {
			continue
		}
		pos[t.ID] = 0
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	for i, id := range ids {
		pos[id] = i
	}
	deps = make([][]int, len(ids))
	seen := make(map[string]bool, len(b.Tasks))
	for _, t := range b.Tasks {
		if t.ID == "" || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		i := pos[t.ID]
		for _, d := range t.DependsOn {
			if j, ok := pos[d]; ok {
				deps[i] = append(deps[i], j)
			}
		}
		sort.Ints(deps[i])
	}
	return ids, deps
}

// taskIndexLocked maps task ID → slice position and returns the IDs sorted, so
// every traversal that decides scheduling walks the board in the same order
// regardless of how the tasks were appended.
func (b *Board) taskIndexLocked() (idx map[string]int, order []string) {
	idx = make(map[string]int, len(b.Tasks))
	order = make([]string, 0, len(b.Tasks))
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
		if _, dup := idx[b.Tasks[i].ID]; dup || b.Tasks[i].ID == "" {
			continue
		}
		idx[b.Tasks[i].ID] = i
		order = append(order, b.Tasks[i].ID)
	}
	sort.Strings(order)
	return idx, order
}

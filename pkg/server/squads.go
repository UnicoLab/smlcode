package server

import (
	"net/http"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// handleGetSquads serves the virtual-team org chart and its live progress.
//
// Progress is computed from the CURRENT board rather than stored, because a
// stored copy is wrong the moment a wave finishes: with two teams running, the
// number a watcher needs is "which half is behind", and that changes on every
// task. The plan itself is stable and comes from disk.
func (s *Server) handleGetSquads(w http.ResponseWriter, _ *http.Request) {
	p, ok, err := squads.Load(s.slmDir())
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if !ok || len(p.Squads) == 0 {
		// Not an error: the overwhelming majority of runs are single-stream and
		// have no org chart at all. The UI hides the panel on this.
		writeJSON(w, map[string]interface{}{"ok": false, "squads": nil})
		return
	}

	tasks := s.boardTasks()
	statuses := squads.Progress(&p, tasks)

	out := make([]map[string]interface{}, 0, len(p.Squads))
	byID := map[string]squads.Status{}
	for _, st := range statuses {
		byID[st.ID] = st
	}
	for _, sq := range p.Squads {
		st := byID[sq.ID]
		out = append(out, map[string]interface{}{
			"id":         sq.ID,
			"name":       sq.Name,
			"charter":    sq.Charter,
			"owns":       sq.Owns,
			"acceptance": sq.Acceptance,
			"worker":     sq.Worker,
			"reviewer":   sq.Reviewer,
			"total":      st.Total,
			"done":       st.Done,
			"blocked":    st.Blocked,
			"in_flight":  st.InFlight,
			"complete":   st.Complete,
			"stuck":      st.Stuck,
		})
	}

	interfaces := make([]map[string]interface{}, 0, len(p.Contract.Interfaces))
	for _, in := range p.Contract.Interfaces {
		interfaces = append(interfaces, map[string]interface{}{
			"id": in.ID, "provider": in.Provider, "consumers": in.Consumers, "spec": in.Spec,
		})
	}

	// A consumer blocked on an undelivered interface is not a task defect, and
	// the UI must be able to say so rather than showing it as a red task.
	stalls := make([]map[string]interface{}, 0)
	for _, st := range squads.WaitingOn(&p, tasks) {
		stalls = append(stalls, map[string]interface{}{
			"squad": st.Squad, "interface": st.Interface, "provider": st.Provider,
		})
	}

	gate := squads.ReadyForIntegration(&p, tasks)
	writeJSON(w, map[string]interface{}{
		"ok":         true,
		"summary":    p.Summary,
		"squads":     out,
		"interfaces": interfaces,
		"stalls":     stalls,
		"integration": map[string]interface{}{
			"acceptance": p.Integration.Acceptance,
			"notes":      p.Integration.Notes,
			"ready":      gate.Ready,
			"reason":     gate.Reason,
		},
	})
}

// boardTasks reads the live board, tolerating a run that has not built one.
func (s *Server) boardTasks() []plan.Task {
	o := s.orch()
	if o == nil {
		return nil
	}
	b := o.Board()
	if b == nil {
		return nil
	}
	snap := b.Snapshot()
	return snap.Tasks
}

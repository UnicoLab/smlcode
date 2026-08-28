package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
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
			"manager":    sq.Manager,
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
		"managers":   s.triageCapableAgents(),
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

// handlePatchSquads applies edits to the saved org chart outside a run.
//
// The approval gate can edit teams, but only while a plan is pending. A team
// structure outlives one approval — the ownership boundaries and the staffing
// are what the NEXT run inherits — so there has to be a way to fix them from
// the Teams page without waiting for a gate to open.
//
// Validation is the same as everywhere else and is not negotiable: an edit that
// makes two squads share a path is refused whole, and the saved plan is left
// exactly as it was. A half-applied org chart is worse than the one it
// replaced, because the user believes they fixed it.
func (s *Server) handlePatchSquads(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Squads       []plan.SquadEdit `json:"squads"`
		RemoveSquads []string         `json:"remove_squads"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Squads) == 0 && len(body.RemoveSquads) == 0 {
		http.Error(w, "no edits", http.StatusBadRequest)
		return
	}

	p, ok, err := squads.Load(s.slmDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no squad plan to edit", http.StatusNotFound)
		return
	}

	if probs := squads.ApplyEdits(&p, body.Squads, body.RemoveSquads); probs.Errors() {
		// 422, not 400: the request was well-formed and the harness understood
		// it — it is the resulting org chart that cannot be run, and the client
		// needs the reasons to show the user.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": false, "problems": probs.Strings(),
		})
		return
	}
	if err := squads.Save(s.slmDir(), p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "charter", Kind: "output",
		Message: "squad plan edited: " + p.Summarize(), Time: time.Now(),
	})
	writeJSON(w, map[string]interface{}{"ok": true, "summary": p.Summarize()})
}

// triageCapableAgents lists the agents a team may be given as its manager.
//
// Not every agent can be one. The decoding grammar for a request is derived
// from the agent's own system prompt, so an agent that does not answer the
// triage contract returns something the reassignment step cannot read — and it
// only finds that out after a full model call. Offering a choice the harness
// would then refuse is worse than offering a short list.
func (s *Server) triageCapableAgents() []string {
	return agents.AgentsEmitting(schema.RoleTriage, s.loadCustomAgents())
}

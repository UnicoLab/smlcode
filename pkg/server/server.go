package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// Server exposes the SLMCode Studio API + optional embedded web UI.
type Server struct {
	h       *harness.Harness
	mux     *http.ServeMux
	ui      fs.FS
	mu      sync.Mutex
	events  []orchestrator.Event
	lastRes *orchestrator.Result
	running bool
	subs    map[chan orchestrator.Event]struct{}
}

func New(h *harness.Harness, ui fs.FS) *Server {
	s := &Server{
		h:    h,
		mux:  http.NewServeMux(),
		ui:   ui,
		subs: map[chan orchestrator.Event]struct{}{},
	}
	s.wireOrchestratorEvents()
	s.routes()
	return s
}

// wireOrchestratorEvents keeps Studio SSE subscribed across config rebuilds.
func (s *Server) wireOrchestratorEvents() {
	if s.h == nil || s.h.Orchestrator == nil {
		return
	}
	s.h.Orchestrator.OnEvent(func(e orchestrator.Event) {
		s.emit(e)
	})
}

func (s *Server) emit(e orchestrator.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	if len(s.events) > 800 {
		s.events = s.events[len(s.events)-800:]
	}
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
			// Prefer latest progress over silent drops under backpressure.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- e:
			default:
			}
		}
	}
}

func (s *Server) Handler() http.Handler { return withCORS(s.mux) }

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	s.mux.HandleFunc("GET /api/docs", s.handleListDocs)
	s.mux.HandleFunc("GET /api/docs/{name}", s.handleGetDoc)
	s.mux.HandleFunc("PUT /api/docs/{name}", s.handlePutDoc)
	s.mux.HandleFunc("GET /api/tasks", s.handleGetTasks)
	s.mux.HandleFunc("PUT /api/tasks", s.handlePutTasks)
	s.mux.HandleFunc("POST /api/tasks", s.handleAddTask)
	s.mux.HandleFunc("PATCH /api/tasks/{id}", s.handlePatchTask)
	s.mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	s.mux.HandleFunc("GET /api/board", s.handleGetBoard)
	s.mux.HandleFunc("GET /api/columns", s.handleColumns)
	s.mux.HandleFunc("GET /api/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/skills/{name}", s.handleGetSkill)
	s.mux.HandleFunc("PUT /api/skills/{name}", s.handlePutSkill)
	s.mux.HandleFunc("POST /api/skills", s.handleCreateSkill)
	s.mux.HandleFunc("DELETE /api/skills/{name}", s.handleDeleteSkill)
	s.mux.HandleFunc("POST /api/runs", s.handleStartRun)
	s.mux.HandleFunc("POST /api/runs/stop", s.handleStopRun)
	s.mux.HandleFunc("GET /api/runs/latest", s.handleLatestRun)
	s.mux.HandleFunc("GET /api/clarify/pending", s.handleClarifyPending)
	s.mux.HandleFunc("POST /api/clarify/answer", s.handleClarifyAnswer)
	s.mux.HandleFunc("GET /api/plan/pending", s.handlePlanPending)
	s.mux.HandleFunc("POST /api/plan/approve", s.handlePlanApprove)
	s.mux.HandleFunc("GET /api/shell/pending", s.handleShellPending)
	s.mux.HandleFunc("POST /api/shell/approve", s.handleShellApprove)
	s.mux.HandleFunc("GET /api/rewind", s.handleRewindList)
	s.mux.HandleFunc("POST /api/rewind/{id}", s.handleRewindRestore)
	s.mux.HandleFunc("POST /api/compact", s.handleCompact)
	s.mux.HandleFunc("GET /api/events", s.handleSSE)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/models", s.handleModels)
	s.mux.HandleFunc("GET /api/agents", s.handleAgents)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	s.mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	s.mux.HandleFunc("PUT /api/agents/{id}", s.handlePutAgent)
	s.mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	s.mux.HandleFunc("GET /api/archives", s.handleListArchives)
	s.mux.HandleFunc("GET /api/archives/{name}", s.handleGetArchive)
	s.mux.HandleFunc("GET /api/queries", s.handleListQueries)
	s.mux.HandleFunc("GET /api/queries/{id}", s.handleGetQuery)

	if s.ui != nil {
		fileServer := http.FileServer(http.FS(s.ui))
		s.mux.Handle("GET /", spaHandler(fileServer))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	running := s.running
	nEvents := len(s.events)
	s.mu.Unlock()
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"api":      "ok",
		"ui":       "embedded",
		"provider": s.h.Config.Provider,
		"model":    s.h.Config.Model,
		"backend":  s.h.Config.Backend,
		"root":     s.h.Config.Root,
		"running":  running,
		"events":   nEvents,
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.h.Config.Public())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var patch config.Patch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c := s.h.Config
	c.ApplyPatch(patch)
	if err := c.Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Rebuild orchestrator with new settings (tools pick up permission/dry-run),
	// but never drop the Studio SSE fan-out — re-wire OnEvent after swap.
	orch, err := orchestrator.New(c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.h.Orchestrator = orch
	s.wireOrchestratorEvents()
	writeJSON(w, c.Public())
}

func (s *Server) handleListDocs(w http.ResponseWriter, r *http.Request) {
	names := []string{
		contextstore.DocProject, contextstore.DocContext, contextstore.DocQuery,
		contextstore.DocPlan, contextstore.DocTasks, contextstore.DocMemory,
		contextstore.DocSkills, contextstore.DocScratch,
	}
	writeJSON(w, names)
}

func (s *Server) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	body, err := s.h.Orchestrator.Store().Read(name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"name": name, "content": body})
}

func (s *Server) handlePutDoc(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.h.Orchestrator.Store().Write(name, body.Content); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	_ = s.h.Orchestrator.Board().Load()
	b := s.h.Orchestrator.Board().Snapshot()
	tasks := b.Tasks
	if tasks == nil {
		tasks = []plan.Task{}
	}
	byCol := b.ByColumn()
	for _, col := range plan.Columns() {
		if byCol[col] == nil {
			byCol[col] = []plan.Task{}
		}
	}
	writeJSON(w, map[string]interface{}{
		"plan":      b.Plan,
		"tasks":     tasks,
		"columns":   plan.Columns(),
		"by_column": byCol,
	})
}

func (s *Server) handleColumns(w http.ResponseWriter, r *http.Request) {
	type col struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	var out []col
	for _, c := range plan.Columns() {
		out = append(out, col{ID: c, Label: plan.ColumnLabel(c)})
	}
	writeJSON(w, out)
}

func (s *Server) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	_ = s.h.Orchestrator.Board().Load()
	writeJSON(w, s.h.Orchestrator.Board().Snapshot())
}

func (s *Server) handlePutTasks(w http.ResponseWriter, r *http.Request) {
	var board plan.Board
	if err := json.NewDecoder(r.Body).Decode(&board); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.h.Orchestrator.Board().Replace(board); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, board)
}

func (s *Server) handleAddTask(w http.ResponseWriter, r *http.Request) {
	var t plan.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	err := s.h.Orchestrator.Board().Update(func(b *plan.Board) error {
		if t.ID == "" {
			t.ID = b.NextID()
		}
		if t.Role == "" {
			t.Role = plan.RoleWorker
		}
		if t.Column == "" {
			t.Column = plan.ColToScope
		}
		t.Normalize()
		b.Tasks = append(b.Tasks, t)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, t)
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch plan.Task
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var out plan.Task
	err := s.h.Orchestrator.Board().Update(func(b *plan.Board) error {
		t, ok := b.Get(id)
		if !ok {
			return fmt.Errorf("not found")
		}
		if patch.Title != "" {
			t.Title = patch.Title
		}
		if patch.Description != "" {
			t.Description = patch.Description
		}
		if patch.Acceptance != "" {
			t.Acceptance = patch.Acceptance
		}
		if patch.Notes != "" {
			t.Notes = patch.Notes
		}
		if patch.Role != "" {
			t.Delegate(patch.Role)
		}
		if patch.Column != "" {
			t.MoveTo(patch.Column)
		}
		if patch.Files != nil {
			t.Files = patch.Files
		}
		if patch.Checklist != nil {
			t.Checklist = patch.Checklist
		}
		if patch.Priority > 0 {
			t.Priority = patch.Priority
		}
		t.Normalize()
		b.UpdateTask(t)
		out = t
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, out)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.h.Orchestrator.Board().Update(func(b *plan.Board) error {
		if !b.RemoveTask(id) {
			return fmt.Errorf("not found")
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.h.Orchestrator.Skills().List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		Path          string   `json:"path"`
		Triggers      []string `json:"triggers,omitempty"`
		Agents        []string `json:"agents,omitempty"`
		UserInvocable bool     `json:"user_invocable"`
	}
	out := make([]item, 0, len(list))
	for _, sk := range list {
		out = append(out, item{
			Name: sk.Name, Description: sk.Description, Path: sk.Path,
			Triggers: sk.Triggers, Agents: sk.Agents, UserInvocable: sk.UserInvocable,
		})
	}
	writeJSON(w, out)
}

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sk, ok := s.h.Orchestrator.Skills().Get(name)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, sk)
}

func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	var req skills.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name required", 400)
		return
	}
	if req.Body == "" {
		req = skills.Template(req.Name, strings.Join(req.Agents, ","))
	}
	path, err := skills.WriteSkill(s.h.Config.SkillsDir(), req)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sk, _ := skills.ParseFile(path)
	writeJSON(w, sk)
}

func (s *Server) handlePutSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req skills.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = name
	}
	path, err := skills.WriteSkill(s.h.Config.SkillsDir(), req)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sk, _ := skills.ParseFile(path)
	writeJSON(w, sk)
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := skills.DeleteSkill(s.h.Config.SkillsDir(), name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "deleted": name})
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query      string   `json:"query"`
		Mode       string   `json:"mode"`
		Specialist string   `json:"specialist"`
		Skills     []string `json:"skills"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query required", 400)
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		http.Error(w, "run already in progress", 409)
		return
	}
	s.running = true
	s.events = nil
	s.mu.Unlock()

	// Apply per-run engine/specialist/skill selection (restore after run)
	prevMode, prevSpec := s.h.Config.Mode, s.h.Config.Specialist
	prevPins := append([]string{}, s.h.Config.PinnedSkills...)
	if req.Mode != "" {
		s.h.Config.Mode = req.Mode
	}
	if req.Specialist != "" {
		s.h.Config.Specialist = req.Specialist
		s.h.Config.Mode = config.ModeSpecialist
	}
	query := req.Query
	if len(req.Skills) > 0 {
		pins := append([]string{}, prevPins...)
		for _, sk := range req.Skills {
			sk = strings.TrimSpace(sk)
			if sk == "" {
				continue
			}
			pins = append(pins, sk)
			if !strings.Contains(strings.ToLower(query), "@skill:"+strings.ToLower(sk)) {
				query += " @skill:" + sk
			}
		}
		s.h.Config.PinnedSkills = pins
	}

	// Ensure SSE stays wired for this run (config rebuilds call wireOrchestratorEvents too).
	s.wireOrchestratorEvents()
	s.emit(orchestrator.Event{
		Phase: "init", Kind: "run_start", Message: "run started", Time: time.Now(),
	})

	go func() {
		defer func() {
			s.h.Config.Mode = prevMode
			s.h.Config.Specialist = prevSpec
			s.h.Config.PinnedSkills = prevPins
		}()
		ctx := context.Background()
		res, err := s.h.Run(ctx, query)
		s.mu.Lock()
		s.running = false
		if res != nil {
			s.lastRes = res
		}
		s.mu.Unlock()
		phase := "done"
		msg := "finished"
		if err != nil {
			phase = "error"
			msg = err.Error()
		} else if res != nil {
			msg = res.Summary
		}
		s.emit(orchestrator.Event{Phase: phase, Kind: "run_end", Message: msg, Time: time.Now()})
	}()

	writeJSON(w, map[string]string{"status": "started", "query": req.Query})
}

func (s *Server) handleClarifyPending(w http.ResponseWriter, r *http.Request) {
	path := plan.ClarifyAskPath(s.h.Config.SlmDir())
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, map[string]any{"pending": false})
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	var ask plan.ScopeAsk
	if err := json.Unmarshal(data, &ask); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"pending": true, "ask": ask})
}

func (s *Server) handleClarifyAnswer(w http.ResponseWriter, r *http.Request) {
	var ans plan.ScopeAnswers
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, "invalid answers JSON", 400)
		return
	}
	if err := plan.WriteScopeAnswers(s.h.Config.SlmDir(), ans); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "clarify", Kind: "ask_answered", Message: "clarify answers saved", Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePlanPending(w http.ResponseWriter, r *http.Request) {
	var ask plan.PlanApproveAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "plan", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	writeJSON(w, map[string]any{"pending": true, "ask": ask})
}

func (s *Server) handlePlanApprove(w http.ResponseWriter, r *http.Request) {
	var ans plan.PlanApproveAnswer
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := hitl.WriteAnswers(s.h.Config.SlmDir(), "plan", ans); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "plan", Kind: "ask_answered", Message: "plan decision: " + ans.Decision, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleShellPending(w http.ResponseWriter, r *http.Request) {
	var ask workspace.ShellAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "shell", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	writeJSON(w, map[string]any{"pending": true, "ask": ask})
}

func (s *Server) handleShellApprove(w http.ResponseWriter, r *http.Request) {
	var ans workspace.ShellAnswer
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := hitl.WriteAnswers(s.h.Config.SlmDir(), "shell", ans); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "execute", Kind: "ask_answered", Message: "shell decision: " + ans.Decision, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRewindList(w http.ResponseWriter, r *http.Request) {
	mgr := &rewind.Manager{SlmDir: s.h.Config.SlmDir(), Root: s.h.Config.Root}
	list, err := mgr.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"snapshots": list})
}

func (s *Server) handleRewindRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mgr := &rewind.Manager{SlmDir: s.h.Config.SlmDir(), Root: s.h.Config.Root}
	n, err := mgr.Restore(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "execute", Kind: "output", Message: fmt.Sprintf("rewound %d files from %s", n, id), Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true, "restored": n, "id": id})
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	res, err := s.h.Orchestrator.CompactContextNow()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok": true, "compacted": res.Compacted,
		"before_bytes": res.BeforeBytes, "after_bytes": res.AfterBytes,
	})
}

func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	s.h.Orchestrator.Stop()
	s.mu.Lock()
	was := s.running
	s.running = false
	s.mu.Unlock()
	if was {
		s.emit(orchestrator.Event{
			Phase: "done", Kind: "run_stop", Message: "run stopped by user", Time: time.Now(),
		})
	}
	writeJSON(w, map[string]string{"status": "stopping"})
}

func (s *Server) handleLatestRun(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.events
	if events == nil {
		events = []orchestrator.Event{}
	}
	writeJSON(w, map[string]interface{}{
		"running": s.running,
		"result":  s.lastRes,
		"events":  events,
	})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan orchestrator.Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	replay := append([]orchestrator.Event(nil), s.events...)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
		close(ch)
	}()

	// Immediate hello so the UI can show "API connected" without waiting for a run.
	hello := orchestrator.Event{
		Phase: "idle", Kind: "connected", Message: "studio api connected", Time: time.Now(),
	}
	data, _ := json.Marshal(hello)
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", data)
	flusher.Flush()
	for _, e := range replay {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	notify := r.Context().Done()
	ticker := time.NewTicker(12 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			// Comment heartbeats keep proxies/browsers from idle-closing the stream.
			fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.h.Status()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"text": st})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.h.Config
	cfg.ResolveAPIKey()
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	url := endpoint + "/models"
	if config.IsOllama(cfg.Provider) {
		base := strings.TrimSuffix(endpoint, "/v1")
		url = strings.TrimRight(base, "/") + "/api/tags"
	} else if !strings.HasSuffix(endpoint, "/v1") {
		url = endpoint + "/v1/models"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, map[string]interface{}{"models": []string{cfg.Model}, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var names []string
	var payload map[string]interface{}
	if json.Unmarshal(body, &payload) == nil {
		if data, ok := payload["data"].([]interface{}); ok {
			for _, m := range data {
				if mm, ok := m.(map[string]interface{}); ok {
					if id, ok := mm["id"].(string); ok {
						names = append(names, id)
					}
				}
			}
		}
		if models, ok := payload["models"].([]interface{}); ok {
			for _, m := range models {
				if mm, ok := m.(map[string]interface{}); ok {
					if id, ok := mm["name"].(string); ok {
						names = append(names, id)
					} else if id, ok := mm["model"].(string); ok {
						names = append(names, id)
					}
				}
			}
		}
	}
	if len(names) == 0 {
		names = []string{cfg.Model}
	}
	writeJSON(w, map[string]interface{}{"models": names, "current": cfg.Model})
}

func (s *Server) agentDirs() []string {
	return append([]string{s.h.Config.AgentsDir()}, agents.GlobalAgentRoots()...)
}

func (s *Server) loadCustomAgents() []agents.CustomSpec {
	list, _ := agents.LoadCustomSpecs(s.agentDirs()...)
	return list
}

func (s *Server) rebuildOrchestrator() error {
	orch, err := orchestrator.New(s.h.Config)
	if err != nil {
		return err
	}
	s.h.Orchestrator = orch
	s.wireOrchestratorEvents()
	return nil
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, agents.PublicSpecsWithCustom(s.loadCustomAgents()))
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if a := agents.AgentDetail(id, s.loadCustomAgents()); a != nil {
		writeJSON(w, a)
		return
	}
	http.Error(w, "not found", 404)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req agents.CustomSpec
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	path, err := agents.WriteCustom(s.h.Config.AgentsDir(), req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	got, err := agents.ReadCustomFile(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "saved but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, got)
}

func (s *Server) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	var req agents.CustomSpec
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		req.ID = id
	}
	if strings.ToLower(req.ID) != id {
		http.Error(w, "id mismatch", 400)
		return
	}
	path, err := agents.WriteCustom(s.h.Config.AgentsDir(), req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	got, err := agents.ReadCustomFile(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "saved but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, got)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if err := agents.DeleteCustom(s.h.Config.AgentsDir(), id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "deleted but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "deleted": id})
}

func (s *Server) archivesDir() string {
	return filepath.Join(s.h.Config.SlmDir(), "archives")
}

func (s *Server) handleListArchives(w http.ResponseWriter, r *http.Request) {
	dir := s.archivesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified string `json:"modified"`
	}
	var out []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, item{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	if out == nil {
		out = []item{}
	}
	writeJSON(w, out)
}

func (s *Server) handleGetArchive(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	if name == "." || name == ".." || !strings.HasSuffix(strings.ToLower(name), ".md") {
		http.Error(w, "invalid archive name", 400)
		return
	}
	path := filepath.Join(s.archivesDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"name": name, "content": string(data)})
}

func (s *Server) handleListQueries(w http.ResponseWriter, r *http.Request) {
	dir := session.QueriesDir(s.h.Config.SlmDir())
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		ID        string `json:"id"`
		Query     string `json:"query"`
		Success   bool   `json:"success"`
		Summary   string `json:"summary,omitempty"`
		UpdatedAt string `json:"updated_at"`
	}
	var out []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := session.LoadTurn(s.h.Config.SlmDir(), e.Name())
		if err != nil {
			continue
		}
		out = append(out, item{
			ID: t.ID, Query: t.Query, Success: t.Success,
			Summary: t.Summary, UpdatedAt: t.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if out == nil {
		out = []item{}
	}
	writeJSON(w, out)
}

func (s *Server) handleGetQuery(w http.ResponseWriter, r *http.Request) {
	id := filepath.Base(r.PathValue("id"))
	if id == "." || id == ".." || id == "" {
		http.Error(w, "invalid query id", 400)
		return
	}
	t, err := session.LoadTurn(s.h.Config.SlmDir(), id)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	sum, _ := os.ReadFile(filepath.Join(session.TurnDir(s.h.Config.SlmDir(), id), "summary.md"))
	planMD, _ := os.ReadFile(filepath.Join(session.TurnDir(s.h.Config.SlmDir(), id), "PLAN.md"))
	tasksMD, _ := os.ReadFile(filepath.Join(session.TurnDir(s.h.Config.SlmDir(), id), "TASKS.md"))
	writeJSON(w, map[string]interface{}{
		"id": t.ID, "query": t.Query, "success": t.Success,
		"summary": t.Summary, "updated_at": t.UpdatedAt, "board": t.Board,
		"summary_md": string(sum), "plan_md": string(planMD), "tasks_md": string(tasksMD),
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func init() {
	_ = mime.AddExtensionType(".jsx", "text/javascript")
	_ = mime.AddExtensionType(".css", "text/css")
}

func spaHandler(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, ".jsx"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case path == "/" || path == "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		fileServer.ServeHTTP(w, r)
	})
}

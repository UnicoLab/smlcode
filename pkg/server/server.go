package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/skills"
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
	s.routes()
	return s
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
	s.mux.HandleFunc("GET /api/events", s.handleSSE)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/models", s.handleModels)
	s.mux.HandleFunc("GET /api/agents", s.handleAgents)

	if s.ui != nil {
		fileServer := http.FileServer(http.FS(s.ui))
		s.mux.Handle("GET /", spaHandler(fileServer))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"provider": s.h.Config.Provider,
		"model":    s.h.Config.Model,
		"backend":  s.h.Config.Backend,
		"root":     s.h.Config.Root,
		"running":  s.running,
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
	// Rebuild orchestrator with new settings (tools pick up permission/dry-run)
	orch, err := orchestrator.New(c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.h.Orchestrator = orch
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
	writeJSON(w, map[string]interface{}{
		"plan":    b.Plan,
		"tasks":   b.Tasks,
		"columns": plan.Columns(),
		"by_column": func() map[string][]plan.Task {
			return b.ByColumn()
		}(),
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

	s.h.Orchestrator.OnEvent(func(e orchestrator.Event) {
		s.mu.Lock()
		s.events = append(s.events, e)
		for ch := range s.subs {
			select {
			case ch <- e:
			default:
			}
		}
		s.mu.Unlock()
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
		phase := "done"
		msg := "finished"
		if err != nil {
			phase = "error"
			msg = err.Error()
		} else if res != nil {
			msg = res.Summary
		}
		e := orchestrator.Event{Phase: phase, Message: msg, Time: time.Now()}
		s.events = append(s.events, e)
		for ch := range s.subs {
			select {
			case ch <- e:
			default:
			}
		}
		s.mu.Unlock()
	}()

	writeJSON(w, map[string]string{"status": "started", "query": req.Query})
}

func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	s.h.Orchestrator.Stop()
	writeJSON(w, map[string]string{"status": "stopping"})
}

func (s *Server) handleLatestRun(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]interface{}{
		"running": s.running,
		"result":  s.lastRes,
		"events":  s.events,
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

	ch := make(chan orchestrator.Event, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	// replay
	for _, e := range s.events {
		select {
		case ch <- e:
		default:
		}
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
		close(ch)
	}()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
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

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, agents.PublicSpecs())
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

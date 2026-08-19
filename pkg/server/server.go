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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/readiness"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/stacks"
	"github.com/UnicoLab/slmcode/pkg/updatecheck"
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

// Version is set at build time via -ldflags from the main package.
var Version = "dev"

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
	s.mux.HandleFunc("GET /api/readiness", s.handleReadiness)
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
	s.mux.HandleFunc("GET /api/runs/interrupted", s.handleInterruptedRuns)
	s.mux.HandleFunc("POST /api/runs/resume", s.handleResumeRun)
	s.mux.HandleFunc("GET /api/runs/latest", s.handleLatestRun)
	s.mux.HandleFunc("GET /api/clarify/pending", s.handleClarifyPending)
	s.mux.HandleFunc("POST /api/clarify/answer", s.handleClarifyAnswer)
	s.mux.HandleFunc("GET /api/plan/pending", s.handlePlanPending)
	s.mux.HandleFunc("POST /api/plan/approve", s.handlePlanApprove)
	s.mux.HandleFunc("GET /api/continue/pending", s.handleContinuePending)
	s.mux.HandleFunc("POST /api/continue/answer", s.handleContinueAnswer)
	s.mux.HandleFunc("GET /api/escalate/pending", s.handleEscalatePending)
	s.mux.HandleFunc("POST /api/escalate/answer", s.handleEscalateAnswer)
	s.mux.HandleFunc("GET /api/shell/pending", s.handleShellPending)
	s.mux.HandleFunc("POST /api/shell/approve", s.handleShellApprove)
	s.mux.HandleFunc("GET /api/rewind", s.handleRewindList)
	s.mux.HandleFunc("POST /api/rewind/{id}", s.handleRewindRestore)
	s.mux.HandleFunc("POST /api/compact", s.handleCompact)
	s.mux.HandleFunc("GET /api/events", s.handleSSE)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/update", s.handleUpdateCheck)
	s.mux.HandleFunc("GET /api/models", s.handleModels)
	s.mux.HandleFunc("GET /api/auth", s.handleAuthStatus)
	s.mux.HandleFunc("PUT /api/auth", s.handlePutAuth)
	s.mux.HandleFunc("GET /api/mcp", s.handleMCPStatus)
	s.mux.HandleFunc("GET /api/config/schema", s.handleConfigSchema)
	s.mux.HandleFunc("GET /api/queries/{id}/events", s.handleQueryEvents)
	s.mux.HandleFunc("GET /api/stacks", s.handleListStacks)
	s.mux.HandleFunc("GET /api/stacks/{id}", s.handleGetStack)
	s.mux.HandleFunc("POST /api/stacks/{id}/apply", s.handleApplyStack)
	s.mux.HandleFunc("GET /api/agents", s.handleAgents)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	s.mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	s.mux.HandleFunc("PUT /api/agents/{id}", s.handlePutAgent)
	s.mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	s.mux.HandleFunc("GET /api/pipeline", s.handleGetPipeline)
	s.mux.HandleFunc("GET /api/composition", s.handleGetComposition)
	s.mux.HandleFunc("POST /api/composition/preview", s.handlePreviewComposition)
	s.mux.HandleFunc("PUT /api/pipeline", s.handlePutPipeline)
	s.mux.HandleFunc("POST /api/pipeline/reset", s.handleResetPipeline)
	s.mux.HandleFunc("GET /api/blocks", s.handleListBlocks)
	s.mux.HandleFunc("GET /api/blocks/{kind}/{id}", s.handleGetBlock)
	s.mux.HandleFunc("POST /api/blocks/{kind}", s.handleCreateBlock)
	s.mux.HandleFunc("PUT /api/blocks/{kind}/{id}", s.handleUpdateBlock)
	s.mux.HandleFunc("DELETE /api/blocks/{kind}/{id}", s.handleDeleteBlock)
	s.mux.HandleFunc("POST /api/packs/{id}/apply", s.handleApplyPack)
	s.mux.HandleFunc("POST /api/pipeline-presets/{id}/apply", s.handleApplyPipelineBlock)
	s.mux.HandleFunc("GET /api/archives", s.handleListArchives)
	s.mux.HandleFunc("GET /api/archives/{name}", s.handleGetArchive)
	s.mux.HandleFunc("GET /api/queries", s.handleListQueries)
	s.mux.HandleFunc("GET /api/queries/{id}", s.handleGetQuery)
	s.mux.HandleFunc("GET /api/workspace/file", s.handleWorkspaceFile)
	s.mux.HandleFunc("GET /api/workspace/tree", s.handleWorkspaceTree)
	s.mux.HandleFunc("GET /api/feedback", s.handleGetFeedback)
	s.mux.HandleFunc("POST /api/feedback", s.handleSetFeedback)
	s.mux.HandleFunc("DELETE /api/feedback", s.handleClearFeedback)

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
		"version":  Version,
		"provider": s.h.Config.Provider,
		"model":    s.h.Config.Model,
		"backend":  s.h.Config.Backend,
		"root":     s.h.Config.Root,
		"running":  running,
		"events":   nEvents,
	})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	cfg := s.h.Config
	skillCount := 0
	if s.h != nil && s.h.Orchestrator != nil && s.h.Orchestrator.Skills() != nil {
		if list, err := s.h.Orchestrator.Skills().List(); err == nil {
			skillCount = len(list)
		}
	}
	ctx := r.Context()
	if r.URL.Query().Get("probe") == "1" || strings.EqualFold(r.URL.Query().Get("probe"), "true") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		writeJSON(w, readiness.BuildWithProbe(ctx, cfg, skillCount))
		return
	}
	writeJSON(w, readiness.Build(cfg, skillCount))
}

// handleUpdateCheck reports whether a newer SLMCode release exists (cached 6h).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, updatecheck.Check(Version))
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.h.Config.Public())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
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
	hitl.ClearAll(s.h.Config.SlmDir())
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

func (s *Server) handleInterruptedRuns(w http.ResponseWriter, r *http.Request) {
	turns, err := session.ListInterrupted(s.h.Config.SlmDir())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		ID          string `json:"id"`
		Query       string `json:"query"`
		UpdatedAt   string `json:"updated_at"`
		Phase       string `json:"phase,omitempty"`
		ResumeFrom  string `json:"resume_from,omitempty"`
		Tasks       int    `json:"tasks"`
		Done        int    `json:"done"`
		Blocked     int    `json:"blocked"`
		ReactResume bool   `json:"react_resume"`
	}
	out := make([]item, 0, len(turns))
	for _, t := range turns {
		done := 0
		for _, task := range t.Board.Tasks {
			task.Normalize()
			if task.Column == plan.ColDone {
				done++
			}
		}
		out = append(out, item{
			ID: t.ID, Query: t.Query, UpdatedAt: t.UpdatedAt,
			Phase: t.Phase, ResumeFrom: t.ResumeFrom,
			Tasks: len(t.Board.Tasks), Done: done, Blocked: t.Board.FailedCount(),
			ReactResume: session.HasReactHistory(s.h.Config.SlmDir(), t.ID),
		})
	}
	writeJSON(w, out)
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	id := strings.TrimSpace(req.ID)
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		http.Error(w, "run already in progress", 409)
		return
	}
	s.running = true
	s.events = nil
	s.mu.Unlock()

	s.wireOrchestratorEvents()
	s.emit(orchestrator.Event{
		Phase: "init", Kind: "run_start", Message: "resume started", Time: time.Now(),
	})
	go func() {
		ctx := context.Background()
		res, err := s.h.Resume(ctx, id)
		s.mu.Lock()
		s.running = false
		if res != nil {
			s.lastRes = res
		}
		s.mu.Unlock()
		phase := "done"
		msg := "resumed"
		if err != nil {
			phase = "error"
			msg = err.Error()
		} else if res != nil {
			msg = res.Summary
		}
		s.emit(orchestrator.Event{Phase: phase, Kind: "run_end", Message: msg, Time: time.Now()})
	}()

	writeJSON(w, map[string]string{"status": "started", "id": id})
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
	if answered, expired := answeredAskState(plan.ClarifyAnswersPath(s.h.Config.SlmDir()), ask.ID, ask.CreatedAt, ask.TimeoutS, s.h.Config.ClarifyTimeout, path); answered || expired {
		if expired {
			plan.ClearScopeAsk(s.h.Config.SlmDir())
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ClarifyTimeout, path) {
		plan.ClearScopeAsk(s.h.Config.SlmDir())
		writeJSON(w, map[string]any{"pending": false, "expired": true})
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
	var ask plan.ScopeAsk
	data, err := os.ReadFile(plan.ClarifyAskPath(s.h.Config.SlmDir()))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "no pending clarify ask", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	if err := json.Unmarshal(data, &ask); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !requireMatchingAskID(w, ans.AskID, ask.ID) {
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ClarifyTimeout, plan.ClarifyAskPath(s.h.Config.SlmDir())) {
		plan.ClearScopeAsk(s.h.Config.SlmDir())
		http.Error(w, "clarify ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if err := plan.WriteScopeAnswersOnce(s.h.Config.SlmDir(), ans); err != nil {
		if os.IsExist(err) {
			http.Error(w, "clarify ask already answered", http.StatusConflict)
			return
		}
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
	if answered, expired := answeredAskState(hitl.AnswersPath(s.h.Config.SlmDir(), "plan"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.h.Config.PlanApproveTimeout, hitl.AskPath(s.h.Config.SlmDir(), "plan")); answered || expired {
		if expired {
			hitl.Clear(s.h.Config.SlmDir(), "plan")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.PlanApproveTimeout, hitl.AskPath(s.h.Config.SlmDir(), "plan")) {
		hitl.Clear(s.h.Config.SlmDir(), "plan")
		writeJSON(w, map[string]any{"pending": false, "expired": true})
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
	var ask plan.PlanApproveAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "plan", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		http.Error(w, "no pending plan ask", http.StatusNotFound)
		return
	}
	if !requireMatchingAskID(w, ans.AskID, ask.ID) {
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.PlanApproveTimeout, hitl.AskPath(s.h.Config.SlmDir(), "plan")) {
		hitl.Clear(s.h.Config.SlmDir(), "plan")
		http.Error(w, "plan ask expired", http.StatusGone)
		return
	}
	ans.Decision = strings.ToLower(strings.TrimSpace(ans.Decision))
	if ans.Decision != "approve" && ans.Decision != "replan" {
		http.Error(w, "invalid plan decision", http.StatusBadRequest)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := hitl.WriteAnswersOnce(s.h.Config.SlmDir(), "plan", ans); err != nil {
		if os.IsExist(err) {
			http.Error(w, "plan ask already answered", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "plan", Kind: "ask_answered", Message: "plan decision: " + ans.Decision, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleContinuePending(w http.ResponseWriter, r *http.Request) {
	var ask plan.ContinueAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "continue", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.h.Config.SlmDir(), "continue"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.h.Config.ContinueAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "continue")); answered || expired {
		if expired {
			hitl.Clear(s.h.Config.SlmDir(), "continue")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ContinueAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "continue")) {
		hitl.Clear(s.h.Config.SlmDir(), "continue")
		writeJSON(w, map[string]any{"pending": false, "expired": true})
		return
	}
	writeJSON(w, map[string]any{"pending": true, "ask": ask})
}

func (s *Server) handleContinueAnswer(w http.ResponseWriter, r *http.Request) {
	var ans plan.ContinueAnswer
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	var ask plan.ContinueAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "continue", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		http.Error(w, "no pending continue ask", http.StatusNotFound)
		return
	}
	if !requireMatchingAskID(w, ans.AskID, ask.ID) {
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ContinueAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "continue")) {
		hitl.Clear(s.h.Config.SlmDir(), "continue")
		http.Error(w, "continue ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	ans.Action = plan.NormalizeContinueAction(ans.Action)
	if err := hitl.WriteAnswersOnce(s.h.Config.SlmDir(), "continue", ans); err != nil {
		if os.IsExist(err) {
			http.Error(w, "continue ask already answered", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "test", Kind: "ask_answered", Message: "continue decision: " + ans.Action, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true, "action": ans.Action})
}

func (s *Server) handleEscalatePending(w http.ResponseWriter, r *http.Request) {
	var ask plan.EscalateAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "escalate", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.h.Config.SlmDir(), "escalate"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.h.Config.EscalateAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "escalate")); answered || expired {
		if expired {
			hitl.Clear(s.h.Config.SlmDir(), "escalate")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.EscalateAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "escalate")) {
		hitl.Clear(s.h.Config.SlmDir(), "escalate")
		writeJSON(w, map[string]any{"pending": false, "expired": true})
		return
	}
	writeJSON(w, map[string]any{"pending": true, "ask": ask})
}

func (s *Server) handleEscalateAnswer(w http.ResponseWriter, r *http.Request) {
	var ans plan.EscalateAnswer
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	var ask plan.EscalateAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "escalate", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		http.Error(w, "no pending escalate ask", http.StatusNotFound)
		return
	}
	if !requireMatchingAskID(w, ans.AskID, ask.ID) {
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.EscalateAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "escalate")) {
		hitl.Clear(s.h.Config.SlmDir(), "escalate")
		http.Error(w, "escalate ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	ans.Action = plan.NormalizeEscalateAction(ans.Action)
	if err := hitl.WriteAnswersOnce(s.h.Config.SlmDir(), "escalate", ans); err != nil {
		if os.IsExist(err) {
			http.Error(w, "escalate ask already answered", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "execute", Kind: "ask_answered", Message: "escalate decision: " + ans.Action, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true, "action": ans.Action})
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
	if answered, expired := answeredAskState(hitl.AnswersPath(s.h.Config.SlmDir(), "shell"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.h.Config.ShellAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "shell")); answered || expired {
		if expired {
			hitl.Clear(s.h.Config.SlmDir(), "shell")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ShellAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "shell")) {
		hitl.Clear(s.h.Config.SlmDir(), "shell")
		writeJSON(w, map[string]any{"pending": false, "expired": true})
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
	var ask workspace.ShellAsk
	ok, err := hitl.ReadAsk(s.h.Config.SlmDir(), "shell", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		http.Error(w, "no pending shell ask", http.StatusNotFound)
		return
	}
	if !requireMatchingAskID(w, ans.AskID, ask.ID) {
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.h.Config.ShellAskTimeout, hitl.AskPath(s.h.Config.SlmDir(), "shell")) {
		hitl.Clear(s.h.Config.SlmDir(), "shell")
		http.Error(w, "shell ask expired", http.StatusGone)
		return
	}
	ans.Decision = strings.ToLower(strings.TrimSpace(ans.Decision))
	if ans.Decision != "approve" && ans.Decision != "deny" {
		http.Error(w, "invalid shell decision", http.StatusBadRequest)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := hitl.WriteAnswersOnce(s.h.Config.SlmDir(), "shell", ans); err != nil {
		if os.IsExist(err) {
			http.Error(w, "shell ask already answered", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "execute", Kind: "ask_answered", Message: "shell decision: " + ans.Decision, Time: time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true})
}

func requireMatchingAskID(w http.ResponseWriter, postedID, currentID string) bool {
	postedID = strings.TrimSpace(postedID)
	currentID = strings.TrimSpace(currentID)
	if currentID == "" {
		http.Error(w, "pending ask has no id", http.StatusConflict)
		return false
	}
	if postedID == "" {
		http.Error(w, "missing ask_id", http.StatusConflict)
		return false
	}
	if postedID != currentID {
		http.Error(w, "ask_id does not match current pending ask", http.StatusConflict)
		return false
	}
	return true
}

func askExpiredWithFallback(createdAt string, timeoutSec int, fallback time.Duration, path string) bool {
	deadline, ok := askDeadline(createdAt, timeoutSec, fallback, path)
	return ok && time.Now().After(deadline)
}

func answeredAskState(answerPath, askID, createdAt string, timeoutSec int, fallback time.Duration, askPath string) (answered bool, expired bool) {
	data, err := os.ReadFile(answerPath)
	if err != nil {
		return false, false
	}
	var meta struct {
		AskID string `json:"ask_id"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || strings.TrimSpace(meta.AskID) != strings.TrimSpace(askID) {
		_ = os.Remove(answerPath)
		return false, false
	}
	deadline, ok := askDeadline(createdAt, timeoutSec, fallback, askPath)
	if !ok {
		return true, false
	}
	if info, err := os.Stat(answerPath); err == nil && info.ModTime().After(deadline) {
		return false, true
	}
	return true, false
}

func askDeadline(createdAt string, timeoutSec int, fallback time.Duration, path string) (time.Time, bool) {
	if timeoutSec <= 0 && fallback > 0 {
		timeoutSec = int(fallback.Seconds())
	}
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	timeout := time.Duration(timeoutSec) * time.Second
	if created, ok := parseAskCreatedAt(createdAt); ok {
		return created.Add(timeout), true
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Add(timeout), true
	}
	return time.Time{}, false
}

func parseAskCreatedAt(createdAt string) (time.Time, bool) {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, createdAt); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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

func (s *Server) handleGetFeedback(w http.ResponseWriter, r *http.Request) {
	text, at := "", ""
	if s.h != nil && s.h.Orchestrator != nil {
		text, at = s.h.Orchestrator.LiveFeedbackInfo()
	}
	writeJSON(w, map[string]string{"text": text, "set_at": at})
}

func (s *Server) handleSetFeedback(w http.ResponseWriter, r *http.Request) {
	if s.h == nil || s.h.Orchestrator == nil {
		writeJSON(w, map[string]any{"ok": false, "text": ""})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	text := s.h.Orchestrator.SetLiveFeedback(body.Text)
	writeJSON(w, map[string]any{"ok": true, "text": text})
}

func (s *Server) handleClearFeedback(w http.ResponseWriter, r *http.Request) {
	if s.h != nil && s.h.Orchestrator != nil {
		s.h.Orchestrator.ClearLiveFeedback()
	}
	writeJSON(w, map[string]any{"ok": true})
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
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	skillCount := 0
	if s.h != nil && s.h.Orchestrator != nil && s.h.Orchestrator.Skills() != nil {
		if list, err := s.h.Orchestrator.Skills().List(); err == nil {
			skillCount = len(list)
		}
	}
	comp, ok, compErr := composer.LoadDynamic(s.h.Config.SlmDir())
	var compPtr *composer.Composition
	var compErrText string
	if compErr != nil {
		compErrText = compErr.Error()
	} else if ok {
		compPtr = &comp
	}
	var planAsk plan.PlanApproveAsk
	planPending, _ := hitl.ReadAsk(s.h.Config.SlmDir(), "plan", &planAsk)
	writeJSON(w, map[string]any{
		"text":              st,
		"running":           running,
		"readiness":         readiness.Build(s.h.Config, skillCount),
		"composition":       s.savedCompositionView(compPtr),
		"composition_error": compErrText,
		"plan_pending":      planPending,
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		q = r.URL.Query().Get("query")
	}
	limit := 64
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	cat := models.Find(r.Context(), s.h.Config, q, limit)
	writeJSON(w, cat)
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	st := models.ResolveAuth(s.h.Config)
	writeJSON(w, map[string]interface{}{
		"provider":    st.Provider,
		"configured":  st.Configured,
		"required":    st.Required,
		"source":      st.Source,
		"env_key":     st.EnvKey,
		"has_api_key": st.HasAPIKey,
		"message":     st.Message,
		"auth_json":   authstore.PublicKeys(s.h.Config.SlmDir()),
		"auth_path":   authstore.Path(s.h.Config.SlmDir()),
	})
}

func (s *Server) handlePutAuth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	prov := body.Provider
	if prov == "" {
		prov = s.h.Config.Provider
	}
	if err := authstore.Set(s.h.Config.SlmDir(), prov, body.APIKey); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Refresh in-memory config key when targeting active provider.
	if config.NormalizeProvider(prov) == config.NormalizeProvider(s.h.Config.Provider) &&
		strings.TrimSpace(body.APIKey) != "" {
		s.h.Config.APIKey = strings.TrimSpace(body.APIKey)
	}
	writeJSON(w, map[string]interface{}{
		"ok":   true,
		"auth": models.ResolveAuth(s.h.Config),
	})
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if s.h.Orchestrator == nil {
		writeJSON(w, map[string]interface{}{
			"enabled": false, "meta_tool": "mcp_call", "servers": []interface{}{},
		})
		return
	}
	writeJSON(w, s.h.Orchestrator.MCPStatus())
}

func (s *Server) handleConfigSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"fields": config.Schema(),
		"slash":  config.SlashHelp(),
	})
}

func (s *Server) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit := 2000
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := session.ReadEvents(s.h.Config.SlmDir(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if events == nil {
		events = []session.EventRecord{}
	}
	writeJSON(w, map[string]interface{}{
		"id":      id,
		"events":  events,
		"summary": session.AnalyzeEvents(events),
	})
}

func (s *Server) handleGetComposition(w http.ResponseWriter, r *http.Request) {
	comp, ok, err := composer.LoadDynamic(s.h.Config.SlmDir())
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "composition": nil, "composition_error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, map[string]interface{}{"ok": false, "composition": nil})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "composition": s.savedCompositionView(&comp)})
}

func (s *Server) handlePreviewComposition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query required", 400)
		return
	}
	var comp composer.Composition
	if s.h != nil && s.h.Orchestrator != nil {
		comp = s.h.Orchestrator.PreviewComposition(req.Query)
	} else {
		comp = orchestrator.PreviewCompositionForConfig(s.h.Config, req.Query)
	}
	prof := config.ResolveModelProfile(s.h.Config.ModelProfiles, s.h.Config.Model)
	writeJSON(w, map[string]interface{}{
		"ok":              true,
		"dynamic_enabled": s.h.Config.DynamicPipeline,
		"composition":     s.compositionView(&comp),
		"slm_fit":         composer.FitHints(comp, s.h.Config.DynamicPipeline, prof.ContextLimit),
	})
}

// enrichAgentMaps attaches effective_* inheritance fields for Studio/TUI.
func (s *Server) enrichAgentMaps(list []map[string]interface{}) []map[string]interface{} {
	cfg := s.h.Config
	return agents.EnrichPublicSpecs(list, config.NormalizeProvider(cfg.Provider), cfg.Model, cfg.ActiveStack)
}

func (s *Server) loadCustomAgents() []agents.CustomSpec {
	dirs := append([]string{s.h.Config.AgentsDir()}, agents.GlobalAgentRoots()...)
	if blk := filepath.Join(blocks.ProjectBlocksDir(s.h.Config.Root), "agents"); blk != "" {
		dirs = append(dirs, blk)
	}
	list, _ := agents.LoadCustomSpecs(dirs...)
	// Merge agent blocks from the full registry (builtin + project + user) so
	// specialists like go-tester / go-worker are visible in Studio even when
	// not materialized. On-disk custom files win on id clash.
	if reg, err := blocks.Load(s.h.Config.Root); err == nil {
		seen := map[string]bool{}
		for _, c := range list {
			seen[c.ID] = true
		}
		for _, ab := range reg.Agents {
			if seen[ab.ID] {
				continue
			}
			spec := ab.Spec
			spec.Path = ab.Path
			if err := agents.NormalizeCustom(&spec); err == nil {
				list = append(list, spec)
			}
		}
	}
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

func (s *Server) rejectMutationWhileRunning(w http.ResponseWriter) bool {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		http.Error(w, "cannot update configuration while a run is active", http.StatusConflict)
		return true
	}
	return false
}

func (s *Server) handleListStacks(w http.ResponseWriter, r *http.Request) {
	list, err := stacks.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cfg := s.h.Config
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, list[i].PresetView(list[i].Matches(cfg)))
	}
	writeJSON(w, map[string]any{
		"stacks":       out,
		"active_stack": cfg.ActiveStack,
		"provider":     cfg.Provider,
		"model":        cfg.Model,
		"endpoint":     cfg.Endpoint,
	})
}

func (s *Server) handleGetStack(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	st, err := stacks.Load(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, st.PresetView(st.Matches(s.h.Config)))
}

func (s *Server) handleApplyStack(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st, err := stacks.Load(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	var body struct {
		ApplyAgentDefaults bool `json:"apply_agent_defaults"`
		ForceAgents        bool `json:"force_agents"`
		ClearAgentLLM      bool `json:"clear_agent_llm"`
		ApplyPack          bool `json:"apply_pack"`
		ForcePackAgents    bool `json:"force_pack_agents"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	c := s.h.Config
	res, err := stacks.Apply(c, st, c.AgentsDir(), stacks.ApplyOptions{
		ApplyAgentDefaults: body.ApplyAgentDefaults,
		ForceAgents:        body.ForceAgents,
		ClearAgentLLM:      body.ClearAgentLLM,
		ApplyPack:          body.ApplyPack,
		ForcePackAgents:    body.ForcePackAgents,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := c.Save(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":     true,
		"result": res,
		"config": c.Public(),
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	list := agents.PublicSpecsWithCustom(s.loadCustomAgents())
	writeJSON(w, s.enrichAgentMaps(list))
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if a := agents.AgentDetail(id, s.loadCustomAgents()); a != nil {
		writeJSON(w, s.enrichAgentMaps([]map[string]interface{}{a})[0])
		return
	}
	http.Error(w, "not found", 404)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
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
	if detail := agents.AgentDetail(got.ID, s.loadCustomAgents()); detail != nil {
		writeJSON(w, s.enrichAgentMaps([]map[string]interface{}{detail})[0])
		return
	}
	writeJSON(w, got)
}

func (s *Server) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
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
	if detail := agents.AgentDetail(got.ID, s.loadCustomAgents()); detail != nil {
		writeJSON(w, s.enrichAgentMaps([]map[string]interface{}{detail})[0])
		return
	}
	writeJSON(w, got)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	// An agent can live in two places: a materialized override in
	// .slmcode/agents/ and a block definition in .slmcode/blocks/agents/.
	// Remove whatever exists — succeed if either was deleted.
	var firstErr error
	deletedAny := false
	if err := agents.DeleteCustom(s.h.Config.AgentsDir(), id); err != nil {
		firstErr = err
	} else {
		deletedAny = true
	}
	if found, bErr := blocks.Delete(s.h.Config.Root, blocks.KindAgent, id); bErr != nil {
		// Prefer the block-level error — it explains builtin protection.
		if firstErr == nil || strings.Contains(bErr.Error(), "cannot be deleted") {
			firstErr = bErr
		}
	} else if found {
		deletedAny = true
	}
	if !deletedAny {
		http.Error(w, firstErr.Error(), 400)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, "deleted but rebuild failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "deleted": id})
}

func (s *Server) handleGetPipeline(w http.ResponseWriter, r *http.Request) {
	if s.h.Orchestrator != nil {
		_ = s.h.Orchestrator.ReloadPipeline()
		writeJSON(w, pipeline.View(s.h.Orchestrator.Pipeline()))
		return
	}
	cfg, err := pipeline.Load(s.h.Config.SlmDir())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, pipeline.View(cfg))
}

func (s *Server) handlePutPipeline(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var cfg pipeline.Config
	var wrapped struct {
		Config pipeline.Config `json:"config"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && (len(wrapped.Config.Order) > 0 || len(wrapped.Config.Phases) > 0 || len(wrapped.Config.Slots) > 0) {
		cfg = wrapped.Config
	} else if err := json.Unmarshal(raw, &cfg); err != nil {
		http.Error(w, "expected pipeline config JSON: "+err.Error(), 400)
		return
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.h.Orchestrator != nil {
		if err := s.h.Orchestrator.SetPipeline(&cfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, pipeline.View(s.h.Orchestrator.Pipeline()))
		return
	}
	if err := pipeline.Save(s.h.Config.SlmDir(), &cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, pipeline.View(&cfg))
}

func (s *Server) handleResetPipeline(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	cfg := pipeline.Default()
	if s.h.Orchestrator != nil {
		if err := s.h.Orchestrator.SetPipeline(&cfg); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, pipeline.View(s.h.Orchestrator.Pipeline()))
		return
	}
	if err := pipeline.Save(s.h.Config.SlmDir(), &cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, pipeline.View(&cfg))
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
		ID          string `json:"id"`
		Query       string `json:"query"`
		Success     bool   `json:"success"`
		Summary     string `json:"summary,omitempty"`
		UpdatedAt   string `json:"updated_at"`
		Interrupted bool   `json:"interrupted,omitempty"`
		Phase       string `json:"phase,omitempty"`
		ResumeFrom  string `json:"resume_from,omitempty"`
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
			Interrupted: t.Interrupted, Phase: t.Phase, ResumeFrom: t.ResumeFrom,
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
	comp, ok, compErr := composer.LoadDynamic(session.TurnDir(s.h.Config.SlmDir(), id))
	var compPtr *composer.Composition
	if ok {
		compPtr = &comp
	}
	var compErrText string
	if compErr != nil {
		compErrText = compErr.Error()
	}
	writeJSON(w, map[string]interface{}{
		"id": t.ID, "query": t.Query, "success": t.Success,
		"summary": t.Summary, "updated_at": t.UpdatedAt, "board": t.Board,
		"interrupted": t.Interrupted, "phase": t.Phase, "resume_from": t.ResumeFrom,
		"summary_md": string(sum), "plan_md": string(planMD), "tasks_md": string(tasksMD),
		"composition": s.savedCompositionView(compPtr), "composition_error": compErrText,
	})
}

func (s *Server) compositionView(comp *composer.Composition) interface{} {
	return s.compositionViewFor(comp, s.h.Config.DynamicPipeline)
}

func (s *Server) savedCompositionView(comp *composer.Composition) interface{} {
	return s.compositionViewFor(comp, true)
}

func (s *Server) compositionViewFor(comp *composer.Composition, dynamicEnabled bool) interface{} {
	if comp == nil {
		return nil
	}
	prof := config.ResolveModelProfile(s.h.Config.ModelProfiles, s.h.Config.Model)
	return composer.Annotate(*comp, dynamicEnabled, prof.ContextLimit)
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
		// Prevent caching for HTML, allow caching for hashed assets
		if strings.HasSuffix(path, ".html") || path == "/" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else if strings.HasSuffix(path, ".js") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		} else if strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// handleWorkspaceFile reads a file from the project workspace.
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path required", 400)
		return
	}
	fullPath := filepath.Join(s.h.Config.Root, filepath.Clean(path))
	if !strings.HasPrefix(fullPath, filepath.Clean(s.h.Config.Root)) {
		http.Error(w, "path traversal", 403)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, map[string]any{"path": path, "content": string(data), "size": len(data)})
}

// handleWorkspaceTree lists files and directories in a workspace subdirectory.
func (s *Server) handleWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	fullPath := filepath.Join(s.h.Config.Root, filepath.Clean(path))
	if !strings.HasPrefix(fullPath, filepath.Clean(s.h.Config.Root)) {
		http.Error(w, "path traversal", 403)
		return
	}
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	type treeEntry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size,omitempty"`
	}
	var result []treeEntry
	for _, e := range entries {
		// Skip hidden files/directories
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		entry := treeEntry{
			Name:  e.Name(),
			Path:  filepath.Join(path, e.Name()),
			IsDir: e.IsDir(),
		}
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				entry.Size = info.Size()
			}
		}
		result = append(result, entry)
	}
	// Sort: directories first, then alphabetical
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	writeJSON(w, map[string]any{"path": path, "entries": result})
}

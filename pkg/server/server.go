package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// seqEvent pairs a run event with its monotonic SSE sequence number.
//
// The sequence lives beside the event rather than inside it: pkg/stream may or
// may not grow a Seq field, and Studio must compile either way. The number is
// transported in the standard SSE `id:` line, which is exactly what
// EventSource echoes back as Last-Event-ID.
type seqEvent struct {
	Seq   uint64
	Event orchestrator.Event
}

// subscriber is one connected SSE client.
type subscriber struct {
	ch chan seqEvent
	// lagged is set when the fan-out had to drop for this client; the writer
	// then emits an explicit `event: gap` and resynchronises from the buffer
	// instead of silently losing progress.
	lagged atomic.Bool
}

// Server exposes the SLMCode Studio API + optional embedded web UI.
type Server struct {
	h    *harness.Harness
	mux  *http.ServeMux
	ui   fs.FS
	opts Options

	// runWG tracks in-flight run/resume goroutines so Shutdown can wait for
	// them instead of merely canceling their context.
	runWG sync.WaitGroup

	mu      sync.Mutex
	events  []seqEvent
	seq     uint64
	lastRes *orchestrator.Result
	running bool
	subs    map[*subscriber]struct{}
	closed  bool

	// cfgMu guards mutation of the shared *config.Config and of the
	// Orchestrator pointer. Handlers must go through cfg()/orch()/
	// withConfigWrite rather than touching s.h.* directly.
	cfgMu sync.RWMutex

	// baseCtx is canceled by Shutdown so in-flight runs stop cleanly instead
	// of being hard-killed mid-write.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	srvMu   sync.Mutex
	httpSrv *http.Server
}

// eventBufferSize bounds the replay ring. Token-stream events are evicted
// first (see emit) so a chatty model cannot push the structural run timeline
// out of the buffer.
const eventBufferSize = 1500

// tokenKind is the streaming-delta event kind. It is referenced as a literal
// on purpose: pkg/stream may add stream.KindToken later, and Studio must build
// with or without it.
const tokenKind = "token"

// Version is set at build time via -ldflags from the main package.
var Version = "dev"

// New builds a Studio server with the legacy (token-less) auth profile:
// loopback-only, no CORS, no session token. Embedded callers and tests keep
// working unchanged.
//
// New binaries should prefer NewWithOptions(h, ui, DefaultOptions()), which
// additionally requires a session token on every /api/* request.
func New(h *harness.Harness, ui fs.FS) *Server {
	return NewWithOptions(h, ui, Options{})
}

// NewWithOptions builds a Studio server with an explicit security profile.
func NewWithOptions(h *harness.Harness, ui fs.FS, opts Options) *Server {
	opts.normalize()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		h:          h,
		mux:        http.NewServeMux(),
		ui:         ui,
		opts:       opts,
		subs:       map[*subscriber]struct{}{},
		baseCtx:    ctx,
		baseCancel: cancel,
	}
	s.wireOrchestratorEvents()
	s.routes()
	return s
}

// wireOrchestratorEvents keeps Studio SSE subscribed across config rebuilds.
func (s *Server) wireOrchestratorEvents() {
	orch := s.orch()
	if orch == nil {
		return
	}
	orch.OnEvent(func(e orchestrator.Event) {
		s.emit(e)
	})
}

func (s *Server) emit(e orchestrator.Event) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.seq++
	se := seqEvent{Seq: s.seq, Event: e}
	s.events = append(s.events, se)
	if len(s.events) > eventBufferSize {
		s.events = evictOldest(s.events)
	}
	subs := make([]*subscriber, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- se:
		default:
			// Never drop silently: flag the client so its writer emits a gap
			// marker and replays from the ring buffer.
			sub.lagged.Store(true)
		}
	}
}

// evictOldest drops one buffered event, preferring the oldest token delta so
// the structural timeline survives a long streaming response.
func evictOldest(buf []seqEvent) []seqEvent {
	for i := range buf {
		if buf[i].Event.Kind == tokenKind {
			return append(buf[:i], buf[i+1:]...)
		}
	}
	return buf[1:]
}

// Handler returns the fully wrapped HTTP handler (security policy included).
func (s *Server) Handler() http.Handler { return s.secure(s.mux) }

// httpServer builds the *http.Server with the timeouts that plain
// http.ListenAndServe lacks (gosec G114 / Slowloris).
//
// Read/Write timeouts stay zero deliberately: /api/events is a long-lived SSE
// stream and any WriteTimeout would cut it off mid-run. ReadHeaderTimeout is
// what actually bounds a Slowloris header dribble.
func (s *Server) httpServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		BaseContext:       func(net.Listener) context.Context { return s.baseCtx },
	}
}

// ListenAndServe serves Studio until Shutdown is called.
func (s *Server) ListenAndServe(addr string) error {
	srv := s.httpServer(addr)
	s.srvMu.Lock()
	s.httpSrv = srv
	s.srvMu.Unlock()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// shutdownRunGrace bounds how long Shutdown waits for an in-flight run to
// unwind when the caller passed a context with no deadline.
const shutdownRunGrace = 10 * time.Second

// Shutdown stops accepting connections, cancels the in-flight run context and
// closes every SSE stream so clients see a clean end instead of a truncated
// response. Callers should wire it to SIGINT/SIGTERM.
func (s *Server) Shutdown(ctx context.Context) error {
	s.baseCancel()

	// Stop the orchestrator so a mid-flight run unwinds rather than being
	// killed between file writes.
	if orch := s.orch(); orch != nil {
		orch.Stop()
	}

	// …and then actually WAIT for it. Canceling a context only asks; before
	// this, Shutdown returned (and the CLI exited) while the run goroutine was
	// still mid-write, which is how a half-written source file gets left on
	// disk. Bounded by the caller's context so a wedged run cannot hang exit.
	runDone := make(chan struct{})
	go func() { s.runWG.Wait(); close(runDone) }()
	select {
	case <-runDone:
	case <-ctx.Done():
	case <-time.After(shutdownRunGrace):
	}

	s.mu.Lock()
	s.closed = true
	subs := make([]*subscriber, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.subs = map[*subscriber]struct{}{}
	s.mu.Unlock()
	for _, sub := range subs {
		close(sub.ch)
	}

	s.srvMu.Lock()
	srv := s.httpSrv
	s.srvMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// ── Synchronized access to shared harness state ──

// cfg returns the shared config pointer. Field *mutation* must go through
// withConfigWrite; multi-field reads that must be internally consistent
// (Public(), health, status) should use withConfigRead.
func (s *Server) cfg() *config.Config {
	if s.h == nil {
		return nil
	}
	return s.h.Config
}

// withConfigRead runs fn under the config read lock.
func (s *Server) withConfigRead(fn func(c *config.Config)) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	fn(s.cfg())
}

// withConfigWrite runs fn under the config write lock.
func (s *Server) withConfigWrite(fn func(c *config.Config)) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	fn(s.cfg())
}

// orch returns the current orchestrator under the read lock — handlePutConfig
// swaps this pointer, and every reader used to race that write.
func (s *Server) orch() *orchestrator.Orchestrator {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.h == nil {
		return nil
	}
	return s.h.Orchestrator
}

// setOrch installs a rebuilt orchestrator and CLOSES the one it replaces.
//
// Assigning s.h.Orchestrator directly stranded the previous engine: an
// orchestrator OWNS PROCESSES — every stdio MCP server it started is a child
// process that only dies in mcp.Manager.Close — and Studio rebuilds on every
// PUT /api/config, so a daemon that had its settings edited a dozen times was
// holding a dozen orphaned MCP subprocesses and evolve stores.
// Harness.SetOrchestrator does the swap-and-reap under its own lock.
func (s *Server) setOrch(o *orchestrator.Orchestrator) {
	s.cfgMu.Lock()
	h := s.h
	s.cfgMu.Unlock()
	if h != nil {
		if err := h.SetOrchestrator(o); err != nil {
			// The new engine is live; only reaping the old one failed. Never
			// fatal — but it must not be silent, since a leak here is exactly
			// the defect this call site had.
			s.emit(orchestrator.Event{
				Phase:   "init",
				Kind:    "debug",
				Level:   "warning",
				Message: "previous orchestrator did not close cleanly: " + err.Error(),
				Time:    time.Now(),
			})
		}
	}
	s.wireOrchestratorEvents()
}

// rootDir / slmDir / permissionMode are read-locked convenience accessors.
func (s *Server) rootDir() string {
	var v string
	s.withConfigRead(func(c *config.Config) {
		if c != nil {
			v = c.Root
		}
	})
	return v
}

func (s *Server) slmDir() string {
	var v string
	s.withConfigRead(func(c *config.Config) {
		if c != nil {
			v = c.SlmDir()
		}
	})
	return v
}

func (s *Server) permissionMode() string {
	var v string
	s.withConfigRead(func(c *config.Config) {
		if c != nil {
			v = c.Permission
		}
	})
	return v
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
	s.mux.HandleFunc("GET /api/queries/{id}/trace", s.handleQueryTrace)
	s.mux.HandleFunc("GET /api/review/pending", s.handleReviewPending)
	s.mux.HandleFunc("GET /api/review/pending/{id}", s.handleReviewChange)
	s.mux.HandleFunc("POST /api/review/apply", s.handleReviewApply)
	s.mux.HandleFunc("POST /api/review/reject", s.handleReviewReject)
	s.mux.HandleFunc("GET /api/workspace/file", s.handleWorkspaceFile)
	s.mux.HandleFunc("GET /api/workspace/tree", s.handleWorkspaceTree)
	s.mux.HandleFunc("GET /api/feedback", s.handleGetFeedback)
	s.mux.HandleFunc("POST /api/feedback", s.handleSetFeedback)
	s.mux.HandleFunc("DELETE /api/feedback", s.handleClearFeedback)

	// `GET /` is always wired. With a built SPA embedded it serves the shell
	// and its assets; without one it serves the placeholder page from
	// placeholder.go, so a from-source build says what is missing instead of
	// 404ing at the root. Both sit behind the same token gate (Handler →
	// secure → mux), so neither is reachable unauthenticated.
	if UIIsBuilt(s.ui) {
		fileServer := http.FileServer(http.FS(s.ui))
		s.mux.Handle("GET /", s.spaHandler(fileServer))
	} else {
		s.mux.Handle("GET /", s.placeholderHandler())
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	running := s.running
	nEvents := len(s.events)
	lastSeq := s.seq
	s.mu.Unlock()
	out := map[string]interface{}{
		"ok":       true,
		"api":      "ok",
		"ui":       "embedded",
		"version":  Version,
		"running":  running,
		"events":   nEvents,
		"last_seq": lastSeq,
		"auth":     s.AuthEnabled(),
		"pending":  s.pendingCount(),
	}
	s.withConfigRead(func(c *config.Config) {
		if c == nil {
			return
		}
		out["provider"] = c.Provider
		out["model"] = c.Model
		out["backend"] = c.Backend
		out["root"] = c.Root
		out["permission"] = c.Permission
	})
	writeJSON(w, out)
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg()
	skillCount := s.skillCount()
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

// skillCount reads the loaded skill count through the orchestrator accessor.
func (s *Server) skillCount() int {
	orch := s.orch()
	if orch == nil || orch.Skills() == nil {
		return 0
	}
	list, err := orch.Skills().List()
	if err != nil {
		return 0
	}
	return len(list)
}

// handleUpdateCheck reports whether a newer SLMCode release exists (cached 6h).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, updatecheck.Check(Version))
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	var out interface{}
	s.withConfigRead(func(c *config.Config) { out = c.Public() })
	writeJSON(w, out)
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

	var saveErr error
	var pub interface{}
	s.withConfigWrite(func(c *config.Config) {
		c.ApplyPatch(patch)
		saveErr = c.Save()
		pub = c.Public()
	})
	if saveErr != nil {
		http.Error(w, saveErr.Error(), 500)
		return
	}
	// Rebuild orchestrator with new settings (tools pick up permission/dry-run),
	// but never drop the Studio SSE fan-out — re-wire OnEvent after swap.
	orch, err := orchestrator.New(s.cfg())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.setOrch(orch)
	writeJSON(w, pub)
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
	body, err := s.orch().Store().Read(name)
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
	if err := s.orch().Store().Write(name, body.Content); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	_ = s.orch().Board().Load()
	b := s.orch().Board().Snapshot()
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
	_ = s.orch().Board().Load()
	writeJSON(w, s.orch().Board().Snapshot())
}

func (s *Server) handlePutTasks(w http.ResponseWriter, r *http.Request) {
	var board plan.Board
	if err := json.NewDecoder(r.Body).Decode(&board); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.orch().Board().Replace(board); err != nil {
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
	err := s.orch().Board().Update(func(b *plan.Board) error {
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
	err := s.orch().Board().Update(func(b *plan.Board) error {
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
	err := s.orch().Board().Update(func(b *plan.Board) error {
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
	list, err := s.orch().Skills().List()
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
	sk, ok := s.orch().Skills().Get(name)
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
	path, err := skills.WriteSkill(s.cfg().SkillsDir(), req)
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
	path, err := skills.WriteSkill(s.cfg().SkillsDir(), req)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sk, _ := skills.ParseFile(path)
	writeJSON(w, sk)
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := skills.DeleteSkill(s.cfg().SkillsDir(), name); err != nil {
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
	hitl.ClearAll(s.slmDir())
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		http.Error(w, "run already in progress", http.StatusConflict)
		return
	}
	s.running = true
	s.events = nil
	s.mu.Unlock()

	// Per-run engine/specialist/skill selection. These belong on the Run call
	// (see runOptions / "wiring required"), but until orchestrator.Run accepts
	// them they are applied to the shared config under the write lock and
	// restored the same way, so /api/config readers never observe a torn state.
	opts := runOptions{Mode: req.Mode, Specialist: req.Specialist, Skills: req.Skills}
	query, saved := s.applyRunOptions(opts, req.Query)

	// Ensure SSE stays wired for this run (config rebuilds call wireOrchestratorEvents too).
	s.wireOrchestratorEvents()
	s.emit(orchestrator.Event{
		Phase: "init", Kind: "run_start", Message: "run started", Time: time.Now(),
	})

	s.runWG.Add(1)
	go func() {
		defer s.runWG.Done()
		defer s.restoreRunOptions(saved)
		ctx := s.runContext()
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

// runOptions carries the per-run overrides Studio sends with POST /api/runs.
//
// The engine currently has no Run(ctx, query, opts) entry point, so these are
// swapped into the shared config for the duration of the run. When
// orchestrator.Run grows an options argument, applyRunOptions/restoreRunOptions
// collapse into a single value passed straight through — no shared mutation.
type runOptions struct {
	Mode       string
	Specialist string
	Skills     []string
}

// savedRunOptions is the config state to restore once the run ends.
type savedRunOptions struct {
	Mode       string
	Specialist string
	Skills     []string
	applied    bool
}

// applyRunOptions installs per-run overrides under the config write lock and
// returns the (possibly skill-annotated) query plus the state to restore.
func (s *Server) applyRunOptions(opts runOptions, query string) (string, savedRunOptions) {
	saved := savedRunOptions{applied: true}
	s.withConfigWrite(func(c *config.Config) {
		if c == nil {
			return
		}
		saved.Mode, saved.Specialist = c.Mode, c.Specialist
		saved.Skills = append([]string{}, c.PinnedSkills...)

		if opts.Mode != "" {
			c.Mode = opts.Mode
		}
		if opts.Specialist != "" {
			c.Specialist = opts.Specialist
			c.Mode = config.ModeSpecialist
		}
		if len(opts.Skills) > 0 {
			pins := append([]string{}, saved.Skills...)
			for _, sk := range opts.Skills {
				sk = strings.TrimSpace(sk)
				if sk == "" {
					continue
				}
				pins = append(pins, sk)
				if !strings.Contains(strings.ToLower(query), "@skill:"+strings.ToLower(sk)) {
					query += " @skill:" + sk
				}
			}
			c.PinnedSkills = pins
		}
	})
	return query, saved
}

func (s *Server) restoreRunOptions(saved savedRunOptions) {
	if !saved.applied {
		return
	}
	s.withConfigWrite(func(c *config.Config) {
		if c == nil {
			return
		}
		c.Mode = saved.Mode
		c.Specialist = saved.Specialist
		c.PinnedSkills = saved.Skills
	})
}

// runContext derives the run's context from the server lifetime so Shutdown
// (Ctrl-C) unwinds the run instead of hard-killing it.
func (s *Server) runContext() context.Context {
	if s.baseCtx != nil {
		return s.baseCtx
	}
	return context.Background()
}

func (s *Server) handleInterruptedRuns(w http.ResponseWriter, r *http.Request) {
	turns, err := session.ListInterrupted(s.slmDir())
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
			ReactResume: session.HasReactHistory(s.slmDir(), t.ID),
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
		http.Error(w, "run already in progress", http.StatusConflict)
		return
	}
	s.running = true
	s.events = nil
	s.mu.Unlock()

	s.wireOrchestratorEvents()
	s.emit(orchestrator.Event{
		Phase: "init", Kind: "run_start", Message: "resume started", Time: time.Now(),
	})
	s.runWG.Add(1)
	go func() {
		defer s.runWG.Done()
		// runContext(), NOT context.Background(): a resumed run used to be
		// invisible to Shutdown, so Ctrl-C returned to the shell while the
		// agent kept writing files.
		ctx := s.runContext()
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
	path := plan.ClarifyAskPath(s.slmDir())
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
	if answered, expired := answeredAskState(plan.ClarifyAnswersPath(s.slmDir()), ask.ID, ask.CreatedAt, ask.TimeoutS, s.cfg().ClarifyTimeout, path); answered || expired {
		if expired {
			plan.ClearScopeAsk(s.slmDir())
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ClarifyTimeout, path) {
		plan.ClearScopeAsk(s.slmDir())
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
	data, err := os.ReadFile(plan.ClarifyAskPath(s.slmDir()))
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
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ClarifyTimeout, plan.ClarifyAskPath(s.slmDir())) {
		plan.ClearScopeAsk(s.slmDir())
		http.Error(w, "clarify ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if err := plan.WriteScopeAnswersOnce(s.slmDir(), ans); err != nil {
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
	ok, err := hitl.ReadAsk(s.slmDir(), "plan", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.slmDir(), "plan"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.cfg().PlanApproveTimeout, hitl.AskPath(s.slmDir(), "plan")); answered || expired {
		if expired {
			hitl.Clear(s.slmDir(), "plan")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().PlanApproveTimeout, hitl.AskPath(s.slmDir(), "plan")) {
		hitl.Clear(s.slmDir(), "plan")
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
	ok, err := hitl.ReadAsk(s.slmDir(), "plan", &ask)
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
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().PlanApproveTimeout, hitl.AskPath(s.slmDir(), "plan")) {
		hitl.Clear(s.slmDir(), "plan")
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
	if err := hitl.WriteAnswersOnce(s.slmDir(), "plan", ans); err != nil {
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
	ok, err := hitl.ReadAsk(s.slmDir(), "continue", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.slmDir(), "continue"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.cfg().ContinueAskTimeout, hitl.AskPath(s.slmDir(), "continue")); answered || expired {
		if expired {
			hitl.Clear(s.slmDir(), "continue")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ContinueAskTimeout, hitl.AskPath(s.slmDir(), "continue")) {
		hitl.Clear(s.slmDir(), "continue")
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
	ok, err := hitl.ReadAsk(s.slmDir(), "continue", &ask)
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
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ContinueAskTimeout, hitl.AskPath(s.slmDir(), "continue")) {
		hitl.Clear(s.slmDir(), "continue")
		http.Error(w, "continue ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	ans.Action = plan.NormalizeContinueAction(ans.Action)
	if err := hitl.WriteAnswersOnce(s.slmDir(), "continue", ans); err != nil {
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
	ok, err := hitl.ReadAsk(s.slmDir(), "escalate", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.slmDir(), "escalate"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.cfg().EscalateAskTimeout, hitl.AskPath(s.slmDir(), "escalate")); answered || expired {
		if expired {
			hitl.Clear(s.slmDir(), "escalate")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().EscalateAskTimeout, hitl.AskPath(s.slmDir(), "escalate")) {
		hitl.Clear(s.slmDir(), "escalate")
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
	ok, err := hitl.ReadAsk(s.slmDir(), "escalate", &ask)
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
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().EscalateAskTimeout, hitl.AskPath(s.slmDir(), "escalate")) {
		hitl.Clear(s.slmDir(), "escalate")
		http.Error(w, "escalate ask expired", http.StatusGone)
		return
	}
	ans.AskID = ask.ID
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	ans.Action = plan.NormalizeEscalateAction(ans.Action)
	if err := hitl.WriteAnswersOnce(s.slmDir(), "escalate", ans); err != nil {
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
	ok, err := hitl.ReadAsk(s.slmDir(), "shell", &ask)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"pending": false})
		return
	}
	if answered, expired := answeredAskState(hitl.AnswersPath(s.slmDir(), "shell"), ask.ID, ask.CreatedAt, ask.TimeoutS, s.cfg().ShellAskTimeout, hitl.AskPath(s.slmDir(), "shell")); answered || expired {
		if expired {
			hitl.Clear(s.slmDir(), "shell")
			writeJSON(w, map[string]any{"pending": false, "expired": true})
			return
		}
		writeJSON(w, map[string]any{"pending": false, "answered": true})
		return
	}
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ShellAskTimeout, hitl.AskPath(s.slmDir(), "shell")) {
		hitl.Clear(s.slmDir(), "shell")
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
	ok, err := hitl.ReadAsk(s.slmDir(), "shell", &ask)
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
	if askExpiredWithFallback(ask.CreatedAt, ask.TimeoutS, s.cfg().ShellAskTimeout, hitl.AskPath(s.slmDir(), "shell")) {
		hitl.Clear(s.slmDir(), "shell")
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
	if err := hitl.WriteAnswersOnce(s.slmDir(), "shell", ans); err != nil {
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
	mgr := &rewind.Manager{SlmDir: s.slmDir(), Root: s.cfg().Root}
	list, err := mgr.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"snapshots": list})
}

func (s *Server) handleRewindRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mgr := &rewind.Manager{SlmDir: s.slmDir(), Root: s.cfg().Root}
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
	res, err := s.orch().CompactContextNow()
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
	if s.h != nil && s.orch() != nil {
		text, at = s.orch().LiveFeedbackInfo()
	}
	writeJSON(w, map[string]string{"text": text, "set_at": at})
}

func (s *Server) handleSetFeedback(w http.ResponseWriter, r *http.Request) {
	if s.h == nil || s.orch() == nil {
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
	text := s.orch().SetLiveFeedback(body.Text)
	writeJSON(w, map[string]any{"ok": true, "text": text})
}

func (s *Server) handleClearFeedback(w http.ResponseWriter, r *http.Request) {
	if s.h != nil && s.orch() != nil {
		s.orch().ClearLiveFeedback()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	s.orch().Stop()
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
	events := make([]orchestrator.Event, 0, len(s.events))
	seqs := make([]uint64, 0, len(s.events))
	for _, se := range s.events {
		events = append(events, se.Event)
		seqs = append(seqs, se.Seq)
	}
	writeJSON(w, map[string]interface{}{
		"running": s.running,
		"result":  s.lastRes,
		"events":  events,
		// event_seqs[i] is the SSE id of events[i]; the client uses the last
		// one as its Last-Event-ID baseline so a snapshot + stream never
		// double-renders.
		"event_seqs": seqs,
		"last_seq":   s.seq,
	})
}

// handleSSE streams run events.
//
// Every frame carries `id: <seq>` with a server-monotonic sequence number, so
// a reconnecting EventSource sends `Last-Event-ID` and only receives what it
// missed instead of re-rendering the whole run from the top. When the requested
// id has already rolled out of the ring buffer — or when a slow client had to
// be dropped — an explicit `event: gap` frame is emitted so the UI can say so
// rather than silently losing progress.
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

	// Last-Event-ID header (browser reconnect) or ?last_event_id= (manual).
	after := parseLastEventID(r)

	sub := &subscriber{ch: make(chan seqEvent, 256)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.subs[sub] = struct{}{}
	replay, gapFrom, gapTo := bufferSince(s.events, after)
	head := s.seq
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if _, still := s.subs[sub]; still {
			delete(s.subs, sub)
			close(sub.ch)
		}
		s.mu.Unlock()
	}()

	write := func(name string, id uint64, payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		if name != "" {
			_, _ = fmt.Fprintf(w, "event: %s\n", name)
		}
		if id > 0 {
			_, _ = fmt.Fprintf(w, "id: %d\n", id)
		}
		// SSE stream write: a broken pipe here just means the client
		// disconnected, which the next flush/heartbeat will notice.
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}

	// Immediate hello so the UI can show "API connected" without waiting for a
	// run. The client must listen for the named `connected` event — plain
	// onmessage never fires for named frames.
	write("connected", 0, map[string]any{
		"phase": "idle", "kind": "connected", "message": "studio api connected",
		"last_seq": head, "resumed_from": after, "time": time.Now(),
	})
	if gapFrom > 0 {
		write("gap", 0, map[string]any{
			"from": gapFrom, "to": gapTo,
			"message": "event buffer rolled past the requested id; earlier events were dropped",
		})
	}
	last := after
	for _, se := range replay {
		write("", se.Seq, se.Event)
		last = se.Seq
	}
	flusher.Flush()

	notify := r.Context().Done()
	done := s.baseCtx.Done()
	ticker := time.NewTicker(12 * time.Second)
	defer ticker.Stop()
	for {
		// A lagged client resynchronises from the ring rather than losing events.
		if sub.lagged.CompareAndSwap(true, false) {
			s.mu.Lock()
			catchup, gFrom, gTo := bufferSince(s.events, last)
			s.mu.Unlock()
			if gFrom > 0 {
				write("gap", 0, map[string]any{
					"from": gFrom, "to": gTo,
					"message": "client fell behind; earlier events were dropped",
				})
			}
			for _, se := range catchup {
				write("", se.Seq, se.Event)
				last = se.Seq
			}
			flusher.Flush()
		}

		select {
		case <-notify:
			return
		case <-done:
			return
		case <-ticker.C:
			// Comment heartbeats keep proxies/browsers from idle-closing the stream.
			// A write error here just means the client disconnected.
			_, _ = fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
			flusher.Flush()
		case se, ok := <-sub.ch:
			if !ok {
				return
			}
			if se.Seq <= last {
				continue // already delivered by a catch-up replay
			}
			write("", se.Seq, se.Event)
			last = se.Seq
			flusher.Flush()
		}
	}
}

// parseLastEventID reads the resume point from the standard header or the
// query fallback. Returns 0 when the client wants the full buffer.
func parseLastEventID(r *http.Request) uint64 {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("last_event_id"))
	}
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// bufferSince returns buffered events newer than `after`, plus the range of
// sequence numbers that were requested but are no longer in the buffer.
func bufferSince(buf []seqEvent, after uint64) (out []seqEvent, gapFrom, gapTo uint64) {
	if len(buf) == 0 {
		return nil, 0, 0
	}
	if after > 0 && buf[0].Seq > after+1 {
		gapFrom, gapTo = after+1, buf[0].Seq-1
	}
	for _, se := range buf {
		if se.Seq > after {
			out = append(out, se)
		}
	}
	return out, gapFrom, gapTo
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
	skillCount := s.skillCount()
	comp, ok, compErr := composer.LoadDynamic(s.slmDir())
	var compPtr *composer.Composition
	var compErrText string
	if compErr != nil {
		compErrText = compErr.Error()
	} else if ok {
		compPtr = &comp
	}
	var planAsk plan.PlanApproveAsk
	planPending, _ := hitl.ReadAsk(s.slmDir(), "plan", &planAsk)
	var ready any
	s.withConfigRead(func(c *config.Config) { ready = readiness.Build(c, skillCount) })
	writeJSON(w, map[string]any{
		"text":              st,
		"running":           running,
		"readiness":         ready,
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
	cat := models.Find(r.Context(), s.cfg(), q, limit)
	writeJSON(w, cat)
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	st := models.ResolveAuth(s.cfg())
	writeJSON(w, map[string]interface{}{
		"provider":    st.Provider,
		"configured":  st.Configured,
		"required":    st.Required,
		"source":      st.Source,
		"env_key":     st.EnvKey,
		"has_api_key": st.HasAPIKey,
		"message":     st.Message,
		"auth_json":   authstore.PublicKeys(s.slmDir()),
		"auth_path":   authstore.Path(s.slmDir()),
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
		s.withConfigRead(func(c *config.Config) { prov = c.Provider })
	}
	if err := authstore.Set(s.slmDir(), prov, body.APIKey); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Refresh in-memory config key when targeting active provider — under the
	// write lock, since /api/config and /api/models read the same struct.
	var auth any
	s.withConfigWrite(func(c *config.Config) {
		if config.NormalizeProvider(prov) == config.NormalizeProvider(c.Provider) &&
			strings.TrimSpace(body.APIKey) != "" {
			c.APIKey = strings.TrimSpace(body.APIKey)
		}
		auth = models.ResolveAuth(c)
	})
	writeJSON(w, map[string]interface{}{"ok": true, "auth": auth})
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if s.orch() == nil {
		writeJSON(w, map[string]interface{}{
			"enabled": false, "meta_tool": "mcp_call", "servers": []interface{}{},
		})
		return
	}
	writeJSON(w, s.orch().MCPStatus())
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
	events, err := session.ReadEvents(s.slmDir(), id, limit)
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
	comp, ok, err := composer.LoadDynamic(s.slmDir())
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
	if s.h != nil && s.orch() != nil {
		comp = s.orch().PreviewComposition(req.Query)
	} else {
		comp = orchestrator.PreviewCompositionForConfig(s.cfg(), req.Query)
	}
	prof := config.ResolveModelProfile(s.cfg().ModelProfiles, s.cfg().Model)
	writeJSON(w, map[string]interface{}{
		"ok":              true,
		"dynamic_enabled": s.cfg().DynamicPipeline,
		"composition":     s.compositionView(&comp),
		"slm_fit":         composer.FitHints(comp, s.cfg().DynamicPipeline, prof.ContextLimit),
	})
}

// enrichAgentMaps attaches effective_* inheritance fields for Studio/TUI.
func (s *Server) enrichAgentMaps(list []map[string]interface{}) []map[string]interface{} {
	cfg := s.cfg()
	return agents.EnrichPublicSpecs(list, config.NormalizeProvider(cfg.Provider), cfg.Model, cfg.ActiveStack)
}

func (s *Server) loadCustomAgents() []agents.CustomSpec {
	dirs := append([]string{s.cfg().AgentsDir()}, agents.GlobalAgentRoots()...)
	if blk := filepath.Join(blocks.ProjectBlocksDir(s.cfg().Root), "agents"); blk != "" {
		dirs = append(dirs, blk)
	}
	list, _ := agents.LoadCustomSpecs(dirs...)
	// Merge agent blocks from the full registry (builtin + project + user) so
	// specialists like go-tester / go-worker are visible in Studio even when
	// not materialized. On-disk custom files win on id clash.
	if reg, err := blocks.Load(s.cfg().Root); err == nil {
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
	orch, err := orchestrator.New(s.cfg())
	if err != nil {
		return err
	}
	s.setOrch(orch)
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
	cfg := s.cfg()
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
	writeJSON(w, st.PresetView(st.Matches(s.cfg())))
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
	var res any
	var applyErr, saveErr error
	var pub any
	s.withConfigWrite(func(c *config.Config) {
		res, applyErr = stacks.Apply(c, st, c.AgentsDir(), stacks.ApplyOptions{
			ApplyAgentDefaults: body.ApplyAgentDefaults,
			ForceAgents:        body.ForceAgents,
			ClearAgentLLM:      body.ClearAgentLLM,
			ApplyPack:          body.ApplyPack,
			ForcePackAgents:    body.ForcePackAgents,
		})
		if applyErr != nil {
			return
		}
		saveErr = c.Save()
		pub = c.Public()
	})
	if applyErr != nil {
		http.Error(w, applyErr.Error(), 500)
		return
	}
	if saveErr != nil {
		http.Error(w, saveErr.Error(), 500)
		return
	}
	if err := s.rebuildOrchestrator(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":     true,
		"result": res,
		"config": pub,
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
	path, err := agents.WriteCustom(s.cfg().AgentsDir(), req)
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
	path, err := agents.WriteCustom(s.cfg().AgentsDir(), req)
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
	if err := agents.DeleteCustom(s.cfg().AgentsDir(), id); err != nil {
		firstErr = err
	} else {
		deletedAny = true
	}
	if found, bErr := blocks.Delete(s.cfg().Root, blocks.KindAgent, id); bErr != nil {
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
	if s.orch() != nil {
		_ = s.orch().ReloadPipeline()
		writeJSON(w, pipeline.View(s.orch().Pipeline()))
		return
	}
	cfg, err := pipeline.Load(s.slmDir())
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
	if s.orch() != nil {
		if err := s.orch().SetPipeline(&cfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, pipeline.View(s.orch().Pipeline()))
		return
	}
	if err := pipeline.Save(s.slmDir(), &cfg); err != nil {
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
	if s.orch() != nil {
		if err := s.orch().SetPipeline(&cfg); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, pipeline.View(s.orch().Pipeline()))
		return
	}
	if err := pipeline.Save(s.slmDir(), &cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, pipeline.View(&cfg))
}

func (s *Server) archivesDir() string {
	return filepath.Join(s.slmDir(), "archives")
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
	dir := session.QueriesDir(s.slmDir())
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
		t, err := session.LoadTurn(s.slmDir(), e.Name())
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
	t, err := session.LoadTurn(s.slmDir(), id)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	sum, _ := os.ReadFile(filepath.Join(session.TurnDir(s.slmDir(), id), "summary.md"))
	planMD, _ := os.ReadFile(filepath.Join(session.TurnDir(s.slmDir(), id), "PLAN.md"))
	tasksMD, _ := os.ReadFile(filepath.Join(session.TurnDir(s.slmDir(), id), "TASKS.md"))
	comp, ok, compErr := composer.LoadDynamic(session.TurnDir(s.slmDir(), id))
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
	return s.compositionViewFor(comp, s.cfg().DynamicPipeline)
}

func (s *Server) savedCompositionView(comp *composer.Composition) interface{} {
	return s.compositionViewFor(comp, true)
}

func (s *Server) compositionViewFor(comp *composer.Composition, dynamicEnabled bool) interface{} {
	if comp == nil {
		return nil
	}
	prof := config.ResolveModelProfile(s.cfg().ModelProfiles, s.cfg().Model)
	return composer.Annotate(*comp, dynamicEnabled, prof.ContextLimit)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func init() {
	_ = mime.AddExtensionType(".jsx", "text/javascript")
	_ = mime.AddExtensionType(".css", "text/css")
}

// spaHandler serves the embedded SPA.
//
// It NEVER embeds the session token in the document. It used to inject
// `<meta name="slmcode-token" content="…">`, which made `GET /` a token
// dispenser for any process on the machine that could open a socket to
// loopback — the exact adversary the token exists to stop. Every request that
// reaches here has already presented a valid token (see secure/tokenSourceOf),
// and the browser keeps it in an HttpOnly cookie from then on.
func (s *Server) spaHandler(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path
		isHTML := strings.HasSuffix(path, ".html") || path == "/"
		// Prevent caching for HTML, allow caching for hashed assets
		if isHTML {
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

// maxWorkspaceFileBytes bounds a single file read so the browser is not handed
// a multi-hundred-megabyte blob.
const maxWorkspaceFileBytes = 2 << 20 // 2 MiB

// alwaysHiddenDirs are never listed regardless of the `hidden` toggle: huge,
// noisy, and never what a reviewer is looking for.
var alwaysHiddenDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// handleWorkspaceFile reads a file from the project workspace.
//
// The path is resolved with filepath.EvalSymlinks on both root and target and
// compared with a trailing separator, so neither `../`, a sibling directory
// sharing the root's name prefix (`/home/u/proj-secrets`), nor a symlink out of
// the tree can escape.
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		http.Error(w, "path required", 400)
		return
	}
	fullPath, err := s.workspaceReadPath(path)
	if err != nil {
		if errors.Is(err, ErrSecretPath) {
			http.Error(w, "forbidden: file holds harness credentials", http.StatusForbidden)
			return
		}
		http.Error(w, "path traversal", http.StatusForbidden)
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", 400)
		return
	}
	if info.Size() > maxWorkspaceFileBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, map[string]any{
		"path": filepath.ToSlash(filepath.Clean(path)), "content": string(data), "size": len(data),
	})
}

// handleWorkspaceTree lists files and directories in a workspace subdirectory.
//
// Dot-entries are shown by default: `.slmcode/pending/` is the review queue and
// `.github/` is real project content, and hiding both made a core workflow
// invisible. `?hidden=false` restores the old behavior; `.git` and
// `node_modules` are always excluded.
func (s *Server) handleWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	fullPath, err := s.workspacePath(path)
	if err != nil {
		http.Error(w, "path traversal", http.StatusForbidden)
		return
	}
	showHidden := boolParam(r, "hidden", true)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	type treeEntry struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		IsDir  bool   `json:"is_dir"`
		Size   int64  `json:"size,omitempty"`
		Hidden bool   `json:"hidden,omitempty"`
	}
	result := make([]treeEntry, 0, len(entries))
	hiddenCount := 0
	for _, e := range entries {
		name := e.Name()
		if alwaysHiddenDirs[name] {
			continue
		}
		// Never advertise the credential store, even to an authenticated
		// browser: a listing that names auth.json is a map for anything that
		// later gets to read a path of its choosing.
		if rel, rerr := filepath.Rel(s.rootDir(), filepath.Join(fullPath, name)); rerr == nil &&
			workspace.IsHarnessSecretPath(rel) {
			continue
		}
		dot := strings.HasPrefix(name, ".")
		if dot {
			hiddenCount++
			if !showHidden {
				continue
			}
		}
		entry := treeEntry{
			Name:   name,
			Path:   filepath.ToSlash(filepath.Join(filepath.Clean(path), name)),
			IsDir:  e.IsDir(),
			Hidden: dot,
		}
		if entry.Path == "" || strings.HasPrefix(entry.Path, "./") {
			entry.Path = strings.TrimPrefix(entry.Path, "./")
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
	writeJSON(w, map[string]any{
		"path": path, "entries": result,
		"hidden_shown": showHidden, "hidden_count": hiddenCount,
	})
}

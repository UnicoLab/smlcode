package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// Harness is the embeddable entrypoint for SLMCode.
//
// Orchestrator OWNS PROCESSES: every stdio MCP server it starts is a child
// process that lives until mcp.Manager.Close runs, and the evolve engine holds
// an open store. Replacing the pointer without closing the old value therefore
// leaks both — and `slmcode studio` replaces it on every PUT /api/config, so
// the subprocesses accumulated for the lifetime of the daemon. Use
// SetOrchestrator (or RebuildOrchestrator) to swap, and Close on teardown.
type Harness struct {
	Config *config.Config
	// Orchestrator is the live engine. Prefer SetOrchestrator over assigning
	// this field directly: a direct assignment silently drops the previous
	// orchestrator's MCP subprocesses and evolve engine on the floor.
	Orchestrator *orchestrator.Orchestrator

	// mu serializes swap/close so a rebuild racing a teardown cannot close the
	// same orchestrator twice or leak the one it just installed.
	mu sync.Mutex
}

// SetOrchestrator installs orch and CLOSES the one it replaces.
//
// It returns the close error of the previous orchestrator (nil when there was
// none); the new orchestrator is installed either way, because a failure to
// reap the old one must not leave the harness without an engine.
func (h *Harness) SetOrchestrator(orch *orchestrator.Orchestrator) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	prev := h.Orchestrator
	h.Orchestrator = orch
	h.mu.Unlock()
	if prev == nil || prev == orch {
		return nil
	}
	return prev.Close()
}

// Close releases the harness's engine: MCP stdio subprocesses and the evolve
// engine. It is idempotent and nil-safe, so callers can always defer it.
func (h *Harness) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	prev := h.Orchestrator
	h.Orchestrator = nil
	h.mu.Unlock()
	if prev == nil {
		return nil
	}
	return prev.Close()
}

func New(root string) (*Harness, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	cfg.Root = root
	orch, err := orchestrator.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Harness{Config: cfg, Orchestrator: orch}, nil
}

func (h *Harness) Init() error {
	if h == nil || h.Config == nil {
		return fmt.Errorf("harness not initialized")
	}
	return orchestrator.InitWorkspace(h.Config.Root, h.Config)
}

func (h *Harness) EnsureInitialized() error {
	if h == nil || h.Config == nil {
		return fmt.Errorf("harness not initialized")
	}
	// Skills materialize may create .slmcode/ early — require config.yaml as the real init marker.
	if _, err := os.Stat(h.Config.ConfigPath()); os.IsNotExist(err) {
		return h.Init()
	}
	return nil
}

func (h *Harness) Run(ctx context.Context, query string) (*orchestrator.Result, error) {
	if h == nil || h.Config == nil {
		return nil, fmt.Errorf("harness not initialized")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.EnsureInitialized(); err != nil {
		return nil, err
	}
	if h.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not initialized or harness closed")
	}
	return h.Orchestrator.Run(ctx, query)
}

// RunTeamwork executes a query using the 4-stage Teamwork multi-agent pipeline:
// Stage 1: Specification / Interview (locks PRD.md)
// Stage 2: Architecture / Contracts (freezes CONTRACT.md)
// Stage 3: Worker Execution (focus-guarded waves)
// Stage 4: Adversarial Review & QA (hard gates + challenge probing, writes REVIEW.md)
func (h *Harness) RunTeamwork(ctx context.Context, query string) (*orchestrator.Result, error) {
	if h == nil {
		return nil, fmt.Errorf("nil harness")
	}
	if h.Config == nil {
		return nil, fmt.Errorf("harness not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("nothing to run — empty query")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := h.EnsureInitialized(); err != nil {
		return nil, err
	}
	orch := h.Orchestrator
	cfg := h.Config
	if orch == nil {
		return nil, fmt.Errorf("orchestrator not initialized or harness closed")
	}

	prevWorkflow := cfg.Workflow
	prevDynamic := cfg.DynamicPipeline
	cfg.Workflow = config.WorkflowTeamwork
	cfg.DynamicPipeline = false
	defer func() {
		cfg.Workflow = prevWorkflow
		cfg.DynamicPipeline = prevDynamic
	}()

	return orch.Run(ctx, query)
}

// Resume continues an interrupted query turn from the last board checkpoint.
func (h *Harness) Resume(ctx context.Context, turnID string) (*orchestrator.Result, error) {
	if h == nil || h.Config == nil {
		return nil, fmt.Errorf("harness not initialized")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.EnsureInitialized(); err != nil {
		return nil, err
	}
	if h.Orchestrator == nil {
		return nil, fmt.Errorf("orchestrator not initialized or harness closed")
	}
	return h.Orchestrator.Resume(ctx, turnID)
}

func (h *Harness) Status() (string, error) {
	if h == nil {
		return "", fmt.Errorf("harness not initialized")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Config == nil {
		return "", fmt.Errorf("harness not initialized")
	}
	store := contextstore.New(h.Config.SlmDir())
	query, _ := store.Read(contextstore.DocQuery)
	planDoc, _ := store.Read(contextstore.DocPlan)
	tasks, _ := store.Read(contextstore.DocTasks)
	return fmt.Sprintf("Root: %s\nProvider: %s · Model: %s · Backend: %s\n\n--- QUERY ---\n%s\n\n--- PLAN ---\n%s\n\n--- TASKS ---\n%s\n",
		h.Config.Root, h.Config.Provider, h.Config.Model, h.Config.Backend,
		clip(query, 800), clip(planDoc, 1200), clip(tasks, 1600)), nil
}

// RebuildOrchestrator recreates the orchestrator after agent/config changes
// (same path Studio uses after Agents API CRUD) and closes the previous one.
//
// A failed rebuild keeps the CURRENT orchestrator: the old engine is only
// closed once its replacement exists.
func (h *Harness) RebuildOrchestrator() error {
	if h == nil || h.Config == nil {
		return fmt.Errorf("harness not initialized")
	}
	orch, err := orchestrator.New(h.Config)
	if err != nil {
		return err
	}
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		// The new engine is live; reaping the old one failed. Surface it —
		// a leaked MCP subprocess is exactly the bug this call site had.
		return fmt.Errorf("orchestrator rebuilt, but closing the previous one failed: %w", cerr)
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}

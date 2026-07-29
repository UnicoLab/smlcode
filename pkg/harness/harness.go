package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/piotrlaczkowski/slmcode/pkg/config"
	contextstore "github.com/piotrlaczkowski/slmcode/pkg/context"
	"github.com/piotrlaczkowski/slmcode/pkg/orchestrator"
)

// Harness is the embeddable entrypoint for SLMCode.
type Harness struct {
	Config       *config.Config
	Orchestrator *orchestrator.Orchestrator
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
	return orchestrator.InitWorkspace(h.Config.Root, h.Config)
}

func (h *Harness) EnsureInitialized() error {
	// Skills materialize may create .slmcode/ early — require config.yaml as the real init marker.
	if _, err := os.Stat(h.Config.ConfigPath()); os.IsNotExist(err) {
		return h.Init()
	}
	return nil
}

func (h *Harness) Run(ctx context.Context, query string) (*orchestrator.Result, error) {
	if err := h.EnsureInitialized(); err != nil {
		return nil, err
	}
	return h.Orchestrator.Run(ctx, query)
}

func (h *Harness) Status() (string, error) {
	store := contextstore.New(h.Config.SlmDir())
	query, _ := store.Read(contextstore.DocQuery)
	planDoc, _ := store.Read(contextstore.DocPlan)
	tasks, _ := store.Read(contextstore.DocTasks)
	return fmt.Sprintf("Root: %s\nProvider: %s · Model: %s · Backend: %s\n\n--- QUERY ---\n%s\n\n--- PLAN ---\n%s\n\n--- TASKS ---\n%s\n",
		h.Config.Root, h.Config.Provider, h.Config.Model, h.Config.Backend,
		clip(query, 800), clip(planDoc, 1200), clip(tasks, 1600)), nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}

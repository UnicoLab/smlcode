package harness

import (
	"os"
	"path/filepath"

	"github.com/piotrlaczkowski/slmcode/pkg/config"
	contextstore "github.com/piotrlaczkowski/slmcode/pkg/context"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
)

// Workspace is a lightweight handle for board/docs/skills without starting LLMs.
type Workspace struct {
	Config *config.Config
	Store  *contextstore.Store
	Board  *plan.LiveStore
	Skills *skills.Loader
}

// OpenWorkspace loads .slmcode memory + board only (fast CLI path).
func OpenWorkspace(root string) (*Workspace, error) {
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
	store := contextstore.New(cfg.SlmDir())
	board := plan.NewLiveStore(cfg.SlmDir())
	_ = board.Load()
	board.OnChange(func(b *plan.Board) {
		p, t := b.ToMarkdown()
		_ = store.Write(contextstore.DocPlan, p)
		_ = store.Write(contextstore.DocTasks, t)
	})
	bundled := filepath.Join(cfg.SlmDir(), "skills", "_bundled")
	_ = skills.MaterializeBundled(bundled)
	loader := skills.NewLoader(filepath.Join(cfg.SlmDir(), "skills"), bundled)
	loader.Roots = append(loader.Roots, cfg.SkillsDirs...)
	return &Workspace{Config: cfg, Store: store, Board: board, Skills: loader}, nil
}

func (w *Workspace) EnsureInitialized() error {
	// Directory alone is not enough (bundled skills mkdir .slmcode/). Require config.yaml.
	if _, err := os.Stat(w.Config.ConfigPath()); os.IsNotExist(err) {
		h := &Harness{Config: w.Config}
		return h.Init()
	}
	return nil
}

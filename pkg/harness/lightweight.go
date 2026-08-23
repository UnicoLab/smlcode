package harness

import (
	"os"
	"path/filepath"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/skills"
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
	// Point the persistent backend caches at this project BEFORE anything
	// reads them. Without this a lightweight workspace (every non-run command,
	// `slmcode doctor` included) queried an empty in-memory capability cache
	// and reported "unprobed" however many times the endpoint had been
	// negotiated. The throughput store has the same problem and the same fix.
	backends.SetCapabilityCacheDir(cfg.SlmDir())
	backends.SetThroughputCacheDir(cfg.SlmDir())
	store := contextstore.New(cfg.SlmDir())
	board := plan.NewLiveStore(cfg.SlmDir())
	_ = board.Load()
	board.OnChange(func(b *plan.Board) {
		p, t := b.ToMarkdown()
		_ = store.Write(contextstore.DocPlan, p)
		_ = store.Write(contextstore.DocTasks, t)
	})
	// Only unpack the bundled skills into a workspace that EXISTS. This call
	// mkdir -p's .slmcode/skills/_bundled, so running any read-only command
	// (`slmcode status`, `slmcode doctor`, `slmcode board`) in a directory
	// that was never initialized used to scatter a half-workspace through it —
	// after which `slmcode doctor` reported "✔ .slmcode present".
	bundled := filepath.Join(cfg.SlmDir(), "skills", "_bundled")
	if Initialized(root) {
		_ = skills.MaterializeBundled(bundled)
	}
	loader := skills.NewLoader(filepath.Join(cfg.SlmDir(), "skills"), bundled)
	loader.Roots = append(loader.Roots, cfg.SkillsDirs...)
	return &Workspace{Config: cfg, Store: store, Board: board, Skills: loader}, nil
}

// Initialized reports whether root holds a real slmcode workspace. The
// directory alone does not count: several code paths mkdir .slmcode/ as a side
// effect, so config.yaml is the marker.
func Initialized(root string) bool {
	if root == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".slmcode", "config.yaml"))
	return err == nil
}

func (w *Workspace) EnsureInitialized() error {
	// Directory alone is not enough (bundled skills mkdir .slmcode/). Require config.yaml.
	if _, err := os.Stat(w.Config.ConfigPath()); os.IsNotExist(err) {
		h := &Harness{Config: w.Config}
		return h.Init()
	}
	return nil
}

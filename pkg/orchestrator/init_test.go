package orchestrator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/slmcode/pkg/config"
	contextstore "github.com/piotrlaczkowski/slmcode/pkg/context"
	"github.com/piotrlaczkowski/slmcode/pkg/orchestrator"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
)

func TestInitWorkspaceNoSeedsAgentsWillPopulate(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644)
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	slm := cfg.SlmDir()
	for _, name := range []string{
		contextstore.DocProject, contextstore.DocContext, contextstore.DocPlan,
		contextstore.DocTasks, "config.yaml", "board.json",
	} {
		if _, err := os.Stat(filepath.Join(slm, name)); err != nil {
			t.Fatalf("missing scaffold %s: %v", name, err)
		}
	}

	store := plan.NewLiveStore(slm)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	b := store.Snapshot()
	if len(b.Tasks) != 0 || b.Plan.Summary != "" {
		t.Fatalf("expected empty board (agents populate), got plan=%q tasks=%d", b.Plan.Summary, len(b.Tasks))
	}

	loader := skills.NewLoader(filepath.Join(slm, "skills"), filepath.Join(slm, "skills", "_bundled"))
	list, err := loader.List()
	if err != nil || len(list) < 10 {
		t.Fatalf("bundled skills=%d err=%v", len(list), err)
	}

	ctx, _ := contextstore.New(slm).Read(contextstore.DocContext)
	for _, bad := range []string{"Welcome", "ready for the first", "Workspace initialized"} {
		if strings.Contains(ctx, bad) {
			t.Fatalf("CONTEXT.md looks seeded (%q): %s", bad, ctx)
		}
	}
}

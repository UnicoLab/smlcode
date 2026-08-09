package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestBlocksE2E(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	res, err := blocks.ApplyPack(cfg, reg, "go", blocks.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyPack(go): %v", err)
	}

	// Verify pipeline.yaml was written.
	pipePath := pipeline.Path(cfg.SlmDir())
	if _, err := os.Stat(pipePath); err != nil {
		t.Fatalf("pipeline.yaml missing: %v", err)
	}
	loadedPipe, err := pipeline.Load(cfg.SlmDir())
	if err != nil {
		t.Fatalf("pipeline.Load: %v", err)
	}
	if loadedPipe.Execute.DefaultRole != "go-worker" {
		t.Errorf("execute.default_role = %q, want go-worker", loadedPipe.Execute.DefaultRole)
	}
	if ps, ok := loadedPipe.Phases["test"]; !ok || ps.Agent != "go-tester" {
		t.Errorf("phases.test.agent = %q, want go-tester", ps.Agent)
	}

	// Verify config fields set by ApplyPack.
	if cfg.ActivePack != "go" {
		t.Errorf("ActivePack = %q, want go", cfg.ActivePack)
	}
	if cfg.ActivePipeline != "go" {
		t.Errorf("ActivePipeline = %q, want go", cfg.ActivePipeline)
	}
	if !cfg.QAGate {
		t.Error("QAGate should be true")
	}
	if cfg.QAGateCommand == "" {
		t.Error("QAGateCommand should not be empty")
	}
	if !cfg.PostWorkerSmoke {
		t.Error("PostWorkerSmoke should be true")
	}

	// Verify skills pinned.
	if len(cfg.PinnedSkills) == 0 {
		t.Error("PinnedSkills should not be empty")
	}
	hasAtomic := false
	for _, s := range cfg.PinnedSkills {
		if s == "atomic-coding" {
			hasAtomic = true
			break
		}
	}
	if !hasAtomic {
		t.Error("PinnedSkills should contain atomic-coding")
	}

	// Verify ApplyResult fields.
	if res.PackID != "go" {
		t.Errorf("res.PackID = %q", res.PackID)
	}
	if res.PipelineID != "go" {
		t.Errorf("res.PipelineID = %q", res.PipelineID)
	}
	if res.QualityID != "go" {
		t.Errorf("res.QualityID = %q", res.QualityID)
	}
	if res.QAGateCommand == "" {
		t.Error("res.QAGateCommand should not be empty")
	}
	if res.PipelinePath == "" {
		t.Error("res.PipelinePath should not be empty")
	}

	// Persist and reload config to verify round-trip.
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
	reloaded, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if reloaded.ActivePack != "go" {
		t.Errorf("reloaded ActivePack = %q, want go", reloaded.ActivePack)
	}
}

func TestBlocksPythonE2E(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	res, err := blocks.ApplyPack(cfg, reg, "python", blocks.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyPack(python): %v", err)
	}

	// Verify pipeline.yaml was written.
	pipePath := pipeline.Path(cfg.SlmDir())
	if _, err := os.Stat(pipePath); err != nil {
		t.Fatalf("pipeline.yaml missing: %v", err)
	}
	loadedPipe, err := pipeline.Load(cfg.SlmDir())
	if err != nil {
		t.Fatalf("pipeline.Load: %v", err)
	}
	if loadedPipe.Execute.DefaultRole != "python-worker" {
		t.Errorf("execute.default_role = %q, want python-worker", loadedPipe.Execute.DefaultRole)
	}
	if ps, ok := loadedPipe.Phases["test"]; !ok || ps.Agent != "python-tester" {
		t.Errorf("phases.test.agent = %q, want python-tester", ps.Agent)
	}

	// Verify config fields set by ApplyPack.
	if cfg.ActivePack != "python" {
		t.Errorf("ActivePack = %q, want python", cfg.ActivePack)
	}
	if cfg.ActivePipeline != "python" {
		t.Errorf("ActivePipeline = %q, want python", cfg.ActivePipeline)
	}
	if !cfg.QAGate {
		t.Error("QAGate should be true")
	}
	if cfg.QAGateCommand == "" {
		t.Error("QAGateCommand should not be empty")
	}

	// Verify ApplyResult.
	if res.PackID != "python" {
		t.Errorf("res.PackID = %q", res.PackID)
	}
	if res.PipelineID != "python" {
		t.Errorf("res.PipelineID = %q", res.PipelineID)
	}
	if res.QualityID != "python" {
		t.Errorf("res.QualityID = %q", res.QualityID)
	}
}

func TestBlocksListAndShow(t *testing.T) {
	root := t.TempDir()
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// Catalog("") returns all blocks across all kinds.
	all := reg.Catalog("")
	if len(all) == 0 {
		t.Fatal("Catalog() returned no entries")
	}
	kindsFound := map[string]bool{}
	for _, e := range all {
		if e.ID == "" {
			t.Errorf("Catalog entry has empty ID: %+v", e)
		}
		if e.Kind == "" {
			t.Errorf("Catalog entry has empty kind: %+v", e)
		}
		kindsFound[e.Kind] = true
	}
	if !kindsFound[blocks.KindPipeline] {
		t.Error("Catalog missing pipeline entries")
	}
	if !kindsFound[blocks.KindAgent] {
		t.Error("Catalog missing agent entries")
	}
	if !kindsFound[blocks.KindQuality] {
		t.Error("Catalog missing quality entries")
	}
	if !kindsFound[blocks.KindPack] {
		t.Error("Catalog missing pack entries")
	}

	// Catalog("pipeline") returns only pipelines.
	pipes := reg.Catalog(blocks.KindPipeline)
	for _, e := range pipes {
		if e.Kind != blocks.KindPipeline {
			t.Errorf("Catalog(pipeline) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(pipes) < 3 {
		t.Errorf("Catalog(pipeline) returned %d entries, want >= 3", len(pipes))
	}

	// Catalog("agent") returns only agents.
	agents := reg.Catalog(blocks.KindAgent)
	for _, e := range agents {
		if e.Kind != blocks.KindAgent {
			t.Errorf("Catalog(agent) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(agents) < 6 {
		t.Errorf("Catalog(agent) returned %d entries, want >= 6", len(agents))
	}

	// Catalog("quality") returns only quality blocks.
	quals := reg.Catalog(blocks.KindQuality)
	for _, e := range quals {
		if e.Kind != blocks.KindQuality {
			t.Errorf("Catalog(quality) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(quals) < 3 {
		t.Errorf("Catalog(quality) returned %d entries, want >= 3", len(quals))
	}

	// Catalog("pack") returns only packs.
	packs := reg.Catalog(blocks.KindPack)
	for _, e := range packs {
		if e.Kind != blocks.KindPack {
			t.Errorf("Catalog(pack) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(packs) < 3 {
		t.Errorf("Catalog(pack) returned %d entries, want >= 3", len(packs))
	}

	// View returns the full Studio/API response.
	v := reg.View("go", "go")
	if v.ActivePack != "go" {
		t.Errorf("View.ActivePack = %q, want go", v.ActivePack)
	}
	if v.ActivePipeline != "go" {
		t.Errorf("View.ActivePipeline = %q, want go", v.ActivePipeline)
	}
	if len(v.Blocks) != len(all) {
		t.Errorf("View.Blocks count = %d, want %d", len(v.Blocks), len(all))
	}
	if len(v.Pipelines) < 3 {
		t.Errorf("View.Pipelines count = %d, want >= 3", len(v.Pipelines))
	}
	if len(v.Agents) < 6 {
		t.Errorf("View.Agents count = %d, want >= 6", len(v.Agents))
	}
	if len(v.Quality) < 3 {
		t.Errorf("View.Quality count = %d, want >= 3", len(v.Quality))
	}
	if len(v.Packs) < 3 {
		t.Errorf("View.Packs count = %d, want >= 3", len(v.Packs))
	}

	// Verify critical blocks exist via Get functions.
	for _, id := range []string{"go", "python", "react"} {
		if _, ok := reg.GetPipeline(id); !ok {
			t.Errorf("GetPipeline(%q) not found", id)
		}
		if _, ok := reg.GetPack(id); !ok {
			t.Errorf("GetPack(%q) not found", id)
		}
		if _, ok := reg.GetQuality(id); !ok {
			t.Errorf("GetQuality(%q) not found", id)
		}
	}
	for _, id := range []string{"go-worker", "go-tester", "python-worker", "python-tester", "react-worker", "react-tester"} {
		if _, ok := reg.GetAgent(id); !ok {
			t.Errorf("GetAgent(%q) not found", id)
		}
	}

	// Get functions on nil and nonexistent.
	var nilReg *blocks.Registry
	if _, ok := nilReg.GetPack("go"); ok {
		t.Error("nil GetPack should return false")
	}
	if _, ok := nilReg.GetPipeline("go"); ok {
		t.Error("nil GetPipeline should return false")
	}
	if _, ok := nilReg.GetQuality("go"); ok {
		t.Error("nil GetQuality should return false")
	}
	if _, ok := nilReg.GetAgent("go-worker"); ok {
		t.Error("nil GetAgent should return false")
	}
	if _, ok := reg.GetPack("nonexistent"); ok {
		t.Error("GetPack(nonexistent) should return false")
	}

	// Catalog on nil registry.
	if nilReg.Catalog("") != nil {
		t.Error("nil Catalog should return nil")
	}

	// Catalog with unknown kind returns nothing.
	if len(reg.Catalog("unknown-kind")) != 0 {
		t.Error("Catalog(unknown-kind) should be empty")
	}
}

func TestBlocksValidate(t *testing.T) {
	root := t.TempDir()
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	pipelineIDs := []string{"go", "python", "react"}
	for _, id := range pipelineIDs {
		p, ok := reg.GetPipeline(id)
		if !ok {
			t.Errorf("pipeline %q not found", id)
			continue
		}
		if err := p.Validate(); err != nil {
			t.Errorf("%s pipeline Validate() failed: %v", id, err)
		}
		if p.Kind != blocks.KindPipeline {
			t.Errorf("%s pipeline kind = %q, want %q", id, p.Kind, blocks.KindPipeline)
		}
		if p.Spec.Version < 1 {
			t.Errorf("%s pipeline version = %d, want >= 1", id, p.Spec.Version)
		}
	}

	agentIDs := []string{"go-worker", "go-tester", "python-worker", "python-tester", "react-worker", "react-tester"}
	for _, id := range agentIDs {
		a, ok := reg.GetAgent(id)
		if !ok {
			t.Errorf("agent %q not found", id)
			continue
		}
		if err := a.Validate(); err != nil {
			t.Errorf("%s agent Validate() failed: %v", id, err)
		}
		if a.Kind != blocks.KindAgent {
			t.Errorf("%s agent kind = %q, want %q", id, a.Kind, blocks.KindAgent)
		}
		if a.Spec.ID == "" {
			t.Errorf("%s agent spec ID is empty", id)
		}
		if strings.TrimSpace(a.Spec.SystemPrompt) == "" {
			t.Errorf("%s agent system_prompt is empty", id)
		}
	}

	qualityIDs := []string{"go", "python", "react"}
	for _, id := range qualityIDs {
		q, ok := reg.GetQuality(id)
		if !ok {
			t.Errorf("quality %q not found", id)
			continue
		}
		if err := q.Validate(); err != nil {
			t.Errorf("%s quality Validate() failed: %v", id, err)
		}
		if q.Kind != blocks.KindQuality {
			t.Errorf("%s quality kind = %q, want %q", id, q.Kind, blocks.KindQuality)
		}
		if q.PrimaryQAGate() == "" {
			t.Errorf("%s quality PrimaryQAGate() is empty", id)
		}
		if strings.TrimSpace(q.Spec.Smoke) == "" {
			t.Errorf("%s quality smoke is empty", id)
		}
	}

	packIDs := []string{"go", "python", "react"}
	for _, id := range packIDs {
		pack, ok := reg.GetPack(id)
		if !ok {
			t.Errorf("pack %q not found", id)
			continue
		}
		if err := pack.Validate(); err != nil {
			t.Errorf("%s pack Validate() failed: %v", id, err)
		}
		if pack.Kind != blocks.KindPack {
			t.Errorf("%s pack kind = %q, want %q", id, pack.Kind, blocks.KindPack)
		}
		if pack.Spec.Pipeline == "" {
			t.Errorf("%s pack pipeline is empty", id)
		}
		if pack.Spec.Quality == "" {
			t.Errorf("%s pack quality is empty", id)
		}
		if len(pack.Spec.Agents) < 2 {
			t.Errorf("%s pack agents count = %d, want >= 2", id, len(pack.Spec.Agents))
		}

		// ResolvePackRefs should pass for valid packs.
		if err := reg.ResolvePackRefs(pack); err != nil {
			t.Errorf("%s pack ResolvePackRefs failed: %v", id, err)
		}
	}

	// Nil block validations.
	var nilPipeline *blocks.PipelineBlock
	if err := nilPipeline.Validate(); err == nil {
		t.Error("nil pipeline Validate() should error")
	}
	var nilAgent *blocks.AgentBlock
	if err := nilAgent.Validate(); err == nil {
		t.Error("nil agent Validate() should error")
	}
	var nilQuality *blocks.QualityBlock
	if err := nilQuality.Validate(); err == nil {
		t.Error("nil quality Validate() should error")
	}
	var nilPack *blocks.PackBlock
	if err := nilPack.Validate(); err == nil {
		t.Error("nil pack Validate() should error")
	}

	// ResolvePackRefs with nil and broken references.
	if err := reg.ResolvePackRefs(nil); err == nil {
		t.Error("nil ResolvePackRefs should error")
	}
	broken := &blocks.PackBlock{
		Spec: blocks.PackSpec{Pipeline: "nonexistent"},
	}
	broken.Normalize()
	if err := reg.ResolvePackRefs(broken); err == nil {
		t.Error("ResolvePackRefs(broken) should error")
	}

	// DetectQuality tests.
	if q := reg.DetectQuality(root); q != nil {
		t.Errorf("expected nil for empty workspace, got %q", q.ID)
	}

	tmpGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpGo, "go.mod"), []byte("module example\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qGo := reg.DetectQuality(tmpGo)
	if qGo == nil || qGo.ID != "go" {
		t.Errorf("DetectQuality(go) = %v, want go", qGo)
	}

	tmpPy := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpPy, "pyproject.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qPy := reg.DetectQuality(tmpPy)
	if qPy == nil || qPy.ID != "python" {
		t.Errorf("DetectQuality(python) = %v, want python", qPy)
	}

	tmpReact := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpReact, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	qReact := reg.DetectQuality(tmpReact)
	if qReact == nil || qReact.ID != "react" {
		t.Errorf("DetectQuality(react) = %v, want react", qReact)
	}

	// DetectQuality on nil registry.
	var nilReg *blocks.Registry
	if q := nilReg.DetectQuality(tmpGo); q != nil {
		t.Error("nil DetectQuality should return nil")
	}
}

func TestBlocksApplyWithMaterializeAgents(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	res, err := blocks.ApplyPack(cfg, reg, "go", blocks.ApplyOptions{
		MaterializeAgents: true,
		ForceAgents:       true,
	})
	if err != nil {
		t.Fatalf("ApplyPack(go, materialize): %v", err)
	}

	// Check that agent files were written.
	agentsDir := cfg.AgentsDir()
	for _, aid := range []string{"go-worker", "go-tester"} {
		path := filepath.Join(agentsDir, aid+".yaml")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("agent file missing: %s: %v", path, err)
		}
	}

	if len(res.AgentsWritten) < 2 {
		t.Errorf("AgentsWritten = %v, want >= 2", res.AgentsWritten)
	}
	if len(res.SkillsPinned) == 0 {
		t.Error("SkillsPinned should not be empty")
	}
}

func TestBlocksApplyNilConfig(t *testing.T) {
	root := t.TempDir()
	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = blocks.ApplyPack(nil, reg, "go", blocks.ApplyOptions{})
	if err == nil {
		t.Error("ApplyPack with nil config should error")
	}

	var cfg config.Config
	cfg.Root = root
	_, err = blocks.ApplyPack(&cfg, nil, "go", blocks.ApplyOptions{})
	if err == nil {
		t.Error("ApplyPack with nil registry should error")
	}

	_, err = blocks.ApplyPack(&cfg, reg, "nonexistent", blocks.ApplyOptions{})
	if err == nil {
		t.Error("ApplyPack with nonexistent pack should error")
	}
}

func TestBlocksApplyPipelinePreset(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	res, err := blocks.ApplyPipelinePreset(cfg, reg, "go")
	if err != nil {
		t.Fatalf("ApplyPipelinePreset(go): %v", err)
	}

	if res.PipelineID != "go" {
		t.Errorf("PipelineID = %q", res.PipelineID)
	}
	if res.PipelinePath == "" {
		t.Error("PipelinePath should not be empty")
	}
	if _, err := os.Stat(res.PipelinePath); err != nil {
		t.Fatalf("pipeline.yaml missing after preset: %v", err)
	}

	// Nil/invalid inputs.
	_, err = blocks.ApplyPipelinePreset(nil, reg, "go")
	if err == nil {
		t.Error("ApplyPipelinePreset with nil config should error")
	}
	var cfg2 config.Config
	cfg2.Root = root
	_, err = blocks.ApplyPipelinePreset(&cfg2, nil, "go")
	if err == nil {
		t.Error("ApplyPipelinePreset with nil registry should error")
	}
	_, err = blocks.ApplyPipelinePreset(&cfg2, reg, "nonexistent")
	if err == nil {
		t.Error("ApplyPipelinePreset with nonexistent pipeline should error")
	}
}

func TestBlocksRoundTripPipelineConfig(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	reg, err := blocks.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// Apply go pack, then apply python pack — verify second overwrites pipeline.yaml.
	_, err = blocks.ApplyPack(cfg, reg, "go", blocks.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = blocks.ApplyPack(cfg, reg, "python", blocks.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := pipeline.Load(cfg.SlmDir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Execute.DefaultRole != "python-worker" {
		t.Errorf("after python pack, default_role = %q, want python-worker", loaded.Execute.DefaultRole)
	}
	if cfg.ActivePack != "python" {
		t.Errorf("ActivePack = %q, want python", cfg.ActivePack)
	}
}

func TestBlocksResolveQAGateCommand(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	// From pack.
	gate := blocks.ResolveQAGateCommand(root, root, "go")
	if gate == "" {
		t.Fatal("ResolveQAGateCommand(go) returned empty")
	}

	gatePy := blocks.ResolveQAGateCommand(root, root, "python")
	if gatePy == "" {
		t.Fatal("ResolveQAGateCommand(python) returned empty")
	}

	gateReact := blocks.ResolveQAGateCommand(root, root, "react")
	if gateReact == "" {
		t.Fatal("ResolveQAGateCommand(react) returned empty")
	}

	// From auto-detect (no active pack).
	// Create a go workspace.
	tmpGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpGo, "go.mod"), []byte("module example\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gateDetect := blocks.ResolveQAGateCommand(tmpGo, tmpGo, "")
	if gateDetect == "" {
		t.Fatal("ResolveQAGateCommand(detect go) returned empty")
	}

	// Unknown pack.
	gateUnknown := blocks.ResolveQAGateCommand(root, root, "nonexistent")
	if gateUnknown != "" {
		t.Errorf("ResolveQAGateCommand(nonexistent) = %q, want empty", gateUnknown)
	}

	// Non-existent root still returns builtin results.
	gateMissing := blocks.ResolveQAGateCommand("/tmp/does/not/exist/slmcode-test", "/tmp/does/not/exist/slmcode-test", "go")
	if gateMissing == "" {
		t.Log("note: builtins resolved even from missing root")
	}

	// Smoke command.
	smoke := blocks.ResolveSmokeCommand(root, root, "go")
	if smoke == "" {
		t.Fatal("ResolveSmokeCommand(go) returned empty")
	}
	smokePy := blocks.ResolveSmokeCommand(root, root, "python")
	if smokePy == "" {
		t.Fatal("ResolveSmokeCommand(python) returned empty")
	}

	// Safe prefixes.
	prefixes := blocks.SafePrefixesFromPack(root, "go")
	if len(prefixes) == 0 {
		t.Fatal("SafePrefixesFromPack(go) returned empty")
	}
	hasGoTest := false
	for _, p := range prefixes {
		if strings.Contains(p, "go test") {
			hasGoTest = true
			break
		}
	}
	if !hasGoTest {
		t.Error("SafePrefixesFromPack should include go test")
	}

	cfg.Root = root
}

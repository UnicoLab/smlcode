package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()

	ab := &AgentBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindAgent, ID: "my-worker", Name: "My Worker"},
		Spec: agents.CustomSpec{
			Title:        "My Worker",
			SystemPrompt: "You are a scoped implementation specialist.",
			MaxIter:      12,
		},
	}
	if _, err := Save(root, ab); err != nil {
		t.Fatalf("Save(agent): %v", err)
	}
	reg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.GetAgent("my-worker")
	if !ok {
		t.Fatal("GetAgent(my-worker) not found after Save")
	}
	if got.Spec.ID != "my-worker" {
		t.Errorf("spec.id = %q, want my-worker", got.Spec.ID)
	}
	if got.Spec.SystemPrompt != "You are a scoped implementation specialist." {
		t.Errorf("system_prompt not round-tripped: %q", got.Spec.SystemPrompt)
	}
	if got.Source != SourceProject {
		t.Errorf("source = %q, want project", got.Source)
	}

	// PipelineBlock (use the default config so validation passes).
	pb := &PipelineBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindPipeline, ID: "my-pipe", Name: "My Pipe"},
		Spec: pipeline.Default(),
	}
	if _, err := Save(root, pb); err != nil {
		t.Fatalf("Save(pipeline): %v", err)
	}
	reg, err = Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.GetPipeline("my-pipe"); !ok {
		t.Fatal("GetPipeline(my-pipe) not found after Save")
	}

	// QualityBlock.
	qb := &QualityBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindQuality, ID: "my-qa", Name: "My QA"},
		Spec: QualitySpec{QAGate: "make test"},
	}
	if _, err := Save(root, qb); err != nil {
		t.Fatalf("Save(quality): %v", err)
	}
	reg, err = Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q, ok := reg.GetQuality("my-qa")
	if !ok {
		t.Fatal("GetQuality(my-qa) not found after Save")
	}
	if q.Spec.QAGate != "make test" {
		t.Errorf("qa_gate = %q, want make test", q.Spec.QAGate)
	}

	// PackBlock.
	pkb := &PackBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindPack, ID: "my-pack", Name: "My Pack"},
		Spec: PackSpec{Pipeline: "my-pipe", Agents: []string{"my-worker"}},
	}
	if _, err := Save(root, pkb); err != nil {
		t.Fatalf("Save(pack): %v", err)
	}
	reg, err = Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pk, ok := reg.GetPack("my-pack")
	if !ok {
		t.Fatal("GetPack(my-pack) not found after Save")
	}
	if pk.Spec.Pipeline != "my-pipe" {
		t.Errorf("pack pipeline = %q, want my-pipe", pk.Spec.Pipeline)
	}
}

func TestSaveReturnsProjectPath(t *testing.T) {
	root := t.TempDir()
	path, err := Save(root, &QualityBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindQuality, ID: "qa-path"},
		Spec: QualitySpec{Smoke: "make test"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("path %q is not absolute", path)
	}
	want := filepath.Join(root, ".slmcode", "blocks", "quality", "qa-path.yaml")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("saved file missing: %v", err)
	}
}

func TestSaveAgentSpecIDMismatch(t *testing.T) {
	root := t.TempDir()
	_, err := Save(root, &AgentBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindAgent, ID: "my-worker"},
		Spec: agents.CustomSpec{ID: "other-id", SystemPrompt: "x"},
	})
	if err == nil {
		t.Fatal("expected error for mismatched spec.id, got nil")
	}
	if !strings.Contains(err.Error(), "spec.id") {
		t.Errorf("error %q should mention spec.id", err)
	}
}

func TestSaveInvalidBlock(t *testing.T) {
	root := t.TempDir()
	// Bad id: uppercase + space violates blockIDRe.
	_, err := Save(root, &PipelineBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindPipeline, ID: "Bad ID!"},
		Spec: pipeline.Default(),
	})
	if err == nil {
		t.Fatal("expected error for invalid id, got nil")
	}
	// Unsupported type.
	if _, err := Save(root, "not a block"); err == nil {
		t.Fatal("expected error for unsupported block type, got nil")
	}
	// No file should have been written.
	matches, _ := filepath.Glob(filepath.Join(root, ".slmcode", "blocks", "pipelines", "*"))
	if len(matches) != 0 {
		t.Errorf("no files expected, found %v", matches)
	}
}

func TestDeleteRemovesBlock(t *testing.T) {
	root := t.TempDir()
	if _, err := Save(root, &QualityBlock{
		Meta: Meta{APIVersion: APIVersion, Kind: KindQuality, ID: "del-qa"},
		Spec: QualitySpec{QAGate: "make test"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetQuality("del-qa"); !ok {
		t.Fatal("quality block should exist before delete")
	}

	found, err := Delete(root, KindQuality, "del-qa")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !found {
		t.Error("Delete should report found=true")
	}
	reg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetQuality("del-qa"); ok {
		t.Error("quality block still present after delete")
	}
}

func TestDeleteYMLVariant(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".slmcode", "blocks", "quality")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old-qa.yml"), []byte("kind: quality\nid: old-qa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := Delete(root, KindQuality, "old-qa")
	if err != nil {
		t.Fatalf("Delete(.yml): %v", err)
	}
	if !found {
		t.Error("Delete should report found=true for .yml file")
	}
	if _, err := os.Stat(filepath.Join(dir, "old-qa.yml")); !os.IsNotExist(err) {
		t.Error(".yml file should be removed")
	}
}

func TestDeleteBuiltinWithoutOverride(t *testing.T) {
	root := t.TempDir()
	found, err := Delete(root, KindPipeline, "go")
	if err == nil {
		t.Fatal("expected error for builtin-only block, got nil")
	}
	if found {
		t.Error("Delete of builtin should report found=false")
	}
	if !strings.Contains(err.Error(), "cannot be deleted") {
		t.Errorf("error %q should mention override guidance", err)
	}
	// Registry still serves the builtin.
	reg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetPipeline("go"); !ok {
		t.Error("builtin pipeline should still exist")
	}
}

func TestDeleteUnknownBlock(t *testing.T) {
	root := t.TempDir()
	found, err := Delete(root, KindPack, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown block, got nil")
	}
	if found {
		t.Error("Delete of unknown block should report found=false")
	}
	// Unknown kind is rejected too.
	if _, err := Delete(root, "wizard", "x"); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestParseAndValidateBlock(t *testing.T) {
	cases := []struct {
		kind     string
		yaml     string
		metaKind string
		id       string
	}{
		{
			kind:     KindPipeline,
			yaml:     "api_version: blocks/v1\nkind: pipeline\nid: p1\nname: P1\n",
			metaKind: KindPipeline,
			id:       "p1",
		},
		{
			kind:     KindAgent,
			yaml:     "api_version: blocks/v1\nkind: agent\nid: a1\nname: A1\nspec:\n  system_prompt: work\n",
			metaKind: KindAgent,
			id:       "a1",
		},
		{
			kind:     KindQuality,
			yaml:     "api_version: blocks/v1\nkind: quality\nid: q1\nname: Q1\nspec:\n  qa_gate: make test\n",
			metaKind: KindQuality,
			id:       "q1",
		},
		{
			kind:     KindPack,
			yaml:     "api_version: blocks/v1\nkind: pack\nid: k1\nname: K1\nspec:\n  pipeline: go\n",
			metaKind: KindPack,
			id:       "k1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			b, err := ParseAndValidateBlock(tc.kind, []byte(tc.yaml))
			if err != nil {
				t.Fatalf("ParseAndValidateBlock(%s): %v", tc.kind, err)
			}
			m := blockMetaForTest(b)
			if m == nil || m.Kind != tc.metaKind || m.ID != tc.id {
				t.Fatalf("got kind=%v id=%v, want kind=%s id=%s", m.Kind, m.ID, tc.metaKind, tc.id)
			}
			switch tc.kind {
			case KindPipeline:
				if _, ok := b.(*PipelineBlock); !ok {
					t.Fatal("expected *PipelineBlock")
				}
			case KindAgent:
				if _, ok := b.(*AgentBlock); !ok {
					t.Fatal("expected *AgentBlock")
				}
			case KindQuality:
				if _, ok := b.(*QualityBlock); !ok {
					t.Fatal("expected *QualityBlock")
				}
			case KindPack:
				if _, ok := b.(*PackBlock); !ok {
					t.Fatal("expected *PackBlock")
				}
			}
		})
	}
	// Invalid YAML and unknown kinds error.
	if _, err := ParseAndValidateBlock(KindPack, []byte("not: [valid")); err == nil {
		t.Error("expected error for invalid YAML")
	}
	if _, err := ParseAndValidateBlock("wizard", []byte("kind: pack")); err == nil {
		t.Error("expected error for unknown kind")
	}
	if _, err := ParseAndValidateBlock(KindAgent, []byte("kind: agent\nid: BAD ID\n")); err == nil {
		t.Error("expected error for invalid id")
	}
}

func blockMetaForTest(block any) *Meta {
	switch b := block.(type) {
	case *PipelineBlock:
		return &b.Meta
	case *AgentBlock:
		return &b.Meta
	case *QualityBlock:
		return &b.Meta
	case *PackBlock:
		return &b.Meta
	default:
		return nil
	}
}

func TestNormalizeKindAliases(t *testing.T) {
	cases := map[string]string{
		"pipeline":  KindPipeline,
		"pipelines": KindPipeline,
		"Pipelines": KindPipeline,
		"agent":     KindAgent,
		"agents":    KindAgent,
		"quality":   KindQuality,
		"pack":      KindPack,
		"packs":     KindPack,
		"":          "",
		"wizard":    "",
	}
	for in, want := range cases {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}

package blocks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// NormalizeKind maps URL kind aliases ("pipelines", "agents", "packs") to the
// canonical block kinds. Returns "" for unknown kinds.
func NormalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindPipeline, "pipelines":
		return KindPipeline
	case KindAgent, "agents":
		return KindAgent
	case KindQuality:
		return KindQuality
	case KindPack, "packs":
		return KindPack
	default:
		return ""
	}
}

// Save persists a block to the project blocks dir as <id>.yaml.
// block must be a pointer to *PipelineBlock, *AgentBlock, *QualityBlock,
// or *PackBlock. The block is Normalize()d and Validate()d first, and the
// returned path is absolute.
func Save(projectRoot string, block any) (string, error) {
	kind, id, err := normalizeAndValidate(block)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(ProjectBlocksDir(projectRoot), kindSubdir(kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".yaml")
	data, err := yaml.Marshal(block)
	if err != nil {
		return "", err
	}
	if err := atomicfile.WriteWithBackup(path, data, 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

// Delete removes a project-level block file (<id>.yaml or <id>.yml).
// found reports whether a project file existed and was removed. Builtin-only
// blocks (no project override) cannot be deleted and return an error; unknown
// ids return found=false with an error as well.
func Delete(projectRoot, kind, id string) (bool, error) {
	kind = NormalizeKind(kind)
	if kind == "" {
		return false, fmt.Errorf("unknown kind %q", kind)
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if !blockIDRe.MatchString(id) {
		return false, fmt.Errorf("invalid id %q", id)
	}
	dir := filepath.Join(ProjectBlocksDir(projectRoot), kindSubdir(kind))
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, id+ext)
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return true, err
			}
			return true, nil
		}
	}
	// No project file. Builtin-only blocks must be edited, not deleted.
	reg, err := Load(projectRoot)
	if err != nil {
		return false, err
	}
	if isBuiltinBlock(reg, kind, id) {
		return false, fmt.Errorf("builtin %s block %q cannot be deleted — edit it to create an override instead", kind, id)
	}
	return false, fmt.Errorf("block %q not found", id)
}

// ParseAndValidateBlock decodes raw YAML bytes into a typed, normalized and
// validated block of the given kind. Returns *PipelineBlock, *AgentBlock,
// *QualityBlock, or *PackBlock.
func ParseAndValidateBlock(kind string, data []byte) (any, error) {
	kind = NormalizeKind(kind)
	if kind == "" {
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	var block any
	switch kind {
	case KindPipeline:
		var b PipelineBlock
		if err := yaml.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		block = &b
	case KindAgent:
		var b AgentBlock
		if err := yaml.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		block = &b
	case KindQuality:
		var b QualityBlock
		if err := yaml.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		block = &b
	case KindPack:
		var b PackBlock
		if err := yaml.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		block = &b
	}
	if _, _, err := normalizeAndValidate(block); err != nil {
		return nil, err
	}
	return block, nil
}

// normalizeAndValidate runs the block-specific Normalize + Validate and
// returns its canonical kind and id.
func normalizeAndValidate(block any) (string, string, error) {
	switch b := block.(type) {
	case *PipelineBlock:
		if b == nil {
			return "", "", fmt.Errorf("nil pipeline block")
		}
		b.Normalize()
		if err := b.Validate(); err != nil {
			return "", "", err
		}
		return b.Kind, b.ID, nil
	case *AgentBlock:
		if b == nil {
			return "", "", fmt.Errorf("nil agent block")
		}
		b.Normalize()
		if err := b.Validate(); err != nil {
			return "", "", err
		}
		if strings.TrimSpace(b.Spec.ID) != b.ID {
			return "", "", fmt.Errorf("agent block id %q does not match spec.id %q", b.ID, b.Spec.ID)
		}
		return b.Kind, b.ID, nil
	case *QualityBlock:
		if b == nil {
			return "", "", fmt.Errorf("nil quality block")
		}
		b.Normalize()
		if err := b.Validate(); err != nil {
			return "", "", err
		}
		return b.Kind, b.ID, nil
	case *PackBlock:
		if b == nil {
			return "", "", fmt.Errorf("nil pack block")
		}
		b.Normalize()
		if err := b.Validate(); err != nil {
			return "", "", err
		}
		return b.Kind, b.ID, nil
	default:
		return "", "", fmt.Errorf("unsupported block type %T (want *PipelineBlock, *AgentBlock, *QualityBlock, or *PackBlock)", block)
	}
}

// isBuiltinBlock reports whether the registry only knows id as an embedded
// builtin (no project/user/extra override).
func isBuiltinBlock(reg *Registry, kind, id string) bool {
	switch kind {
	case KindPipeline:
		b, ok := reg.GetPipeline(id)
		return ok && b.Source == SourceBuiltin
	case KindAgent:
		b, ok := reg.GetAgent(id)
		return ok && b.Source == SourceBuiltin
	case KindQuality:
		b, ok := reg.GetQuality(id)
		return ok && b.Source == SourceBuiltin
	case KindPack:
		b, ok := reg.GetPack(id)
		return ok && b.Source == SourceBuiltin
	default:
		return false
	}
}

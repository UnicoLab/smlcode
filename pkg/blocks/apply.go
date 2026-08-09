package blocks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

// ApplyOptions controls pack / pipeline materialization into a project.
type ApplyOptions struct {
	// MaterializeAgents writes referenced agent blocks into .slmcode/agents/.
	MaterializeAgents bool
	// ForceAgents overwrites existing agent YAML files.
	ForceAgents bool
}

// ApplyResult summarizes what changed when applying a pack or pipeline.
type ApplyResult struct {
	PackID        string   `json:"pack_id,omitempty"`
	PipelineID    string   `json:"pipeline_id,omitempty"`
	QualityID     string   `json:"quality_id,omitempty"`
	QAGateCommand string   `json:"qa_gate_command,omitempty"`
	AgentsWritten []string `json:"agents_written,omitempty"`
	SkillsPinned  []string `json:"skills_pinned,omitempty"`
	PipelinePath  string   `json:"pipeline_path,omitempty"`
}

// ApplyPack materializes a language/domain pack into cfg + project files.
func ApplyPack(cfg *config.Config, reg *Registry, packID string, opts ApplyOptions) (*ApplyResult, error) {
	if cfg == nil || reg == nil {
		return nil, fmt.Errorf("config and registry required")
	}
	pack, ok := reg.GetPack(packID)
	if !ok {
		return nil, fmt.Errorf("pack %q not found", packID)
	}
	if err := reg.ResolvePackRefs(pack); err != nil {
		return nil, err
	}

	res := &ApplyResult{PackID: pack.ID}

	if pack.Spec.Pipeline != "" {
		pipe, _ := reg.GetPipeline(pack.Spec.Pipeline)
		cfgCopy := pipe.Spec
		// Clone phases map so we never mutate the registry copy.
		if cfgCopy.Phases != nil {
			cloned := make(map[string]pipeline.PhaseSpec, len(cfgCopy.Phases))
			for k, v := range cfgCopy.Phases {
				cloned[k] = v
			}
			cfgCopy.Phases = cloned
		}
		if pack.Spec.OverrideTester != "" {
			ps := cfgCopy.Phases["test"]
			ps.Agent = pack.Spec.OverrideTester
			if cfgCopy.Phases == nil {
				cfgCopy.Phases = map[string]pipeline.PhaseSpec{}
			}
			cfgCopy.Phases["test"] = ps
		}
		if pack.Spec.OverrideWorker != "" {
			cfgCopy.Execute.DefaultRole = pack.Spec.OverrideWorker
		}
		if pack.Spec.DeferPlanApprove {
			cfg.PlanApprove = "ask"
		}
		if pack.Spec.DeferClarify {
			cfg.ClarifyMode = "ask"
		}
		cfgCopy.Normalize()
		if err := cfgCopy.Validate(); err != nil {
			return nil, err
		}
		if err := pipeline.Save(cfg.SlmDir(), &cfgCopy); err != nil {
			return nil, err
		}
		res.PipelineID = pack.Spec.Pipeline
		res.PipelinePath = pipeline.Path(cfg.SlmDir())
	}

	if pack.Spec.Quality != "" {
		q, _ := reg.GetQuality(pack.Spec.Quality)
		gate := q.PrimaryQAGate()
		if gate != "" {
			cfg.QAGateCommand = gate
			cfg.QAGate = true
			res.QAGateCommand = gate
		}
		if q.Spec.Smoke != "" {
			cfg.PostWorkerSmoke = true
		}
		res.QualityID = pack.Spec.Quality
	}

	if opts.MaterializeAgents {
		agentsDir := cfg.AgentsDir()
		for _, aid := range pack.Spec.Agents {
			ab, ok := reg.GetAgent(aid)
			if !ok {
				continue
			}
			dest := filepath.Join(agentsDir, ab.Spec.ID+".yaml")
			if !opts.ForceAgents {
				if _, err := os.Stat(dest); err == nil {
					continue
				}
			}
			spec := ab.Spec
			if _, err := agents.WriteCustom(agentsDir, spec); err != nil {
				return res, fmt.Errorf("agent %s: %w", aid, err)
			}
			res.AgentsWritten = append(res.AgentsWritten, ab.Spec.ID)
		}
		sort.Strings(res.AgentsWritten)
	}

	if pack.Spec.PinSkills && len(pack.Spec.Skills) > 0 {
		cfg.PinnedSkills = mergeUnique(cfg.PinnedSkills, pack.Spec.Skills)
		res.SkillsPinned = append([]string{}, pack.Spec.Skills...)
	}

	cfg.ActivePack = pack.ID
	if res.PipelineID != "" {
		cfg.ActivePipeline = res.PipelineID
	}
	return res, nil
}

// ApplyPipelinePreset writes a named pipeline block to .slmcode/pipeline.yaml.
func ApplyPipelinePreset(cfg *config.Config, reg *Registry, pipelineID string) (*ApplyResult, error) {
	if cfg == nil || reg == nil {
		return nil, fmt.Errorf("config and registry required")
	}
	pipe, ok := reg.GetPipeline(pipelineID)
	if !ok {
		return nil, fmt.Errorf("pipeline %q not found", pipelineID)
	}
	spec := pipe.Spec
	spec.Normalize()
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := pipeline.Save(cfg.SlmDir(), &spec); err != nil {
		return nil, err
	}
	cfg.ActivePipeline = pipe.ID
	return &ApplyResult{
		PipelineID:   pipe.ID,
		PipelinePath: pipeline.Path(cfg.SlmDir()),
	}, nil
}

func mergeUnique(base, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, base...), extra...) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

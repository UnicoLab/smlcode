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
	// ShellAllowed lists the quality pack's safe_prefixes merged into
	// cfg.ShellAllow, so the CLI can show which toolchain the pack unlocked.
	ShellAllowed []string `json:"shell_allowed,omitempty"`
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
		// A pack's safe_prefixes were previously inert: nothing merged them into
		// the shell allow list, so `npx tsc --noEmit` or `dotnet test` — the very
		// commands the pack tells the tester to run — were refused by the shell
		// guard as unapproved executors. Applying a pack is the operator's
		// explicit opt-in to that language's toolchain, so the prefixes land in
		// ShellAllow here.
		if len(q.Spec.SafePrefixes) > 0 {
			cfg.ShellAllow = mergeUnique(cfg.ShellAllow, q.Spec.SafePrefixes)
			res.ShellAllowed = append([]string{}, q.Spec.SafePrefixes...)
		}
		res.QualityID = pack.Spec.Quality
	}

	if opts.MaterializeAgents {
		written, err := materializeAgents(cfg, reg, pack.Spec.Agents, opts.ForceAgents)
		if err != nil {
			return res, err
		}
		res.AgentsWritten = append(res.AgentsWritten, written...)
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
	res := &ApplyResult{
		PipelineID:   pipe.ID,
		PipelinePath: pipeline.Path(cfg.SlmDir()),
	}
	// Materialize agents referenced by the pipeline spec so the runtime picks
	// them up immediately. Existing .slmcode/agents files are left untouched.
	written, err := materializeAgents(cfg, reg, referencedAgentIDs(&spec), false)
	if err != nil {
		return res, err
	}
	res.AgentsWritten = written
	return res, nil
}

// referencedAgentIDs collects every agent id referenced by a pipeline config:
// phase agents, execute loop roles, and slot agents.
func referencedAgentIDs(cfg *pipeline.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, ps := range cfg.Phases {
		add(ps.Agent)
	}
	add(cfg.Execute.DefaultRole)
	add(cfg.Execute.Reviewer)
	add(cfg.Execute.Corrector)
	for _, sl := range cfg.Slots {
		add(sl.Agent)
	}
	sort.Strings(out)
	return out
}

// materializeAgents writes the given agent block ids into .slmcode/agents/.
// When force is false, existing files are skipped. Returns the sorted list of
// agent ids written.
func materializeAgents(cfg *config.Config, reg *Registry, ids []string, force bool) ([]string, error) {
	if cfg == nil || reg == nil {
		return nil, fmt.Errorf("config and registry required")
	}
	var written []string
	agentsDir := cfg.AgentsDir()
	for _, aid := range ids {
		ab, ok := reg.GetAgent(aid)
		if !ok {
			continue
		}
		dest := filepath.Join(agentsDir, ab.Spec.ID+".yaml")
		if !force {
			if _, err := os.Stat(dest); err == nil {
				continue
			}
		}
		spec := ab.Spec
		if _, err := agents.WriteCustom(agentsDir, spec); err != nil {
			return written, fmt.Errorf("agent %s: %w", aid, err)
		}
		written = append(written, ab.Spec.ID)
	}
	sort.Strings(written)
	return written, nil
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

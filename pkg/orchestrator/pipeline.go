package orchestrator

import (
	"context"
	"fmt"
	"strings"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Pipeline returns the loaded pipeline config (never nil).
func (o *Orchestrator) Pipeline() *pipeline.Config {
	if o == nil {
		d := pipeline.Default()
		return &d
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pipe == nil {
		d := pipeline.Default()
		o.pipe = &d
	}
	return o.pipe
}

// ReloadPipeline re-reads .slmcode/pipeline.yaml (after Studio edits).
func (o *Orchestrator) ReloadPipeline() error {
	if o == nil || o.cfg == nil {
		return fmt.Errorf("orchestrator not ready")
	}
	cfg, err := pipeline.Load(o.cfg.SlmDir())
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.pipe = cfg
	o.mu.Unlock()
	return nil
}

// SetPipeline saves and activates a new pipeline config.
func (o *Orchestrator) SetPipeline(cfg *pipeline.Config) error {
	if o == nil || o.cfg == nil {
		return fmt.Errorf("orchestrator not ready")
	}
	if cfg == nil {
		return fmt.Errorf("nil pipeline")
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := pipeline.Save(o.cfg.SlmDir(), cfg); err != nil {
		return err
	}
	o.mu.Lock()
	o.pipe = cfg
	o.mu.Unlock()
	return nil
}

func (o *Orchestrator) loadPipelineLocked() {
	if o == nil || o.cfg == nil {
		return
	}
	cfg, err := pipeline.Load(o.cfg.SlmDir())
	if err != nil || cfg == nil {
		d := pipeline.Default()
		o.pipe = &d
		return
	}
	o.pipe = cfg
}

// phaseAgent resolves which registered agent runs a built-in phase.
func (o *Orchestrator) phaseAgent(phase, defaultAgent string) string {
	return o.Pipeline().PhaseAgent(phase, defaultAgent)
}

// phaseEnabled reports whether a built-in phase should execute.
func (o *Orchestrator) phaseEnabled(phase string) bool {
	return o.Pipeline().PhaseEnabled(phase)
}

// runPipelineSlots runs user-inserted agents around a phase anchor.
// position: before | after | replace
func (o *Orchestrator) runPipelineSlots(ctx context.Context, phase, position, query, exploration, planMD string) error {
	slots := o.Pipeline().SlotsAt(phase, position)
	for _, s := range slots {
		if !pipeline.SlotMatchesWhen(s.When, query) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		title := s.Title
		if title == "" {
			title = s.ID
		}
		o.emitFull(phase, stream.KindAgentStart, s.Agent, s.ID,
			fmt.Sprintf("pipeline slot · %s (%s %s)", title, position, phase),
			"slot:"+s.ID, "")
		input := pipeline.RenderSlotInput(s.Input, query, exploration, planMD, phase)
		if sp := strings.TrimSpace(s.SystemPrompt); sp != "" {
			input = "## Slot instructions\n\n" + sp + "\n\n" + input
		}
		var (
			out string
			err error
		)
		if s.Multipass {
			out, err = o.runRoleMultipassTracked(ctx, s.Agent, s.ID, input)
		} else {
			out, err = o.runRoleTracked(ctx, s.Agent, s.ID, input)
		}
		if err != nil {
			o.emitFull(phase, stream.KindAgentEnd, s.Agent, s.ID,
				"slot error: "+err.Error(), "slot:"+s.ID, "")
			if strings.EqualFold(s.FailMode, pipeline.FailAbort) {
				return fmt.Errorf("pipeline slot %s: %w", s.ID, err)
			}
			continue
		}
		o.persistSlotOutput(s, out)
		o.emitFull(phase, stream.KindAgentEnd, s.Agent, s.ID,
			"slot finished", "slot:"+s.ID, truncate(out, 1200))
		o.emitLoop(phase, LoopEvent{
			Action: "slot",
			Reason: fmt.Sprintf("%s · %s %s", title, position, phase),
			From:   phase,
			To:     phase,
			Wave:   o.waveCounter,
		})
	}
	return nil
}

func (o *Orchestrator) persistSlotOutput(s pipeline.Slot, out string) {
	if o == nil || o.store == nil || strings.TrimSpace(out) == "" {
		return
	}
	title := "Pipeline slot: " + s.ID
	switch strings.ToLower(strings.TrimSpace(s.PersistTo)) {
	case pipeline.PersistNone, "":
		if s.PersistTo == pipeline.PersistNone {
			return
		}
		_ = o.store.Append(contextstore.DocScratch, title, truncate(out, 6000))
	case pipeline.PersistScratch:
		_ = o.store.Append(contextstore.DocScratch, title, truncate(out, 6000))
	case pipeline.PersistContext:
		_ = o.store.Append(contextstore.DocContext, title, truncate(out, 4000))
	case pipeline.PersistMemory:
		_ = o.store.Append(contextstore.DocMemory, title, truncate(out, 2000))
	default:
		_ = o.store.Append(contextstore.DocScratch, title, truncate(out, 6000))
	}
}

// knownAgent reports whether a role/agent id is registered (builtin or custom).
func (o *Orchestrator) knownAgent(id string) bool {
	if o == nil || o.factory == nil || id == "" {
		return false
	}
	for _, s := range o.factory.AllSpecs() {
		if s.ID == id {
			return true
		}
	}
	return false
}

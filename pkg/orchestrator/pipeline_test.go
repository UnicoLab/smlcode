package orchestrator

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestPipelineLoadAndPhaseAgent(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := pipeline.EnsureFile(cfg.SlmDir()); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{cfg: cfg}
	o.loadPipelineLocked()
	if o.phaseAgent("plan", "planner") != "planner" {
		t.Fatal(o.phaseAgent("plan", "planner"))
	}
	p := o.Pipeline()
	p.Phases["plan"] = pipeline.PhaseSpec{Agent: "worker", When: pipeline.WhenAlways}
	p.Slots = []pipeline.Slot{{
		ID: "pre-plan", Agent: "explorer", Before: "plan", When: pipeline.WhenAlways,
	}}
	if err := o.SetPipeline(p); err != nil {
		t.Fatal(err)
	}
	if o.phaseAgent("plan", "planner") != "worker" {
		t.Fatal(o.phaseAgent("plan", "planner"))
	}
	slots := o.Pipeline().SlotsAt("plan", "before")
	if len(slots) != 1 || slots[0].Agent != "explorer" {
		t.Fatalf("%+v", slots)
	}
}

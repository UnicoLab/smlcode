package readiness

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

var servedModels = []string{
	"Qwen3-Coder-30B-A3B-Instruct-MLX-4bit",
	"Qwen3.5-9B-MLX-4bit",
	"Qwen3.8-27B-4bit",
}

// model_roles and model_escalation are not exercised until a run REACHES that
// role — minutes in, after real work. A typo there used to pass readiness at
// 100/100 and then fail at the reviewer.
func TestUnservedRoutedModelsAreReported(t *testing.T) {
	cfg := &config.Config{
		ModelRoles: map[string]string{
			"reviewer": "Qwen3.5-9B-MLX-4bti", // transposed: 4bti, not 4bit
			"worker":   "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit",
		},
		ModelEscalation: []string{"NoSuchModel-70B"},
	}
	missing := unservedRoutedModels(cfg, servedModels)
	if len(missing) != 2 {
		t.Fatalf("got %d unserved, want 2: %v", len(missing), missing)
	}
	joined := strings.Join(missing, " | ")
	// The message has to say WHICH role or rung breaks, or it names a string
	// the operator then has to go hunting for.
	for _, want := range []string{"Qwen3.5-9B-MLX-4bti", "@reviewer", "NoSuchModel-70B", "escalation rung 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "Qwen3-Coder-30B") {
		t.Errorf("a served model was reported unserved: %q", joined)
	}
}

func TestFullyServedRoutingReportsNothing(t *testing.T) {
	cfg := &config.Config{
		ModelRoles:      map[string]string{"reviewer": "Qwen3.5-9B-MLX-4bit"},
		ModelEscalation: []string{"Qwen3.8-27B-4bit"},
	}
	if missing := unservedRoutedModels(cfg, servedModels); len(missing) != 0 {
		t.Fatalf("got %v, want none", missing)
	}
}

// No routing configured is the common case and must stay silent.
func TestNoRoutingIsNotAFinding(t *testing.T) {
	if missing := unservedRoutedModels(&config.Config{}, servedModels); len(missing) != 0 {
		t.Fatalf("got %v, want none", missing)
	}
	if missing := unservedRoutedModels(nil, servedModels); len(missing) != 0 {
		t.Fatalf("got %v for a nil config", missing)
	}
}

// The report is read by a human and diffed across runs, so map iteration order
// must not reach it.
func TestUnservedRoutedModelsIsStable(t *testing.T) {
	cfg := &config.Config{ModelRoles: map[string]string{
		"reviewer": "missing-a", "worker": "missing-b", "tester": "missing-c",
	}}
	first := strings.Join(unservedRoutedModels(cfg, servedModels), "|")
	for i := 0; i < 25; i++ {
		if got := strings.Join(unservedRoutedModels(cfg, servedModels), "|"); got != first {
			t.Fatalf("unstable ordering: %q then %q", first, got)
		}
	}
}

// A model named by two different roles is one finding that names both.
func TestOneModelNamedBySeveralRoles(t *testing.T) {
	cfg := &config.Config{ModelRoles: map[string]string{
		"reviewer": "missing-model", "tester": "missing-model",
	}}
	missing := unservedRoutedModels(cfg, servedModels)
	if len(missing) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(missing), missing)
	}
	if !strings.Contains(missing[0], "@reviewer") || !strings.Contains(missing[0], "@tester") {
		t.Errorf("the finding does not name both roles: %q", missing[0])
	}
}

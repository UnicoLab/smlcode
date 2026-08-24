package autoresearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Everything in this file exists so the tests never touch a model, never run
// the real eval suite and never write outside t.TempDir().

const testWorkerYAML = `id: worker
title: Worker
# this comment must survive an edit
system_prompt: |-
  Do the task.
  Keep diffs small.
skills:
  - specialist-worker
tools: true
max_iter: 12
temperature: 0.2
max_tokens: 3072
`

const testConfigYAML = `provider: omlx
model: test-model
api_key: super-secret
permission: auto
think_passes: 1
`

// newTestProject builds a throwaway .slmcode workspace with one agent and one
// config file.
func newTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".slmcode", "agents")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(agentsDir, "worker.yaml"), testWorkerYAML)
	writeFile(t, filepath.Join(root, ".slmcode", "config.yaml"), testConfigYAML)
	return root
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustReflect(t *testing.T, root string) *Surface {
	t.Helper()
	s, err := Reflect(Options{Root: root})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	return s
}

// scriptEvaluator returns a canned score per call, so a test states the shape
// of the experiment (improves / regresses / panics) instead of simulating a
// model well enough to produce it.
type scriptEvaluator struct {
	scores  []Score
	errs    []error
	panicAt int // 1-based call index that panics; 0 = never
	onCall  func(call int)
	calls   int
}

func (e *scriptEvaluator) Evaluate(ctx context.Context) (Score, error) {
	e.calls++
	if e.onCall != nil {
		e.onCall(e.calls)
	}
	if e.panicAt == e.calls {
		panic("evaluator exploded")
	}
	i := e.calls - 1
	if i < len(e.errs) && e.errs[i] != nil {
		return Score{}, e.errs[i]
	}
	switch {
	case len(e.scores) == 0:
		return Score{}, nil
	case i < len(e.scores):
		return e.scores[i], nil
	default:
		return e.scores[len(e.scores)-1], nil
	}
}

// healthy is a score with every guarded metric present and unremarkable, so a
// test only has to state the field it is actually exercising.
func healthy(primary float64) Score {
	return Score{
		Primary:        primary,
		TokensPerTask:  1000,
		SecondsPerTask: 10,
		ToolErrorRate:  0.05,
		EditApplyRate:  0.90,
		Tokens:         500,
	}
}

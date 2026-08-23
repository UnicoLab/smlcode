package blocks

import (
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// builtinRegistry loads ONLY the embedded blocks, so a stray YAML in the
// developer's ~/.slmcode or in this repo's .slmcode cannot make the suite pass
// or fail for reasons that have nothing to do with what ships in the binary.
func builtinRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := reg.loadBuiltin(); err != nil {
		t.Fatalf("loadBuiltin: %v", err)
	}
	return reg
}

func TestBundledBlocksValidate(t *testing.T) {
	reg := builtinRegistry(t)
	if len(reg.Packs) == 0 || len(reg.Quality) == 0 || len(reg.Pipelines) == 0 {
		t.Fatal("no bundled blocks embedded")
	}
	for id, b := range reg.Pipelines {
		if err := b.Validate(); err != nil {
			t.Errorf("pipeline %s: %v", id, err)
		}
	}
	for id, b := range reg.Agents {
		if err := b.Validate(); err != nil {
			t.Errorf("agent %s: %v", id, err)
		}
	}
	for id, b := range reg.Quality {
		if err := b.Validate(); err != nil {
			t.Errorf("quality %s: %v", id, err)
		}
	}
	for id, b := range reg.Packs {
		if err := b.Validate(); err != nil {
			t.Errorf("pack %s: %v", id, err)
			continue
		}
		if err := reg.ResolvePackRefs(b); err != nil {
			t.Errorf("pack %s: %v", id, err)
		}
	}
}

// TestBundledQualityCommandsAreRunnable is the load-bearing one.
//
// A quality pack's commands are handed to the tester as instructions and to the
// QA gate as a command to execute. The shell layer refuses command
// substitution outright and requires an explicit allow-list entry for anything
// that can run arbitrary code (npx, pnpm, dotnet, bundle …). A pack that names
// a command the harness will refuse is worse than a pack with no command at
// all: the model burns turns being told "no" and then invents a workaround.
//
// Applying a pack merges its safe_prefixes into cfg.ShellAllow, so the bar is:
// every command must be safe under the builtin allow-list PLUS its own pack's
// declared prefixes — and nothing may use shell syntax that is refused outright.
func TestBundledQualityCommandsAreRunnable(t *testing.T) {
	reg := builtinRegistry(t)
	for _, id := range sortedKeys(reg.Quality) {
		q := reg.Quality[id]
		prefixes := workspace.SafePrefixes(q.Spec.SafePrefixes)
		check := func(kind, cmd string) {
			t.Helper()
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				return
			}
			if reason, unsafe := workspace.UnsafeShellSyntax(cmd); unsafe {
				t.Errorf("quality %s: %s command %q uses refused shell syntax\n  %s", id, kind, cmd, reason)
				return
			}
			if !workspace.IsSafeBash(cmd, prefixes) {
				t.Errorf("quality %s: %s command %q is not allowed by the builtin "+
					"allow-list or this pack's safe_prefixes (%v) — the harness would "+
					"refuse to run it", id, kind, cmd, q.Spec.SafePrefixes)
			}
		}
		for _, c := range q.Spec.Format {
			check("format", c.Cmd)
		}
		for _, c := range q.Spec.Lint {
			check("lint", c.Cmd)
		}
		for _, c := range q.Spec.Typecheck {
			check("typecheck", c.Cmd)
		}
		for _, c := range q.Spec.Test {
			check("test", c.Cmd)
		}
		for _, c := range q.Spec.Build {
			check("build", c.Cmd)
		}
		check("smoke", q.Spec.Smoke)
		check("qa_gate", q.Spec.QAGate)
	}
}

// TestBundledQualityShape asserts the fields the runtime actually depends on.
func TestBundledQualityShape(t *testing.T) {
	reg := builtinRegistry(t)
	for _, id := range sortedKeys(reg.Quality) {
		q := reg.Quality[id]
		if q.PrimaryQAGate() == "" {
			t.Errorf("quality %s: no qa_gate, smoke or test command — nothing gates a run", id)
		}
		if strings.TrimSpace(q.Spec.TesterHints) == "" {
			t.Errorf("quality %s: no tester_hints — the tester gets no ecosystem guidance", id)
		}
		if q.Spec.Detect.Priority < 0 {
			continue // deliberate opt-out from auto-detection
		}
		if len(q.Spec.Detect.Files) == 0 && len(q.Spec.Detect.Extensions) == 0 {
			t.Errorf("quality %s: detect matches nothing, so the pack can never be auto-selected", id)
		}
	}
}

// TestBundledPacksReferenceRealSkills catches a pack pinning a skill that does
// not ship. ResolvePackRefs checks pipelines, agents and quality but not
// skills, so a typo there fails silently at runtime as a missing pin.
func TestBundledPacksReferenceRealSkills(t *testing.T) {
	dir := t.TempDir()
	if err := skills.MaterializeBundled(dir); err != nil {
		t.Fatalf("materialize skills: %v", err)
	}
	loaded, err := skills.NewLoader(dir).List()
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	known := map[string]bool{}
	for _, s := range loaded {
		known[strings.ToLower(s.Name)] = true
	}
	if len(known) == 0 {
		t.Fatal("no bundled skills found")
	}
	reg := builtinRegistry(t)
	for _, packID := range sortedKeys(reg.Packs) {
		for _, name := range reg.Packs[packID].Spec.Skills {
			if !known[strings.ToLower(name)] {
				t.Errorf("pack %s pins skill %q, which does not ship", packID, name)
			}
		}
	}
	for _, agentID := range sortedKeys(reg.Agents) {
		for _, name := range reg.Agents[agentID].Spec.Skills {
			if !known[strings.ToLower(name)] {
				t.Errorf("agent %s references skill %q, which does not ship", agentID, name)
			}
		}
	}
}

// TestBundledPipelineAgentsExist verifies every agent id a pipeline names is
// either a bundled agent block or a builtin specialist. A pipeline pointing at
// a nonexistent role silently falls back to the generic worker.
func TestBundledPipelineAgentsExist(t *testing.T) {
	reg := builtinRegistry(t)
	builtin := agents.BuiltinIDs()
	for _, id := range sortedKeys(reg.Pipelines) {
		spec := reg.Pipelines[id].Spec
		for _, aid := range referencedAgentIDs(&spec) {
			if aid == "" || builtin[aid] {
				continue
			}
			if _, ok := reg.GetAgent(aid); !ok {
				t.Errorf("pipeline %s references unknown agent %q", id, aid)
			}
		}
	}
}

// TestEveryPackHasAWorkerAndTester keeps a pack from shipping half-wired: the
// point of a language pack is that the execute loop and the test phase get the
// language-aware roles, not the generic ones.
func TestEveryPackHasAWorkerAndTester(t *testing.T) {
	reg := builtinRegistry(t)
	for _, id := range sortedKeys(reg.Packs) {
		p := reg.Packs[id]
		if p.Spec.OverrideWorker == "" {
			t.Errorf("pack %s: no override_worker", id)
		}
		if p.Spec.OverrideTester == "" {
			t.Errorf("pack %s: no override_tester", id)
		}
		if p.Spec.Pipeline == "" {
			t.Errorf("pack %s: no pipeline — override_worker/tester are only applied "+
				"through one, so they would be inert", id)
		}
		if p.Spec.Quality == "" {
			t.Errorf("pack %s: no quality block", id)
		}
	}
}

// TestLanguagePackCoverage documents the languages the harness claims to
// support. It fails when a pack is removed, so the README and docs cannot drift
// away from what actually ships.
func TestLanguagePackCoverage(t *testing.T) {
	want := []string{
		"cpp", "dotnet", "go", "java", "kotlin", "php", "python",
		"react", "ruby", "rust", "swift", "typescript", "web",
	}
	reg := builtinRegistry(t)
	have := sortedKeys(reg.Packs)
	missing := []string{}
	haveSet := map[string]bool{}
	for _, id := range have {
		haveSet[id] = true
	}
	for _, w := range want {
		if !haveSet[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing language packs %v (have: %v)", missing, have)
	}
}

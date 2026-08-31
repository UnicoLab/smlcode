package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// ── The team path against a real small model ────────────────────────────────
//
// The offline suites prove the mechanism; this proves it survives contact with
// a 30B, which is the only model class this harness is for.
//
// What it is actually checking is the one claim the team library makes:
//
//	The decision every later phase is derived from — which teams exist, what
//	they own, who staffs them — no longer depends on a small model emitting
//	valid JSON with non-overlapping globs and agent ids that exist.
//
// So the assertions are split in two. The org chart is asserted STRICTLY: it
// comes from the library, it is deterministic, and a miss is a bug in this
// feature. The contract is asserted LENIENTLY: it is the one part still left to
// the model, and a 30B that returns a thin contract has produced a run that
// still works — it is the seam quality that degrades, not the run.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestTeamsLive -timeout 90m -v
//	RUN_E2E=1 SLMCODE_MODEL=Qwen3-Coder-Next-MLX-4bit go test ./test/e2e/ -run TestTeamsLive -timeout 90m -v

const teamsLiveDefaultModel = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"

// teamsLiveMarker is grepped out of `go test -v` output when comparing models.
const teamsLiveMarker = "E2E-TEAMS-ROW"

// A fixture with two genuine halves and nothing else: the smallest workspace in
// which "which teams does this involve" has a right answer.
var teamsLiveFixture = map[string]string{
	"go.mod":             "module todo\n\ngo 1.22\n",
	"cmd/server/main.go": "package main\n\nfunc main() {}\n",
	"web/package.json":   "{\n  \"name\": \"web\",\n  \"private\": true\n}\n",
	"web/src/App.tsx":    "export default function App() {\n  return null;\n}\n",
	"AGENTS.md": "# Agents\n\nA tiny Go + React todo app. Make the smallest change that works.\n" +
		"Go code lives under cmd/. The React client lives under web/src/.\n",
}

func TestTeamsLiveAgainstASmallModel(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit a local model")
	}

	root := t.TempDir()
	for rel, body := range teamsLiveFixture {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default(root)
	cfg.Model = teamsLiveDefaultModel
	if m := strings.TrimSpace(os.Getenv("SLMCODE_MODEL")); m != "" {
		cfg.Model = m
	}
	cfg.Verbose = true
	cfg.DryRun = false
	cfg.Squads = true
	cfg.TeamLibrary = true
	cfg.DynamicPipeline = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 2
	cfg.TaskTimeout = liveTaskTimeout()
	cfg.AutoApprove = true
	cfg.ClarifyMode = "off"
	cfg.ContinueAsk = "auto"
	cfg.EscalateAsk = "auto"
	cfg.Normalize()
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("no local model API key — set OMLX_API_KEY or configure ~/.omlx/settings.json")
	}

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if serr := h.SetOrchestrator(orch); serr != nil {
		t.Fatalf("install orchestrator: %v", serr)
	}
	defer func() { _ = h.Close() }()

	// The event stream, kept. Without it a failed live run is undiagnosable:
	// the board says a task never left ready_to_dev and nothing anywhere says
	// WHY — which round it stopped on, which gate rejected, whether the wave
	// budget ran out. Two runs were spent guessing at that before this existed.
	var mu sync.Mutex
	var log []string
	// Set after the run so the dump can also fire when the HARNESS reports
	// failure, not only when an assertion here does. A run that finishes with
	// most of its board untouched is the case worth reading, and it does not
	// necessarily break an invariant.
	harnessFailed := false
	orch.OnEvent(func(e orchestrator.Event) {
		mu.Lock()
		defer mu.Unlock()
		line := e.Phase + "/" + e.Kind
		if e.Agent != "" {
			line += " @" + e.Agent
		}
		if e.TaskID != "" {
			line += " [" + e.TaskID + "]"
		}
		log = append(log, line+": "+firstLineOf(e.Message))
	})
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		// Only on failure: a green run's stream is a thousand lines nobody
		// reads, and burying the one that matters is how a log stops being read
		// at all.
		if !t.Failed() && !harnessFailed {
			return
		}
		t.Logf("── event stream (%d) ──", len(log))
		for _, line := range log {
			t.Log("  " + line)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), liveBudget())
	defer cancel()

	started := time.Now()
	res, err := h.Run(ctx, "Add a todos list: a Go HTTP handler that serves the todos as JSON, "+
		"and a React component that fetches and renders them.")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	harnessFailed = !res.Success
	t.Logf("model=%s elapsed=%s success=%v tasks=%d failed=%d",
		cfg.Model, elapsed.Round(time.Second), res.Success, len(res.Board.Tasks), res.FailedTasks)

	// ── The org chart: strict. This is the part that no longer needs a model.
	slmDir := filepath.Join(root, ".slmcode")
	p, ok, lerr := squads.Load(slmDir)
	if lerr != nil || !ok {
		t.Fatalf("no team plan on disk: ok=%v err=%v", ok, lerr)
	}
	ids := p.IDs()
	if len(ids) < 2 {
		t.Fatalf("teams = %v — the library must preselect both halves of this request "+
			"deterministically, with no model call", ids)
	}
	for _, pr := range p.Validate() {
		if pr.Severity == squads.SeverityError {
			t.Errorf("the composed plan does not validate: %s", pr)
		}
	}
	// Ownership is what the write deny list and task routing are derived from.
	for path, wantPrefix := range map[string]string{
		"cmd/server/main.go": "backend",
		"web/src/App.tsx":    "frontend",
	} {
		owner, owned := p.Owner(path)
		if !owned || !strings.HasPrefix(owner, wantPrefix) {
			t.Errorf("%s is owned by %q (owned=%v), want a %s team", path, owner, owned, wantPrefix)
		}
	}

	// Routing is asserted as an INVARIANT, not as a count.
	//
	// How many tasks land in a lane depends on whether the splitter produced
	// file-disjoint tasks, which is the model's judgment and not something the
	// harness controls — a model that puts both halves in one task produces a
	// board where every task correctly straddles and none is assigned. That is
	// worth REPORTING (it means the run did no parallel work) and wrong to fail
	// on, or this stops being a harness test and becomes a model-quality gate.
	//
	// What must hold either way: a task stamped with a team is a task that team
	// owns every file of. The wave's write deny list is derived from that
	// stamp, so a violation is a task refused at the tool layer on its own
	// files — work that can never complete.
	bySquad := map[string]int{}
	straddling := 0
	for _, task := range res.Board.Tasks {
		bySquad[task.Squad]++
		t.Logf("task %s role=%-14s squad=%-16s col=%-11s files=%v",
			task.ID, task.Role, task.Squad, task.Column, task.Files)
		if task.Squad == "" {
			if len(task.Files) > 1 && squads.LaneOf(&p, task.Files) == "" {
				straddling++
			}
			continue
		}
		for _, f := range task.Files {
			owner, owned := p.Owner(f)
			if !owned || owner != task.Squad {
				t.Errorf("task %s is stamped %q but %s is owned by %q — the wave would deny "+
					"it write access to its own file", task.ID, task.Squad, f, owner)
			}
		}
	}
	assigned := len(res.Board.Tasks) - bySquad[""]
	t.Logf("routing: %d task(s) in a lane, %d straddling both halves", assigned, straddling)
	if assigned == 0 {
		t.Logf("NOTE: %s split every task across both halves, so the run did no parallel "+
			"work — the org chart and contract were right and unused", cfg.Model)
	}

	// ── The contract: lenient. Still the model's job, and a thin one degrades
	// the seam rather than the run.
	contract, cerr := os.ReadFile(filepath.Join(slmDir, squads.ContractFile)) // #nosec G304 -- temp fixture
	if cerr != nil {
		t.Fatalf("CONTRACT.md must exist alongside the plan: %v", cerr)
	}
	if !strings.Contains(string(contract), "FROZEN") {
		t.Errorf("CONTRACT.md is not the frozen contract:\n%s", contract)
	}
	clauses := len(p.Contract.Interfaces)
	// Whatever the model produced, no clause may name a team that is not on the
	// run — that is the failure this feature's reference resolution exists to
	// prevent, and it is the one contract property asserted strictly.
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	for _, in := range p.Contract.Interfaces {
		if !known[in.Provider] {
			t.Errorf("clause %q names provider %q, which is not a team on this run (%v)",
				in.ID, in.Provider, ids)
		}
		for _, c := range in.Consumers {
			if !known[c] {
				t.Errorf("clause %q names consumer %q, which is not a team on this run", in.ID, c)
			}
		}
	}
	if clauses == 0 {
		t.Logf("NOTE: %s froze no interfaces — the run works, the seam is unprotected", cfg.Model)
	}

	// ── The work: both halves have to have been touched.
	touched := 0
	for _, rel := range []string{"cmd/server/main.go", "web/src/App.tsx"} {
		body, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) // #nosec G304 -- temp fixture
		if rerr != nil {
			t.Errorf("%s disappeared: %v", rel, rerr)
			continue
		}
		if string(body) != teamsLiveFixture[rel] {
			touched++
		}
	}
	if touched == 0 {
		t.Error("neither half was edited — the run produced no work at all")
	}

	t.Logf("%s model=%s teams=%v clauses=%d halves_touched=%d success=%v elapsed=%s",
		teamsLiveMarker, cfg.Model, ids, clauses, touched, res.Success, elapsed.Round(time.Second))
}

// liveTaskTimeout bounds ONE task's model work. A 30B on a busy laptop is slow
// enough that the default would abort honest work.
func liveTaskTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("SLMCODE_E2E_TASK_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 12 * time.Minute
}

// liveBudget is the wall-clock ceiling for the whole run — a backstop against a
// harness that never stops, not a performance target.
func liveBudget() time.Duration {
	if v := strings.TrimSpace(os.Getenv("SLMCODE_E2E_BUDGET")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 60 * time.Minute
}

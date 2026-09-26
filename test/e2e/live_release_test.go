package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/autoconfig"
	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/laya"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/readiness"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// TestLiveReleaseSurface is the check to run before cutting a release, against
// the model server you actually use.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveReleaseSurface -timeout 90m -v
//	make e2e-release                                   # the same thing
//
// The rest of this tree proves each mechanism in isolation, against a fake
// server that returns exactly what the fixture told it to. That is the right
// way to test a parser and the wrong way to answer "does this release work on
// my machine", because the two features this cycle adds fail in ways no
// fixture can produce:
//
//   - `configure` ranks a REAL model list. The fixtures contain the names we
//     thought of; a real server serves the names it was given, and the failure
//     is a config that names an embedding model and a first run that returns
//     no JSON.
//   - a live endpoint can answer `/v1/models` and still not complete a chat —
//     a model listed but not loaded, a context window smaller than the system
//     prompt, a proxy that passes GETs and drops POSTs. Discovery alone would
//     call that machine configured.
//   - squads asks a REAL model for an org chart at temperature. Offline the
//     manager's answer is a const in squads_e2e_test.go and always parses.
//
// So each subtest drives one of those against whatever is actually running,
// and asserts on the mechanism rather than on the model's prose — the same
// model asked twice writes different code, and a test that demands particular
// code is a test that fails for the wrong reason.
//
// This one runs with your REAL environment, not the hermetic one
// binary_acceptance_test.go builds: the credentials live in `~/.omlx`,
// `~/.slmcode` and the environment, and redirecting HOME to hide them would
// test a machine you do not have. Nothing is written outside a temp directory
// — no subtest passes `configure --user`.
func TestLiveReleaseSurface(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to check this release against your own model server")
	}
	// Ordered deliberately: each subtest is the precondition of the next, and
	// "squads produced no code" is a useless report when the real answer is
	// "nothing was listening".
	t.Run("discovery", liveDiscovery)
	t.Run("configure", liveConfigure)
	t.Run("chat", liveChat)
	t.Run("decision_openai", liveDecisionOpenAI)
	t.Run("squads", liveSquads)
}

// liveDiscovery is `slmcode configure`'s engine against the real machine.
func liveDiscovery(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ResolveAPIKey()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res := autoconfig.Discover(ctx, cfg, os.Getenv, autoconfig.HTTPProber(5*time.Second))
	t.Logf("what is running here:\n%s", res.Explain())

	if !res.Found {
		t.Fatalf("nothing to configure against: %s", res.NothingFound())
	}
	c := res.Choice
	if c.Endpoint == "" || c.Model == "" || c.Provider == "" {
		t.Fatalf("Found is true but the choice is incomplete: %+v", c)
	}
	t.Logf("chosen: %s · %s · %s (%s)", c.Provider, c.Endpoint, c.Model, c.Why)

	// The ranking is the whole point of the feature, so check it did its job on
	// a real list rather than merely returning the first name. An embedding or
	// speech model here is the failure this feature exists to prevent, and it
	// is invisible until the first run returns something that is not JSON.
	for _, f := range res.Findings {
		if !f.Live() || f.Endpoint != c.Endpoint {
			continue
		}
		ranked := autoconfig.Rank(f.Models)
		if len(ranked) == 0 {
			t.Fatalf("%s serves %d models and none ranked", f.Endpoint, len(f.Models))
		}
		if !ranked[0].Usable {
			t.Errorf("best of %v is unusable: %+v", f.Models, ranked[0])
		}
		if ranked[0].Name != c.Model {
			t.Errorf("Choose picked %q but Rank's best is %q", c.Model, ranked[0].Name)
		}
		break
	}
}

// liveConfigure drives the CLI command itself — the binary, its flags and the
// bytes it leaves on disk. `configure` is the command a new user runs first,
// and the library being right does not prove the command is wired to it.
func liveConfigure(t *testing.T) {
	dir := t.TempDir()
	mustLiveSlm(t, dir, "init")
	cfgPath := filepath.Join(dir, ".slmcode", "config.yaml")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("init did not write a config: %v", err)
	}

	// --dry-run must decide everything and write nothing. A "preview" that
	// edits your config is worse than no preview: you ran it to avoid that.
	var dry struct {
		OK      bool              `json:"ok"`
		Written bool              `json:"written"`
		Choice  autoconfig.Choice `json:"choice"`
		Reason  string            `json:"reason"`
	}
	out := mustLiveSlm(t, dir, "configure", "--json", "--yes", "--dry-run")
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("configure --json is not JSON: %v\n%s", err, out)
	}
	if !dry.OK {
		t.Fatalf("configure found nothing: %s", dry.Reason)
	}
	if dry.Written {
		t.Error(`--dry-run reported "written": true`)
	}
	if dry.Choice.Endpoint == "" || dry.Choice.Model == "" {
		t.Fatalf("incomplete choice: %+v", dry.Choice)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("--dry-run rewrote the config")
	}

	// Now for real.
	var wet struct {
		OK      bool              `json:"ok"`
		Written bool              `json:"written"`
		Scope   string            `json:"scope"`
		Choice  autoconfig.Choice `json:"choice"`
	}
	out = mustLiveSlm(t, dir, "configure", "--json", "--yes")
	if err := json.Unmarshal([]byte(out), &wet); err != nil {
		t.Fatalf("configure --json is not JSON: %v\n%s", err, out)
	}
	if !wet.Written || wet.Scope != "project" {
		t.Errorf("written=%v scope=%q, want true/project", wet.Written, wet.Scope)
	}
	// Default-valued keys can be omitted from the intent-only YAML. Validate
	// the effective configuration reloaded from disk, not literal key presence.
	saved, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Endpoint != wet.Choice.Endpoint || saved.Model != wet.Choice.Model || saved.Provider != wet.Choice.Provider {
		t.Error("effective config does not match the choice reported as written")
	}

	// A working configuration is confirmed, never replaced. Run it again: the
	// endpoint just written is now the configured one, and it is probed first.
	// Without that, `configure` on a working machine is a coin flip between
	// two live servers and yesterday's setup silently moves.
	var again struct {
		Choice autoconfig.Choice `json:"choice"`
	}
	out = mustLiveSlm(t, dir, "configure", "--json", "--yes")
	if err := json.Unmarshal([]byte(out), &again); err != nil {
		t.Fatalf("configure --json is not JSON: %v\n%s", err, out)
	}
	if again.Choice.Endpoint != wet.Choice.Endpoint {
		t.Errorf("configure moved a working endpoint: %q -> %q",
			wet.Choice.Endpoint, again.Choice.Endpoint)
	}

	// --endpoint is an instruction. Pointed at a dead address it must fail,
	// not fall through to whatever else happens to be up.
	if _, code := liveSlm(t, dir, "configure", "--json", "--yes",
		"--endpoint", "http://127.0.0.1:9/v1", "--timeout", "2s"); code == 0 {
		t.Error("configure --endpoint <dead> exited 0")
	}
}

// liveChat is the assertion discovery cannot make: that the endpoint completes
// a chat, not merely that it lists models.
func liveChat(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ResolveAPIKey()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	check := readiness.ProbeProvider(ctx, cfg)
	t.Logf("provider probe: ok=%v %s (%s, %dms) — %s",
		check.OK, check.Label, check.Endpoint, check.Latency, check.Message)
	if !check.OK {
		t.Fatalf("the configured model is unavailable: %s", check.Message)
	}
	manager := llm.NewProviderManager()
	if err := backends.RegisterLLM(manager, cfg); err != nil {
		t.Fatal(err)
	}
	provider, err := manager.GetProvider(cfg.Provider)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()
	response, err := provider.Complete(ctx, llm.CompletionRequest{Model: cfg.Model, MaxTokens: 64,
		Messages: []llm.Message{{Role: "user", Content: "Reply with the single word ready."}}})
	if err != nil {
		t.Fatalf("actual chat completion failed: %v", err)
	}
	if response == nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		t.Fatal("chat endpoint returned no assistant content")
	}
}

// Exercise the optional JSON decision transport with an actual served chat
// model. This checks compatibility, not the accuracy of its numeric judgment.
func liveDecisionOpenAI(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ResolveAPIKey()
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if config.NormalizeProvider(cfg.Provider) == "ollama" {
		endpoint += "/v1"
	}
	client, err := laya.New(laya.Options{Provider: "openai", Endpoint: endpoint, Model: cfg.Model, APIKey: cfg.APIKey, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err = client.Probability(ctx, "Task: fix a JSON parser. Candidate: JSON parser error handling and tests.", "Is the candidate useful for this task?")
	if err != nil {
		t.Fatalf("actual JSON decision failed: %v", err)
	}
}

// liveSquads runs the feature this cycle is named for, end to end, through a
// real model: one query, two languages, a frozen contract between them.
//
// The fixture uses Go and Node's built-in test runner, with no package install — a real React half
// would make this a test of whether npm can reach the registry, which is not
// the thing under test and is the most likely way it would fail on someone
// else's laptop.
//
// The assertions require a successful delivery as well as real teams: a
// contract frozen before anyone started, and nobody writing in anybody else's
// lane.
func liveSquads(t *testing.T) {
	if os.Getenv("RUN_E2E_SQUADS") == "0" {
		t.Skip("RUN_E2E_SQUADS=0")
	}
	root := t.TempDir()
	if artifacts := os.Getenv("SLMCODE_E2E_ARTIFACTS"); artifacts != "" {
		if err := os.MkdirAll(artifacts, 0o750); err != nil {
			t.Fatal(err)
		}
		var err error
		root, err = os.MkdirTemp(artifacts, "squads-")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("retaining live workspace and run diagnostics: %s", root)
	}
	writeFixture(t, root, map[string]string{
		"go.mod":             "module livesquads\n\ngo 1.22\n",
		"cmd/server/main.go": "package main\n\nfunc main() {}\n",
		"web/index.html":     "<!doctype html>\n<title>app</title>\n",
		"web/app.js":         "// client\n",
	})
	before := snapshot(t, root)

	cfg := config.Default(root)
	cfg.DryRun = false
	cfg.Squads = true
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	// Exercise the production retry allowance: a one-retry artificial ceiling
	// prevented the correction loop from recovering ordinary model mistakes.
	cfg.TaskTimeout = 10 * time.Minute
	cfg.PlanApprove = "auto"
	cfg.ClarifyMode = "auto"
	cfg.EscalateAsk = "off"
	cfg.ContinueAsk = "off"
	cfg.AutoApprove = true
	cfg.ResolveAPIKey()
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
	h.Orchestrator = orch

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	const query = "Serve a counter from a Go net/http server in cmd/server and drive it " +
		"from the static page in web/ with fetch(). The Go half owns cmd/ and " +
		"internal/; the web half owns web/ and must stay plain HTML and JavaScript " +
		"with no build step, no npm and no framework."
	res, err := h.Run(ctx, query)
	if err != nil && !isDeadline(err) {
		t.Fatalf("live squads run: %v", err)
	}
	if res != nil {
		t.Logf("outcome=%s success=%v failed=%d unexecuted=%d in %s",
			res.Outcome, res.Success, res.FailedTasks, res.UnexecutedTasks, res.Duration)
		t.Logf("summary:\n%s", res.Summary)
	}

	if outcomeErr := liveDeliveryError(res, err); outcomeErr != nil {
		t.Error(outcomeErr)
	}

	slmDir := cfg.SlmDir()
	p, ok, err := squads.Load(slmDir)
	if err != nil {
		t.Fatalf("squads plan on disk is unreadable: %v", err)
	}
	if !ok {
		t.Skip("the manager returned a single squad — a valid answer, but there " +
			"is no cross-team behavior to assert here")
	}
	if problems := p.Validate(); problems.Errors() {
		t.Fatalf("a plan that was ACTIVATED does not validate:\n%s",
			strings.Join(problems.Strings(), "\n"))
	}
	t.Logf("squads: %v", p.IDs())

	// The contract has to be a file, frozen before any worker ran: two squads
	// working concurrently cannot ask each other what the seam is.
	contract, err := os.ReadFile(filepath.Join(slmDir, squads.ContractFile))
	if err != nil {
		t.Fatalf("%s must exist for an activated plan: %v", squads.ContractFile, err)
	}
	if len(p.Contract.Interfaces) == 0 {
		t.Error("two squads were activated with no interface between them")
	}
	for _, iface := range p.Contract.Interfaces {
		if !strings.Contains(string(contract), iface.ID) {
			t.Errorf("%s does not name the %q interface", squads.ContractFile, iface.ID)
		}
	}

	// Ownership is enforced, not requested. Every file the run touched must
	// belong to the squad that touched it — a write outside a lane is how one
	// team silently discards the other's work, and it is the single failure
	// that makes concurrent teams worse than a single stream.
	changed := changedFiles(t, root, before)
	t.Logf("%d files written", len(changed))
	if len(changed) == 0 {
		t.Fatal("the run wrote nothing")
	}
	var unowned []string
	for _, rel := range changed {
		if strings.HasPrefix(rel, ".slmcode/") {
			continue // the harness's own bookkeeping, owned by nobody
		}
		if _, owned := p.Owner(rel); !owned {
			unowned = append(unowned, rel)
		}
	}
	// A file no squad owns is not automatically a violation — the integration
	// step legitimately writes at the seam — but every one of them should be
	// explainable, so name them rather than hiding them.
	if len(unowned) > 0 {
		t.Logf("written outside every squad's lane (integration seam, or a leak): %v", unowned)
	}
	for _, rel := range changed {
		owner, owned := p.Owner(rel)
		if !owned {
			continue
		}
		for _, s := range p.Squads {
			if s.ID != owner && s.OwnsPath(rel) {
				t.Errorf("%s is owned by both %q and %q — Validate should have refused this plan",
					rel, owner, s.ID)
			}
		}
	}
}

// --- helpers -------------------------------------------------------------

// liveEnv keeps the real environment, unlike fixtureEnv: the point of these
// tests is the machine's own model server and its own credentials. Only the
// interactive/TUI knobs are forced, so the output is scriptable.
func liveEnv(t *testing.T) []string {
	t.Helper()
	return append(os.Environ(),
		"NO_COLOR=1",
		"SLMCODE_TUI=0",
		"CI=true",
		"SLMCODE_SKIP_UPDATE_CHECK=1",
	)
}

func liveSlm(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	return slmWithEnv(t, dir, liveEnv(t), args...)
}

func mustLiveSlm(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code := liveSlm(t, dir, args...)
	if code != 0 {
		t.Fatalf("slmcode %s exited %d:\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// liveDeliveryError prevents a structurally valid but unsuccessful run from
// passing release qualification. Keep checking the artifacts after reporting it.
func liveDeliveryError(res *orchestrator.Result, err error) error {
	if err != nil {
		return fmt.Errorf("live delivery did not complete: %w", err)
	}
	if res == nil {
		return errors.New("live delivery returned no result")
	}
	if !res.Success || res.FailedTasks != 0 || res.UnexecutedTasks != 0 {
		return fmt.Errorf("live delivery failed qualification: outcome=%s failed=%d unexecuted=%d: %s", res.Outcome, res.FailedTasks, res.UnexecutedTasks, res.Summary)
	}
	return nil
}

func TestLiveDeliveryQualificationRequiresSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  *orchestrator.Result
		err     error
		wantErr bool
	}{
		{name: "no result", wantErr: true},
		{name: "failed delivery", result: &orchestrator.Result{Success: false}, wantErr: true},
		{name: "failed tasks", result: &orchestrator.Result{Success: true, FailedTasks: 1}, wantErr: true},
		{name: "unfinished tasks", result: &orchestrator.Result{Success: true, UnexecutedTasks: 1}, wantErr: true},
		{name: "deadline", result: &orchestrator.Result{Success: true}, err: context.DeadlineExceeded, wantErr: true},
		{name: "completed", result: &orchestrator.Result{Success: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := liveDeliveryError(tc.result, tc.err); (err != nil) != tc.wantErr {
				t.Fatalf("qualification error=%v, want error=%v", err, tc.wantErr)
			}
		})
	}
}

// isDeadline identifies timeouts so their partial artifacts can still be
// inspected. liveDeliveryError above still fails qualification for that run.
func isDeadline(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context canceled")
}

func writeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot hashes every file under root so changedFiles can tell an edit from
// an untouched file. Hashes rather than mtimes: a run that rewrites a file with
// identical bytes did not change it, whatever the timestamp says.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable file is simply "not in the snapshot"
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		sum := sha256.Sum256(body)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out
}

func changedFiles(t *testing.T, root string, before map[string]string) []string {
	t.Helper()
	var changed []string
	for rel, sum := range snapshot(t, root) {
		if before[rel] != sum {
			changed = append(changed, rel)
		}
	}
	sort.Strings(changed)
	return changed
}

package e2e_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The acceptance test for the product.
//
// Every other test in this tree drives Go code: harness_smoke_test.go builds an
// orchestrator in-process, the unit tests exercise one package. None of them
// answers the question a user actually asks — "if I install this binary and type
// `slmcode run`, do my files change?" — because none of them runs the binary.
// A CLI can be broken in ways no library test sees: a flag that never reaches
// the config, an exit code that lies, a summary that claims an edit the tool
// layer refused, a `go:embed` that fails on a fresh clone.
//
// So this builds the real `slmcode` and the real `test/fakemodel`, then drives
// the documented workflow — init → doctor → run → task show → diff (→ apply) —
// against two languages, and asserts on the bytes on disk and on the text the
// binary printed. It needs no model, no network and no API key beyond a
// placeholder, so it runs in CI on every push.
//
// If this test fails, the product is broken, whatever the unit tests say.

const acceptanceTimeout = 5 * time.Minute

// The binaries under test are built once per `go test` process, into a temp
// dir that TestMain removes (see main_test.go).
var (
	buildOnce  sync.Once
	buildDir   string
	slmcodeBin string
	fakeBin    string
	buildErr   error
)

// repoRoot locates the module root from this file's own path, so the test does
// not depend on the working directory `go test` happened to choose.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// buildBinaries compiles slmcode and fakemodel once per test binary.
//
// The BUILD inherits the ambient environment on purpose: a local checkout may
// carry GOFLAGS=-modfile=go.local.mod, and CI carries nothing. What must NOT be
// inherited is that same GOFLAGS when the built binary later shells out inside
// a throwaway fixture module — see fixtureEnv.
func buildBinaries(t *testing.T) (string, string) {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		buildDir, buildErr = os.MkdirTemp("", "slmcode-acceptance-")
		if buildErr != nil {
			return
		}
		for _, spec := range []struct{ out, pkg string }{
			{"slmcode", "./cmd/slmcode"},
			{"fakemodel", "./test/fakemodel"},
		} {
			bin := filepath.Join(buildDir, spec.out)
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			cmd := exec.Command("go", "build", "-o", bin, spec.pkg)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("go build %s: %v\n%s", spec.pkg, err, out)
				return
			}
			if spec.out == "slmcode" {
				slmcodeBin = bin
			} else {
				fakeBin = bin
			}
		}
	})
	if buildErr != nil {
		t.Fatalf("building the binaries under test: %v", buildErr)
	}
	return slmcodeBin, fakeBin
}

// startFakeModel launches test/fakemodel on an ephemeral port and returns its
// /v1 base URL. It reads the port off the process's first stdout line rather
// than hardcoding one, so parallel CI jobs cannot collide.
func startFakeModel(t *testing.T, args ...string) string {
	t.Helper()
	_, fake := buildBinaries(t)

	cmd := exec.Command(fake, append([]string{"-addr", "127.0.0.1:0"}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fakemodel: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	type banner struct {
		line string
		err  error
	}
	ch := make(chan banner, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			ch <- banner{line: sc.Text()}
			// Keep draining so the child never blocks on a full pipe.
			for sc.Scan() { //nolint:revive // intentional drain
			}
			return
		}
		ch <- banner{err: fmt.Errorf("fakemodel printed nothing: %v", sc.Err())}
	}()

	var line string
	select {
	case b := <-ch:
		if b.err != nil {
			t.Fatal(b.err)
		}
		line = b.line
	case <-time.After(30 * time.Second):
		t.Fatal("fakemodel did not announce its address within 30s")
	}

	// "fakemodel listening on http://127.0.0.1:41234 (mode=ok, model=fake-model)"
	i := strings.Index(line, "http://")
	if i < 0 {
		t.Fatalf("cannot parse fakemodel banner: %q", line)
	}
	base := strings.Fields(line[i:])[0]
	hostport := strings.TrimPrefix(base, "http://")
	waitForListener(t, hostport)
	return base + "/v1"
}

func waitForListener(t *testing.T, hostport string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hostport, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("nothing accepted a connection on %s", hostport)
}

// fixtureEnv is the environment the binary under test runs in.
//
// Hermetic on purpose: HOME is redirected because the cross-project memory,
// repair rules and bandit policy live there and this test must not read or
// write the developer's real ~/.slmcode. GOFLAGS/GOWORK are cleared because a
// checkout's -modfile shim would follow the binary into the fixture module and
// break every `go` command the harness runs there.
func fixtureEnv(t *testing.T, home string) []string {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{"PATH": true, "SYSTEMROOT": true, "TMPDIR": true, "TEMP": true, "TMP": true}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && keep[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	return append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GOFLAGS=",
		"GOWORK=off",
		// Deterministic, scriptable output: no ANSI, no TUI, no update check.
		"NO_COLOR=1",
		"SLMCODE_TUI=0",
		"CI=true",
		"SLMCODE_SKIP_UPDATE_CHECK=1",
		"OPENAI_API_KEY=acceptance-test-key",
	)
}

// slm runs the binary in the fixture and returns combined output plus exit code.
func slm(t *testing.T, dir, home string, args ...string) (string, int) {
	t.Helper()
	return slmWithEnv(t, dir, fixtureEnv(t, home), args...)
}

// slmWithEnv is slm with the environment chosen by the caller. The hermetic
// fixtureEnv is right for this file and wrong for the live release check, which
// exists to drive the developer's own model server with the developer's own
// credentials (see live_release_test.go).
func slmWithEnv(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	bin, _ := buildBinaries(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("slmcode %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// mustSlm fails the test when the command did not exit 0, showing its output.
func mustSlm(t *testing.T, dir, home string, args ...string) string {
	t.Helper()
	out, code := slm(t, dir, home, args...)
	if code != 0 {
		t.Fatalf("slmcode %s exited %d:\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func requireContains(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s does not mention %q:\n%s", what, w, body)
		}
	}
}

// deterministicRun turns off everything that needs a human, a second opinion or
// an external toolchain, so the assertions are about the harness rather than
// about whether a Go compiler happens to work inside a temp module.
var deterministicRun = [][2]string{
	{"qa_gate", "false"},
	{"post_worker_smoke", "false"},
	{"require_smoke", "false"},
	{"scope_judge", "false"},
	{"placeholder_pass", "false"},
	{"dynamic_pipeline", "false"},
	{"structured_decoding", "off"},
	{"clarify_mode", "off"},
	{"plan_approve", "auto"},
	{"continue_ask", "off"},
	{"escalate_ask", "off"},
	{"max_parallel", "1"},
	{"max_retries", "1"},
	{"think_passes", "1"},
}

func configure(t *testing.T, dir, home string, extra ...[2]string) {
	t.Helper()
	for _, kv := range append(append([][2]string{}, deterministicRun...), extra...) {
		mustSlm(t, dir, home, "config", "set", kv[0], kv[1])
	}
}

// TestRealBinaryEditsAGoProject is the whole product in one test: the shipped
// binary, a real project on disk, and a model that follows the tool contract.
func TestRealBinaryEditsAGoProject(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a full run")
	}
	endpoint := startFakeModel(t)

	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	write(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.22\n")
	write(t, filepath.Join(dir, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	gitInit(t, dir)

	// ── init ──────────────────────────────────────────────────────────────
	out := mustSlm(t, dir, home, "init", "--provider", "openai", "--model", "fake-model", "--endpoint", endpoint)
	requireContains(t, "init output", out, "workspace ready", ".slmcode/.gitignore")
	// pkg/blocks.DetectPack must prove the language from file content, not
	// from the query text.
	if !strings.Contains(out, "pack            go") {
		t.Errorf("init did not detect the Go language pack:\n%s", out)
	}
	// The gitignore init wrote must actually work, checked with real git.
	for _, probe := range []string{
		".slmcode/auth.json", ".slmcode/memory/episodes.jsonl",
		".slmcode/metrics/runs.jsonl", ".slmcode/queries/x/events.jsonl",
	} {
		if !gitIgnoresPath(t, dir, probe) {
			t.Errorf("`git add -A` would stage %s — `slmcode commit` leaks it", probe)
		}
	}

	// ── doctor ────────────────────────────────────────────────────────────
	out = mustSlm(t, dir, home, "doctor")
	requireContains(t, "doctor output", out,
		"LLM ok", "fake-model", ".slmcode workspace initialized", ".slmcode secrets are git-ignored")

	configure(t, dir, home)

	// ── run ───────────────────────────────────────────────────────────────
	out, code := slmTimeout(t, dir, home, acceptanceTimeout, "run", "Add a Divide function to calc.go")
	if code != 0 {
		t.Fatalf("run exited %d (0 = every task done):\n%s", code, out)
	}
	requireContains(t, "the run summary", out,
		"1/1 tasks done", "Changes", "calc.go", "slmcode diff", "slmcode commit")
	if strings.Contains(out, "no files changed") {
		t.Fatalf("the run reported that nothing changed:\n%s", out)
	}

	// The claim in the summary must match the bytes on disk. This is the
	// assertion the whole test exists for: a harness that prints "3 files
	// changed" and writes nothing is the failure mode small local models
	// produce most often.
	body := read(t, filepath.Join(dir, "calc.go"))
	if !strings.Contains(body, "func Divide") {
		t.Fatalf("calc.go on disk has no Divide function — the run summary lied:\n%s", body)
	}
	if !strings.Contains(body, "func Add") {
		t.Fatalf("calc.go lost its existing Add function — the write clobbered the file:\n%s", body)
	}

	// ── task show ─────────────────────────────────────────────────────────
	out = mustSlm(t, dir, home, "task", "show", "T1", "--json")
	var shown struct {
		Task struct {
			ID     string   `json:"id"`
			Column string   `json:"column"`
			Files  []string `json:"files"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("task show --json is not JSON: %v\n%s", err, out)
	}
	if shown.Task.ID != "T1" || shown.Task.Column != "done" {
		t.Errorf("task show reports %s in %q, want T1 in done", shown.Task.ID, shown.Task.Column)
	}
	if len(shown.Task.Files) == 0 || !strings.Contains(strings.Join(shown.Task.Files, ","), "calc.go") {
		t.Errorf("task show lost the focus file: %v", shown.Task.Files)
	}
	// The human-readable form must render the diff of what actually happened.
	out = mustSlm(t, dir, home, "task", "show", "T1")
	requireContains(t, "task show", out, "Scope", "Acceptance criteria", "calc.go")

	// ── diff ──────────────────────────────────────────────────────────────
	out = mustSlm(t, dir, home, "diff", "--stat")
	requireContains(t, "diff --stat", out, "calc.go", "changed 1 file(s)")
	out = mustSlm(t, dir, home, "diff")
	// The rendered diff shows added lines with a "+" gutter and a line number,
	// so assert on the content rather than on a unified-diff prefix.
	requireContains(t, "diff", out, "func Divide", "@@ -1,3 +1,10 @@")

	// ── apply, with nothing pending ───────────────────────────────────────
	// permission=auto writes directly, so the review queue must be EMPTY. An
	// `apply --list` that invented work here would mean edits were staged and
	// silently dropped.
	out = mustSlm(t, dir, home, "apply", "--json")
	var pending struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &pending); err != nil {
		t.Fatalf("apply --json is not JSON: %v\n%s", err, out)
	}
	if pending.Count != 0 {
		t.Errorf("permission=auto left %d edits in the review queue", pending.Count)
	}
}

// TestRealBinaryReviewsATypeScriptProject drives the OTHER half of the
// contract: a second language pack, and permission=review, where a run must
// change nothing on disk until a human says so.
func TestRealBinaryReviewsATypeScriptProject(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries and drives a full run")
	}
	src := filepath.Join(t.TempDir(), "calc.ts")
	write(t, src, "export function add(a: number, b: number): number {\n  return a + b;\n}\n\n"+
		"export function divide(a: number, b: number): number {\n"+
		"  if (b === 0) {\n    throw new Error(\"division by zero\");\n  }\n  return a / b;\n}\n")
	endpoint := startFakeModel(t, "-file", "src/calc.ts", "-source-file", src)

	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	write(t, filepath.Join(dir, "package.json"),
		`{"name":"demo","private":true,"devDependencies":{"typescript":"^5.4.0"},"scripts":{"test":"tsc --noEmit"}}`+"\n")
	write(t, filepath.Join(dir, "tsconfig.json"), `{"compilerOptions":{"target":"ES2020","strict":true,"noEmit":true}}`+"\n")
	write(t, filepath.Join(dir, "src", "calc.ts"),
		"export function add(a: number, b: number): number {\n  return a + b;\n}\n")
	gitInit(t, dir)

	out := mustSlm(t, dir, home, "init", "--provider", "openai", "--model", "fake-model", "--endpoint", endpoint)
	requireContains(t, "init output", out, "workspace ready")
	// A package.json + tsconfig.json project must not be detected as "web" or
	// "javascript": DetectPack proves the language from file content.
	if !strings.Contains(out, "pack            typescript") {
		t.Errorf("init did not detect the TypeScript pack:\n%s", out)
	}

	configure(t, dir, home, [2]string{"permission", "review"})

	before := read(t, filepath.Join(dir, "src", "calc.ts"))
	out, code := slmTimeout(t, dir, home, acceptanceTimeout, "run", "Add a divide function to src/calc.ts")
	if code != 0 {
		t.Fatalf("run exited %d:\n%s", code, out)
	}
	// permission=review: the summary must say the edit is HELD, not applied.
	requireContains(t, "the run summary", out,
		"no files changed", "held for review", "slmcode apply")
	if got := read(t, filepath.Join(dir, "src", "calc.ts")); got != before {
		t.Fatalf("permission=review wrote to the tree anyway:\n%s", got)
	}

	// The queue names the file and the diff.
	out = mustSlm(t, dir, home, "apply", "--json")
	var queue struct {
		Count int `json:"count"`
		Items []struct {
			Path  string `json:"path"`
			Diff  string `json:"diff"`
			Added int    `json:"added"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &queue); err != nil {
		t.Fatalf("apply --json is not JSON: %v\n%s", err, out)
	}
	if queue.Count != 1 || len(queue.Items) != 1 {
		t.Fatalf("review queue holds %d items, want 1:\n%s", queue.Count, out)
	}
	if filepath.ToSlash(queue.Items[0].Path) != "src/calc.ts" {
		t.Errorf("queued path = %q, want src/calc.ts", queue.Items[0].Path)
	}
	if !strings.Contains(queue.Items[0].Diff, "+export function divide") {
		t.Errorf("the queued diff does not contain the proposed edit:\n%s", queue.Items[0].Diff)
	}
	if queue.Items[0].Added < 1 {
		t.Errorf("queued item claims %d added lines", queue.Items[0].Added)
	}

	// Interactive review with no TTY is a documented exit 2, not a hang and
	// not a silent apply.
	if _, code := slm(t, dir, home, "apply"); code != 2 {
		t.Errorf("`slmcode apply` without a TTY exited %d, want the documented 2", code)
	}

	// ── apply ─────────────────────────────────────────────────────────────
	out = mustSlm(t, dir, home, "apply", "--all")
	requireContains(t, "apply --all", out, "src/calc.ts")
	body := read(t, filepath.Join(dir, "src", "calc.ts"))
	if !strings.Contains(body, "export function divide") {
		t.Fatalf("apply --all did not write the proposal:\n%s", body)
	}
	if !strings.Contains(body, "export function add") {
		t.Fatalf("apply --all clobbered the existing code:\n%s", body)
	}
	out = mustSlm(t, dir, home, "apply", "--json")
	if err := json.Unmarshal([]byte(out), &queue); err != nil {
		t.Fatal(err)
	}
	if queue.Count != 0 {
		t.Errorf("%d items still queued after apply --all", queue.Count)
	}

	out = mustSlm(t, dir, home, "diff", "--stat")
	requireContains(t, "diff --stat", out, "calc.ts", "changed 1 file(s)")
}

// slmTimeout is slm with a wall clock, so a wedged run fails with the output it
// produced instead of taking the whole `go test` timeout down with it.
func slmTimeout(t *testing.T, dir, home string, d time.Duration, args ...string) (string, int) {
	t.Helper()
	type result struct {
		out  string
		code int
	}
	ch := make(chan result, 1)
	go func() {
		out, code := slm(t, dir, home, args...)
		ch <- result{out, code}
	}()
	select {
	case r := <-ch:
		return r.out, r.code
	case <-time.After(d):
		t.Fatalf("slmcode %s did not finish within %s", strings.Join(args, " "), d)
		return "", 0
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- temp fixture
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// gitInit makes the fixture a real repository so `slmcode diff` and the
// gitignore probes exercise the git path rather than the checkpoint fallback.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=acceptance@example.invalid", "-c", "user.name=acceptance", "add", "-A"},
		{"-c", "user.email=acceptance@example.invalid", "-c", "user.name=acceptance", "commit", "-qm", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed: %s %v", args, out, err)
		}
	}
}

func gitIgnoresPath(t *testing.T, dir, rel string) bool {
	t.Helper()
	return exec.Command("git", "-C", dir, "check-ignore", "-q", rel).Run() == nil
}

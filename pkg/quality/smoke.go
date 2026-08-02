package quality

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// SmokeResult is the outcome of a deterministic (non-LLM) verification command.
type SmokeResult struct {
	OK      bool
	Ran     bool
	Command string
	Output  string
	Summary string
}

// Section markers embedded into task output for review gates.
const (
	SmokeSectionHeader      = "## Deterministic smoke"
	AcceptanceSectionHeader = "## Acceptance smoke"
	SmokeFailedMarker       = "FAILED"
	SmokePassedMarker       = "PASSED"
)

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
	cmd := DetectPostWorkerCommand(root, t.Files)
	if cmd == "" {
		return SmokeResult{OK: true, Ran: false, Summary: "no smoke command"}
	}
	return RunSmoke(ctx, root, cmd, timeout)
}

// DetectPostWorkerCommand picks a fast per-task smoke command for changed files.
func DetectPostWorkerCommand(root string, files []string) string {
	var py, goFiles, jsFiles []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || strings.Contains(f, "..") {
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
	return ""
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
	if fileExists(filepath.Join(root, "Makefile")) {
		data, _ := os.ReadFile(filepath.Join(root, "Makefile"))
		if bytes.Contains(data, []byte("\ntest:")) || bytes.Contains(data, []byte("\ntest :")) {
			return "make test"
		}
	}
	return ""
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
			idx := strings.Index(lower, p)
			if idx < 0 {
				continue
			}
			cmd := strings.TrimSpace(line[idx:])
			// Cut trailing prose after the command (period+space, " exits", " prints").
			for _, stop := range []string{" exits", " prints", " returns", " —", " - ", ". ", " ("} {
				if i := strings.Index(strings.ToLower(cmd), stop); i >= len(p) {
					cmd = strings.TrimSpace(cmd[:i])
				}
			}
			cmd = strings.TrimRight(cmd, ".,;:")
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

// RunAcceptanceSmoke runs whitelisted acceptance commands; first failure wins.
func RunAcceptanceSmoke(ctx context.Context, root, acceptance string, timeout time.Duration) SmokeResult {
	cmds := ExtractAcceptanceCommands(acceptance)
	if len(cmds) == 0 {
		return SmokeResult{OK: true, Ran: false, Summary: "no acceptance commands"}
	}
	var combined strings.Builder
	for i, cmd := range cmds {
		if boot := BootstrapDeps(root, cmd); boot != "" && i == 0 {
			_ = RunSmoke(ctx, root, boot, timeout) // best-effort install
		}
		sr := RunSmoke(ctx, root, cmd, timeout)
		if combined.Len() > 0 {
			combined.WriteString("\n---\n")
		}
		combined.WriteString(sr.Output)
		if !sr.OK {
			sr.Output = combined.String()
			sr.Summary = fmt.Sprintf("%s: acceptance %s", SmokeFailedMarker, cmd)
			return sr
		}
	}
	return SmokeResult{
		OK: true, Ran: true,
		Command: strings.Join(cmds, " && "),
		Output:  combined.String(),
		Summary: SmokePassedMarker + ": acceptance",
	}
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
		b.WriteString(truncate(sr.Output, 2000))
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

// BootstrapDeps returns a dependency-install command to run before QA, or "".
func BootstrapDeps(root, cmd string) string {
	if root == "" {
		return ""
	}
	lower := strings.ToLower(cmd)
	switch {
	case strings.Contains(lower, "pytest") || strings.Contains(lower, "python"):
		if fileExists(filepath.Join(root, "uv.lock")) {
			return "uv sync"
		}
		if fileExists(filepath.Join(root, "requirements.txt")) {
			return "python -m pip install -q -r requirements.txt"
		}
		if fileExists(filepath.Join(root, "pyproject.toml")) {
			return "python -m pip install -q -e ."
		}
	case strings.Contains(lower, "go test"):
		return "go mod tidy"
	case strings.Contains(lower, "npm"):
		if fileExists(filepath.Join(root, "package.json")) && !dirExists(filepath.Join(root, "node_modules")) {
			return "npm install --no-fund --no-audit"
		}
	}
	return ""
}

// RunSmoke executes command in root and returns a structured result.
func RunSmoke(ctx context.Context, root, command string, timeout time.Duration) SmokeResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return SmokeResult{OK: true, Ran: false, Summary: "empty command"}
	}
	out, err := runCommand(ctx, root, command, timeout)
	res := SmokeResult{Ran: true, Command: command, Output: out}
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
		b.WriteString(SmokePassedMarker)
	} else {
		b.WriteString(SmokeFailedMarker)
	}
	b.WriteString("\ncmd: ")
	b.WriteString(sr.Command)
	b.WriteString("\n")
	if strings.TrimSpace(sr.Output) != "" {
		b.WriteString(truncate(sr.Output, 2000))
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

// SmokePassedInOutput reports a successful deterministic smoke section.
func SmokePassedInOutput(output string) bool {
	idx := strings.Index(output, SmokeSectionHeader)
	if idx < 0 {
		return false
	}
	rest := output[idx:]
	return strings.Contains(rest, SmokePassedMarker) && !strings.Contains(rest, SmokeFailedMarker)
}

// HasSmokeCommand reports whether a post-worker smoke command exists for files.
func HasSmokeCommand(root string, files []string) bool {
	return DetectPostWorkerCommand(root, files) != ""
}

func runCommand(ctx context.Context, root, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if timeout > 8*time.Minute {
		timeout = 8 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-lc", command)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > 20_000 {
		out = out[:20_000] + "\n...[truncated]"
	}
	return out, err
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

func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

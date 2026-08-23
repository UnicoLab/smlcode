package quality

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Pre-QA preparation: formatting and dependency bootstrap.
//
// Both used to be unattended and unbounded:
//
//   - AutoFixFormatting ran `gofmt -w .` and `goimports -w .` against the
//     PROJECT ROOT before the first QA round — every file in the repo, not just
//     the ones an agent touched, outside the focus guard, with no checkpoint and
//     no context timeout. On a repo that was not already gofmt-clean that
//     produced an enormous unrelated diff attributed to the agent run, and
//     `goimports -w` can delete imports that only exist for a build-tagged file
//     it did not parse.
//   - BootstrapDeps proposed `pip install -r requirements.txt`, `pip install -e .`,
//     `uv sync`, `npm install` and `go mod tidy` on manifests frequently written
//     by the worker in the SAME run, so a hallucinated or typosquatted package
//     was fetched and its install scripts executed with nobody asked.
//
// The package now formats only the files a wave changed (snapshotting each one
// first, and with goimports opt-in), and states a dependency-bootstrap policy
// explicitly instead of assuming consent.

// DefaultFormatTimeout bounds a formatting pass.
const DefaultFormatTimeout = 30 * time.Second

// MaxFormatFiles bounds one formatting pass, so a wave that reports hundreds of
// changed files cannot turn into a repo-wide rewrite by another route.
const MaxFormatFiles = 200

// FormatRequest describes a scoped formatting pass.
type FormatRequest struct {
	// Root is the project root. Required.
	Root string
	// Files is the union of the wave's changed files, relative to Root.
	// EMPTY MEANS NOTHING IS FORMATTED — this is deliberate: there is no
	// "format everything" mode any more.
	Files []string
	// Goimports opts into `goimports -w` on the changed Go files. It is off by
	// default because goimports rewrites the import block from the file it can
	// see, and will happily delete an import that only a build-tagged sibling
	// justifies.
	Goimports bool
	// Timeout bounds each formatter invocation (DefaultFormatTimeout when zero).
	Timeout time.Duration
	// Snapshot is called with each relative path BEFORE it is formatted, so the
	// change is undoable. workspace.FileCheckpointer.BackupIfNeeded satisfies
	// it. Nil disables snapshotting (tests, callers with their own checkpoint).
	Snapshot func(rel string)
}

// FormatChangedFiles formats ONLY the listed files and returns a human summary
// ("" when nothing ran). It never touches a path outside Root, a path that does
// not exist, or a path that is not a plain, safe relative path.
func FormatChangedFiles(ctx context.Context, req FormatRequest) string {
	root := strings.TrimSpace(req.Root)
	if root == "" || len(req.Files) == 0 {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultFormatTimeout
	}

	goFiles, pyFiles := splitFormattable(root, req.Files)
	if len(goFiles) == 0 && len(pyFiles) == 0 {
		return ""
	}

	var done []string
	snapshot := func(rels []string) {
		if req.Snapshot == nil {
			return
		}
		for _, rel := range rels {
			req.Snapshot(rel)
		}
	}

	if len(goFiles) > 0 && fileExists(filepath.Join(root, "go.mod")) {
		snapshot(goFiles)
		if out, ok := runFormatter(ctx, root, timeout, "gofmt", append([]string{"-l", "-w"}, goFiles...)); ok {
			if changed := countLines(out); changed > 0 {
				done = append(done, "gofmt: "+strings.Join(strings.Fields(out), " "))
			}
		}
		if req.Goimports {
			if _, err := exec.LookPath("goimports"); err == nil {
				if _, ok := runFormatter(ctx, root, timeout, "goimports", append([]string{"-w"}, goFiles...)); ok {
					done = append(done, "goimports: ok")
				}
			}
		}
	}

	if len(pyFiles) > 0 && fileExists(filepath.Join(root, "pyproject.toml")) {
		if _, err := exec.LookPath("ruff"); err == nil {
			snapshot(pyFiles)
			if _, ok := runFormatter(ctx, root, timeout, "ruff", append([]string{"format"}, pyFiles...)); ok {
				done = append(done, "ruff format: ok")
			}
		}
	}
	return strings.Join(done, "; ")
}

// AutoFixFormatting is the old repo-wide entry point.
//
// Deprecated: it now does NOTHING and returns "". Formatting the whole project
// root before QA is the defect described above, and silently keeping the old
// behavior behind the old name would preserve it. Callers must move to
// FormatChangedFiles with the wave's changed files.
func AutoFixFormatting(root string) string {
	_ = root
	return ""
}

// splitFormattable filters the requested paths down to real, in-root, safe
// files and splits them by language.
func splitFormattable(root string, files []string) (goFiles, pyFiles []string) {
	seen := map[string]bool{}
	for _, f := range files {
		rel := strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(f)), "./"))
		if rel == "" || seen[rel] || !SafeFocusPath(rel) {
			continue
		}
		if !fileExists(filepath.Join(root, filepath.FromSlash(rel))) {
			continue
		}
		seen[rel] = true
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".go":
			goFiles = append(goFiles, rel)
		case ".py":
			pyFiles = append(pyFiles, rel)
		}
	}
	sort.Strings(goFiles)
	sort.Strings(pyFiles)
	if len(goFiles) > MaxFormatFiles {
		goFiles = goFiles[:MaxFormatFiles]
	}
	if len(pyFiles) > MaxFormatFiles {
		pyFiles = pyFiles[:MaxFormatFiles]
	}
	return goFiles, pyFiles
}

// runFormatter executes one formatter with an explicit argv (never a shell) and
// a hard timeout inherited from ctx.
func runFormatter(ctx context.Context, root string, timeout time.Duration, bin string, args []string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...) //nolint:gosec // fixed binary, path-validated args
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), false
	}
	return string(out), true
}

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// BootstrapPolicy states what the harness may do about missing dependencies.
type BootstrapPolicy string

const (
	// BootstrapOff never installs anything.
	BootstrapOff BootstrapPolicy = "off"
	// BootstrapAsk proposes the command and requires an approval decision from
	// the caller's permission layer. It is the DEFAULT: a manifest written by
	// the model in this same run is not consent to execute its install scripts.
	BootstrapAsk BootstrapPolicy = "ask"
	// BootstrapAuto installs without asking.
	BootstrapAuto BootstrapPolicy = "auto"
)

// NormalizeBootstrapPolicy maps free-form config text onto a policy, defaulting
// to BootstrapAsk. Unknown values are NOT treated as "auto".
func NormalizeBootstrapPolicy(s string) BootstrapPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(BootstrapOff), "never", "deny", "no", "false":
		return BootstrapOff
	case string(BootstrapAuto), "always", "allow", "yes", "true":
		return BootstrapAuto
	default:
		return BootstrapAsk
	}
}

// BootstrapPlan is the decision about one dependency bootstrap.
type BootstrapPlan struct {
	// Command is the install command, "" when none applies.
	Command string
	// Manifest is the file that motivated it (relative to root).
	Manifest string
	// Policy is the policy that produced this plan.
	Policy BootstrapPolicy
	// Run reports whether the caller may execute Command unattended.
	Run bool
	// NeedsApproval reports that Command must go through the permission layer
	// before it runs.
	NeedsApproval bool
	// Reason explains a refusal or a pending approval, for the operator.
	Reason string
}

// PlanBootstrap decides what, if anything, to install before running cmd.
//
// It never executes anything. Under BootstrapAsk (the default) it returns
// NeedsApproval so the caller's permission layer owns the decision; only
// BootstrapAuto sets Run.
func PlanBootstrap(root, cmd string, policy BootstrapPolicy) BootstrapPlan {
	command, manifest := bootstrapCommand(root, cmd)
	plan := BootstrapPlan{Command: command, Manifest: manifest, Policy: policy}
	if command == "" {
		return plan
	}
	switch policy {
	case BootstrapOff:
		plan.Command = ""
		plan.Reason = "dependency bootstrap disabled (policy=off): " + command + " was NOT run"
	case BootstrapAuto:
		plan.Run = true
	default:
		plan.NeedsApproval = true
		plan.Reason = "dependency bootstrap needs approval (policy=ask): " + command +
			" — the manifest may have been written by the model in this run"
	}
	return plan
}

// BootstrapDeps returns the dependency-install command that WOULD apply, or "".
//
// It makes no policy decision and grants no permission: callers must gate it
// through PlanBootstrap or their own permission layer before executing it.
func BootstrapDeps(root, cmd string) string {
	command, _ := bootstrapCommand(root, cmd)
	return command
}

// hasCommandWord reports whether hay contains needle on word boundaries.
//
// A plain substring match read `cargo test` as `go test` — "car-GO TEST" — and
// proposed `go mod tidy` inside a Rust project.
func hasCommandWord(hay, needle string) bool {
	for i := 0; ; {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		beforeOK := start == 0 || !isWordByte(hay[start-1])
		afterOK := end == len(hay) || !isWordByte(hay[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
		if i >= len(hay) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || b == '-' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func bootstrapCommand(root, cmd string) (command, manifest string) {
	if root == "" {
		return "", ""
	}
	lower := strings.ToLower(cmd)
	switch {
	case hasCommandWord(lower, "pytest") || hasCommandWord(lower, "python"):
		if fileExists(filepath.Join(root, "uv.lock")) {
			return "uv sync", "uv.lock"
		}
		if fileExists(filepath.Join(root, "requirements.txt")) {
			return "python -m pip install -q -r requirements.txt", "requirements.txt"
		}
		if fileExists(filepath.Join(root, "pyproject.toml")) {
			return "python -m pip install -q -e .", "pyproject.toml"
		}
	case hasCommandWord(lower, "go test"):
		return "go mod tidy", "go.mod"
	case hasCommandWord(lower, "npm"):
		if fileExists(filepath.Join(root, "package.json")) && !dirExists(filepath.Join(root, "node_modules")) {
			return "npm install --no-fund --no-audit", "package.json"
		}
	}
	return "", ""
}

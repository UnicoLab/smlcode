package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// roleKey carries the executing role (the agent id the harness dispatched)
// through context, alongside taskIDKey in loopguard.go.
//
// The write guard needs it for one reason: the refusal it hands back is the
// only thing the model reads, and for a read-only role the decisive fact is
// not "this path is out of scope" but "your role never writes at all".
type roleKey struct{}

// WithRole tags ctx with the role that owns the tool calls made under it.
// pkg/loop sets it when it dispatches a subagent; an untagged context simply
// yields the role-agnostic refusal, which is still actionable.
func WithRole(ctx context.Context, role string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	role = normalizeRole(role)
	if role == "" {
		return ctx
	}
	return context.WithValue(ctx, roleKey{}, role)
}

// RoleFrom returns the role carried by ctx ("" when unset).
func RoleFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(roleKey{}).(string); ok {
		return v
	}
	return ""
}

// readOnlyRoles are the roles whose contract is to READ and REPORT. Some of
// them (explorer, docs) are handed the full coding toolset because they need
// ws_glob/ws_grep/ws_read, and nothing but the prompt stopped them reaching for
// ws_edit — which is exactly the failure this table exists to explain.
//
// Kept as data here rather than imported from pkg/agents: this package is a
// leaf and must stay one.
var readOnlyRoles = map[string]bool{
	"explorer": true, "docs": true, "context": true, "planner": true,
	"splitter": true, "reviewer": true, "reviewer-strict": true,
	"architect": true, "describer": true, "coordinator": true,
	"orchestrator": true, "memory": true, "composer": true, "escalate": true,
}

// roleSuffixes maps a language-specialised agent id (go-explorer,
// python-reviewer) back to its generic role. Mirrors agents.genericRole.
var roleSuffixes = map[string]bool{
	"worker": true, "tester": true, "reviewer": true, "corrector": true,
	"explorer": true, "planner": true, "splitter": true, "architect": true,
	"editor": true, "describer": true, "docs": true, "context": true,
	"placeholder": true, "deep": true,
}

// IsReadOnlyRole reports whether role's contract forbids editing files at all.
func IsReadOnlyRole(role string) bool {
	return readOnlyRoles[genericRoleName(normalizeRole(role))]
}

func normalizeRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

// genericRoleName strips a language prefix ("go-explorer" → "explorer"). Ids
// whose tail is not a known role ("reviewer-strict") are returned unchanged.
func genericRoleName(role string) string {
	if i := strings.LastIndex(role, "-"); i >= 0 {
		if tail := role[i+1:]; roleSuffixes[tail] {
			return tail
		}
	}
	return role
}

// FocusGuard constrains workspace writes to task focus files / packages.
// Safe for concurrent waves: SetWave replaces the active allowlist atomically.
//
// Greenfield / scaffold mode: when focus includes root project files
// (pyproject.toml, package.json, …) or explicit directory prefixes (src/),
// new package trees may be created without treating that as wander.
type FocusGuard struct {
	mu       sync.RWMutex
	enabled  bool
	scaffold bool // allow creating project tree files
	files    map[string]struct{}
	dirs     map[string]struct{}
	// protect is a DENY list that outranks everything above: paths the task was
	// explicitly told not to touch ("do not edit, add or delete any _test.go
	// file"). Unlike focus, it is a boundary rather than an anti-wander
	// heuristic, so it holds even when the allowlist is disabled — and it is
	// deliberately NOT cleared by SetWave, which only rebuilds the allowlist.
	protect []string
}

// NewFocusGuard returns an inactive guard (all writes allowed until SetWave).
func NewFocusGuard() *FocusGuard {
	return &FocusGuard{
		files: map[string]struct{}{},
		dirs:  map[string]struct{}{},
	}
}

// Protect installs the deny list: glob patterns (MatchGlob syntax, so `**` and
// `*` both work) naming paths no task may create, modify or delete. A pattern
// with no `/` also matches at any depth, so `*_test.go` means what a reader
// expects. Calling it replaces any previous list; Protect() with no arguments
// clears it.
//
// The deny list outranks the focus allowlist: a protected path stays refused
// even when it is one of the task's own focus files. That is the point — the
// live failure this exists for was a task that legitimately owned a package and
// was told to leave its tests alone.
func (g *FocusGuard) Protect(patterns ...string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.protect = nil
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			g.protect = append(g.protect, filepath.ToSlash(strings.TrimPrefix(p, "./")))
		}
	}
}

// HasProtections reports whether a deny list is installed. Guards that key off
// "is this workspace constrained at all" must test Enabled() OR this: a task
// can be given protections without a focus allowlist.
func (g *FocusGuard) HasProtections() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.protect) > 0
}

// IsProtected reports whether path matches the deny list.
func (g *FocusGuard) IsProtected(path string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isProtectedLocked(normalizeRel(path))
}

// isProtectedLocked is IsProtected without the lock; callers already hold it.
func (g *FocusGuard) isProtectedLocked(path string) bool {
	if path == "" || len(g.protect) == 0 {
		return false
	}
	for _, pat := range g.protect {
		if MatchGlob(pat, path) {
			return true
		}
		// A bare pattern ("*_test.go", "secrets.yaml") means "at any depth" —
		// the same convenience ws_glob already gives.
		if !strings.Contains(pat, "/") && MatchGlob("**/"+pat, path) {
			return true
		}
	}
	return false
}

// TrackedPaths returns the concrete (non-glob) paths this guard names: the
// focus files plus any literal deny-list entries. The shell scope guard seeds
// its snapshot with them so a watched path is never missed for having no file
// extension (Makefile, Dockerfile).
func (g *FocusGuard) TrackedPaths() []string {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.files)+len(g.protect))
	for f := range g.files {
		out = append(out, f)
	}
	for _, p := range g.protect {
		if !strings.ContainsAny(p, "*?[") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Clear disables focus enforcement (explore / docs / unrestricted phases).
func (g *FocusGuard) Clear() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = false
	g.scaffold = false
	g.files = map[string]struct{}{}
	g.dirs = map[string]struct{}{}
}

// SetWave activates an allowlist from the union of task focus file lists.
// When every list is empty, the guard stays disabled.
func (g *FocusGuard) SetWave(focusLists [][]string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files = map[string]struct{}{}
	g.dirs = map[string]struct{}{}
	g.scaffold = false
	for _, list := range focusLists {
		for _, f := range list {
			f = normalizeRel(f)
			if f == "" || f == "." {
				continue
			}
			// Trailing slash or bare dir name → directory allow.
			if strings.HasSuffix(f, "/") || !strings.Contains(filepath.Base(f), ".") {
				g.dirs[strings.TrimSuffix(f, "/")] = struct{}{}
				continue
			}
			g.files[f] = struct{}{}
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir != "" && dir != "." {
				g.dirs[dir] = struct{}{}
			} else if isRootProjectFile(f) {
				// Root manifest ⇒ greenfield scaffold (src/, tests/, README, …).
				g.scaffold = true
				g.dirs["src"] = struct{}{}
				g.dirs["tests"] = struct{}{}
				g.dirs["test"] = struct{}{}
				g.dirs["lib"] = struct{}{}
				g.dirs["app"] = struct{}{}
				g.dirs["pkg"] = struct{}{}
			}
		}
	}
	g.enabled = len(g.files) > 0 || len(g.dirs) > 0
}

// Enabled reports whether writes are constrained.
func (g *FocusGuard) Enabled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// Allow reports whether a relative path may be written.
func (g *FocusGuard) Allow(path string) bool {
	if g == nil {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	// The deny list is checked FIRST and independently of g.enabled: it is a
	// boundary, not a wander heuristic, so a task with protections but no
	// allowlist is still protected.
	if g.isProtectedLocked(normalizeRel(path)) {
		return false
	}
	if !g.enabled {
		return true
	}
	path = normalizeRel(path)
	if path == "" {
		return false
	}
	// .slmcode/ is harness state, not agent workspace. It used to be
	// unconditionally writable, which let an agent drop a hooks.json (arbitrary
	// bash on the next run), rewrite config.yaml to disable its own guards, or
	// forge pending/*.patch.json. Only the scratch subtree is agent-writable.
	if IsHarnessStatePath(path) {
		return AllowedScratchPath(path)
	}
	if _, ok := g.files[path]; ok {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if _, ok := g.dirs[dir]; ok {
		return true
	}
	for d := range g.dirs {
		if path == d || strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	if g.scaffold && isScaffoldPath(path) {
		return true
	}
	return false
}

// Refusal texts. Each one names WHO is being refused, says the refusal is
// about the path rather than the syntax of the call, and ends with a concrete
// next action.
//
// The old messages said only what was blocked ("stay within focus files /
// their packages"). A 9B explorer read that as an edit-syntax problem: it
// retried ws_edit with more context, then with less, then listed the workspace,
// then read go.mod — six LLM calls and its whole task budget, on a refusal that
// could never be argued with. Naming the role's contract is the decisive fact,
// and "finish now" is the only action that ends the loop.
//
// Style note: these are error strings, so they start lowercase and end without
// punctuation (staticcheck ST1005), like the rest of this file.
const (
	readOnlyRoleRefusal = "out-of-scope write blocked: %s — the %s role does not edit files at all.\n" +
		"This role reads and reports; a later task makes the change. Rewording this call cannot make it work.\n" +
		"Next action: stop calling edit/write tools and finish now — put the file, the symbol and the " +
		"change that is needed in your answer"

	focusFileRefusal = "out-of-scope write blocked: %s — %s writes only inside the task focus files and their packages.\n" +
		"The path is the problem, not the call: rewording it cannot make it work.\n" +
		"Next action: make the change in a focus file. If it genuinely belongs in %s, say so in your answer " +
		"so the planner can rescope — do not repeat this call"

	protectedPathRefusal = "protected-path write blocked: %s — the task explicitly forbids touching this file.\n" +
		"This is not a scope heuristic and not about how the call was worded: the path is off limits for " +
		"the whole task, focus file or not.\n" +
		"Next action: make the change somewhere else, or report in your answer that it cannot be done " +
		"without editing %[1]s — do not repeat this call"

	entrypointRefusal = "out-of-scope write blocked: %s — %s writes only inside the task focus files, and root " +
		"entrypoints are never created here.\n" +
		"The path is the problem, not the call: rewording it cannot make it work.\n" +
		"Next action: make the change in a focus file. If it genuinely belongs in %s, say so in your answer " +
		"so the planner can rescope — do not repeat this call"
)

// Check returns an error when a write is out of scope.
//
// ctx supplies the executing role (see WithRole). A read-only role gets the
// only fact that actually resolves its situation — that this role never edits
// anything, so no reformulation of the call will be accepted — instead of a
// generic scope complaint it will try to satisfy by editing harder.
func (g *FocusGuard) Check(ctx context.Context, path string) error {
	if err := CheckHarnessStateWrite(path); err != nil {
		return err
	}
	if g == nil || g.Allow(path) {
		return nil
	}
	path = normalizeRel(path)
	role := RoleFrom(ctx)
	// A protected path fails for a different reason than a wander, and saying
	// "stay in your focus files" about a file that IS a focus file is the kind
	// of refusal a small model argues with for six turns.
	if g.IsProtected(path) {
		return fmt.Errorf(protectedPathRefusal, path)
	}
	if IsReadOnlyRole(role) {
		return fmt.Errorf(readOnlyRoleRefusal, path, role)
	}
	base := filepath.Base(path)
	if isEntrypointName(base) && !strings.Contains(path, "/") {
		return fmt.Errorf(entrypointRefusal, path, writerName(role), path)
	}
	return fmt.Errorf(focusFileRefusal, path, writerName(role), path)
}

// writerName names the offender for a focus-file refusal. An untagged context
// still produces a sentence that reads correctly.
func writerName(role string) string {
	if role == "" {
		return "this task"
	}
	return "the " + role + " role"
}

// OutOfScopeReason is Check's message for the REVIEW side: the same contract,
// for paths a task claimed to have changed rather than a write it attempted.
// Returns "" for an empty path list.
func OutOfScopeReason(role string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	role = normalizeRole(role)
	list := strings.Join(paths, ", ")
	if IsReadOnlyRole(role) {
		return "out-of-scope files_changed: " + list + " — the " + role + " role does not edit files at all. " +
			"Report what you found instead of editing; the change is a later task's job."
	}
	return "out-of-scope files_changed: " + list + " — edit only the task focus files. " +
		"If the change genuinely belongs elsewhere, say so in your answer so the planner can rescope."
}

// OutOfScopeFiles filters claimed/changed paths that violate the allowlist.
func (g *FocusGuard) OutOfScopeFiles(paths []string) []string {
	// Protections alone are enough to make this meaningful: a task may be given
	// a deny list without an allowlist.
	if g == nil || (!g.Enabled() && !g.HasProtections()) {
		return nil
	}
	var bad []string
	for _, p := range paths {
		p = normalizeRel(p)
		// Harness state is skipped as "not the worker's claim to make" — except
		// when it is protected, which is exactly the claim worth flagging.
		if p == "" || (IsHarnessStatePath(p) && !g.IsProtected(p)) {
			continue
		}
		if !g.Allow(p) {
			bad = append(bad, p)
		}
	}
	return bad
}

func normalizeRel(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." {
		return ""
	}
	return p
}

func isEntrypointName(base string) bool {
	switch strings.ToLower(base) {
	case "main.go", "main.py", "main.ts", "main.js", "main.rs",
		"index.js", "index.ts", "index.tsx", "app.js", "app.ts", "app.tsx",
		"program.cs", "__main__.py":
		return true
	default:
		return false
	}
}

func isRootProjectFile(path string) bool {
	if strings.Contains(path, "/") {
		return false
	}
	switch strings.ToLower(path) {
	case "pyproject.toml", "setup.py", "setup.cfg", "requirements.txt",
		"package.json", "cargo.toml", "go.mod", "pom.xml", "build.gradle",
		"readme.md", "makefile", "cmakelists.txt":
		return true
	default:
		return false
	}
}

func isScaffoldPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "readme.md", "pyproject.toml", "requirements.txt", "setup.py",
		"package.json", "go.mod", "makefile", ".env.example", "license",
		"license.md", "conftest.py", "pytest.ini", "tox.ini", ".gitignore":
		return true
	}
	// main.py at project root is OK for Python MVP entrypoints during scaffold.
	if !strings.Contains(path, "/") && base == "main.py" {
		return true
	}
	for _, prefix := range []string{"src/", "tests/", "test/", "lib/", "app/", "pkg/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// HarnessStateDir is the harness's private control directory.
const HarnessStateDir = ".slmcode"

// ScratchDir is the ONLY path under HarnessStateDir that tools may write.
// Agents use it for notes / todo lists; nothing in it is ever executed or
// interpreted as configuration.
const ScratchDir = ".slmcode/scratch"

// IsHarnessStatePath reports whether rel lives under .slmcode/.
//
// The comparison is CASE-INSENSITIVE. macOS (APFS/HFS+ default) and Windows
// resolve `.SLMCODE/hooks.json` to the same inode as `.slmcode/hooks.json`, so
// a case-sensitive check was a one-keystroke bypass of the whole harness-state
// boundary on two of the three supported platforms: `ws_write .SLMCODE/hooks.json`
// dropped an arbitrary-bash hook and `ws_read .SLMCODE/auth.json` returned the
// operator's API keys. Folding case costs nothing on Linux, where the two names
// are genuinely different files and neither is state we own.
func IsHarnessStatePath(rel string) bool {
	return underFolded(normalizeRel(rel), HarnessStateDir)
}

// AllowedScratchPath reports whether rel is inside the agent scratch subtree.
func AllowedScratchPath(rel string) bool {
	return underFolded(normalizeRel(rel), ScratchDir)
}

// underFolded reports whether rel equals dir or lives under it, ignoring case.
func underFolded(rel, dir string) bool {
	if strings.EqualFold(rel, dir) {
		return true
	}
	return len(rel) > len(dir) && strings.EqualFold(rel[:len(dir)], dir) && rel[len(dir)] == '/'
}

// SecretFileNames are basenames under HarnessStateDir that hold credentials.
// They must never be visible to a tool, in any form, ever.
var SecretFileNames = map[string]bool{"auth.json": true, "credentials.json": true}

// IsHarnessSecretPath reports whether rel names a credential file the harness
// keeps under .slmcode/.
func IsHarnessSecretPath(rel string) bool {
	rel = normalizeRel(rel)
	if !IsHarnessStatePath(rel) {
		return false
	}
	return SecretFileNames[strings.ToLower(filepath.Base(rel))]
}

// CheckHarnessStateRead refuses tool READS of .slmcode/ outside scratch.
//
// Reads used to be unguarded while writes were blocked, so `ws_read
// .slmcode/auth.json` handed the operator's provider API keys straight to the
// model (and from there to the transcript, the session artifacts under
// .slmcode/queries/, and — for a hosted model — to the provider). The same
// applied to ws_grep, ws_glob and ws_list. The read boundary now mirrors the
// write boundary: scratch is the agent's, the rest is the harness's.
func CheckHarnessStateRead(path string) error {
	rel := normalizeRel(path)
	if !IsHarnessStatePath(rel) || AllowedScratchPath(rel) {
		return nil
	}
	if IsHarnessSecretPath(rel) {
		return fmt.Errorf(
			"read refused — %s holds the operator's provider API keys and is never readable by tools.\n"+
				"Nothing in a coding task requires it. If you need scratch space, use %s/",
			rel, ScratchDir)
	}
	return fmt.Errorf(
		"read refused — %s is harness control state (config, hooks, queue, checkpoints), not project source.\n"+
			"Read project files instead; %s/ is the only part of .slmcode/ tools may touch",
		rel, ScratchDir)
}

// HideFromListing reports whether a project-relative path must be omitted from
// ws_list / ws_glob / ws_grep results. Filtering the listing matters as much as
// blocking the read: a directory listing that names auth.json tells a
// prompt-injected model exactly what to go after through the shell.
func HideFromListing(rel string) bool {
	rel = normalizeRel(rel)
	if rel == "" || !IsHarnessStatePath(rel) {
		return false
	}
	return !AllowedScratchPath(rel)
}

// CheckHarnessStateWrite refuses tool writes into .slmcode/ outside scratch.
// This holds even when the focus guard is disabled — it is a privilege
// boundary, not an anti-wander heuristic.
func CheckHarnessStateWrite(path string) error {
	rel := normalizeRel(path)
	if !IsHarnessStatePath(rel) || AllowedScratchPath(rel) {
		return nil
	}
	return fmt.Errorf(
		"write refused — %s is harness control state, not project source.\n"+
			"Files under .slmcode/ (hooks.json, config.yaml, pending/, checkpoints/) configure the "+
			"harness itself and are never edited by tools.\n"+
			"If you need scratch space, write under %s/ instead. "+
			"If you meant to change project code, use the real source path",
		rel, ScratchDir,
	)
}

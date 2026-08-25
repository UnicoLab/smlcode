package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ------------------------------------------------------------------------
// The hole this file closes.
//
// ws_write / ws_edit / ws_patch / ws_mv / ws_delete all call
// Workspace.checkFocus before touching the disk, so the harness's central
// invariant — a worker writes only its focus files — is enforced at the moment
// of the write. ws_shell had NO path guard of any kind: RunBounded
// (runcmd.go) hands the command to bash and the only thing between it and an
// arbitrary write was the upstream command allowlist plus the STATIC redirect
// analysis in GuardShellWrites. Anything opaque — `python fix.py`,
// `gofmt -w ./...`, `make generate`, a test that rewrites a fixture — wrote
// wherever it liked, silently. In a live 9B run whose task said "do not edit,
// add or delete any _test.go file", a protected test file's sha256 changed and
// nothing in the harness noticed.
//
// WHY DETECTION AND NOT PREVENTION, AND WHY NOTHING IS REVERTED
//
// A shell command is an opaque subprocess: by the time we can observe a write
// it has already happened, so there is no "block the call" here the way
// checkFocus blocks a tool call. That leaves three possible responses, and
// this file deliberately implements the third:
//
//  1. Revert every out-of-scope change. REJECTED. A legitimate build writes
//     caches, generated code and lockfiles, and reverting those breaks the
//     command that just succeeded. Worse for correctness: .slmcode/ is written
//     by the HARNESS ITSELF (session logs, wave snapshots, sibling tasks in a
//     parallel wave) while the command runs, so a revert would clobber harness
//     state that the command never touched.
//  2. Silently allow, log somewhere. REJECTED — that is the current bug.
//  3. Never silently allow: surface it. Every out-of-scope change is reported
//     IN BAND to the model (it is the only actor that can undo it) and recorded
//     on the Workspace as gate evidence for the reviewer, which is exactly what
//     the existing scopeOK gate and "## Disk evidence" section are for.
//     Changes to EXPLICITLY PROTECTED paths — harness control state, and files
//     the task was told not to touch — are additionally flagged as violations
//     and raise an intervention, because those are never a build side effect.
//
// COST RULE (stated so it can be argued with rather than discovered):
//
//   - The guard is INERT unless the task actually constrains writes, i.e.
//     FocusGuard.Enabled() or a protected list is set. Explore/docs phases run
//     unrestricted and pay exactly nothing — not one stat call.
//   - One pass fingerprints at most maxScopeScanFiles files, and only files
//     that are (a) named by the focus / protected lists, (b) plausibly source
//     by extension and not a known lockfile or build artifact, or (c) a control
//     file at the TOP LEVEL of .slmcode/. Dependency and generated-output trees
//     (.git, node_modules, vendor, dist, site, .claude, language caches …) are
//     pruned with fs.SkipDir, so the walk never descends into them.
//   - Deeper .slmcode/ state (queries/, waves/, checkpoints/, sessions/) is
//     deliberately NOT scanned: the harness writes there while the command
//     runs, so watching it would report the harness to itself on every call.
//     A guard that cries wolf on every `go test` gets disabled, which is worse
//     than no guard.
//
// Measured on this repository (672 .go files plus config/docs): 970 files
// fingerprinted in ~29 ms per pass, so ~58 ms for the before/after pair —
// against a ws_shell call whose own floor is a bash spawn and whose typical
// body (`go test ./...`) runs for seconds. With the guard inert the minimal
// pass reads 18 entries in ~0.2 ms.
//
// ------------------------------------------------------------------------

// FileFingerprint is the content identity of a file: byte length + sha256 hex.
// It returns "" when the file does not exist or cannot be read, so "" is the
// well-defined "absent" marker in a snapshot map.
//
// The format is byte-identical to pkg/loop's fileFingerprint (runner.go), which
// is the harness's existing deterministic write detector, and to the shape of
// Runner.snapshotTargets' map[rel]fingerprint. It lives HERE because
// pkg/workspace is the leaf package that owns path semantics and pkg/loop
// already imports it. pkg/loop's own copy should become a one-line delegation
// to this function so the harness keeps exactly ONE implementation — see the
// wiring note that accompanies this change.
func FileFingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%d:%s", len(data), hex.EncodeToString(sum[:]))
}

// maxScopeScanFiles bounds ONE snapshot pass. Two passes run per ws_shell call
// (before + after), so the worst case is 2×. The cap exists so a monorepo or a
// checked-in dataset cannot turn a shell call into a tree walk of unbounded
// cost; when it trips, the snapshot is marked truncated and creations /
// deletions are no longer claimed (see diffScopeSnapshots).
const maxScopeScanFiles = 4000

// maxReportedScopePaths caps the in-band notice so it survives the head+tail
// truncation every tool result goes through (capResult). Ten names is already
// more than a model will act on.
const maxReportedScopePaths = 10

// ShellScopeMarker is the in-band header of the out-of-scope notice appended to
// a ws_shell result. Exported so callers and tests can match on it without
// duplicating the prose.
const ShellScopeMarker = "SHELL WRITE OUT OF SCOPE"

// ShellScopeEvidencePrefix / ShellScopeProtectedPrefix start the "## Disk
// evidence" bullets produced by ShellScopeEvidenceLines.
//
// Neither contains one of pkg/loop's evidentialDiskMarkers ("modified:",
// "created/present:", …) on purpose: an out-of-scope shell write is evidence
// AGAINST the task, and must never be miscounted as proof that the task did its
// job.
const (
	ShellScopeEvidencePrefix  = "- out-of-scope shell write: "
	ShellScopeProtectedPrefix = "- PROTECTED-PATH shell write: "
)

// ShellScopeEvent is one file a shell command changed outside the task's focus.
type ShellScopeEvent struct {
	// TaskID is the task that owns the ws_shell call, from the context tag
	// pkg/loop sets per dispatch (WithTaskID). It is "" for an untagged call.
	//
	// It exists because the ledger is per-WORKSPACE while the gates are
	// per-TASK: on the shipped default of MaxParallel=4 four workers share one
	// Workspace, so a drain without attribution would report task A's stray
	// write as task B's scope failure. A false accusation is the one outcome
	// this whole file is written to avoid.
	TaskID string
	// Command is the ws_shell command that ran, truncated for display.
	Command string
	// Path is project-relative and slash-separated.
	Path string
	// Change is one of created | modified | deleted.
	Change string
	// Protected marks harness control state, or a path the task was explicitly
	// told not to touch. Those are violations, not build side effects.
	Protected bool
}

// scopeSnapshot is a bounded fingerprint of the paths this guard watches.
type scopeSnapshot struct {
	files map[string]string // rel (slash) → FileFingerprint
	// scanned counts files actually fingerprinted. Tests assert on it to prove
	// the walk does not touch the whole tree.
	scanned int
	// truncated records that maxScopeScanFiles stopped the pass early.
	truncated bool
}

// scopeChange is one before/after difference.
type scopeChange struct {
	path   string
	change string
}

// scopeGuardActive reports whether this workspace constrains writes at all.
// Everything in this file short-circuits on it: an unconstrained phase pays no
// cost and gets no complaints.
func (w *Workspace) scopeGuardActive() bool {
	if w == nil || w.Root == "" || w.Focus == nil {
		return false
	}
	return w.Focus.Enabled() || w.Focus.HasProtections()
}

// scopeScan fingerprints the watched set.
//
// TWO MODES, and the difference is the whole cost story:
//
//   - MINIMAL (the task does not constrain writes — explore, docs, any phase
//     with the focus guard cleared): only the control files directly under
//     .slmcode/ are fingerprinted. That is one ReadDir of a directory with a
//     couple of dozen entries. Focus scope is genuinely inert here — an
//     unconstrained phase gets no complaints about project files — but the
//     HARNESS-STATE boundary is not a scope heuristic, it is a privilege
//     boundary that CheckHarnessStateWrite already enforces in every mode, and
//     a shell command that drops a hooks.json is arbitrary bash on the next
//     run whatever phase we are in.
//   - FULL (FocusGuard.Enabled() or protections installed): the above plus the
//     bounded source walk described at the top of this file.
func (w *Workspace) scopeScan() *scopeSnapshot {
	if w == nil || w.Root == "" {
		return nil
	}
	snap := &scopeSnapshot{files: map[string]string{}}
	w.scanHarnessControl(snap)
	if !w.scopeGuardActive() {
		return snap
	}
	// Seed with the paths the task itself names. They may have no extension at
	// all (Makefile, Dockerfile) and would otherwise miss the source filter —
	// and a protected path is precisely the one we must not fail to watch.
	for _, rel := range w.Focus.TrackedPaths() {
		snap.record(rel, filepath.Join(w.Root, filepath.FromSlash(rel)))
	}
	_ = filepath.WalkDir(w.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(w.Root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if scopeSkipDirName(d.Name()) {
				return fs.SkipDir
			}
			// Enter .slmcode/ itself (its top-level control files are watched)
			// but never descend: everything below is harness-written churn.
			if IsHarnessStatePath(rel) && !strings.EqualFold(rel, HarnessStateDir) {
				return fs.SkipDir
			}
			return nil
		}
		if _, seen := snap.files[rel]; seen {
			return nil
		}
		if !scopeCandidate(rel) {
			return nil
		}
		if snap.scanned >= maxScopeScanFiles {
			snap.truncated = true
			return fs.SkipAll
		}
		snap.record(rel, path)
		return nil
	})
	return snap
}

// scanHarnessControl fingerprints the escalation targets under .slmcode/.
// A handful of stats, no directory walk, no recursion.
//
// Each is seeded even when ABSENT, so a command that CREATES one is caught and
// not just one that edits an existing file — creating .slmcode/hooks.json is
// the whole attack.
func (w *Workspace) scanHarnessControl(snap *scopeSnapshot) {
	base := filepath.Join(w.Root, HarnessStateDir)
	for name := range watchedControlFiles {
		snap.record(HarnessStateDir+"/"+name, filepath.Join(base, name))
	}
}

// watchedControlFiles are the ONLY files under .slmcode/ this guard watches:
// the ones whose contents grant privilege. hooks.json runs arbitrary bash on
// the next turn, config.yaml / pipeline.yaml can switch the harness's own
// guards off, trust.json blesses a hook file, and the credential files are the
// operator's provider keys.
//
// It is deliberately an ALLOWLIST rather than "every file at depth 1". The rest
// of .slmcode/ — board.json, TASKS.md, SCRATCH.md, checkpoint.json, queries/,
// waves/ — is written by the HARNESS ITSELF while a command runs, so watching
// it would fire on every shell call in a parallel wave and the guard would be
// switched off within a day. Writes to those paths are still refused at the
// tool layer by CheckHarnessStateWrite, and statically for shell redirects by
// GuardShellWrites; what this list adds is detection for the opaque case.
//
// Keep it in step with anything new under .slmcode/ that confers privilege.
var watchedControlFiles = func() map[string]bool {
	m := map[string]bool{
		"hooks.json": true, "config.yaml": true, "config.yml": true,
		"pipeline.yaml": true, "pipeline.yml": true, "trust.json": true,
	}
	for name := range SecretFileNames {
		m[strings.ToLower(name)] = true
	}
	return m
}()

func (s *scopeSnapshot) record(rel, abs string) {
	if rel == "" || s.files == nil {
		return
	}
	if _, ok := s.files[rel]; ok {
		return
	}
	s.scanned++
	s.files[rel] = FileFingerprint(abs)
}

// scopeCandidate decides whether a file is worth fingerprinting.
func scopeCandidate(rel string) bool {
	rel = normalizeRel(rel)
	if rel == "" {
		return false
	}
	if IsHarnessStatePath(rel) {
		// Only the privilege-granting control files directly under .slmcode/;
		// see watchedControlFiles for why this is an allowlist. Scratch is the
		// agent's own, and the walk prunes the deeper subtrees before we get
		// here anyway.
		return !AllowedScratchPath(rel) && strings.Count(rel, "/") == 1 &&
			watchedControlFiles[strings.ToLower(filepath.Base(rel))]
	}
	base := strings.ToLower(filepath.Base(rel))
	if isBuildNoiseName(base) {
		return false
	}
	return sourceishExts[strings.ToLower(filepath.Ext(base))]
}

// scopeSkipDirName extends skipDirName (used by ws_glob / ws_grep) with
// GENERATED-OUTPUT and tooling trees. It is deliberately a separate predicate:
// adding these to skipDirName would hide real files from search, which is a
// different and unwanted behavior change.
func scopeSkipDirName(name string) bool {
	if skipDirName(name) {
		return true
	}
	switch name {
	case "site", "build", "out", "bin", "coverage", "htmlcov", "_build",
		".next", ".nuxt", ".cache", ".gradle", ".idea", ".vscode",
		".ruff_cache", ".terraform", ".claude", ".venv-docs", "__snapshots__":
		return true
	}
	return false
}

// sourceishExts is the "plausibly source" allowlist. Extensions outside it
// (binaries, images, logs, compiled objects) are not fingerprinted at all.
var sourceishExts = map[string]bool{
	".go": true, ".py": true, ".pyi": true, ".js": true, ".jsx": true,
	".ts": true, ".tsx": true, ".mjs": true, ".cjs": true, ".vue": true,
	".rs": true, ".java": true, ".kt": true, ".kts": true, ".scala": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".cs": true,
	".rb": true, ".php": true, ".swift": true, ".m": true, ".mm": true,
	".sh": true, ".bash": true, ".zsh": true, ".sql": true, ".proto": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".cfg": true, ".md": true, ".rst": true, ".html": true, ".css": true,
	".scss": true, ".tf": true, ".gradle": true, ".mod": true,
}

// buildNoiseNames are files that legitimate build tooling rewrites as a matter
// of course. `go test` and `go build` touch go.sum; npm touches its lockfile.
// Reporting those would train everyone to ignore this guard.
//
// go.mod is NOT here: rewriting it is a deliberate act (`go mod tidy`), not a
// side effect, and an out-of-focus go.mod change is worth a reviewer's time.
var buildNoiseNames = map[string]bool{
	"go.sum": true, "go.work.sum": true, "package-lock.json": true,
	"yarn.lock": true, "pnpm-lock.yaml": true, "poetry.lock": true,
	"cargo.lock": true, "pipfile.lock": true, "composer.lock": true,
	"gemfile.lock": true, "uv.lock": true, ".ds_store": true,
	"coverage.out": true, "coverage.xml": true, "junit.xml": true,
	"tags": true, "npm-debug.log": true,
}

// buildNoiseSuffixes catch editor droppings and tool leftovers.
var buildNoiseSuffixes = []string{"~", ".orig", ".rej", ".bak", ".swp", ".tmp", ".log"}

func isBuildNoiseName(lowerBase string) bool {
	if buildNoiseNames[lowerBase] {
		return true
	}
	for _, s := range buildNoiseSuffixes {
		if strings.HasSuffix(lowerBase, s) {
			return true
		}
	}
	return false
}

// diffScopeSnapshots returns the before→after differences, sorted by path.
//
// When either pass was truncated the two walks may have covered different
// prefixes of the tree, so a path present on one side only proves nothing —
// creations and deletions are suppressed and only same-path content changes are
// reported. Silence about a fact we cannot establish beats a false accusation.
func diffScopeSnapshots(before, after *scopeSnapshot) []scopeChange {
	if before == nil || after == nil {
		return nil
	}
	bothComplete := !before.truncated && !after.truncated
	var out []scopeChange
	for rel, prev := range before.files {
		cur, present := after.files[rel]
		switch {
		case !present:
			if bothComplete && prev != "" {
				out = append(out, scopeChange{path: rel, change: "deleted"})
			}
		case prev != "" && cur == "":
			out = append(out, scopeChange{path: rel, change: "deleted"})
		case prev == "" && cur != "":
			out = append(out, scopeChange{path: rel, change: "created"})
		case prev != cur:
			out = append(out, scopeChange{path: rel, change: "modified"})
		}
	}
	for rel, cur := range after.files {
		if _, seen := before.files[rel]; seen || cur == "" {
			continue
		}
		if bothComplete {
			out = append(out, scopeChange{path: rel, change: "created"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// reportShellScope compares the tree against the pre-command snapshot, records
// every out-of-scope change on the workspace, and returns the notice to append
// to the tool result ("" when there is nothing to say).
//
// It runs on EVERY execution path, including a failed or timed-out command: a
// command that exits non-zero can still have written half a tree.
func (w *Workspace) reportShellScope(ctx context.Context, command string, before *scopeSnapshot) string {
	if before == nil {
		return ""
	}
	after := w.scopeScan()
	if after == nil {
		return ""
	}
	var events []ShellScopeEvent
	shown := truncateSnippet(strings.TrimSpace(command), 120)
	taskID := TaskIDFrom(ctx)
	for _, c := range diffScopeSnapshots(before, after) {
		protected := IsProtectedPath(w.Focus, c.path)
		// Focus.Allow is the same predicate the five file-writing tools are
		// held to. A nil / disabled guard allows everything, which is why the
		// protected check above is separate and unconditional.
		if !protected && w.Focus.Allow(c.path) {
			continue
		}
		events = append(events, ShellScopeEvent{
			TaskID:    taskID,
			Command:   shown,
			Path:      c.path,
			Change:    c.change,
			Protected: protected,
		})
	}
	if len(events) == 0 {
		return ""
	}
	w.recordShellScope(events)
	notice := shellScopeNotice(ctx, shown, events)
	// A protected-path write is a violation, not a report: raise it the same
	// way every other harness gate refusal is raised, so the TUI / Studio and
	// the operator see it even if the model ignores the text.
	for _, e := range events {
		if e.Protected {
			w.intervene("shell_scope_violation", notice)
			break
		}
	}
	return notice
}

// IsProtectedPath reports whether rel is a path no task may write: harness
// control state outside scratch, or an explicit task-level protection.
func IsProtectedPath(g *FocusGuard, rel string) bool {
	rel = normalizeRel(rel)
	if IsHarnessStatePath(rel) && !AllowedScratchPath(rel) {
		return true
	}
	return g.IsProtected(rel)
}

// shellScopeNotice renders the in-band message. Like every other refusal in
// this package it names WHO is being told off, states the decisive fact, and
// ends with one concrete next action — a bare complaint makes a small model
// re-run the command harder.
func shellScopeNotice(ctx context.Context, command string, events []ShellScopeEvent) string {
	var b strings.Builder
	protected := 0
	for _, e := range events {
		if e.Protected {
			protected++
		}
	}
	fmt.Fprintf(&b, "\n\n%s — `%s` changed %d file(s) %s does not own.\n",
		ShellScopeMarker, command, len(events), writerName(RoleFrom(ctx)))
	for i, e := range events {
		if i >= maxReportedScopePaths {
			fmt.Fprintf(&b, "  …and %d more\n", len(events)-maxReportedScopePaths)
			break
		}
		if e.Protected {
			fmt.Fprintf(&b, "  ! PROTECTED %s (%s) — this path is off limits to every task\n", e.Path, e.Change)
			continue
		}
		fmt.Fprintf(&b, "  - out of focus: %s (%s)\n", e.Path, e.Change)
	}
	b.WriteString(
		"Nothing was reverted: once the process has exited a build's own output is " +
			"indistinguishable from a stray write, and reverting would break the command that just ran.\n" +
			"The change is on the record either way — the reviewer is shown it as disk evidence.\n")
	if protected > 0 {
		b.WriteString(
			"Next action: put the protected file(s) back exactly as they were with ws_edit " +
				"(ws_* writes are checkpointed and reviewable; shell writes are not), then say in your " +
				"answer what the command did. Do not re-run it.")
		return b.String()
	}
	b.WriteString(
		"Next action: if that was not intended, undo it with ws_edit/ws_write. If the change was " +
			"genuinely required, say so in your answer so the planner can rescope — do not re-run the " +
			"command to \"fix\" it.")
	return b.String()
}

// recordShellScope appends to the ledger the caller drains for gate evidence.
func (w *Workspace) recordShellScope(events []ShellScopeEvent) {
	if w == nil || len(events) == 0 {
		return
	}
	w.scopeMu.Lock()
	defer w.scopeMu.Unlock()
	w.scopeEvents = append(w.scopeEvents, events...)
}

// ShellScopeEvents returns a copy of the recorded out-of-scope shell writes
// without clearing them.
func (w *Workspace) ShellScopeEvents() []ShellScopeEvent {
	if w == nil {
		return nil
	}
	w.scopeMu.Lock()
	defer w.scopeMu.Unlock()
	return append([]ShellScopeEvent(nil), w.scopeEvents...)
}

// TakeShellScopeEvents drains the ledger. This is the seam pkg/loop wires into
// its per-task gate pass: call it after the worker turn, feed the result to
// ShellScopeEvidenceLines for the "## Disk evidence" section, and treat any
// event with Protected set as a scope failure.
func (w *Workspace) TakeShellScopeEvents() []ShellScopeEvent {
	if w == nil {
		return nil
	}
	w.scopeMu.Lock()
	defer w.scopeMu.Unlock()
	out := w.scopeEvents
	w.scopeEvents = nil
	return out
}

// ShellScopeEvidenceLines renders events as "## Disk evidence" bullets, sorted
// and de-duplicated so the same file reported by two commands appears once per
// (path, change) pair.
func ShellScopeEvidenceLines(events []ShellScopeEvent) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		prefix := ShellScopeEvidencePrefix
		if e.Protected {
			prefix = ShellScopeProtectedPrefix
		}
		line := fmt.Sprintf("%s%s (%s by `%s`)", prefix, e.Path, e.Change, e.Command)
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

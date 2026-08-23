package blocks

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Language detection for quality packs.
//
// The old scorer stat'd every marker file and then walked the whole tree once
// PER quality block, counting any file with a matching extension anywhere
// underneath. Two things went wrong with that on real repositories:
//
//   - A polyglot repo (this one: a Go module with a Vite app in web/) had its
//     nested sub-project's .ts/.tsx files counted toward the ROOT language
//     score, so a big enough frontend could out-vote the backend's go.mod.
//   - Marker files and stray source files were weighted almost the same
//     (10 vs 2×3), so "some .py files exist somewhere" nearly equalled "this
//     repo has a pyproject.toml".
//
// The scorer below fixes both:
//
//	root marker file present     +MarkerScore   (12)   — strong
//	content proof (Contains)     +ContentScore  (25)   — strongest
//	source file with our ext     +2 each, capped at 3  — weak tiebreak
//	                             + Detect.Priority
//
// and the extension walk skips any subdirectory that carries its own
// project-marker file, because that directory is a separate project whose files
// say nothing about the root's language. `DetectQualityForPath` uses the same
// rule in reverse to answer per-path questions in a polyglot repo.
const (
	// MarkerScore is awarded per root marker file that exists.
	MarkerScore = 12
	// ContentScore is awarded per satisfied Detect.Contains entry.
	ContentScore = 25
	// ExtScore is awarded per source file with a matching extension.
	ExtScore = 2
	// MaxExtHits caps how many matching source files can score.
	MaxExtHits = 3
	// maxContainsBytes bounds how much of a marker file is read for Contains.
	maxContainsBytes = 256 << 10
	// maxScanEntries bounds the extension walk on very large trees.
	maxScanEntries = 40000
)

// subProjectMarkers name files that make a directory its own project root.
// A nested directory holding one of these is skipped by the extension walk —
// its sources belong to that sub-project, not to the outer one.
var subProjectMarkers = map[string]bool{
	"go.mod":           true,
	"cargo.toml":       true,
	"package.json":     true,
	"pyproject.toml":   true,
	"pom.xml":          true,
	"build.gradle":     true,
	"build.gradle.kts": true,
	"composer.json":    true,
	"gemfile":          true,
	"package.swift":    true,
	"pubspec.yaml":     true,
}

// skipDirNames are dependency, cache and build output directories. Files inside
// them are never evidence of anything (a .venv full of .py, a node_modules full
// of .js, a target/ full of Rust build artifacts).
var skipDirNames = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".slmcode": true, ".idea": true,
	"node_modules": true, "bower_components": true, "vendor": true,
	"__pycache__": true, ".tox": true, ".nox": true, ".mypy_cache": true,
	".pytest_cache": true, ".ruff_cache": true, ".venv": true, "venv": true,
	"env": true, "site-packages": true, ".eggs": true,
	"target": true, "dist": true, "build": true, "out": true, "bin": true,
	"obj": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".gradle": true, ".m2": true, ".cargo": true, ".dart_tool": true,
	"coverage": true, ".terraform": true, "Pods": true, ".build": true,
	".stack-work": true, ".bundle": true,
}

func skipDir(name string) bool {
	if skipDirNames[name] {
		return true
	}
	// .venv-3.12, .venv-ci, …
	return strings.HasPrefix(name, ".venv-")
}

// workspaceScan is the single filesystem pass shared by every quality block's
// detect spec, so adding a language pack costs no extra tree walks.
type workspaceScan struct {
	root      string
	rootNames map[string]bool // lowercase dir-entry names at the root
	extCount  map[string]int  // extension → matching files found (capped)
	contains  map[string]bool // "file|needle" → satisfied
}

func newWorkspaceScan(root string, exts map[string]bool) *workspaceScan {
	s := &workspaceScan{
		root:      root,
		rootNames: map[string]bool{},
		extCount:  map[string]int{},
		contains:  map[string]bool{},
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return s
	}
	for _, e := range entries {
		s.rootNames[strings.ToLower(e.Name())] = true
	}
	if len(exts) > 0 {
		s.walkExtensions(exts)
	}
	return s
}

func (s *workspaceScan) walkExtensions(exts map[string]bool) {
	seen := 0
	saturated := func() bool {
		for e := range exts {
			if s.extCount[e] < MaxExtHits {
				return false
			}
		}
		return true
	}
	_ = filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == s.root {
				return nil
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if hasSubProjectMarker(path) {
				// Separate project: its sources describe ITS language, not ours.
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxScanEntries {
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == "" || !exts[ext] {
			return nil
		}
		if s.extCount[ext] < MaxExtHits {
			s.extCount[ext]++
			if saturated() {
				return fs.SkipAll
			}
		}
		return nil
	})
}

// hasSubProjectMarker reports whether dir is the root of its own project.
func hasSubProjectMarker(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if subProjectMarkers[strings.ToLower(e.Name())] {
			return true
		}
	}
	return false
}

// hasRootFile reports whether a Detect.Files entry matches at the root. Entries
// may be a plain name ("go.mod"), a glob ("*.csproj") or a relative path
// ("src/main.rs").
func (s *workspaceScan) hasRootFile(entry string) bool {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return false
	}
	if strings.ContainsAny(entry, "*?[") {
		matches, err := filepath.Glob(filepath.Join(s.root, filepath.FromSlash(entry)))
		return err == nil && len(matches) > 0
	}
	if strings.ContainsAny(entry, "/\\") {
		_, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(entry)))
		return err == nil
	}
	return s.rootNames[strings.ToLower(entry)]
}

// containsMatch reports whether file (root-relative) holds any of the needles.
func (s *workspaceScan) containsMatch(file string, needles []string) bool {
	key := file + "|" + strings.Join(needles, "|")
	if v, ok := s.contains[key]; ok {
		return v
	}
	ok := false
	if s.hasRootFile(file) {
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(file))) //nolint:gosec // inside the user's own project root
		if err == nil {
			if len(data) > maxContainsBytes {
				data = data[:maxContainsBytes]
			}
			body := strings.ToLower(string(data))
			for _, n := range needles {
				n = strings.ToLower(strings.TrimSpace(n))
				if n != "" && strings.Contains(body, n) {
					ok = true
					break
				}
			}
		}
	}
	s.contains[key] = ok
	return ok
}

// score rates one detect spec against the scan. 0 means "no evidence".
func (s *workspaceScan) score(d DetectSpec) int {
	score := 0
	for _, f := range d.Files {
		if s.hasRootFile(f) {
			score += MarkerScore
		}
	}
	for file, needles := range d.Contains {
		if s.containsMatch(file, needles) {
			score += ContentScore
		}
	}
	// Source files are a weak tiebreak, capped in TOTAL rather than per
	// extension: a pack listing six extensions must not out-score a pack
	// listing one purely by listing more.
	hits := 0
	for _, e := range d.Extensions {
		hits += s.extCount[normalizeExt(e)]
	}
	if hits > MaxExtHits {
		hits = MaxExtHits
	}
	score += ExtScore * hits
	if score == 0 {
		return 0
	}
	return score + d.Priority
}

func normalizeExt(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if e == "" {
		return ""
	}
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return e
}

// allExtensions is the union of every extension any quality block cares about,
// so one walk answers every block's question.
func (r *Registry) allExtensions() map[string]bool {
	out := map[string]bool{}
	for _, q := range r.Quality {
		for _, e := range q.Spec.Detect.Extensions {
			if n := normalizeExt(e); n != "" {
				out[n] = true
			}
		}
	}
	return out
}

// LanguageMatch is one scored quality pack for a workspace.
type LanguageMatch struct {
	Quality *QualityBlock `json:"-"`
	ID      string        `json:"id"`
	Score   int           `json:"score"`
}

// DetectAll returns every quality pack with evidence in the workspace, best
// first. A polyglot repo returns more than one entry — callers that must pick a
// single answer take the head, callers that can do per-path work use
// DetectQualityForPath instead of pretending one answer fits the whole tree.
func (r *Registry) DetectAll(workspaceRoot string) []LanguageMatch {
	if r == nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	scan := newWorkspaceScan(workspaceRoot, r.allExtensions())
	var out []LanguageMatch
	for _, q := range r.Quality {
		if q.Spec.Detect.Priority < 0 {
			continue // opt-out: never auto-selected
		}
		if p := scan.score(q.Spec.Detect); p > 0 {
			out = append(out, LanguageMatch{Quality: q, ID: q.ID, Score: p})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID // stable, never map-order dependent
	})
	return out
}

// DetectQuality picks the best quality pack for a workspace root.
func (r *Registry) DetectQuality(workspaceRoot string) *QualityBlock {
	matches := r.DetectAll(workspaceRoot)
	if len(matches) == 0 {
		return nil
	}
	return matches[0].Quality
}

// DetectQualityForPath answers "which language pack governs THIS file?" in a
// polyglot repo. It walks up from the file toward the workspace root and stops
// at the nearest directory that is a project of its own (web/package.json under
// a Go module), scoring that directory instead of the repo root. Falling back
// to the file's own extension keeps a lone .py script under a Go repo from
// being verified with `go test`.
func (r *Registry) DetectQualityForPath(workspaceRoot, relPath string) *QualityBlock {
	if r == nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	rel := filepath.FromSlash(strings.TrimSpace(relPath))
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return r.DetectQuality(workspaceRoot)
	}
	// Nearest enclosing sub-project directory (exclusive of the root).
	dir := filepath.Dir(filepath.Join(workspaceRoot, rel))
	for len(dir) > len(workspaceRoot) {
		if hasSubProjectMarker(dir) {
			if q := r.DetectQuality(dir); q != nil {
				return q
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// No enclosing sub-project: prefer the pack that claims this extension over
	// the repo-wide answer, when exactly one pack claims it.
	if q := r.qualityForExtension(filepath.Ext(rel)); q != nil {
		return q
	}
	return r.DetectQuality(workspaceRoot)
}

// qualityForExtension returns the highest-priority pack claiming an extension,
// or nil when no pack (or more than one equally ranked pack) claims it.
func (r *Registry) qualityForExtension(ext string) *QualityBlock {
	ext = normalizeExt(ext)
	if ext == "" {
		return nil
	}
	var best *QualityBlock
	bestPri := 0
	tie := false
	for _, q := range r.Quality {
		if q.Spec.Detect.Priority < 0 {
			continue
		}
		claims := false
		for _, e := range q.Spec.Detect.Extensions {
			if normalizeExt(e) == ext {
				claims = true
				break
			}
		}
		if !claims {
			continue
		}
		switch {
		case best == nil || q.Spec.Detect.Priority > bestPri:
			best, bestPri, tie = q, q.Spec.Detect.Priority, false
		case q.Spec.Detect.Priority == bestPri:
			tie = true
		}
	}
	if tie {
		return nil
	}
	return best
}

// DetectsIn reports whether a quality block's detect spec matches the workspace.
// Used to avoid applying a stale active_pack (e.g. "python" from a previous run)
// onto a workspace whose files no longer match that language.
func (b *QualityBlock) DetectsIn(workspaceRoot string) bool {
	if b == nil || strings.TrimSpace(workspaceRoot) == "" {
		return false
	}
	if b.Spec.Detect.Priority < 0 {
		return false
	}
	exts := map[string]bool{}
	for _, e := range b.Spec.Detect.Extensions {
		if n := normalizeExt(e); n != "" {
			exts[n] = true
		}
	}
	return newWorkspaceScan(workspaceRoot, exts).score(b.Spec.Detect) > 0
}

// DetectPack returns the id of the language pack that best fits a workspace,
// or "" when nothing matches.
//
// This is the ONE detection answer callers should use. Several call sites grew
// their own marker lists (an init-time list of filenames in the CLI, a second
// one in the smoke package) and they disagree: a bare package.json is "web" to
// one and "typescript" to another, so `init` writes an active_pack that the
// very next run overrides. Detection belongs where the packs are defined,
// because only here is it impossible to name a pack that does not exist.
func DetectPack(projectRoot, workspaceRoot string) string {
	reg, err := Load(projectRoot)
	if err != nil || reg == nil {
		return ""
	}
	return reg.DetectPack(workspaceRoot)
}

// DetectPack resolves the best-matching quality block to the pack that owns it.
func (r *Registry) DetectPack(workspaceRoot string) string {
	if r == nil {
		return ""
	}
	for _, match := range r.DetectAll(workspaceRoot) {
		if id := r.packForQuality(match.ID); id != "" {
			return id
		}
	}
	return ""
}

// packForQuality finds the pack referencing a quality block. Iteration over the
// pack map is sorted so two packs sharing a quality id cannot make the answer
// depend on map ordering — a same-input-different-output bug that would only
// ever show up as a flaky test.
func (r *Registry) packForQuality(qualityID string) string {
	// A pack whose own id matches the quality id is the canonical owner.
	if p, ok := r.Packs[qualityID]; ok && p.Spec.Quality == qualityID {
		return p.ID
	}
	for _, id := range sortedKeys(r.Packs) {
		if r.Packs[id].Spec.Quality == qualityID {
			return id
		}
	}
	return ""
}

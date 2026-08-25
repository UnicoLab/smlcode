package loop

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// ------------------------------------------------------------------------
// Turning a task's own prose into an ENFORCED deny list.
//
// workspace.FocusGuard.Protect has been enforced by Allow/Check (ws_edit
// refuses with "protected-path write blocked") and by the ws_shell scope
// detector since shellscope.go landed. Nothing populated it. This file is what
// populates it.
//
// The live failure: a 9B run under a task whose text said "Do not edit, add or
// delete any _test.go file" changed a _test.go file's sha256 anyway. The
// instruction was in the prompt in plain English and the harness had no
// mechanism that turned it into enforcement — the model was the only thing
// standing between the instruction and the disk, and a 9B model is not that.
//
// THE ASYMMETRY THAT DECIDES EVERY RULE BELOW
//
// A MISSED protection costs exactly what the harness costs today: the write
// happens and the reviewer sees it as ordinary disk evidence. A FALSE
// protection makes the task IMPOSSIBLE — every ws_edit to the file is refused
// with a message that explicitly says rewording cannot help, so the worker
// burns its whole call budget and escalates to a human. The two errors are not
// symmetric, so every rule here is written to fail towards "extract nothing".
//
// Concretely that means:
//
//   - Only the task's OWN fields are read (title, instructions, acceptance).
//     The scoped context pack is stripped first: it embeds repository source,
//     and a `// Code generated ... DO NOT EDIT.` header inside a quoted file
//     would otherwise protect a path the task never mentioned.
//   - A prohibition clause containing "unless" / "except" / "other than" is
//     DISCARDED whole. Those words carve out an exception this code cannot
//     model, and half-reading a conditional instruction is worse than not
//     reading it.
//   - Only file names and file globs are extracted. A bare directory
//     ("do not touch anything under internal/") is deliberately NOT derived:
//     the blast radius of a wrong directory pattern is a whole subtree, and
//     prose is full of accidental `and/or`-shaped tokens.
//   - The test-file rule (rule 2) needs an explicit "the tests already exist
//     and must pass" phrase AND a single-language focus list AND no test file
//     among the task's own targets. Anything ambiguous derives nothing.
//   - Protections are installed on the WAVE-shared guard, so a pattern derived
//     from task A is dropped when it would collide with a declared focus file
//     of a DIFFERENT task in the same wave (see waveProtections).
//
// ------------------------------------------------------------------------

// maxDerivedProtections bounds the deny list a single wave can install. A task
// description that somehow yields dozens of patterns is not a task with dozens
// of protected files, it is a parse that has gone wrong, and a huge deny list
// is the shape of "nothing can be edited any more".
const maxDerivedProtections = 12

// prohibitionRe finds the START of an explicit prohibition: a negation
// immediately followed (within a couple of hedging words like "ever", "under
// any circumstances") by a verb that means "change the bytes of a file".
//
// The match ends at the verb; the CLAUSE that follows is what gets scanned for
// paths, which is what makes "do not edit, add or delete any _test.go file"
// work — the verb chain is inside the clause, not in this pattern.
var prohibitionRe = regexp.MustCompile(`(?i)\b(?:do not|don't|dont|never|must not|mustn't|may not|` +
	`should not|shouldn't|cannot|can't|avoid|refrain from)\b[^.\n;]{0,24}?\b` +
	`(?:edit|edits|editing|modify|modifies|modifying|change|changes|changing|touch|touches|touching|` +
	`alter|alters|altering|delete|deletes|deleting|remove|removes|removing|rewrite|rewrites|rewriting|` +
	`overwrite|overwrites|overwriting|update|updates|updating|rename|renames|renaming|` +
	`write|writes|writing|create|creates|creating|add|adds|adding)\b`)

// protectPathRe pulls file-shaped tokens (including globs) out of a clause.
//
// It is deliberately extension-anchored: a token only counts when it ends in a
// known source/config extension, so "the API" and "and/or" cannot become
// patterns. `*` and `?` are inside the character class so an author who already
// wrote a glob ("*_test.go") gets it through verbatim.
var protectPathRe = regexp.MustCompile(`(?i)([A-Za-z0-9_*?./-]+\.(?:` +
	`go|py|pyi|ts|tsx|js|jsx|mjs|cjs|vue|rs|java|kt|kts|scala|rb|php|swift|cs|` +
	`c|h|cc|cpp|hpp|m|mm|sh|bash|zsh|sql|proto|json|yaml|yml|toml|ini|cfg|` +
	`md|rst|html|css|scss|tf|gradle|mod|txt|lock|env|sum))\b`)

// protectFakeRe rejects the placeholder shapes planners and models emit. Same
// idea as plan.ExtractFilePaths' fakePath, kept local because this file must
// also reject them inside globs, which that regex never sees.
var protectFakeRe = regexp.MustCompile(`(?i)path/to/|placeholder|example\.com|your[_-]?file|TODO_PATH|<[^>]*>`)

// clauseBreakers end a prohibition clause. A period only breaks the clause when
// whitespace or end-of-text follows it, so the dot inside "_test.go" or
// "main.py" does not truncate the very token we are looking for.
var clauseBreakRe = regexp.MustCompile(`(?:\.[\s]|\.$|[;\n]| but | however | although )`)

// exceptionWords disqualify a whole clause. "Do not edit the tests EXCEPT
// fixtures" is an instruction this code cannot represent, and a deny list that
// drops the exception is exactly the false protection that blocks real work.
var exceptionWords = []string{"unless", "except", "other than", "apart from", "besides", "aside from"}

// deriveTaskProtections returns the deny patterns implied by ONE task's text.
// The result is sorted and de-duplicated; an empty result is the normal case
// and means "this task says nothing reliable about protected paths".
func deriveTaskProtections(t plan.Task) []string {
	text := taskProtectionText(t)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] || len(out) >= maxDerivedProtections {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range explicitProhibitions(text) {
		add(p)
	}
	for _, p := range implicitTestProtections(t, text) {
		add(p)
	}
	sort.Strings(out)
	return out
}

// taskProtectionText is the ONLY text this file reads: the task's own title,
// instructions and acceptance, with the injected context pack stripped.
func taskProtectionText(t plan.Task) string {
	return t.Title + "\n" + StripScopedPack(t.Description) + "\n" + t.Acceptance
}

// explicitProhibitions implements rule 1: the task said "do not edit X".
func explicitProhibitions(text string) []string {
	var out []string
	for _, loc := range prohibitionRe.FindAllStringIndex(text, -1) {
		clause := prohibitionClause(text[loc[1]:])
		if clause == "" || hasExceptionWord(clause) {
			continue
		}
		out = append(out, patternsIn(clause)...)
	}
	return out
}

// prohibitionClause returns the text from just after the negated verb to the
// end of its clause, bounded so a run-on description cannot drag unrelated
// paths in. Returns "" when the clause is empty.
func prohibitionClause(rest string) string {
	const maxClause = 240
	if len(rest) > maxClause {
		rest = rest[:maxClause]
	}
	if m := clauseBreakRe.FindStringIndex(rest); m != nil {
		rest = rest[:m[0]]
	}
	return strings.TrimSpace(rest)
}

func hasExceptionWord(clause string) bool {
	lower := strings.ToLower(clause)
	for _, w := range exceptionWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// patternsIn extracts and normalizes the file patterns named in one clause.
func patternsIn(clause string) []string {
	var out []string
	for _, m := range protectPathRe.FindAllStringSubmatch(clause, -1) {
		if len(m) < 2 {
			continue
		}
		if p := normalizeProtectPattern(m[1]); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizeProtectPattern turns a raw token into a MatchGlob pattern, or ""
// when the token is not one we are willing to enforce.
func normalizeProtectPattern(raw string) string {
	p := strings.TrimSpace(filepath.ToSlash(raw))
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "`\"'()[],:")
	if p == "" || p == "." || strings.HasPrefix(p, "/") {
		return ""
	}
	// Traversal, placeholders and absurdly deep paths are never protections.
	if strings.Contains(p, "..") || protectFakeRe.MatchString(p) {
		return ""
	}
	if len(p) > 120 || strings.Count(p, "/") > 6 {
		return ""
	}
	base := filepath.Base(p)
	// A suffix-shaped name ("_test.go") is how people write "every file whose
	// name ends this way". A DOT-shaped name (".env") is a literal file, so it
	// is left alone — turning it into "*.env" would protect foo.env too.
	if !strings.Contains(p, "/") && strings.HasPrefix(base, "_") {
		p = "*" + p
	}
	// A bare extension ("*.go") would freeze an entire language, which is the
	// single most destructive pattern this function could return and never what
	// a task means. It is accepted only when a CONCRETE directory bounds it:
	// "**/testdata/*.json" names a real place, "**/*.json" names the repository.
	if stem := strings.TrimSuffix(base, filepath.Ext(base)); stem == "" || stem == "*" {
		if !hasConcreteDirSegment(p) {
			return ""
		}
	}
	return p
}

// hasConcreteDirSegment reports whether any directory segment of p is a literal
// name rather than a wildcard.
func hasConcreteDirSegment(p string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:max(len(segs)-1, 0)] {
		if s != "" && s != "." && !strings.ContainsAny(s, "*?[") {
			return true
		}
	}
	return false
}

// ── rule 2: implement against tests that already exist ──────────────────────

// testIntentRe is the phrase that has to be present for rule 2 to fire: the
// tests are described as ALREADY EXISTING and as the thing to satisfy. A task
// that merely mentions "tests" — or even "make the tests pass", which is what a
// task whose job is to FIX a broken test also says — does not match.
var testIntentRe = regexp.MustCompile(`(?i)\b(?:existing|current|already[ -]?written|pre[ -]?existing|` +
	`provided|given|supplied)\b[^.\n]{0,40}\btests?\b[^.\n]{0,80}\b(?:pass|passing|green|succeed|` +
	`satisfy|satisfies|satisfied)\b`)

// testAuthoringRe vetoes rule 2: the task is asking for test work, so freezing
// test files would make it unachievable.
var testAuthoringRe = regexp.MustCompile(`(?i)\b(?:write|add|adding|create|creating|update|updating|` +
	`extend|extending|fix|fixing|improve|improving|port|migrate)\b[^.\n]{0,30}\b(?:unit |integration |table[ -]driven )?` +
	`tests?\b|\btest coverage\b|\bcoverage floor\b|\bnew tests?\b`)

// testPatternsByExt is the per-language name shape of a test file. Only the
// language of the task's OWN focus files is used, and only when every focus
// file agrees — a mixed-language task derives nothing, because guessing which
// language's tests were meant is exactly the inference this file refuses to do.
var testPatternsByExt = map[string][]string{
	".go":  {"*_test.go"},
	".py":  {"test_*.py", "*_test.py"},
	".ts":  {"*.test.ts", "*.spec.ts"},
	".tsx": {"*.test.tsx", "*.spec.tsx"},
	".js":  {"*.test.js", "*.spec.js"},
	".jsx": {"*.test.jsx", "*.spec.jsx"},
	".rs":  {"*_test.rs"},
	".rb":  {"*_spec.rb", "*_test.rb"},
}

// implicitTestProtections implements rule 2.
//
// EVERY condition below must hold. They are listed in the order that makes the
// veto cheapest to read: the task must not be about tests, must not own a test
// file, must speak in one language, and must explicitly say the tests already
// exist and have to pass.
func implicitTestProtections(t plan.Task, text string) []string {
	if strings.EqualFold(strings.TrimSpace(t.Role), plan.RoleTester) {
		return nil // the tester's whole job is writing tests
	}
	if testAuthoringRe.MatchString(text) {
		return nil // the task asks for test work
	}
	if !testIntentRe.MatchString(text) {
		return nil // no "the tests already exist and must pass" phrasing
	}
	for _, f := range t.Files {
		if looksLikeTestPath(f) {
			return nil // the task declares a test file as its own target
		}
	}
	ext := soleFocusExt(t.Files)
	if ext == "" {
		return nil // no focus files, or more than one language
	}
	return testPatternsByExt[ext]
}

// soleFocusExt returns the extension shared by every focus file, or "" when the
// task has no files or mixes languages.
func soleFocusExt(files []string) string {
	ext := ""
	for _, f := range files {
		e := strings.ToLower(filepath.Ext(strings.TrimSpace(f)))
		if e == "" {
			return ""
		}
		if ext == "" {
			ext = e
			continue
		}
		if e != ext {
			return ""
		}
	}
	return ext
}

// looksLikeTestPath reports whether a path is a test file under any of the
// naming conventions testPatternsByExt knows about.
func looksLikeTestPath(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(path))))
	if base == "" {
		return false
	}
	for _, pats := range testPatternsByExt {
		for _, p := range pats {
			if ok, err := filepath.Match(p, base); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// ── wave installation ───────────────────────────────────────────────────────

// waveProtections is the deny list to install for a whole wave.
//
// FocusGuard.Protect is wave-global, and a union of DENY lists narrows where a
// union of ALLOW lists widens: task A's "leave the tests alone" would otherwise
// silently bind task B, whose job might be writing exactly those tests. So a
// pattern is dropped when it matches a DECLARED focus file (t.Files) of any
// OTHER task in the wave.
//
// The task's own files are deliberately NOT a veto: Protect's documented
// contract is that a protection outranks the allowlist even for a focus file,
// and that is the live case — a task that legitimately owns a package and was
// told to leave its tests alone. Explicit task text beats the planner's file
// list for the task that wrote the text; it does not get to beat a sibling's.
func waveProtections(wave []plan.Task) []string {
	type candidate struct{ pattern, owner string }
	var cands []candidate
	for _, t := range wave {
		for _, p := range deriveTaskProtections(t) {
			cands = append(cands, candidate{pattern: p, owner: t.ID})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if seen[c.pattern] || collidesWithSibling(wave, c.pattern, c.owner) {
			continue
		}
		seen[c.pattern] = true
		out = append(out, c.pattern)
		if len(out) >= maxDerivedProtections {
			break
		}
	}
	sort.Strings(out)
	return out
}

// collidesWithSibling reports whether pattern would block a file some OTHER
// task in this wave declared as its own target.
func collidesWithSibling(wave []plan.Task, pattern, owner string) bool {
	g := workspace.NewFocusGuard()
	g.Protect(pattern)
	for _, t := range wave {
		if t.ID == owner {
			continue
		}
		for _, f := range t.Files {
			if g.IsProtected(f) {
				return true
			}
		}
	}
	return false
}

// applyWaveProtections installs the derived deny list and returns the function
// that removes it again. FocusGuard.Clear only rebuilds the ALLOWLIST — the
// deny list survives it by design — so the caller must call this undo, or one
// wave's protections would leak into every later wave in the run.
func (r *Runner) applyWaveProtections(wave []plan.Task) func() {
	if r == nil || r.Focus == nil {
		return func() {}
	}
	pats := waveProtections(wave)
	r.Focus.Protect(pats...)
	if len(pats) > 0 {
		r.logf("wave %d protected paths (from task text): %s", r.waveN, strings.Join(pats, ", "))
		// Snapshot the protected files HERE — after the deny list exists and
		// before any agent in the wave runs, which is the only moment they are
		// known to be untouched. Without this the checkpointer holds no prior
		// bytes, and a shell command that rewrites a protected file can only be
		// reported, never undone.
		if r.OnProtect != nil {
			r.OnProtect(pats)
		}
	}
	return func() { r.Focus.Protect() }
}

package skills

import (
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/instructions"
)

// Path gating for skills (`paths:` frontmatter).
//
// Every bundled skill declares the files it is about — `**/*.rs` for
// rust-errors, `**/*.py` for python-typing, `**/Dockerfile*` for
// dockerfile-hygiene — and the docs promised path-gated loading. The resolver
// ignored the field entirely, so a Rust skill's full body could be injected
// into a Python project's worker prompt on nothing but a keyword collision.
// With a 3K context budget that is not a cosmetic problem: it displaces the
// project's own instructions.
//
// The rule:
//
//   - A skill with no `paths:` is ungated and behaves exactly as before.
//   - A skill WITH `paths:` participates only when at least one path in scope
//     matches one of its globs.
//   - An EMPTY scope disables gating entirely. Callers that do not (yet) know
//     which files a run will touch — the CLI's `skills list`, Studio's skill
//     page, any existing ResolveForRun call site — therefore see no change.
//   - An explicit `@skill:name` reference or a config pin always wins. The
//     operator naming a skill outranks a heuristic about file extensions.

// MatchesScope reports whether a skill may participate given the paths a run
// is scoped to. An empty scope means "scope unknown" and matches everything.
func (s Skill) MatchesScope(scope []string) bool {
	if len(s.Paths) == 0 || len(scope) == 0 {
		return true
	}
	return instructions.AnyMatch(s.Paths, normalizeScope(scope))
}

// normalizeScope makes caller-supplied paths comparable to the globs: forward
// slashes, no leading "./", no empties. Absolute paths are left alone — a glob
// like `**/*.go` still matches them.
func normalizeScope(scope []string) []string {
	out := make([]string, 0, len(scope))
	for _, p := range scope {
		p = strings.TrimSpace(filepath.ToSlash(p))
		p = strings.TrimPrefix(p, "./")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// FilterByScope drops skills gated out by their `paths:` frontmatter.
// Exported for callers that resolve skills themselves.
func FilterByScope(list []Skill, scope []string) []Skill {
	if len(scope) == 0 {
		return list
	}
	out := make([]Skill, 0, len(list))
	for _, s := range list {
		if s.MatchesScope(scope) {
			out = append(out, s)
		}
	}
	return out
}

// ── scope-aware entry points ──
//
// These are the ones to wire into the orchestrator once it knows the file set
// for a wave. The unscoped ResolveForRun / ResolveMatches / MatchForAgent /
// PackForAgent forward to them with a nil scope, so nothing regresses.

// ResolveForRunScoped is ResolveForRun restricted to skills whose `paths:`
// frontmatter matches at least one of the given project-relative paths.
func (l *Loader) ResolveForRunScoped(query, agent string, pins []string, limit int, scope []string) []Skill {
	_, _, ranked := l.resolveScored(query, agent, pins, limit, scope)
	return ranked
}

// ResolveMatchesScoped is ResolveMatches with path gating.
func (l *Loader) ResolveMatchesScoped(query, agent string, pins []string, limit int, scope []string) []Match {
	scores, explicit, ranked := l.resolveScored(query, agent, pins, limit, scope)
	out := make([]Match, 0, len(ranked))
	for _, s := range ranked {
		key := strings.ToLower(s.Name)
		out = append(out, Match{Skill: s, Score: scores[key], Explicit: explicit[key]})
	}
	return out
}

// PackForAgentScoped renders the two-stage pack for one specialist, gated on
// the files the run is about.
func (l *Loader) PackForAgentScoped(agent, query string, maxChars int, scope []string) string {
	return RenderMatches(l.ResolveMatchesScoped(query, agent, nil, 6, scope),
		PackOptions{MaxChars: maxChars})
}

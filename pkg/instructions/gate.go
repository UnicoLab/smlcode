package instructions

import (
	"path"
	"regexp"
	"strings"
)

// Path-glob gating (Continue.dev-style `paths:` frontmatter).
//
// A monorepo's AGENTS.md carries rules for Go, for the React app, for the
// Terraform stack. Feeding all of them to a specialist editing one Go file is
// pure dilution. A section can declare which files it applies to, and it is
// dropped when none of them are in scope:
//
//	---
//	paths: pkg/**/*.go, cmd/**
//	---
//
// (file-level frontmatter), or per section:
//
//	## Frontend rules <!-- paths: web/**/*.tsx -->
//
// A section with no `paths:` always applies. An EMPTY scope list disables
// gating entirely (nothing is dropped), so a caller that does not yet know its
// file scope loses nothing.
var (
	frontmatterPathsRe = regexp.MustCompile(`(?mi)^paths\s*:\s*(.+)$`)
	sectionPathsRe     = regexp.MustCompile(`(?i)<!--\s*paths\s*:\s*([^>]*?)\s*-->`)
	headingRe          = regexp.MustCompile(`(?m)^(#{1,6})\s`)
)

// GateSections drops instruction sections whose `paths:` glob matches none of
// scopePaths. It also strips any file-level frontmatter block.
func GateSections(md string, scopePaths []string) string {
	body, filePaths := splitFrontmatter(md)
	if len(scopePaths) == 0 {
		return stripSectionMarkers(body)
	}
	if len(filePaths) > 0 && !AnyMatch(filePaths, scopePaths) {
		return ""
	}
	sections := splitHeadings(body)
	var out []string
	for _, sec := range sections {
		globs := sectionGlobs(sec)
		if len(globs) > 0 && !AnyMatch(globs, scopePaths) {
			continue
		}
		out = append(out, stripSectionMarkers(sec))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripSectionMarkers(s string) string {
	return sectionPathsRe.ReplaceAllString(s, "")
}

// splitFrontmatter peels a leading `---` block and returns its paths globs.
func splitFrontmatter(md string) (body string, paths []string) {
	trimmed := strings.TrimLeft(md, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return md, nil
	}
	rest := trimmed[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return md, nil
	}
	fm := rest[:end]
	body = strings.TrimLeft(rest[end+4:], "\r\n")
	if m := frontmatterPathsRe.FindStringSubmatch(fm); m != nil {
		paths = splitGlobs(m[1])
	}
	return body, paths
}

func sectionGlobs(sec string) []string {
	// Only the section's FIRST line (its heading) may carry the marker.
	firstLine := sec
	if i := strings.IndexByte(sec, '\n'); i >= 0 {
		firstLine = sec[:i]
	}
	if m := sectionPathsRe.FindStringSubmatch(firstLine); m != nil {
		return splitGlobs(m[1])
	}
	return nil
}

func splitGlobs(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		p = strings.Trim(strings.TrimSpace(p), `"'[]`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitHeadings breaks markdown into sections at any heading level, keeping the
// preamble before the first heading as its own always-applicable section.
func splitHeadings(md string) []string {
	locs := headingRe.FindAllStringIndex(md, -1)
	if len(locs) == 0 {
		return []string{md}
	}
	var out []string
	if locs[0][0] > 0 {
		if pre := md[:locs[0][0]]; strings.TrimSpace(pre) != "" {
			out = append(out, pre)
		}
	}
	for i, loc := range locs {
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, md[loc[0]:end])
	}
	return out
}

// AnyMatch reports whether any glob matches any of the paths.
func AnyMatch(globs, paths []string) bool {
	for _, g := range globs {
		for _, p := range paths {
			if MatchGlob(g, p) {
				return true
			}
		}
	}
	return false
}

// MatchGlob matches a slash path against a glob supporting `*`, `?`, `[...]`
// and `**` (any number of path segments). A bare directory prefix
// ("pkg/context") matches everything under it.
func MatchGlob(glob, p string) bool {
	glob = strings.TrimSpace(strings.TrimPrefix(filepathToSlash(glob), "./"))
	p = strings.TrimSpace(strings.TrimPrefix(filepathToSlash(p), "./"))
	if glob == "" || p == "" {
		return false
	}
	if glob == "**" || glob == "*" {
		return true
	}
	// Directory prefix shorthand.
	if !strings.ContainsAny(glob, "*?[") {
		return p == glob || strings.HasPrefix(p, strings.TrimSuffix(glob, "/")+"/")
	}
	return matchSegments(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegments(g, p []string) bool {
	switch {
	case len(g) == 0:
		return len(p) == 0
	case g[0] == "**":
		// Match zero or more path segments.
		for i := 0; i <= len(p); i++ {
			if matchSegments(g[1:], p[i:]) {
				return true
			}
		}
		return false
	case len(p) == 0:
		return false
	default:
		ok, err := path.Match(g[0], p[0])
		if err != nil || !ok {
			return false
		}
		return matchSegments(g[1:], p[1:])
	}
}

func filepathToSlash(s string) string { return strings.ReplaceAll(s, `\`, "/") }

package skills

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// Skill is a Claude Code–compatible SKILL.md pack.
//
// Frontmatter (optional):
//
//	---
//	name: my-skill
//	description: short blurb for matching / UI
//	triggers: keyword1, keyword2
//	agents: worker, corrector, tester   # empty / * = all specialists
//	paths: "**/*.go, cmd/**"          # only when such a file is in scope
//	user-invocable: true              # allow @skill:name in queries
//	---
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Path        string   `json:"path"`
	Triggers    []string `json:"triggers,omitempty"`
	Agents      []string `json:"agents,omitempty"` // empty or ["*"] = global
	// Paths gates the skill on the files a run actually touches. Globs support
	// `*`, `?`, `[...]`, `**` and a bare directory prefix (see MatchesScope).
	// Empty = ungated.
	Paths         []string          `json:"paths,omitempty"`
	UserInvocable bool              `json:"user_invocable"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Loader discovers skills from one or more roots (first name wins).
type Loader struct {
	Roots []string

	cache loaderCache
}

func NewLoader(roots ...string) *Loader {
	return &Loader{Roots: roots}
}

// List returns all discovered skills.
//
// Results are cached and invalidated by the mtime+size fingerprint of every
// SKILL.md under the roots, so the repeated Get/ResolveForRun/MatchForAgent/
// PackForAgent calls in one run cost a stat walk instead of 20+ parse walks.
func (l *Loader) List() ([]Skill, error) {
	if l == nil {
		return nil, nil
	}
	stamp := stampRoots(l.Roots)
	l.cache.mu.RLock()
	entry := l.cache.entry
	l.cache.mu.RUnlock()
	if entry != nil && entry.stamp == stamp {
		return append([]Skill(nil), entry.skills...), nil
	}

	l.cache.mu.Lock()
	defer l.cache.mu.Unlock()
	// Re-check under the write lock.
	if l.cache.entry != nil && l.cache.entry.stamp == stamp {
		return append([]Skill(nil), l.cache.entry.skills...), nil
	}
	out, freshStamp := scanRoots(l.Roots)
	l.cache.entry = &cacheEntry{skills: out, stamp: freshStamp}
	return append([]Skill(nil), out...), nil
}

// Get returns a skill by name (case-insensitive).
func (l *Loader) Get(name string) (Skill, bool) {
	list, _ := l.List()
	for _, s := range list {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Skill{}, false
}

var (
	atSkillRe  = regexp.MustCompile(`(?i)@skill:([a-z0-9][a-z0-9_-]*)`)
	slashSkill = regexp.MustCompile(`(?i)(?:^|\s)/skill\s+([a-z0-9][a-z0-9_-]*)`)
)

// ExtractRefs pulls explicit @skill:name / /skill name mentions and returns
// cleaned query text without those tokens.
func ExtractRefs(query string) (names []string, clean string) {
	seen := map[string]bool{}
	clean = query
	for _, re := range []*regexp.Regexp{atSkillRe, slashSkill} {
		for _, m := range re.FindAllStringSubmatch(query, -1) {
			if len(m) < 2 {
				continue
			}
			n := strings.ToLower(m[1])
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		clean = re.ReplaceAllString(clean, " ")
	}
	clean = strings.Join(strings.Fields(clean), " ")
	return names, clean
}

// ResolveForRun builds the skill set for a pipeline or specialist run.
// Includes: explicit @skill refs, agent-targeted skills, query keyword matches, pins.
func (l *Loader) ResolveForRun(query, agent string, pins []string, limit int) []Skill {
	return l.ResolveForRunScoped(query, agent, pins, limit, nil)
}

// resolveScored is ResolveForRun with the score table and the explicit-ref set
// retained, so progressive disclosure can decide what to expand.
//
// scope is the set of project-relative paths the run is about; it gates skills
// carrying `paths:` frontmatter (see scope.go). A nil/empty scope disables
// gating, which is what every unscoped caller passes.
func (l *Loader) resolveScored(query, agent string, pins []string, limit int, scope []string) (map[string]int, map[string]bool, []Skill) {
	if limit <= 0 {
		limit = 6
	}
	refs, clean := ExtractRefs(query)
	refs = append(refs, pins...)
	all, _ := l.List()

	// byName is built from the UNGATED set: an explicit @skill:/pin must still
	// resolve a skill the path gate would otherwise have dropped.
	byName := map[string]Skill{}
	for _, s := range all {
		byName[strings.ToLower(s.Name)] = s
	}
	// Everything reached by scoring is gated on the files in scope.
	list := FilterByScope(all, scope)

	scores := map[string]int{}
	explicit := map[string]bool{}
	bump := func(s Skill, n int) {
		k := strings.ToLower(s.Name)
		if n > scores[k] {
			scores[k] = n
		}
	}

	for _, ref := range refs {
		ref = strings.ToLower(strings.TrimSpace(ref))
		if s, ok := byName[ref]; ok {
			explicit[ref] = true
			bump(s, ExplicitRefScore)
		}
	}

	agent = strings.ToLower(strings.TrimSpace(agent))
	for _, s := range list {
		sc := scoreQuery(clean, s)
		applies := skillAppliesTo(s, agent)
		switch {
		case applies && len(s.Agents) > 0 && !hasAgent(s, "*"):
			// Specialist-specific default
			bump(s, SpecialistDefaultScore+sc*10)
		case applies && agent != "":
			// Global skill while targeting a specialist
			bump(s, 20+sc*10)
		case agent == "" || agent == "*":
			// Full pipeline: keyword hits + globals with triggers only
			if sc > 0 {
				bump(s, sc*10)
			} else if len(s.Agents) == 0 || hasAgent(s, "*") {
				// lightly include untriggered globals only if few refs
				if len(refs) == 0 && sc == 0 {
					continue
				}
			}
		default:
			if sc > 0 && s.UserInvocable {
				bump(s, sc*5)
			}
		}
	}

	type pair struct {
		s Skill
		n int
	}
	var ranked []pair
	for k, n := range scores {
		if n <= 0 {
			continue
		}
		ranked = append(ranked, pair{s: byName[k], n: n})
	}
	// Ties break by name, NOT by map iteration order. Ranking is what decides
	// which two bodies get inlined (DefaultMaxExpanded), so a nondeterministic
	// tie-break meant the same query could produce a different prompt on every
	// run — unreproducible behavior, and unreproducible bug reports.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].n != ranked[j].n {
			return ranked[i].n > ranked[j].n
		}
		return strings.ToLower(ranked[i].s.Name) < strings.ToLower(ranked[j].s.Name)
	})

	var out []Skill
	for _, r := range ranked {
		out = append(out, r.s)
		if len(out) >= limit {
			break
		}
	}

	// Specialist runs always get role defaults even with empty query score
	if len(out) == 0 && agent != "" && agent != "*" {
		for _, s := range list {
			if skillAppliesTo(s, agent) && len(s.Agents) > 0 && !hasAgent(s, "*") {
				out = append(out, s)
				scores[strings.ToLower(s.Name)] = SpecialistDefaultScore
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return scores, explicit, out
}

// MatchForQuery is the keyword matcher for full-pipeline runs.
func (l *Loader) MatchForQuery(query string, limit int) []Skill {
	return l.ResolveForRun(query, "", nil, limit)
}

// MatchForAgent returns skills for a specialist (+ query/refs).
func (l *Loader) MatchForAgent(agent, query string, limit int) []Skill {
	return l.ResolveForRun(query, agent, nil, limit)
}

// PackForAgent renders a budgeted skill pack for one specialist.
//
// Default-on behavior change: this is now the two-stage progressive-disclosure
// pack (cards for every match, full bodies only for explicit @skill: references
// and above-default-tier scores). Use RenderPack directly for the old
// dump-every-body rendering.
func (l *Loader) PackForAgent(agent, query string, maxChars int) string {
	return l.PackForAgentTiered(agent, query, maxChars)
}

func skillAppliesTo(s Skill, agent string) bool {
	if agent == "" || agent == "*" {
		return len(s.Agents) == 0 || hasAgent(s, "*")
	}
	if len(s.Agents) == 0 || hasAgent(s, "*") {
		return true
	}
	return hasAgent(s, agent)
}

func hasAgent(s Skill, agent string) bool {
	for _, a := range s.Agents {
		if a == "*" || strings.EqualFold(a, agent) {
			return true
		}
	}
	return false
}

func scoreQuery(query string, s Skill) int {
	q := strings.ToLower(query)
	if q == "" {
		return 0
	}
	blob := strings.ToLower(s.Name + " " + s.Description + " " + strings.Join(s.Triggers, " "))
	score := 0
	for _, tok := range strings.Fields(q) {
		tok = strings.Trim(tok, ".,:;!?()[]{}\"'")
		if len(tok) < 3 {
			continue
		}
		if strings.Contains(blob, tok) {
			score++
		}
	}
	for _, tr := range s.Triggers {
		tr = strings.ToLower(strings.TrimSpace(tr))
		if tr != "" && strings.Contains(q, tr) {
			score += 2
		}
	}
	return score
}

// RenderPack embeds full skill bodies into a prompt slice (byte-budgeted).
//
// Prefer RenderMatches / RenderCards: dumping every body is what puts hundreds
// of tokens of always-on directives in front of a 7B.
func RenderPack(list []Skill, maxChars int) string {
	if len(list) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 4000
	}
	var b strings.Builder
	b.WriteString("## Active skills\n\n")
	b.WriteString("Invoke guidance from these skills. Force a skill with `@skill:name` in the query.\n\n")
	for _, s := range list {
		agents := "*"
		if len(s.Agents) > 0 {
			agents = strings.Join(s.Agents, ", ")
		}
		section := fmt.Sprintf("### skill:%s\n%s\n<!-- agents: %s -->\n\n%s\n\n", s.Name, s.Description, agents, s.Body)
		if b.Len()+len(section) > maxChars {
			// One oversized skill must not silently drop every lower-ranked
			// skill behind it: skip this one and keep going.
			continue
		}
		b.WriteString(section)
	}
	return b.String()
}

// MaxSkillFileBytes caps ONE SKILL.md. Project skills live in the repository
// (.slmcode/skills/), so their size is attacker-chosen; os.ReadFile on a
// multi-gigabyte file is a one-line denial of service.
const MaxSkillFileBytes = 512 * 1024

// ParseFile reads SKILL.md with optional YAML-ish front matter.
//
// The file is REPOSITORY CONTENT and its body is rendered straight into a
// specialist's prompt, so it is bounded and its harness-minted markers are
// defused before anything else looks at it (see quality.DefuseHarnessMarkers).
func ParseFile(path string) (Skill, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from WalkDir over our own bundled/project skills dirs
	if err != nil {
		return Skill{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxSkillFileBytes+1))
	if err != nil {
		return Skill{}, err
	}
	if len(data) > MaxSkillFileBytes {
		return Skill{}, fmt.Errorf("skill file %s exceeds %d bytes and was not loaded", path, MaxSkillFileBytes)
	}
	text := quality.DefuseHarnessMarkers(string(data))
	sk := Skill{Path: path, Metadata: map[string]string{}, UserInvocable: true}
	if strings.HasPrefix(text, "---") {
		rest := text[3:]
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			fm := rest[:end]
			body := strings.TrimSpace(rest[end+4:])
			sk.Body = body
			sc := bufio.NewScanner(strings.NewReader(fm))
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				k = strings.ToLower(strings.TrimSpace(k))
				v = strings.TrimSpace(v)
				v = strings.Trim(v, `"'`)
				sk.Metadata[k] = v
				switch k {
				case "name":
					sk.Name = v
				case "description":
					sk.Description = v
				case "triggers":
					sk.Triggers = splitCSV(v)
				case "agents", "agent", "roles", "role":
					sk.Agents = splitCSV(v)
				case "paths", "path", "globs", "glob":
					sk.Paths = splitCSV(v)
				case "user-invocable", "user_invocable":
					sk.UserInvocable = v == "" || strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
				}
			}
		}
	} else {
		sk.Body = text
	}
	if sk.Name == "" {
		sk.Name = filepath.Base(filepath.Dir(path))
		if sk.Name == "." || sk.Name == "/" {
			sk.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}
	if sk.Description == "" {
		for _, line := range strings.Split(sk.Body, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if line != "" {
				sk.Description = line
				break
			}
		}
	}
	if len(sk.Triggers) == 0 && sk.Metadata["triggers"] != "" {
		sk.Triggers = splitCSV(sk.Metadata["triggers"])
	}
	return sk, nil
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// WriteSkill creates/updates skillsDir/<name>/SKILL.md.
func WriteSkill(skillsDir string, sk Skill) (string, error) {
	name := strings.TrimSpace(sk.Name)
	if name == "" {
		return "", fmt.Errorf("skill name required")
	}
	safe := sanitizeName(name)
	dir := filepath.Join(skillsDir, safe)
	if err := os.MkdirAll(dir, 0o750); err != nil { // project skills dir, owner-only
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + safe + "\n")
	if sk.Description != "" {
		b.WriteString("description: " + sk.Description + "\n")
	}
	if len(sk.Triggers) > 0 {
		b.WriteString("triggers: " + strings.Join(sk.Triggers, ", ") + "\n")
	}
	if len(sk.Agents) > 0 {
		b.WriteString("agents: " + strings.Join(sk.Agents, ", ") + "\n")
	}
	inv := sk.UserInvocable
	if !inv && len(sk.Agents) == 0 {
		inv = true
	}
	b.WriteString(fmt.Sprintf("user-invocable: %v\n", inv))
	b.WriteString("---\n\n")
	body := strings.TrimSpace(sk.Body)
	if body == "" {
		body = "# " + safe + "\n\nDescribe how specialists should apply this skill.\n"
	}
	b.WriteString(body)
	b.WriteString("\n")
	if err := atomicfile.Write(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteSkill removes a project skill directory (not bundled).
func DeleteSkill(skillsDir, name string) error {
	safe := sanitizeName(name)
	dir := filepath.Join(skillsDir, safe)
	if strings.Contains(dir, "_bundled") {
		return fmt.Errorf("cannot delete bundled skill")
	}
	return os.RemoveAll(dir)
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "skill"
	}
	return out
}

// Template returns a starter skill body for a given agent focus.
func Template(name, agentsCSV string) Skill {
	return Skill{
		Name:          sanitizeName(name),
		Description:   "Custom project skill — edit me",
		Triggers:      []string{sanitizeName(name)},
		Agents:        splitCSV(agentsCSV),
		UserInvocable: true,
		Body: "# " + sanitizeName(name) + "\n\n" +
			"## When to use\n- …\n\n" +
			"## Rules\n- Be specific and actionable for SLMs\n- Prefer tiny, file-scoped changes\n",
	}
}

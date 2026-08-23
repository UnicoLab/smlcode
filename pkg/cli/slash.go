package cli

import (
	"sort"
	"strings"
)

// SlashCommand describes one REPL command for discovery, help and completion.
type SlashCommand struct {
	Name    string   // with leading slash, e.g. "/permission"
	Aliases []string // alternate spellings, also with slash
	Args    string   // argument hint, e.g. "auto|dry-run|review"
	Help    string   // one-line description
	Group   string   // "run" | "review" | "config" | "session" | "info"
	// LiveOK marks commands that are meaningful while a run is in flight.
	LiveOK bool
}

// SlashRegistry holds the command catalog.
type SlashRegistry struct {
	cmds []SlashCommand
	idx  map[string]*SlashCommand
}

// NewSlashRegistry builds a registry from a command list.
func NewSlashRegistry(cmds []SlashCommand) *SlashRegistry {
	r := &SlashRegistry{idx: map[string]*SlashCommand{}}
	r.cmds = append(r.cmds, cmds...)
	for i := range r.cmds {
		c := &r.cmds[i]
		r.idx[c.Name] = c
		for _, a := range c.Aliases {
			r.idx[a] = c
		}
	}
	return r
}

// All returns every command in registration order.
func (r *SlashRegistry) All() []SlashCommand {
	if r == nil {
		return nil
	}
	return r.cmds
}

// Lookup resolves an exact name or alias.
func (r *SlashRegistry) Lookup(name string) (SlashCommand, bool) {
	if r == nil {
		return SlashCommand{}, false
	}
	c, ok := r.idx[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return SlashCommand{}, false
	}
	return *c, true
}

// scored pairs a command with a fuzzy score.
type scored struct {
	cmd   SlashCommand
	score int
}

// Find fuzzy-matches a partial command (with or without the leading slash) and
// returns matches best-first. An empty query returns everything.
func (r *SlashRegistry) Find(q string) []SlashCommand {
	if r == nil {
		return nil
	}
	q = strings.ToLower(strings.TrimSpace(q))
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		out := append([]SlashCommand(nil), r.cmds...)
		return out
	}
	var hits []scored
	for _, c := range r.cmds {
		name := strings.TrimPrefix(c.Name, "/")
		best := fuzzyScore(name, q)
		for _, a := range c.Aliases {
			if s := fuzzyScore(strings.TrimPrefix(a, "/"), q); s > best {
				best = s
			}
		}
		// A help-text match still counts, just much weaker.
		if best <= 0 && strings.Contains(strings.ToLower(c.Help), q) {
			best = 1
		}
		if best > 0 {
			hits = append(hits, scored{c, best})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].cmd.Name < hits[j].cmd.Name
	})
	out := make([]SlashCommand, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.cmd)
	}
	return out
}

// fuzzyScore returns 0 for no match, and higher for better matches:
// exact > prefix > substring > subsequence.
func fuzzyScore(name, q string) int {
	switch {
	case name == q:
		return 1000
	case strings.HasPrefix(name, q):
		return 500 - len(name)
	case strings.Contains(name, q):
		return 200 - len(name)
	}
	// Subsequence match (e.g. "pm" → "permission").
	i := 0
	for _, r := range name {
		if i < len(q) && rune(q[i]) == r {
			i++
		}
	}
	if i == len(q) {
		return 50 - len(name)
	}
	return 0
}

// Complete returns the completion for a partially typed line. It returns the
// full replacement line and the candidate list; when exactly one candidate
// matches the line is completed outright.
func (r *SlashRegistry) Complete(line string) (completed string, candidates []SlashCommand) {
	if r == nil || !strings.HasPrefix(line, "/") {
		return line, nil
	}
	// Only complete the command word, never the arguments.
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		head := line[:i]
		if _, ok := r.Lookup(head); ok {
			return line, nil
		}
		return line, r.Find(head)
	}
	cands := r.Find(line)
	if len(cands) == 1 {
		return cands[0].Name + " ", cands
	}
	if len(cands) > 1 {
		// A single literal-prefix match wins outright: typing "/pro" and
		// pressing Tab should give "/provider ", not stall because
		// "/permission" happens to contain p…r…o as a subsequence.
		if only, ok := solePrefixMatch(cands, line); ok {
			return only.Name + " ", cands
		}
		// Otherwise extend to the longest common prefix of the candidates.
		if lcp := commonPrefix(cands); len(lcp) > len(line) {
			return lcp, cands
		}
	}
	return line, cands
}

// solePrefixMatch returns the only candidate whose name literally starts with
// the typed text.
func solePrefixMatch(cands []SlashCommand, line string) (SlashCommand, bool) {
	var hit SlashCommand
	n := 0
	for _, c := range cands {
		if strings.HasPrefix(strings.ToLower(c.Name), strings.ToLower(line)) {
			hit = c
			n++
		}
	}
	return hit, n == 1
}

func commonPrefix(cands []SlashCommand) string {
	if len(cands) == 0 {
		return ""
	}
	p := cands[0].Name
	for _, c := range cands[1:] {
		for !strings.HasPrefix(c.Name, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// RenderPicker renders the fuzzy `/` picker: matched commands with argument
// hints and one-line help, clipped to width.
func (r *SlashRegistry) RenderPicker(q string, width, limit int) string {
	cands := r.Find(q)
	if len(cands) == 0 {
		return Dim("  no command matches ") + Yellow(q) + Dim(" — press ? for full help") + "\n"
	}
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	nameW := 0
	for _, c := range cands {
		n := StringWidth(c.Name)
		if c.Args != "" {
			n += 1 + StringWidth(c.Args)
		}
		if n > nameW {
			nameW = n
		}
	}
	if nameW > 34 {
		nameW = 34
	}
	var b strings.Builder
	for _, c := range cands {
		sig := c.Name
		if c.Args != "" {
			sig += " " + c.Args
		}
		line := "  " + Cyan(PadWidth(ClipWidth(sig, nameW), nameW)) + "  " + Dim(c.Help)
		if c.LiveOK {
			line += " " + Green("·live")
		}
		b.WriteString(ClipWidth(line, width))
		b.WriteString("\n")
	}
	return b.String()
}

// RenderHelp renders the grouped full help screen.
func (r *SlashRegistry) RenderHelp(width int) string {
	groups := []string{"run", "review", "session", "config", "info"}
	labels := map[string]string{
		"run":     "Run & steer",
		"review":  "Review changes",
		"session": "Sessions & history",
		"config":  "Configuration",
		"info":    "Inspect",
	}
	byGroup := map[string][]SlashCommand{}
	for _, c := range r.cmds {
		g := c.Group
		if g == "" {
			g = "info"
		}
		byGroup[g] = append(byGroup[g], c)
	}
	var b strings.Builder
	b.WriteString(Bold("Commands") + Dim("  — type / for the fuzzy picker, Tab to complete") + "\n")
	for _, g := range groups {
		list := byGroup[g]
		if len(list) == 0 {
			continue
		}
		b.WriteString("\n" + Bold(Accent(labels[g])) + "\n")
		for _, c := range list {
			sig := c.Name
			if c.Args != "" {
				sig += " " + c.Args
			}
			live := ""
			if c.LiveOK {
				live = " " + Green("·live")
			}
			b.WriteString(ClipWidth("  "+Cyan(PadWidth(sig, 30))+"  "+Dim(c.Help)+live, width) + "\n")
		}
	}
	b.WriteString("\n" + Bold(Accent("Keys")) + "\n")
	for _, kv := range [][2]string{
		{"Esc", "interrupt the running agent and say what to change"},
		{"↑ / ↓", "prompt history"},
		{"Ctrl-R", "reverse-search history"},
		{"Tab", "complete a slash command"},
		{"Ctrl-C", "clear the line · again on an empty line quits"},
		{"Ctrl-D", "quit"},
		{"Ctrl-L", "repaint"},
	} {
		b.WriteString(ClipWidth("  "+White(PadWidth(kv[0], 10))+"  "+Dim(kv[1]), width) + "\n")
	}
	return b.String()
}

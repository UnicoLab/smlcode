package schema

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A GBNF interpreter for the subset GBNF() emits.
//
// It exists so "the grammar accepts this document" is a check anything can run,
// not an assertion about substrings of the generated text. Two callers need it:
// this package's own grammar tests, and pkg/agents, where every role's prompt
// carries an example object that must satisfy BOTH the role's JSON Schema and
// the grammar a constrained-decoding backend would be handed. Schema validation
// alone would miss the thing GBNF adds and a small model gets wrong — key
// ORDER, which the grammar pins and the schema does not.
//
// Supported: literals, character classes (with negation and ranges), sequence,
// alternation, `*`, `?`, `+`, grouping, and rule references. That is exactly
// what GBNF() produces; anything else returns a parse error rather than
// silently matching.

// Grammar is a parsed GBNF grammar.
type Grammar struct {
	rules map[string]gnode
}

type gnode interface{}

type gLit struct{ s string }
type gRef struct{ name string }
type gClass struct {
	neg    bool
	ranges [][2]rune
}
type gSeq struct{ items []gnode }
type gAlt struct{ alts []gnode }
type gRep struct {
	item     gnode
	min, max int // max<0 = unbounded
}

// maxMatchDepth bounds recursion so a pathological grammar cannot hang a test.
const maxMatchDepth = 4000

// ParseGBNF parses a grammar produced by GBNF.
func ParseGBNF(src string) (*Grammar, error) {
	g := &Grammar{rules: map[string]gnode{}}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, body, ok := strings.Cut(line, "::=")
		if !ok {
			return nil, fmt.Errorf("gbnf: bad rule line %q", line)
		}
		name = strings.TrimSpace(name)
		toks, err := lexGBNF(strings.TrimSpace(body))
		if err != nil {
			return nil, fmt.Errorf("gbnf: rule %s: %w", name, err)
		}
		p := &gparser{toks: toks}
		node, err := p.parseAlt()
		if err != nil {
			return nil, fmt.Errorf("gbnf: rule %s: %w", name, err)
		}
		if p.i != len(p.toks) {
			return nil, fmt.Errorf("gbnf: rule %s: trailing tokens %v", name, p.toks[p.i:])
		}
		g.rules[name] = node
	}
	if _, ok := g.rules["root"]; !ok {
		return nil, fmt.Errorf("gbnf: grammar has no root rule")
	}
	return g, nil
}

// Accepts reports whether the grammar derives exactly in.
func (g *Grammar) Accepts(in string) bool {
	if g == nil {
		return false
	}
	root, ok := g.rules["root"]
	if !ok {
		return false
	}
	m := &matcher{g: g}
	for _, end := range m.match(root, in, 0) {
		if end == len(in) {
			return true
		}
	}
	return false
}

// Rules lists the rule names, sorted. Useful when a failure needs to name the
// rule that could not match.
func (g *Grammar) Rules() []string {
	if g == nil {
		return nil
	}
	out := make([]string, 0, len(g.rules))
	for k := range g.rules {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AcceptsGBNF parses src and reports whether it derives in.
func AcceptsGBNF(src, in string) (bool, error) {
	g, err := ParseGBNF(src)
	if err != nil {
		return false, err
	}
	return g.Accepts(in), nil
}

// ---------------------------------------------------------------------------
// Lexer
// ---------------------------------------------------------------------------

func lexGBNF(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case ' ', '\t':
			i++
		case '"':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == '"' {
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated literal in %q", s)
			}
			out = append(out, s[i:j+1])
			i = j + 1
		case '[':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == ']' {
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated character class in %q", s)
			}
			out = append(out, s[i:j+1])
			i = j + 1
		case '(', ')', '|', '*', '?', '+':
			out = append(out, string(c))
			i++
		default:
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("unexpected byte %q in %q", c, s)
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out, nil
}

func isIdentByte(b byte) bool {
	return b == '-' || b == '_' || (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

type gparser struct {
	toks []string
	i    int
}

func (p *gparser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *gparser) parseAlt() (gnode, error) {
	first, err := p.parseSeq()
	if err != nil {
		return nil, err
	}
	alts := []gnode{first}
	for p.peek() == "|" {
		p.i++
		n, err := p.parseSeq()
		if err != nil {
			return nil, err
		}
		alts = append(alts, n)
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &gAlt{alts: alts}, nil
}

func (p *gparser) parseSeq() (gnode, error) {
	var items []gnode
	for {
		tk := p.peek()
		if tk == "" || tk == "|" || tk == ")" {
			break
		}
		n, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	return &gSeq{items: items}, nil
}

func (p *gparser) parsePostfix() (gnode, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case "*":
			p.i++
			atom = &gRep{item: atom, min: 0, max: -1}
		case "?":
			p.i++
			atom = &gRep{item: atom, min: 0, max: 1}
		case "+":
			p.i++
			atom = &gRep{item: atom, min: 1, max: -1}
		default:
			return atom, nil
		}
	}
}

func (p *gparser) parseAtom() (gnode, error) {
	tk := p.peek()
	switch {
	case tk == "":
		return nil, fmt.Errorf("unexpected end of rule")
	case tk == "(":
		p.i++
		inner, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("missing ) near %v", p.toks[p.i:])
		}
		p.i++
		return inner, nil
	case strings.HasPrefix(tk, `"`):
		p.i++
		s, err := unescapeGBNF(tk[1 : len(tk)-1])
		if err != nil {
			return nil, err
		}
		return &gLit{s: s}, nil
	case strings.HasPrefix(tk, "["):
		p.i++
		return parseClass(tk)
	default:
		p.i++
		return &gRef{name: tk}, nil
	}
}

func unescapeGBNF(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("trailing backslash in %q", s)
		}
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'x':
			if i+2 >= len(s) {
				return "", fmt.Errorf("bad \\x escape in %q", s)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf("bad \\x escape in %q: %w", s, err)
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func parseClass(tk string) (*gClass, error) {
	body := tk[1 : len(tk)-1]
	c := &gClass{}
	if strings.HasPrefix(body, "^") {
		c.neg = true
		body = body[1:]
	}
	unescaped, err := unescapeGBNFClass(body)
	if err != nil {
		return nil, err
	}
	runes := []rune(unescaped)
	for i := 0; i < len(runes); i++ {
		if i+2 < len(runes) && runes[i+1] == '-' {
			c.ranges = append(c.ranges, [2]rune{runes[i], runes[i+2]})
			i += 2
			continue
		}
		c.ranges = append(c.ranges, [2]rune{runes[i], runes[i]})
	}
	return c, nil
}

// unescapeGBNFClass keeps '-' meaningful while resolving \x / \\ / \" escapes.
func unescapeGBNFClass(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("trailing backslash in class %q", s)
		}
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'x':
			if i+2 >= len(s) {
				return "", fmt.Errorf("bad \\x escape in class %q", s)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf("bad \\x in class %q: %w", s, err)
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func (c *gClass) match(r rune) bool {
	in := false
	for _, rg := range c.ranges {
		if r >= rg[0] && r <= rg[1] {
			in = true
			break
		}
	}
	if c.neg {
		return !in
	}
	return in
}

// ---------------------------------------------------------------------------
// Matcher
// ---------------------------------------------------------------------------

type matcher struct {
	g     *Grammar
	depth int
}

// match returns every input offset reachable after matching n starting at pos.
func (m *matcher) match(n gnode, in string, pos int) []int {
	m.depth++
	if m.depth > maxMatchDepth {
		m.depth--
		return nil
	}
	defer func() { m.depth-- }()
	switch t := n.(type) {
	case *gLit:
		if strings.HasPrefix(in[pos:], t.s) {
			return []int{pos + len(t.s)}
		}
		return nil
	case *gRef:
		r, ok := m.g.rules[t.name]
		if !ok {
			return nil
		}
		return m.match(r, in, pos)
	case *gClass:
		if pos >= len(in) {
			return nil
		}
		r := rune(in[pos])
		w := 1
		if r >= 0x80 {
			for _, rr := range in[pos:] {
				r = rr
				w = len(string(rr))
				break
			}
		}
		if t.match(r) {
			return []int{pos + w}
		}
		return nil
	case *gAlt:
		var out []int
		for _, a := range t.alts {
			out = append(out, m.match(a, in, pos)...)
		}
		return dedupInts(out)
	case *gSeq:
		cur := []int{pos}
		for _, it := range t.items {
			var next []int
			for _, p := range cur {
				next = append(next, m.match(it, in, p)...)
			}
			cur = dedupInts(next)
			if len(cur) == 0 {
				return nil
			}
		}
		return cur
	case *gRep:
		return m.matchRep(t, in, pos)
	}
	return nil
}

func (m *matcher) matchRep(t *gRep, in string, pos int) []int {
	reached := map[int]bool{}
	cur := []int{pos}
	count := 0
	if t.min == 0 {
		reached[pos] = true
	}
	for len(cur) > 0 {
		count++
		if t.max >= 0 && count > t.max {
			break
		}
		var frontier []int
		for _, p := range cur {
			for _, q := range m.match(t.item, in, p) {
				if q == p {
					continue // zero-width, avoid an infinite loop
				}
				frontier = append(frontier, q)
			}
		}
		frontier = dedupInts(frontier)
		var fresh []int
		for _, q := range frontier {
			if count >= t.min && !reached[q] {
				reached[q] = true
			}
			fresh = append(fresh, q)
		}
		cur = fresh
		if count > len(in)+2 {
			break
		}
	}
	out := make([]int, 0, len(reached))
	for p := range reached {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func dedupInts(in []int) []int {
	if len(in) < 2 {
		return in
	}
	seen := make(map[int]bool, len(in))
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

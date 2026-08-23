package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// A minimal GBNF interpreter, good enough for the subset GBNF() emits.
// It exists so the grammar tests actually prove acceptance/rejection instead of
// asserting on substrings of the generated text.
// ---------------------------------------------------------------------------

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

type gGrammar struct {
	rules map[string]gnode
	depth int
}

func parseGBNF(t *testing.T, src string) *gGrammar {
	t.Helper()
	g := &gGrammar{rules: map[string]gnode{}}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, body, ok := strings.Cut(line, "::=")
		if !ok {
			t.Fatalf("bad grammar line: %q", line)
		}
		name = strings.TrimSpace(name)
		toks := lexGBNF(t, strings.TrimSpace(body))
		p := &gparser{toks: toks, t: t}
		g.rules[name] = p.parseAlt()
		if p.i != len(p.toks) {
			t.Fatalf("rule %s: trailing tokens %v", name, p.toks[p.i:])
		}
	}
	return g
}

func lexGBNF(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '"':
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
				t.Fatalf("unterminated literal in %q", s)
			}
			out = append(out, s[i:j+1])
			i = j + 1
		case c == '[':
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
				t.Fatalf("unterminated class in %q", s)
			}
			out = append(out, s[i:j+1])
			i = j + 1
		case c == '(' || c == ')' || c == '|' || c == '*' || c == '?' || c == '+':
			out = append(out, string(c))
			i++
		default:
			j := i
			for j < len(s) && (isIdentByte(s[j])) {
				j++
			}
			if j == i {
				t.Fatalf("unexpected byte %q in %q", c, s)
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

func isIdentByte(b byte) bool {
	return b == '-' || b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

type gparser struct {
	toks []string
	i    int
	t    *testing.T
}

func (p *gparser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *gparser) parseAlt() gnode {
	alts := []gnode{p.parseSeq()}
	for p.peek() == "|" {
		p.i++
		alts = append(alts, p.parseSeq())
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return &gAlt{alts: alts}
}

func (p *gparser) parseSeq() gnode {
	var items []gnode
	for {
		tk := p.peek()
		if tk == "" || tk == "|" || tk == ")" {
			break
		}
		items = append(items, p.parsePostfix())
	}
	return &gSeq{items: items}
}

func (p *gparser) parsePostfix() gnode {
	atom := p.parseAtom()
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
			return atom
		}
	}
}

func (p *gparser) parseAtom() gnode {
	tk := p.peek()
	switch {
	case tk == "(":
		p.i++
		inner := p.parseAlt()
		if p.peek() != ")" {
			p.t.Fatalf("missing ) near %v", p.toks[p.i:])
		}
		p.i++
		return inner
	case strings.HasPrefix(tk, `"`):
		p.i++
		return &gLit{s: unescapeGBNF(p.t, tk[1:len(tk)-1])}
	case strings.HasPrefix(tk, "["):
		p.i++
		return parseClass(p.t, tk)
	default:
		p.i++
		return &gRef{name: tk}
	}
}

func unescapeGBNF(t *testing.T, s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			t.Fatalf("trailing backslash in %q", s)
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
				t.Fatalf("bad \\x escape in %q", s)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				t.Fatalf("bad \\x escape in %q: %v", s, err)
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func parseClass(t *testing.T, tk string) *gClass {
	body := tk[1 : len(tk)-1]
	c := &gClass{}
	if strings.HasPrefix(body, "^") {
		c.neg = true
		body = body[1:]
	}
	runes := []rune(unescapeGBNFClass(t, body))
	for i := 0; i < len(runes); i++ {
		if i+2 < len(runes) && runes[i+1] == '-' {
			c.ranges = append(c.ranges, [2]rune{runes[i], runes[i+2]})
			i += 2
			continue
		}
		c.ranges = append(c.ranges, [2]rune{runes[i], runes[i]})
	}
	return c
}

// unescapeGBNFClass keeps '-' meaningful while resolving \x / \\ / \" escapes.
func unescapeGBNFClass(t *testing.T, s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			t.Fatalf("trailing backslash in class %q", s)
		}
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'x':
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				t.Fatalf("bad \\x in class %q: %v", s, err)
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
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

// match returns every input offset reachable after matching n starting at pos.
func (g *gGrammar) match(n gnode, in string, pos int) []int {
	g.depth++
	if g.depth > 4000 {
		g.depth--
		return nil
	}
	defer func() { g.depth-- }()
	switch t := n.(type) {
	case *gLit:
		if strings.HasPrefix(in[pos:], t.s) {
			return []int{pos + len(t.s)}
		}
		return nil
	case *gRef:
		r, ok := g.rules[t.name]
		if !ok {
			return nil
		}
		return g.match(r, in, pos)
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
			out = append(out, g.match(a, in, pos)...)
		}
		return dedupInts(out)
	case *gSeq:
		cur := []int{pos}
		for _, it := range t.items {
			var next []int
			for _, p := range cur {
				next = append(next, g.match(it, in, p)...)
			}
			cur = dedupInts(next)
			if len(cur) == 0 {
				return nil
			}
		}
		return cur
	case *gRep:
		reached := map[int]bool{}
		var frontier []int
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
			frontier = nil
			for _, p := range cur {
				for _, q := range g.match(t.item, in, p) {
					if q == p {
						continue // zero-width, avoid infinite loop
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
	return nil
}

func (g *gGrammar) accepts(in string) bool {
	root, ok := g.rules["root"]
	if !ok {
		return false
	}
	g.depth = 0
	for _, end := range g.match(root, in, 0) {
		if end == len(in) {
			return true
		}
	}
	return false
}

func dedupInts(in []int) []int {
	if len(in) < 2 {
		return in
	}
	seen := map[int]bool{}
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGBNFAcceptsAndRejectsFixtures(t *testing.T) {
	cases := []struct {
		role   string
		accept []string
		reject []string
	}{
		{
			role: RoleReview,
			accept: []string{
				`{"approved":true,"score":90,"summary":"ok"}`,
				`{"approved": false, "score": 0, "summary": "no", "issues": ["a", "b"]}`,
				`{"approved":true,"score":12,"summary":"ok","issues":[]}`,
			},
			reject: []string{
				`{"score":90,"approved":true,"summary":"ok"}`, // wrong key order
				`{"approved":"true","score":90,"summary":"ok"}`,
				`{"approved":true,"summary":"ok"}`, // score missing (required)
				`{approved:true,score:1,summary:"x"}`,
				`{"approved":true,"score":90,"summary":"ok"} trailing prose`,
			},
		},
		{
			role: RoleTester,
			accept: []string{
				`{"passed":true,"commands":["go build ./..."],"summary":"build OK"}`,
				`{"passed":false,"commands":[],"summary":"nope","failures":["x"]}`,
			},
			reject: []string{
				`{"passed":1,"commands":[],"summary":"x"}`,
				`{"passed":true,"commands":"go build","summary":"x"}`,
			},
		},
		{
			role: RoleEscalate,
			accept: []string{
				`{"action":"retry","reason":"fixable"}`,
				`{"action":"mark_done","reason":"met","confidence":0.9}`,
			},
			reject: []string{
				`{"action":"give_up","reason":"x"}`,
				`{"action":retry,"reason":"x"}`,
			},
		},
		{
			role: RoleWorker,
			accept: []string{
				`{"status":"done","summary":"edited","files_changed":["a.go"]}`,
				`{"status":"blocked","summary":"no key","files_changed":[],"notes":"needs token"}`,
			},
			reject: []string{
				`{"status":"finished","summary":"x","files_changed":[]}`,
				`{"status":"done","files_changed":[],"summary":"x"}`, // order
			},
		},
		{
			role: RoleTasks,
			accept: []string{
				`{"tasks":[{"id":"T1","title":"a","description":"b","role":"worker","files":["x.go"],"acceptance":"go test ./... passes"}]}`,
				`{"tasks":[]}`,
			},
			reject: []string{
				`{"tasks":[{"id":"T1","title":"a","description":"b","role":"designer","files":[],"acceptance":"x"}]}`,
				`[{"id":"T1"}]`,
			},
		},
		{
			role: RoleLessons,
			accept: []string{
				`{"lessons":[{"kind":"success","text":"prefer ws_edit"}]}`,
			},
			reject: []string{
				`{"lessons":[{"text":"x","kind":"success"}]}`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			spec, ok := For(tc.role)
			if !ok {
				t.Fatalf("no spec for %s", tc.role)
			}
			g := parseGBNF(t, GBNF(spec))
			for _, in := range tc.accept {
				if !g.accepts(in) {
					t.Errorf("grammar rejected valid fixture:\n  %s", in)
				}
			}
			for _, in := range tc.reject {
				if g.accepts(in) {
					t.Errorf("grammar accepted invalid fixture:\n  %s", in)
				}
			}
		})
	}
}

func TestGBNFEveryRoleWellFormed(t *testing.T) {
	for _, role := range Roles() {
		spec, _ := For(role)
		src := GBNF(spec)
		g := parseGBNF(t, src)
		if _, ok := g.rules["root"]; !ok {
			t.Fatalf("%s: no root rule", role)
		}
		// Every referenced rule must be defined.
		var walk func(n gnode)
		walk = func(n gnode) {
			switch v := n.(type) {
			case *gRef:
				if _, ok := g.rules[v.name]; !ok {
					t.Errorf("%s: undefined rule %q", role, v.name)
				}
			case *gSeq:
				for _, i := range v.items {
					walk(i)
				}
			case *gAlt:
				for _, a := range v.alts {
					walk(a)
				}
			case *gRep:
				walk(v.item)
			}
		}
		for _, r := range g.rules {
			walk(r)
		}
	}
}

// TestGBNFRoundTripsSchemaSamples generates a canonical minimal document from
// each strict schema and asserts the grammar accepts it — this catches
// key-ordering drift between PropertyOrder and the emitted grammar.
func TestGBNFRoundTripsSchemaSamples(t *testing.T) {
	for _, role := range Roles() {
		spec, _ := For(role)
		sample := minimalDoc(spec.Schema)
		b, err := json.Marshal(sample)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		// json.Marshal sorts map keys, so re-emit in schema order.
		doc := emitOrdered(spec.Schema, sample)
		g := parseGBNF(t, GBNF(spec))
		if !g.accepts(doc) {
			t.Errorf("%s: grammar rejected its own minimal doc\n  ordered: %s\n  marshal: %s", role, doc, b)
		}
		if err := ValidateSpec(spec, []byte(doc)); err != nil {
			t.Errorf("%s: minimal doc failed validation: %v", role, err)
		}
	}
}

func minimalDoc(node map[string]any) any {
	switch node["type"] {
	case "object":
		props, _ := node["properties"].(map[string]any)
		out := map[string]any{}
		for _, name := range RequiredNames(node) {
			child, _ := props[name].(map[string]any)
			out[name] = minimalDoc(child)
		}
		return out
	case "array":
		n, _ := intOf(node["minItems"])
		item, _ := node["items"].(map[string]any)
		out := []any{}
		for i := 0; i < n; i++ {
			out = append(out, minimalDoc(item))
		}
		return out
	case "boolean":
		return true
	case "integer":
		if lo, ok := intOf(node["minimum"]); ok {
			return lo
		}
		return 0
	case "number":
		return 0.0
	default:
		if e, ok := node["enum"].([]any); ok && len(e) > 0 {
			return e[0]
		}
		return "x"
	}
}

func emitOrdered(node map[string]any, v any) string {
	switch node["type"] {
	case "object":
		props, _ := node["properties"].(map[string]any)
		m, _ := v.(map[string]any)
		var parts []string
		for _, name := range PropertyOrder(node) {
			cv, ok := m[name]
			if !ok {
				continue
			}
			child, _ := props[name].(map[string]any)
			parts = append(parts, fmt.Sprintf("%q:%s", name, emitOrdered(child, cv)))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case "array":
		item, _ := node["items"].(map[string]any)
		arr, _ := v.([]any)
		var parts []string
		for _, e := range arr {
			parts = append(parts, emitOrdered(item, e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

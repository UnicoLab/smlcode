package schema

import (
	"fmt"
	"sort"
	"strings"
)

// GBNF renders a llama.cpp grammar for a Spec.
//
// The grammar pins key order (required properties in declaration order, then
// optional ones as an optional tail), which is exactly what stops a 7B from
// emitting `{"issues": [...]}` and forgetting `approved`. Supported nodes:
// object, array (with minItems), string (with enum), integer, number, boolean,
// plus a generic value/object fallback for open-ended sub-objects.
func GBNF(spec Spec) string {
	g := &grammar{
		bodies: map[string]string{},
		names:  map[string]string{},
	}
	root := g.node(spec.Schema, "root")
	var b strings.Builder
	fmt.Fprintf(&b, "# GBNF for slmcode schema role %q\n", spec.Name)
	fmt.Fprintf(&b, "root ::= ws %s ws\n", root)
	for _, name := range g.order {
		if name == root && name == "root" {
			continue
		}
		fmt.Fprintf(&b, "%s ::= %s\n", name, g.bodies[name])
	}
	b.WriteString(gbnfPrimitives)
	return b.String()
}

const gbnfPrimitives = `ws ::= [ \t\n\r]*
string ::= "\"" schar* "\""
schar ::= [^"\\\x7F\x00-\x1F] | "\\" ["\\/bfnrt] | "\\" "u" hex hex hex hex
hex ::= [0-9a-fA-F]
integer ::= "-"? ("0" | [1-9] [0-9]*)
number ::= integer ("." [0-9]+)? ([eE] [-+]? [0-9]+)?
boolean ::= "true" | "false"
null ::= "null"
value ::= anyobject | anyarray | string | number | boolean | null
anyobject ::= "{" ws (string ws ":" ws value (ws "," ws string ws ":" ws value)*)? ws "}"
anyarray ::= "[" ws (value (ws "," ws value)*)? ws "]"
`

// gbnfLiteral renders a GBNF terminal that matches the JSON-quoted form of s,
// i.e. the six characters `"a"` become the GBNF literal `"\"a\""`. Emitting the
// bare word instead is the classic mistake — it produces a grammar that forbids
// the quotes JSON requires.
func gbnfLiteral(s string) string {
	var b strings.Builder
	b.WriteString(`"\""`)
	b.WriteString(" ")
	b.WriteString(quoteGBNF(s))
	b.WriteString(" ")
	b.WriteString(`"\""`)
	return "(" + b.String() + ")"
}

// quoteGBNF renders s as a single GBNF string terminal.
func quoteGBNF(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type grammar struct {
	bodies map[string]string // rule name -> body
	names  map[string]string // body -> rule name (dedup)
	order  []string
	n      int
}

// define registers a rule body, reusing an existing rule with the same body.
func (g *grammar) define(prefix, body string) string {
	if name, ok := g.names[body]; ok {
		return name
	}
	g.n++
	name := fmt.Sprintf("%s%d", prefix, g.n)
	g.bodies[name] = body
	g.names[body] = name
	g.order = append(g.order, name)
	return name
}

// node returns the rule name (or primitive name) for a schema node.
func (g *grammar) node(node map[string]any, hint string) string {
	if len(node) == 0 {
		return "value"
	}
	typ, _ := node["type"].(string)
	switch typ {
	case "object":
		return g.objectNode(node, hint)
	case "array":
		return g.arrayNode(node, hint)
	case "string":
		if e, ok := node["enum"].([]any); ok && len(e) > 0 {
			var alts []string
			for _, v := range e {
				alts = append(alts, gbnfLiteral(fmt.Sprintf("%v", v)))
			}
			return g.define("enum", strings.Join(alts, " | "))
		}
		return "string"
	case "boolean":
		return "boolean"
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "null":
		return "null"
	default:
		return "value"
	}
}

func (g *grammar) objectNode(node map[string]any, hint string) string {
	props, _ := node["properties"].(map[string]any)
	if len(props) == 0 {
		return "anyobject"
	}
	order := PropertyOrder(node)
	var required, optional []string
	for _, name := range order {
		child, _ := props[name].(map[string]any)
		rule := g.node(child, hint+"-"+name)
		member := fmt.Sprintf(`%s ws ":" ws %s`, gbnfLiteral(name), rule)
		if IsRequired(node, name) {
			required = append(required, member)
		} else {
			optional = append(optional, member)
		}
	}
	var b strings.Builder
	b.WriteString(`"{" ws `)
	for i, m := range required {
		if i > 0 {
			b.WriteString(` ws "," ws `)
		}
		b.WriteString(m)
	}
	// Optional members trail the required block. When nothing is required the
	// first optional member carries the "no leading comma" case.
	leading := len(required) > 0
	for _, m := range optional {
		if leading {
			fmt.Fprintf(&b, ` (ws "," ws %s)?`, m)
			continue
		}
		fmt.Fprintf(&b, ` (%s)?`, m)
		leading = true
	}
	b.WriteString(` ws "}"`)
	return g.define("obj", b.String())
}

func (g *grammar) arrayNode(node map[string]any, hint string) string {
	item, _ := node["items"].(map[string]any)
	itemRule := "value"
	if len(item) > 0 {
		itemRule = g.node(item, hint+"-item")
	}
	minItems := 0
	if n, ok := intOf(node["minItems"]); ok && n > 0 {
		minItems = n
	}
	if minItems > 8 {
		minItems = 8 // keep grammars small; Validate still enforces the real bound
	}
	var b strings.Builder
	b.WriteString(`"[" ws `)
	if minItems == 0 {
		fmt.Fprintf(&b, `(%s (ws "," ws %s)*)?`, itemRule, itemRule)
	} else {
		for i := 0; i < minItems; i++ {
			if i > 0 {
				b.WriteString(` ws "," ws `)
			}
			b.WriteString(itemRule)
		}
		fmt.Fprintf(&b, ` (ws "," ws %s)*`, itemRule)
	}
	b.WriteString(` ws "]"`)
	return g.define("arr", b.String())
}

// GBNFForRole is a convenience wrapper returning the grammar for a role id.
func GBNFForRole(role string) (string, bool) {
	spec, ok := For(role)
	if !ok {
		return "", false
	}
	return GBNF(spec), true
}

// AllGrammars renders every registered role's grammar (used by tests and by
// `slmcode` diagnostics).
func AllGrammars() map[string]string {
	out := make(map[string]string, len(registry))
	names := Roles()
	sort.Strings(names)
	for _, n := range names {
		spec, _ := For(n)
		out[n] = GBNF(spec)
	}
	return out
}

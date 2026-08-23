package schema

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The GBNF interpreter these tests run against lives in gbnfmatch.go, exported
// so pkg/agents can check every role's PROMPT example against the same grammar
// a constrained-decoding backend would be handed.

func parseGBNF(t *testing.T, src string) *Grammar {
	t.Helper()
	g, err := ParseGBNF(src)
	if err != nil {
		t.Fatalf("parse grammar: %v", err)
	}
	return g
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
				if !g.Accepts(in) {
					t.Errorf("grammar rejected valid fixture:\n  %s", in)
				}
			}
			for _, in := range tc.reject {
				if g.Accepts(in) {
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
		if !g.Accepts(doc) {
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

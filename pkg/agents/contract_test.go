package agents

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// The prompt/parser contract test.
//
// pkg/agents states each role's output contract once, as a concrete example
// object at the end of the prompt. pkg/schema holds the JSON Schema the parser
// validates against AND the GBNF grammar a constrained-decoding backend is
// handed. Three artifacts, one contract — so a prompt edit can drift from the
// thing that will actually be enforced, and the model is then told to emit a
// document the grammar forbids.
//
// Schema validation alone does not catch that: JSON Schema says nothing about
// key ORDER, and key order is precisely what GBNF pins (required properties in
// declaration order, then optional ones as a trailing tail — see schema.GBNF).
// A prompt example with `issues` in the middle validates fine and is rejected
// token-for-token by the grammar.

// TestPromptContractsMatchSchemaAndGrammar checks EVERY role that has a schema
// — not only the JSON-only ones — against both artifacts.
func TestPromptContractsMatchSchemaAndGrammar(t *testing.T) {
	covered := map[string]bool{}
	for _, spec := range Specs() {
		if spec.SchemaRole == "" {
			continue
		}
		covered[spec.SchemaRole] = true
		t.Run(spec.ID, func(t *testing.T) {
			sc, ok := schema.For(spec.SchemaRole)
			if !ok {
				t.Fatalf("SchemaRole %q is not registered", spec.SchemaRole)
			}
			contract := contractOf(t, spec.SystemPrompt)
			if contract == "" {
				t.Fatalf("prompt has no %q contract block at the end", outputMarker)
			}
			if !json.Valid([]byte(contract)) {
				t.Fatalf("contract block is not valid JSON:\n%s", contract)
			}
			if err := schema.ValidateSpec(sc, []byte(contract)); err != nil {
				t.Errorf("the prompt's own example fails its schema: %v\n%s", err, contract)
			}
			// The grammar side. A failure here names the exact key that drifted.
			grammar := schema.GBNF(sc)
			accepted, err := schema.AcceptsGBNF(grammar, contract)
			if err != nil {
				t.Fatalf("generated grammar does not parse: %v", err)
			}
			if !accepted {
				why := contractDrift(sc.Schema, []byte(contract), "")
				if why == "" {
					why = "no key-order drift found — the example uses a construct the grammar does not model"
				}
				t.Errorf("GBNF for role %q rejects the prompt's own example.\n%s\nexample: %s",
					spec.SchemaRole, why, contract)
			}
			// The contract must be identifiable from the prompt alone, so a role
			// re-tasked with another contract is still constrained correctly.
			got, ok := schema.DetectRole(spec.SystemPrompt, spec.SchemaRole)
			if !ok || got.Name != sc.Name {
				t.Errorf("DetectRole = %q (ok=%v), want %q", got.Name, ok, sc.Name)
			}
		})
	}
	// Every schema role a built-in agent can emit must be covered by some
	// prompt, or a contract exists that no prompt ever states.
	for _, role := range []string{
		schema.RolePlan, schema.RoleTasks, schema.RoleWorker, schema.RoleReview,
		schema.RoleTester, schema.RoleEscalate, schema.RoleComposition,
		schema.RoleCoordinator,
	} {
		if !covered[role] {
			t.Errorf("schema role %q is not stated by any prompt", role)
		}
	}
}

// TestUnboundPromptContractsMatchGrammarToo covers the prompts run through
// another agent rather than a role of their own. They reach the same
// constrained-decoding path (structuredProvider re-detects the contract from
// the prompt text), so they need the same guarantee.
func TestUnboundPromptContractsMatchGrammarToo(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		// hint is the agent the orchestrator actually runs the prompt through.
		hint string
		role string
	}{
		{"clarify via planner", PromptClarifier, schema.RolePlan, schema.RoleClarify},
		{"scope judge via reviewer", PromptScopeJudge, schema.RoleReview, schema.RoleScopeJudge},
		{"learner via memory", PromptLearner, "", schema.RoleLessons},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, ok := schema.For(tc.role)
			if !ok {
				t.Fatalf("%q not registered", tc.role)
			}
			if n := strings.Count(tc.prompt, outputMarker); n != 1 {
				t.Errorf("%d output contracts, want exactly 1", n)
			}
			contract := contractOf(t, tc.prompt)
			if contract == "" {
				t.Fatal("no contract block")
			}
			if err := schema.ValidateSpec(sc, []byte(contract)); err != nil {
				t.Errorf("contract fails its schema: %v\n%s", err, contract)
			}
			if got, ok := schema.DetectRole(tc.prompt, tc.hint); !ok || got.Name != tc.role {
				t.Errorf("DetectRole = %q (ok=%v), want %q — the wrong schema would be enforced",
					got.Name, ok, tc.role)
			}
			accepted, err := schema.AcceptsGBNF(schema.GBNF(sc), contract)
			if err != nil {
				t.Fatalf("generated grammar does not parse: %v", err)
			}
			if !accepted {
				why := contractDrift(sc.Schema, []byte(contract), "")
				t.Errorf("GBNF for role %q rejects the prompt's own example.\n%s\nexample: %s",
					tc.role, why, contract)
			}
		})
	}
}

// TestEveryRoleGrammarIsParseable is the cheap guard on the generator itself:
// a grammar nothing can parse would make every check above vacuous.
func TestEveryRoleGrammarIsParseable(t *testing.T) {
	for role, src := range schema.AllGrammars() {
		if _, err := schema.ParseGBNF(src); err != nil {
			t.Errorf("role %q produced an unparseable grammar: %v", role, err)
		}
	}
}

// A regression guard on the drift detector itself: reordering a key must be
// reported, by name, at its path.
func TestContractDriftNamesTheKey(t *testing.T) {
	sc, ok := schema.For(schema.RoleReview)
	if !ok {
		t.Fatal("review schema missing")
	}
	bad := `{"approved":true,"issues":[],"score":85,"summary":"one line"}`
	if accepted, _ := schema.AcceptsGBNF(schema.GBNF(sc), bad); accepted {
		t.Fatal("the grammar accepted an out-of-order document — the check is vacuous")
	}
	why := contractDrift(sc.Schema, []byte(bad), "")
	if !strings.Contains(why, "issues") {
		t.Errorf("drift message does not name the offending key: %q", why)
	}
	// An unknown key is drift too — the grammar has no production for it.
	why = contractDrift(sc.Schema, []byte(`{"approved":true,"score":1,"summary":"s","bogus":1}`), "")
	if !strings.Contains(why, "bogus") {
		t.Errorf("an unknown key was not reported: %q", why)
	}
	// The good document produces no complaint.
	good := `{"approved":true,"score":85,"summary":"one line","issues":[]}`
	if accepted, _ := schema.AcceptsGBNF(schema.GBNF(sc), good); !accepted {
		t.Fatal("the grammar rejected a correctly ordered document")
	}
	if why := contractDrift(sc.Schema, []byte(good), ""); why != "" {
		t.Errorf("a conforming document was reported as drifted: %q", why)
	}
}

// ---------------------------------------------------------------------------
// Drift detection
// ---------------------------------------------------------------------------

// contractDrift explains, in one line, why doc does not match the key order
// schema.GBNF pins for node. It returns "" when the order is fine — the caller
// then knows the rejection is about something other than ordering.
//
// The rule it reproduces is objectNode's in pkg/schema/gbnf.go: required
// properties in declaration order, then the optional ones as a trailing tail,
// also in declaration order.
func contractDrift(node map[string]any, doc []byte, path string) string {
	if len(node) == 0 {
		return ""
	}
	switch typ, _ := node["type"].(string); typ {
	case "object":
		return objectDrift(node, doc, path)
	case "array":
		return arrayDrift(node, doc, path)
	}
	return ""
}

func objectDrift(node map[string]any, doc []byte, path string) string {
	props, _ := node["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}
	actual, raw, err := objectKeyOrder(doc)
	if err != nil || len(actual) == 0 {
		return ""
	}
	// Expected order: required in declaration order, then optional.
	var req, opt []string
	present := map[string]bool{}
	for _, k := range actual {
		present[k] = true
	}
	for _, name := range schema.PropertyOrder(node) {
		if !present[name] {
			continue
		}
		if schema.IsRequired(node, name) {
			req = append(req, name)
		} else {
			opt = append(opt, name)
		}
	}
	expected := append(req, opt...)

	// Unknown keys first: the grammar has no production for them at all.
	var unknown []string
	for _, k := range actual {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Sprintf("%s: key %q is not in the schema, so the grammar has no production for it",
			at(path), unknown[0])
	}
	// Missing required keys.
	for _, name := range schema.PropertyOrder(node) {
		if schema.IsRequired(node, name) && !present[name] {
			return fmt.Sprintf("%s: required key %q is missing from the example", at(path), name)
		}
	}
	for i := range expected {
		if i < len(actual) && actual[i] == expected[i] {
			continue
		}
		got := "<end>"
		if i < len(actual) {
			got = actual[i]
		}
		kind := "required"
		if !schema.IsRequired(node, expected[i]) {
			kind = "optional"
		}
		return fmt.Sprintf(
			"%s: key %d is %q but the grammar expects the %s key %q there "+
				"(required keys in schema order first, optional ones after)\n  prompt order:  %s\n  grammar order: %s",
			at(path), i+1, got, kind, expected[i],
			strings.Join(actual, ", "), strings.Join(expected, ", "))
	}
	// Order is fine at this level — descend.
	for _, name := range actual {
		child, _ := props[name].(map[string]any)
		if len(child) == 0 {
			continue
		}
		if why := contractDrift(child, raw[name], join(path, name)); why != "" {
			return why
		}
	}
	return ""
}

func arrayDrift(node map[string]any, doc []byte, path string) string {
	item, _ := node["items"].(map[string]any)
	if len(item) == 0 {
		return ""
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(doc, &elems); err != nil {
		return ""
	}
	for i, e := range elems {
		if why := contractDrift(item, e, fmt.Sprintf("%s[%d]", path, i)); why != "" {
			return why
		}
	}
	return ""
}

// objectKeyOrder returns the keys of a JSON object in the order they appear,
// plus each key's raw value. encoding/json's Decoder is used because a map
// would lose exactly the information under test.
func objectKeyOrder(doc []byte) ([]string, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(string(doc)))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("not an object")
	}
	var order []string
	values := map[string]json.RawMessage{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, ok := k.(string)
		if !ok {
			return nil, nil, fmt.Errorf("non-string key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		order = append(order, name)
		values[name] = v
	}
	return order, values, nil
}

func at(path string) string {
	if path == "" {
		return "root object"
	}
	return path
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

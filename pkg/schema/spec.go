// Package schema holds hand-written JSON Schema (draft-07 subset) definitions
// for every structured output the slmcode harness parses, plus the derived
// artefacts each constrained-decoding backend needs:
//
//   - OpenAI `response_format: {"type":"json_schema", ...}` (strict variant)
//   - vLLM `guided_json`
//   - llama.cpp `grammar` (GBNF, see gbnf.go)
//
// The schemas deliberately stay inside the intersection that GBNF conversion
// and vLLM guided decoding both support:
//
//	type / properties / required / items / enum / additionalProperties
//	minItems / maxItems / minLength / maxLength / minimum / maximum (integers)
//
// Explicitly avoided: uniqueItems, contains, if/then/else, prefixItems,
// patternProperties, oneOf/anyOf/allOf, $ref cycles, non-integer bounds.
package schema

import (
	"sort"
	"strings"
)

// Spec is one named structured-output contract.
type Spec struct {
	// Name is the schema role id (see the Role* constants).
	Name string
	// Schema is the draft-07 subset document describing the payload.
	Schema map[string]any
	// Strict reports whether the contract is simple enough for OpenAI
	// `strict: true` json_schema mode (every property required,
	// additionalProperties false, no free-form nesting).
	Strict bool
}

// Schema role ids. They are output-contract names, not agent ids: several
// agents can emit the same contract, and one agent (e.g. planner) can be
// re-tasked with a different contract by prompt.
const (
	RolePlan        = "plan"
	RoleTasks       = "tasks"
	RoleReview      = "review"
	RoleTester      = "tester"
	RoleClarify     = "clarify"
	RoleEscalate    = "escalate"
	RoleComposition = "composition"
	RoleScopeJudge  = "scope_judge"
	RoleWorker      = "worker"
	RoleExplore     = "explore"
	RoleDocs        = "docs"
	RoleArchitect   = "architect"
	RoleCoordinator = "coordinator"
	RoleOrchestrate = "orchestrator"
	RolePlaceholder = "placeholder"
	RoleLessons     = "lessons"
)

func obj(props map[string]any, required ...string) map[string]any {
	req := make([]any, 0, len(required))
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   req,
	}
}

func str() map[string]any   { return map[string]any{"type": "string"} }
func boolv() map[string]any { return map[string]any{"type": "boolean"} }

func strList() map[string]any {
	return map[string]any{"type": "array", "items": str()}
}

func strListMax(n int) map[string]any {
	return map[string]any{"type": "array", "items": str(), "maxItems": n}
}

func enum(vals ...string) map[string]any {
	e := make([]any, 0, len(vals))
	for _, v := range vals {
		e = append(e, v)
	}
	return map[string]any{"type": "string", "enum": e}
}

func intRange(lo, hi int) map[string]any {
	return map[string]any{"type": "integer", "minimum": lo, "maximum": hi}
}

var registry = map[string]Spec{}

func register(name string, strict bool, s map[string]any) {
	registry[name] = Spec{Name: name, Schema: s, Strict: strict}
}

func init() {
	register(RolePlan, true, obj(map[string]any{
		"summary":     str(),
		"goals":       strListMax(8),
		"assumptions": strListMax(8),
		"risks":       strListMax(8),
		"steps":       map[string]any{"type": "array", "items": str(), "maxItems": 6},
	}, "summary", "steps"))

	register(RoleTasks, true, obj(map[string]any{
		"tasks": map[string]any{
			"type":     "array",
			"maxItems": 8,
			"items": obj(map[string]any{
				"id":          str(),
				"title":       str(),
				"description": str(),
				"role":        enum("worker", "tester", "explorer", "context", "reviewer", "corrector"),
				"depends_on":  strList(),
				"files":       strList(),
				"acceptance":  str(),
			}, "id", "title", "description", "role", "files", "acceptance"),
		},
	}, "tasks"))

	register(RoleReview, true, obj(map[string]any{
		"approved": boolv(),
		"score":    intRange(0, 100),
		"issues":   strListMax(12),
		"summary":  str(),
	}, "approved", "score", "summary"))

	register(RoleTester, true, obj(map[string]any{
		"passed":   boolv(),
		"commands": strListMax(8),
		"summary":  str(),
		"failures": strListMax(12),
	}, "passed", "commands", "summary"))

	register(RoleEscalate, true, obj(map[string]any{
		"action":     enum("retry", "re_scope", "abort", "mark_done"),
		"reason":     str(),
		"confidence": map[string]any{"type": "number"},
	}, "action", "reason"))

	register(RoleScopeJudge, true, obj(map[string]any{
		"ok":            boolv(),
		"issues":        strListMax(20),
		"hints":         strListMax(20),
		"weak_task_ids": strListMax(20),
	}, "ok", "issues"))

	register(RoleWorker, true, obj(map[string]any{
		"status":        enum("done", "blocked"),
		"summary":       str(),
		"files_changed": strListMax(20),
		"notes":         str(),
	}, "status", "summary", "files_changed"))

	register(RoleExplore, true, obj(map[string]any{
		"summary":        str(),
		"relevant_files": strListMax(24),
		"key_symbols":    strListMax(24),
		"risks":          strListMax(8),
		"notes":          str(),
	}, "summary", "relevant_files"))

	register(RoleDocs, true, obj(map[string]any{
		"summary":     str(),
		"doc_files":   strListMax(16),
		"conventions": strListMax(12),
		"apis":        strListMax(16),
		"gaps":        strListMax(8),
	}, "summary", "doc_files"))

	register(RoleArchitect, true, obj(map[string]any{
		"approach":   str(),
		"components": strListMax(10),
		"interfaces": strListMax(10),
		"risks":      strListMax(8),
		"non_goals":  strListMax(8),
	}, "approach", "components"))

	register(RoleCoordinator, true, obj(map[string]any{
		"summary": str(),
		"actions": map[string]any{
			"type":     "array",
			"maxItems": 8,
			"items": obj(map[string]any{
				"type":    enum("note", "promote", "reassign", "add_task", "skip_explore", "focus"),
				"task_id": str(),
				"role":    str(),
				"text":    str(),
			}, "type", "text"),
		},
		"focus_files": strListMax(16),
		"risks":       strListMax(8),
	}, "summary", "actions"))

	register(RoleOrchestrate, true, obj(map[string]any{
		"decision": str(),
		"next":     str(),
		"notes":    str(),
	}, "decision", "next"))

	register(RolePlaceholder, true, obj(map[string]any{
		"status":        enum("done", "blocked"),
		"summary":       str(),
		"files_changed": strListMax(20),
		"gaps_filled":   strListMax(20),
		"gaps_flagged": map[string]any{
			"type":     "array",
			"maxItems": 20,
			"items": obj(map[string]any{
				"path":   str(),
				"reason": str(),
			}, "path", "reason"),
		},
		"notes": str(),
	}, "status", "summary", "files_changed"))

	register(RoleLessons, true, obj(map[string]any{
		"lessons": map[string]any{
			"type":     "array",
			"maxItems": 5,
			"items": obj(map[string]any{
				"kind": enum("success", "failure", "convention"),
				"text": str(),
			}, "kind", "text"),
		},
	}, "lessons"))

	// clarify: nested option lists + a PRD sub-object. Kept non-strict — the
	// nesting depth makes OpenAI strict mode brittle on small models, and the
	// parser (plan.ParseScopeInterview) already accepts the legacy shape.
	register(RoleClarify, false, obj(map[string]any{
		"needs_user": boolv(),
		"questions": map[string]any{
			"type":     "array",
			"maxItems": 3,
			"items": obj(map[string]any{
				"id":       str(),
				"header":   str(),
				"question": str(),
				"options": map[string]any{
					"type":     "array",
					"minItems": 2,
					"maxItems": 4,
					"items": obj(map[string]any{
						"label":       str(),
						"description": str(),
						"recommended": boolv(),
					}, "label", "description"),
				},
				"allow_freeform": boolv(),
				"recommended":    str(),
			}, "id", "header", "question", "options"),
		},
		"assumptions": strListMax(8),
		"acceptance":  strListMax(8),
		"non_goals":   strListMax(8),
		"language":    str(),
		"entrypoint":  str(),
		"prd": obj(map[string]any{
			"summary":     str(),
			"goals":       strListMax(8),
			"non_goals":   strListMax(8),
			"acceptance":  strListMax(8),
			"constraints": strListMax(8),
			"language":    str(),
			"entrypoint":  str(),
		}, "summary", "acceptance"),
	}, "needs_user", "assumptions", "acceptance"))

	// composition: `slots` is an open-ended pipeline.Slot list, so this contract
	// can never be strict. json_object / GBNF still help a lot.
	register(RoleComposition, false, obj(map[string]any{
		"summary":  str(),
		"strategy": str(),
		"handoff":  strListMax(6),
		"phases": map[string]any{
			"type":     "array",
			"maxItems": 16,
			"items": obj(map[string]any{
				"id":      str(),
				"agent":   str(),
				"enabled": boolv(),
				"when":    enum("always", "auto", "never"),
			}, "id", "enabled"),
		},
		"execute": obj(map[string]any{
			"default_role": str(),
			"reviewer":     str(),
			"corrector":    str(),
			"max_waves":    intRange(1, 5),
		}, "default_role", "reviewer", "corrector"),
		"team": map[string]any{
			"type":     "array",
			"maxItems": 12,
			"items": obj(map[string]any{
				"role":   str(),
				"skills": strListMax(4),
			}, "role"),
		},
		"slots": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
	}, "summary", "phases", "execute"))
}

// For returns the Spec registered for a schema role id.
func For(role string) (Spec, bool) {
	s, ok := registry[normalizeRole(role)]
	return s, ok
}

// Roles lists every registered schema role id, sorted.
func Roles() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normalizeRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	switch r {
	case "splitter", "task_split", "tasksplit":
		return RoleTasks
	case "planner":
		return RolePlan
	case "reviewer", "reviewer-strict":
		return RoleReview
	case "clarifier", "interviewer":
		return RoleClarify
	case "scope-judge", "scopejudge":
		return RoleScopeJudge
	case "composer":
		return RoleComposition
	case "explorer":
		return RoleExplore
	case "deep", "corrector":
		return RoleWorker
	case "memory", "learner":
		return RoleLessons
	default:
		return r
	}
}

// RequiredKeys returns the top-level required property names of a spec.
func RequiredKeys(s Spec) []string {
	req, _ := s.Schema["required"].([]any)
	out := make([]string, 0, len(req))
	for _, r := range req {
		if v, ok := r.(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// PropertyOrder returns the spec's top-level property names with required keys
// first (in declaration order), then the remaining ones sorted. GBNF emission
// and the strict-schema rewrite both depend on this being deterministic.
func PropertyOrder(node map[string]any) []string {
	props, _ := node["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	if req, ok := node["required"].([]any); ok {
		for _, r := range req {
			name, _ := r.(string)
			if name == "" || seen[name] {
				continue
			}
			if _, has := props[name]; has {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	var rest []string
	for k := range props {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// IsRequired reports whether name is in node's required list.
func IsRequired(node map[string]any, name string) bool {
	req, _ := node["required"].([]any)
	for _, r := range req {
		if s, ok := r.(string); ok && s == name {
			return true
		}
	}
	return false
}

// StrictSchema returns the provider-facing variant for OpenAI
// `json_schema` + `strict: true`: every property is listed in `required` and
// `additionalProperties` is false at every object level. The semantic
// `required` list on Spec.Schema is what Validate uses — this variant only
// shapes decoding.
func StrictSchema(s Spec) map[string]any {
	return strictNode(s.Schema)
}

func strictNode(node map[string]any) map[string]any {
	out := make(map[string]any, len(node)+2)
	for k, v := range node {
		out[k] = v
	}
	typ, _ := out["type"].(string)
	switch typ {
	case "object":
		props, _ := out["properties"].(map[string]any)
		if len(props) > 0 {
			np := make(map[string]any, len(props))
			names := make([]string, 0, len(props))
			for k, v := range props {
				names = append(names, k)
				if child, ok := v.(map[string]any); ok {
					np[k] = strictNode(child)
				} else {
					np[k] = v
				}
			}
			sort.Strings(names)
			ordered := PropertyOrder(node)
			// PropertyOrder already covers every key; fall back defensively.
			if len(ordered) != len(names) {
				ordered = names
			}
			req := make([]any, 0, len(ordered))
			for _, n := range ordered {
				req = append(req, n)
			}
			out["properties"] = np
			out["required"] = req
		}
		out["additionalProperties"] = false
	case "array":
		if item, ok := out["items"].(map[string]any); ok {
			out["items"] = strictNode(item)
		}
	}
	return out
}

// DetectRole picks the schema role whose required keys best match a prompt
// body. It exists because one agent can be re-tasked with a different output
// contract by prompt (the planner agent also runs the clarify interview, the
// reviewer agent also runs the scope judge), so binding a schema to an agent id
// alone would constrain the wrong contract.
//
// hint is the contract bound to the agent; it wins ties and is returned when
// nothing scores.
func DetectRole(prompt, hint string) (Spec, bool) {
	if strings.TrimSpace(prompt) == "" {
		return For(hint)
	}
	best, bestScore := "", 0
	for name, spec := range registry {
		req := RequiredKeys(spec)
		if len(req) == 0 {
			continue
		}
		hits := 0
		for _, k := range req {
			if strings.Contains(prompt, `"`+k+`"`) {
				hits++
			}
		}
		if hits != len(req) {
			continue // every required key must be quoted in the contract block
		}
		// Optional keys break ties between contracts with the same required set
		// (worker and placeholder both require status/summary/files_changed, but
		// only the placeholder contract mentions gaps_flagged).
		optional := 0
		for _, k := range PropertyOrder(spec.Schema) {
			if IsRequired(spec.Schema, k) {
				continue
			}
			if strings.Contains(prompt, `"`+k+`"`) {
				optional++
			}
		}
		score := hits*10 + optional*3
		if name == normalizeRole(hint) {
			score += 100
		}
		if score > bestScore {
			best, bestScore = name, score
		}
	}
	if best == "" {
		return For(hint)
	}
	return For(best)
}

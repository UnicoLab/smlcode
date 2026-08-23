package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Validate structurally validates raw against the schema registered for role.
// Errors are written for a small model to act on: they name the JSON pointer,
// what was expected, and what was seen.
func Validate(role string, raw []byte) error {
	spec, ok := For(role)
	if !ok {
		return fmt.Errorf("schema: unknown role %q", role)
	}
	return ValidateSpec(spec, raw)
}

// ValidateSpec validates raw against an explicit Spec.
func ValidateSpec(spec Spec, raw []byte) error {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("schema %s: not valid JSON: %w", spec.Name, err)
	}
	var errs []string
	validateNode(spec.Schema, v, "$", &errs)
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	if len(errs) > 8 {
		errs = append(errs[:8], fmt.Sprintf("(+%d more)", len(errs)-8))
	}
	return fmt.Errorf("schema %s: %s", spec.Name, strings.Join(errs, "; "))
}

func validateNode(node map[string]any, v any, path string, errs *[]string) {
	if len(node) == 0 {
		return
	}
	typ, _ := node["type"].(string)
	switch typ {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected object, got %s", path, kindOf(v)))
			return
		}
		for _, name := range RequiredNames(node) {
			if _, present := m[name]; !present {
				*errs = append(*errs, fmt.Sprintf("%s: missing required key %q", path, name))
			}
		}
		props, _ := node["properties"].(map[string]any)
		for k, child := range props {
			cv, present := m[k]
			if !present || cv == nil {
				continue
			}
			cn, ok := child.(map[string]any)
			if !ok {
				continue
			}
			validateNode(cn, cv, path+"."+k, errs)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected array, got %s", path, kindOf(v)))
			return
		}
		if n, ok := intOf(node["minItems"]); ok && len(arr) < n {
			*errs = append(*errs, fmt.Sprintf("%s: needs at least %d items, got %d", path, n, len(arr)))
		}
		if n, ok := intOf(node["maxItems"]); ok && len(arr) > n {
			*errs = append(*errs, fmt.Sprintf("%s: at most %d items allowed, got %d", path, n, len(arr)))
		}
		item, _ := node["items"].(map[string]any)
		if item == nil {
			return
		}
		for i, e := range arr {
			validateNode(item, e, fmt.Sprintf("%s[%d]", path, i), errs)
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected string, got %s", path, kindOf(v)))
			return
		}
		if n, ok := intOf(node["minLength"]); ok && len(s) < n {
			*errs = append(*errs, fmt.Sprintf("%s: string shorter than %d chars", path, n))
		}
		if n, ok := intOf(node["maxLength"]); ok && len(s) > n {
			*errs = append(*errs, fmt.Sprintf("%s: string longer than %d chars", path, n))
		}
		if e, ok := node["enum"].([]any); ok && len(e) > 0 {
			if !enumHas(e, s) {
				*errs = append(*errs, fmt.Sprintf("%s: %q is not one of %s", path, s, enumList(e)))
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected boolean (true/false), got %s", path, kindOf(v)))
		}
	case "integer":
		n, ok := numberOf(v)
		if !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected integer, got %s", path, kindOf(v)))
			return
		}
		if n != float64(int64(n)) {
			*errs = append(*errs, fmt.Sprintf("%s: expected whole integer, got %v", path, n))
			return
		}
		if lo, ok := intOf(node["minimum"]); ok && int64(n) < int64(lo) {
			*errs = append(*errs, fmt.Sprintf("%s: %d is below minimum %d", path, int64(n), lo))
		}
		if hi, ok := intOf(node["maximum"]); ok && int64(n) > int64(hi) {
			*errs = append(*errs, fmt.Sprintf("%s: %d is above maximum %d", path, int64(n), hi))
		}
	case "number":
		if _, ok := numberOf(v); !ok {
			*errs = append(*errs, fmt.Sprintf("%s: expected number, got %s", path, kindOf(v)))
		}
	}
}

// RequiredNames returns node's required property names in declaration order.
func RequiredNames(node map[string]any) []string {
	req, _ := node["required"].([]any)
	out := make([]string, 0, len(req))
	for _, r := range req {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Coerce repairs the type slips small models make constantly — "true" instead
// of true, "3" instead of 3, a bare scalar where an array is required, a number
// where a string is required — using the schema for role as the target shape.
// The returned bytes are compact JSON. Unknown keys are preserved.
func Coerce(role string, raw []byte) ([]byte, error) {
	spec, ok := For(role)
	if !ok {
		return nil, fmt.Errorf("schema: unknown role %q", role)
	}
	return CoerceSpec(spec, raw)
}

// CoerceSpec coerces raw toward an explicit Spec.
func CoerceSpec(spec Spec, raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("schema %s: not valid JSON: %w", spec.Name, err)
	}
	out := coerceNode(spec.Schema, v)
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("schema %s: re-encode: %w", spec.Name, err)
	}
	return b, nil
}

func coerceNode(node map[string]any, v any) any {
	if len(node) == 0 {
		return normalizeNumbers(v)
	}
	typ, _ := node["type"].(string)
	switch typ {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return normalizeNumbers(v)
		}
		props, _ := node["properties"].(map[string]any)
		out := make(map[string]any, len(m))
		for k, cv := range m {
			if cn, ok := props[k].(map[string]any); ok {
				out[k] = coerceNode(cn, cv)
				continue
			}
			out[k] = normalizeNumbers(cv)
		}
		return out
	case "array":
		item, _ := node["items"].(map[string]any)
		arr, ok := v.([]any)
		if !ok {
			if v == nil {
				return []any{}
			}
			// scalar → [scalar]; a comma/newline separated string → list
			if s, isStr := v.(string); isStr {
				if parts := splitScalarList(s); len(parts) > 1 {
					arr = make([]any, 0, len(parts))
					for _, p := range parts {
						arr = append(arr, p)
					}
				} else {
					arr = []any{v}
				}
			} else {
				arr = []any{v}
			}
		}
		out := make([]any, 0, len(arr))
		for _, e := range arr {
			if item != nil {
				out = append(out, coerceNode(item, e))
				continue
			}
			out = append(out, normalizeNumbers(e))
		}
		return out
	case "string":
		switch t := v.(type) {
		case string:
			return t
		case bool:
			return strconv.FormatBool(t)
		case json.Number:
			return t.String()
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case nil:
			return ""
		}
		return normalizeNumbers(v)
	case "boolean":
		switch t := v.(type) {
		case bool:
			return t
		case string:
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "true", "yes", "y", "1", "pass", "passed", "ok", "approved":
				return true
			case "false", "no", "n", "0", "fail", "failed", "rejected":
				return false
			}
		case json.Number:
			if n, err := t.Float64(); err == nil {
				return n != 0
			}
		case float64:
			return t != 0
		case nil:
			return false
		}
		return normalizeNumbers(v)
	case "integer":
		switch t := v.(type) {
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return n
			}
			if f, err := t.Float64(); err == nil {
				return int64(f)
			}
		case float64:
			return int64(t)
		case bool:
			if t {
				return int64(1)
			}
			return int64(0)
		case string:
			s := strings.TrimSpace(t)
			s = strings.TrimSuffix(s, "%")
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return int64(f)
			}
		case nil:
			return int64(0)
		}
		return normalizeNumbers(v)
	case "number":
		switch t := v.(type) {
		case json.Number:
			if f, err := t.Float64(); err == nil {
				return f
			}
		case float64:
			return t
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
				return f
			}
		case bool:
			if t {
				return float64(1)
			}
			return float64(0)
		case nil:
			return float64(0)
		}
		return normalizeNumbers(v)
	}
	return normalizeNumbers(v)
}

// splitScalarList turns "a, b, c" into ["a","b","c"]. Single values (and
// anything containing JSON-ish punctuation) stay whole.
func splitScalarList(s string) []string {
	if strings.ContainsAny(s, "{}[]") {
		return []string{s}
	}
	sep := ""
	switch {
	case strings.Contains(s, "\n"):
		sep = "\n"
	case strings.Contains(s, ","):
		sep = ","
	default:
		return []string{s}
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "- "))
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeNumbers(e)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, normalizeNumbers(e))
		}
		return out
	}
	return v
}

func kindOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number, float64, int, int64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", t)
	}
}

func intOf(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

func numberOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

func enumHas(e []any, s string) bool {
	for _, v := range e {
		if x, ok := v.(string); ok && x == s {
			return true
		}
	}
	return false
}

func enumList(e []any) string {
	var parts []string
	for _, v := range e {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, "|")
}

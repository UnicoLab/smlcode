package workspace

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Small local models routinely emit JSON scalars as strings ("offset": "200",
// "replace_all": "true") or as floats. The coercion helpers below accept every
// shape an SLM realistically produces so a well-intentioned call is never
// silently downgraded to a default.

// intArg returns args[key] as an int, accepting int/float/json.Number/string
// (including "12", " 12 ", "12.0"). Unparseable values fall back to def.
func intArg(args map[string]interface{}, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	case bool:
		if n {
			return 1
		}
		return 0
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
		return def
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return def
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
		return def
	default:
		return def
	}
}

// boolArg returns args[key] as a bool, accepting bool, numbers, and the string
// spellings SLMs emit ("true", "True", "1", "yes", "on"). Anything else → def.
func boolArg(args map[string]interface{}, key string, def bool) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case int:
		return b != 0
	case int32:
		return b != 0
	case int64:
		return b != 0
	case float64:
		return b != 0
	case float32:
		return b != 0
	case json.Number:
		if i, err := b.Int64(); err == nil {
			return i != 0
		}
		return def
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "t", "1", "yes", "y", "on":
			return true
		case "false", "f", "0", "no", "n", "off":
			return false
		default:
			return def
		}
	default:
		return def
	}
}

// strArg returns args[key] as a string. Non-string scalars are rendered rather
// than dropped (a model passing "path": 12 gets "12", not "").
func strArg(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	case bool:
		return strconv.FormatBool(s)
	case float64:
		// JSON numbers decode as float64; render 200 as "200", not "200.000000".
		if s == float64(int64(s)) {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'g', -1, 64)
	case int, int32, int64, uint, uint32, uint64, float32:
		return fmt.Sprintf("%v", s)
	default:
		return ""
	}
}

package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Reflection over the Config struct's YAML tags.
//
// Every consumer of the config surface — `slmcode config set`, the SLMCODE_*
// environment overlay, the Studio settings page, the user-level layer and the
// intent-only writer — needs the same three operations: enumerate the keys,
// read one, write one with validation. Doing that by reflection over the
// struct tags means a new field is configurable everywhere the moment it is
// declared, instead of needing a matching entry in four hand-written tables.

// fieldRef locates one YAML key inside Config.
type fieldRef struct {
	Key   string
	Index int
	Type  reflect.Type
	// OmitEmpty mirrors the struct tag, so the writer can skip empty values.
	OmitEmpty bool
}

var (
	fieldsOnce  sync.Once
	fieldsByKey map[string]fieldRef
	fieldOrder  []string
)

func buildFields() {
	fieldsByKey = map[string]fieldRef{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		key := strings.TrimSpace(parts[0])
		if key == "" || key == "-" {
			continue
		}
		omit := false
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				omit = true
			}
		}
		fieldsByKey[key] = fieldRef{Key: key, Index: i, Type: f.Type, OmitEmpty: omit}
		fieldOrder = append(fieldOrder, key)
	}
}

func fields() map[string]fieldRef {
	fieldsOnce.Do(buildFields)
	return fieldsByKey
}

// Keys lists every persisted config key in struct declaration order.
func Keys() []string {
	fieldsOnce.Do(buildFields)
	out := make([]string, len(fieldOrder))
	copy(out, fieldOrder)
	return out
}

// HasKey reports whether key names a real config field.
func HasKey(key string) bool {
	_, ok := fields()[CanonicalKey(key)]
	return ok
}

// KindOf returns the schema type name ("string", "int", "bool", "float",
// "duration", "string[]", "map") for a key.
func KindOf(key string) (string, bool) {
	f, ok := fields()[CanonicalKey(key)]
	if !ok {
		return "", false
	}
	return typeName(f.Type), true
}

func typeName(t reflect.Type) string {
	if t == reflect.TypeOf(time.Duration(0)) {
		return "duration"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return "string[]"
		}
		return "list"
	case reflect.Map:
		return "map"
	default:
		return "object"
	}
}

// Get returns the current value of key as a plain Go value.
func (c *Config) Get(key string) (any, bool) {
	f, ok := fields()[CanonicalKey(key)]
	if !ok || c == nil {
		return nil, false
	}
	v := reflect.ValueOf(*c).Field(f.Index)
	if f.Type == reflect.TypeOf(time.Duration(0)) {
		return time.Duration(v.Int()).String(), true
	}
	return v.Interface(), true
}

// Values renders the effective config as a flat key→value map, keyed exactly
// like the YAML file and the schema. Durations render as "5m0s" strings.
func (c *Config) Values() map[string]any {
	out := map[string]any{}
	if c == nil {
		return out
	}
	for _, k := range Keys() {
		if v, ok := c.Get(k); ok {
			out[k] = v
		}
	}
	return out
}

// ValueError names the key, the offending value and the allowed set. It is the
// single error shape every config write path reports, so a bad value from a
// flag, an env var, a YAML file or the Studio API reads the same way.
type ValueError struct {
	Key     string
	Value   string
	Want    string
	Allowed []string
}

func (e *ValueError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: invalid value %q", e.Key, e.Value)
	if e.Want != "" {
		b.WriteString(" — want " + e.Want)
	}
	if len(e.Allowed) > 0 {
		b.WriteString(" — allowed: " + strings.Join(e.Allowed, ", "))
	}
	return b.String()
}

// UnknownKeyError is returned for a key that is not part of the config.
type UnknownKeyError struct{ Key string }

func (e *UnknownKeyError) Error() string {
	return fmt.Sprintf("unknown config key %q — `slmcode config show --all` lists every key", e.Key)
}

// Set assigns key from a raw value, which may be a string (CLI, env, YAML
// scalar) or an already-typed Go value (JSON from Studio). It validates
// against the schema's allowed set and never leaves the field half-written.
//
// Set does NOT normalize: callers batch their writes and call Normalize once.
func (c *Config) Set(key string, raw any) error {
	if c == nil {
		return nil
	}
	key = CanonicalKey(key)
	f, ok := fields()[key]
	if !ok {
		return &UnknownKeyError{Key: key}
	}
	val, err := coerce(key, f.Type, raw)
	if err != nil {
		return err
	}
	reflect.ValueOf(c).Elem().Field(f.Index).Set(val)
	return nil
}

// Normalize re-applies every default, clamp and alias. Exported so callers
// that batch Set calls can finish the write.
func (c *Config) Normalize() {
	if c != nil {
		normalize(c)
	}
}

// ApplyValues writes a whole key→value document, skipping unknown keys and
// collecting per-key errors instead of abandoning the batch: one bad value in
// a user config must not discard the rest of the layer.
func (c *Config) ApplyValues(values map[string]any) (applied []string, errs []error) {
	for _, k := range sortedKeys(values) {
		if k == "config_version" {
			continue
		}
		if err := c.Set(k, values[k]); err != nil {
			errs = append(errs, err)
			continue
		}
		applied = append(applied, k)
	}
	sort.Strings(applied)
	return applied, errs
}

// coerce converts raw into a value assignable to typ, validating enums.
func coerce(key string, typ reflect.Type, raw any) (reflect.Value, error) {
	if raw == nil {
		return reflect.Zero(typ), nil
	}
	// An already-correct value passes straight through (JSON decoding of
	// nested maps/slices produces exactly this).
	rv := reflect.ValueOf(raw)
	if rv.Type() == typ {
		if typ.Kind() == reflect.String {
			if err := checkEnum(key, rv.String()); err != nil {
				return reflect.Value{}, err
			}
		}
		return rv, nil
	}

	if typ == reflect.TypeOf(time.Duration(0)) {
		d, err := toDuration(raw)
		if err != nil {
			return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw),
				Want: "a duration such as 30s, 5m or 1h30m"}
		}
		return reflect.ValueOf(d), nil
	}

	switch typ.Kind() {
	case reflect.String:
		s := fmt.Sprint(raw)
		if err := checkEnum(key, s); err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(s).Convert(typ), nil

	case reflect.Bool:
		b, err := toBool(raw)
		if err != nil {
			return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw),
				Want: "a boolean", Allowed: []string{"true", "false"}}
		}
		return reflect.ValueOf(b).Convert(typ), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := toInt(raw)
		if err != nil {
			return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw), Want: "a whole number"}
		}
		return reflect.ValueOf(n).Convert(typ), nil

	case reflect.Float32, reflect.Float64:
		f, err := toFloat(raw)
		if err != nil {
			return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw), Want: "a number"}
		}
		return reflect.ValueOf(f).Convert(typ), nil

	case reflect.Slice:
		return coerceSlice(key, typ, raw)

	case reflect.Map:
		return coerceMap(key, typ, raw)
	}
	return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw), Want: typ.String()}
}

func coerceSlice(key string, typ reflect.Type, raw any) (reflect.Value, error) {
	if typ.Elem().Kind() == reflect.String {
		switch v := raw.(type) {
		case string:
			return reflect.ValueOf(splitList(v)).Convert(typ), nil
		case []string:
			return reflect.ValueOf(v).Convert(typ), nil
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				out = append(out, fmt.Sprint(item))
			}
			return reflect.ValueOf(out).Convert(typ), nil
		}
	}
	// Structured slices (mcp_servers) round-trip through their own decoder.
	if v, err := reencode(typ, raw); err == nil {
		return v, nil
	}
	return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw), Want: "a list"}
}

func coerceMap(key string, typ reflect.Type, raw any) (reflect.Value, error) {
	if v, err := reencode(typ, raw); err == nil {
		return v, nil
	}
	return reflect.Value{}, &ValueError{Key: key, Value: fmt.Sprint(raw), Want: "a mapping"}
}

// splitList parses the CLI's comma-separated list spelling. "-" and "" clear.
func splitList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" || v == "-" {
		return []string{}
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func toBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on", "enable", "enabled":
			return true, nil
		case "0", "false", "no", "off", "disable", "disabled":
			return false, nil
		}
		return false, strconv.ErrSyntax
	case int:
		return v != 0, nil
	case float64:
		return v != 0, nil
	}
	return false, strconv.ErrSyntax
}

func toInt(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	}
	return 0, strconv.ErrSyntax
}

func toFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	return 0, strconv.ErrSyntax
}

// toDuration accepts "5m", a bare number of seconds, or the nanosecond integer
// older config files stored (yaml.v3 marshals time.Duration as an int64).
func toDuration(raw any) (time.Duration, error) {
	switch v := raw.(type) {
	case time.Duration:
		return v, nil
	case int:
		return durationFromNumber(float64(v)), nil
	case int64:
		return durationFromNumber(float64(v)), nil
	case float64:
		return durationFromNumber(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, nil
		}
		if d, err := time.ParseDuration(s); err == nil {
			return d, nil
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return durationFromNumber(n), nil
		}
		return 0, strconv.ErrSyntax
	}
	return 0, strconv.ErrSyntax
}

// durationFromNumber disambiguates the two historical numeric spellings.
// Anything at or above a second's worth of nanoseconds is a nanosecond count
// written by yaml.v3; anything smaller is a human writing seconds.
func durationFromNumber(n float64) time.Duration {
	if n >= float64(time.Second) {
		return time.Duration(n)
	}
	return time.Duration(n * float64(time.Second))
}

// checkEnum validates a string against the schema's allowed set, when it has one.
func checkEnum(key, value string) error {
	f, ok := Field(key)
	if !ok || len(f.Enum) == 0 {
		return nil
	}
	v := strings.ToLower(strings.TrimSpace(value))
	for _, allowed := range f.Enum {
		if v == strings.ToLower(allowed) {
			return nil
		}
	}
	// An empty value means "clear" wherever the empty string is allowed.
	if v == "" && f.AllowEmpty {
		return nil
	}
	return &ValueError{Key: key, Value: value, Allowed: f.Enum}
}

// reencode round-trips a decoded YAML/JSON value into a typed field value.
// It is the escape hatch for the structured fields (model_profiles,
// mcp_servers, context_role_budget) whose shape the scalar coercions above
// cannot express.
func reencode(typ reflect.Type, raw any) (reflect.Value, error) {
	data, err := yaml.Marshal(raw)
	if err != nil {
		return reflect.Value{}, err
	}
	out := reflect.New(typ)
	if err := yaml.Unmarshal(data, out.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return out.Elem(), nil
}

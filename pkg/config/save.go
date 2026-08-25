package config

import (
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// Intent-only persistence.
//
// Save used to marshal the whole struct, so `slmcode init` produced a 138-line
// config.yaml of nothing but defaults. Three things broke as a result:
//
//   - you could not tell what the user chose from what was inherited, which
//     made `config show --origin`, the user layer and Studio's settings page
//     all guess;
//   - an improved default in a new release never reached an existing project,
//     because the old value was written down verbatim;
//   - the file embedded an absolute `root:` that Load honored, so a config
//     copied between machines pointed at someone else's directory.
//
// Save now writes only the keys that differ from the inherited baseline
// (defaults, plus the user-level layer when one is active), stamps
// config_version, and never writes root.

// SaveHeader is the comment block written at the top of every config.yaml.
const SaveHeader = ` slmcode project config — only the values you changed.
 Everything else follows the built-in defaults and your user-level config,
 so upgrades keep improving this project without editing this file.

 slmcode config show --all       every key, its value and where it came from
 slmcode config show --origin    default | user | project | env | flag
 slmcode config set <key> <val>  validated against the schema`

func (c *Config) Save() error {
	if err := os.MkdirAll(c.SlmDir(), 0o750); err != nil {
		return err
	}
	data, err := c.MarshalIntent()
	if err != nil {
		return err
	}
	return atomicfile.WriteWithBackup(c.ConfigPath(), data, 0o644)
}

// MarshalIntent renders the config as the minimal YAML document that
// reproduces it from the inherited baseline.
func (c *Config) MarshalIntent() ([]byte, error) {
	node, err := c.intentNode()
	if err != nil {
		return nil, err
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{node}}
	node.HeadComment = SaveHeader
	return yaml.Marshal(doc)
}

// Diff returns the keys whose effective value differs from the inherited
// baseline — exactly what MarshalIntent writes, minus config_version.
func (c *Config) Diff() []string {
	base := c.saveBaseline()
	var out []string
	for _, key := range Keys() {
		if skipOnSave(key) {
			continue
		}
		if !c.sameAs(base, key) {
			out = append(out, key)
		}
	}
	return out
}

// saveBaseline is what the file is diffed against: the layers below the
// project file. Load records it; a config built by hand falls back to the
// built-in defaults for the same root.
func (c *Config) saveBaseline() *Config {
	base := c.prov.storedBaseline()
	if base == nil {
		base = Default(c.Root)
		normalize(base)
	}
	// max_parallel's default is derived from the effective endpoint, but the
	// baseline was built before the project file could change that endpoint.
	// Without this, a project that only writes `provider: openai` would ALSO
	// have `max_parallel: 4` written into it — an inherited default frozen into
	// the file, which is exactly what intent-only persistence exists to avoid.
	//
	// The equality guard keeps a directly-assigned field (an embedder doing
	// `cfg.MaxParallel = 9`) writable: only a value that IS the derived default
	// for this endpoint is treated as inherited.
	if !c.maxParallelSet && c.MaxParallel == DefaultMaxParallelFor(c.Provider, c.Endpoint) {
		cp := *base
		cp.MaxParallel = c.MaxParallel
		base = &cp
	}
	return base
}

// skipOnSave lists keys that must never be persisted to a project file.
func skipOnSave(key string) bool {
	switch key {
	case "config_version":
		return true // written explicitly, first
	case "api_key", "embedding_api_key":
		// Secrets live in .slmcode/auth.json or the environment. The opt-in
		// escape hatch below is the only way they reach YAML.
		return os.Getenv("SLMCODE_PERSIST_API_KEY") != "1"
	}
	return false
}

func (c *Config) sameAs(base *Config, key string) bool {
	f, ok := fields()[key]
	if !ok {
		return true
	}
	mine := reflect.ValueOf(*c).Field(f.Index).Interface()
	theirs := reflect.ValueOf(*base).Field(f.Index).Interface()
	return reflect.DeepEqual(mine, theirs)
}

// intentNode builds the YAML mapping, in struct declaration order so related
// keys stay together instead of being alphabetised apart.
func (c *Config) intentNode() (*yaml.Node, error) {
	base := c.saveBaseline()
	m := &yaml.Node{Kind: yaml.MappingNode}

	appendPair := func(key string, val *yaml.Node) {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
	}
	appendPair("config_version", &yaml.Node{
		Kind: yaml.ScalarNode, Tag: "!!int", Value: itoa(CurrentConfigVersion)})

	for _, key := range Keys() {
		if skipOnSave(key) || c.sameAs(base, key) {
			continue
		}
		f := fields()[key]
		raw := reflect.ValueOf(*c).Field(f.Index).Interface()
		if f.OmitEmpty && isEmptyValue(reflect.ValueOf(raw)) {
			continue
		}
		val, err := valueNode(f, raw)
		if err != nil {
			return nil, err
		}
		appendPair(key, val)
	}
	return m, nil
}

// valueNode renders one field. Durations become readable strings ("5m0s")
// instead of the nanosecond integers yaml.v3 emits for time.Duration.
func valueNode(f fieldRef, raw any) (*yaml.Node, error) {
	if f.Type == reflect.TypeOf(time.Duration(0)) {
		d, _ := raw.(time.Duration)
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: d.String()}, nil
	}
	var n yaml.Node
	if err := n.Encode(raw); err != nil {
		return nil, err
	}
	return &n, nil
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// SaveInitial writes the minimal config a fresh `slmcode init` should leave
// behind: only the keys detected during scaffolding, plus the header. Keys the
// caller did not name are left to the defaults and the user layer.
func (c *Config) SaveInitial(keys ...string) error {
	if err := os.MkdirAll(c.SlmDir(), 0o750); err != nil {
		return err
	}
	base := c.saveBaseline()
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, val *yaml.Node) {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
	}
	add("config_version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: itoa(CurrentConfigVersion)})

	seen := map[string]bool{}
	var written []string
	for _, key := range keys {
		key = CanonicalKey(key)
		f, ok := fields()[key]
		if !ok || seen[key] || skipOnSave(key) {
			continue
		}
		seen[key] = true
		raw := reflect.ValueOf(*c).Field(f.Index).Interface()
		if isEmptyValue(reflect.ValueOf(raw)) {
			continue
		}
		val, err := valueNode(f, raw)
		if err != nil {
			return err
		}
		add(key, val)
		written = append(written, key)
	}
	// Anything the user already changed by flag/env before init still belongs
	// in the file — otherwise `slmcode init --provider ollama` loses the choice
	// the moment a later command re-reads the config.
	for _, key := range Keys() {
		if seen[key] || skipOnSave(key) || c.sameAs(base, key) {
			continue
		}
		f := fields()[key]
		raw := reflect.ValueOf(*c).Field(f.Index).Interface()
		if f.OmitEmpty && isEmptyValue(reflect.ValueOf(raw)) {
			continue
		}
		val, err := valueNode(f, raw)
		if err != nil {
			return err
		}
		add(key, val)
		written = append(written, key)
	}
	m.HeadComment = SaveHeader
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{m}}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteWithBackup(c.ConfigPath(), data, 0o644); err != nil {
		return err
	}
	c.lastSavedKeys = written
	return nil
}

// SavedKeys reports the config keys the most recent SaveInitial actually wrote,
// excluding the config_version stamp.
//
// `slmcode init` used to report len(Diff()) as "N key(s)", which counts only
// the keys that differ from the baseline — but the file also carries the keys
// init was explicitly told to record, so a fresh Go project was described as
// "4 key(s)" over a file holding six.
func (c *Config) SavedKeys() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.lastSavedKeys...)
}

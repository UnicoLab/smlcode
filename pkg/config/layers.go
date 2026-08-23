package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// Configuration layering.
//
// pkg/blocks already resolved project → user → extra → builtin; config did
// not, so every new repo started from hard-coded defaults and a preferred
// provider, model or parallelism had to be re-set per project. The user layer
// lives here rather than in the CLI so it also covers non-patchable fields and
// so Studio, the TUI and any embedder see exactly the same layering.
//
// Precedence, lowest first: defaults → user file → project file → env → flags.

// Layer names one level of the precedence chain.
type Layer string

const (
	LayerDefault Layer = "default"
	LayerUser    Layer = "user"
	LayerProject Layer = "project"
	LayerEnv     Layer = "env"
	LayerFlag    Layer = "flag"
)

// Provenance records which layer supplied each effective value, so
// `slmcode config show --origin` can attribute a value instead of guessing
// from "does it differ from the default".
type Provenance struct {
	// UserPath / ProjectPath are the files that were read ("" when absent).
	UserPath    string
	ProjectPath string
	// FromVersion is the config_version the project file was written by.
	FromVersion int
	// Migrated is true when the project file needed upgrading on load.
	Migrated bool
	// Warnings collects non-fatal layering problems (a corrupt user file, a
	// key that could not be applied).
	Warnings []string

	layers  map[string]Layer
	envVars map[string]string
	// userKeys records which keys the user file supplied, so Unset can report
	// the layer a project key falls back TO rather than the one it came from.
	userKeys map[string]bool
	// baseline is defaults + user layer: what Save diffs the project file
	// against, so a value inherited from ~/.slmcode/config.yaml is not copied
	// into every project.
	baseline *Config
}

func newProvenance() *Provenance {
	return &Provenance{
		layers:   map[string]Layer{},
		envVars:  map[string]string{},
		userKeys: map[string]bool{},
	}
}

// Layer reports where the effective value for key came from.
func (p *Provenance) Layer(key string) Layer {
	if p == nil {
		return LayerDefault
	}
	if l, ok := p.layers[CanonicalKey(key)]; ok {
		return l
	}
	return LayerDefault
}

// EnvVar returns the environment variable that supplied key, when the layer is
// LayerEnv.
func (p *Provenance) EnvVar(key string) string {
	if p == nil {
		return ""
	}
	return p.envVars[CanonicalKey(key)]
}

// Describe renders the origin of one key: "default", "user", "project",
// "env SLMCODE_MODEL" or "flag --model".
func (p *Provenance) Describe(key string) string {
	l := p.Layer(key)
	switch l {
	case LayerEnv:
		if v := p.EnvVar(key); v != "" {
			return "env " + v
		}
	case LayerFlag:
		if v := p.EnvVar(key); v != "" {
			return "flag " + v
		}
	}
	return string(l)
}

// clearProjectMark drops a project-layer attribution, falling back to the user
// layer when that is where the value now comes from.
func (p *Provenance) clearProjectMark(key string) {
	if p == nil {
		return
	}
	key = CanonicalKey(key)
	if p.layers[key] == LayerEnv || p.layers[key] == LayerFlag {
		return // a higher layer still decides
	}
	if p.userKeys[key] {
		p.layers[key] = LayerUser
		return
	}
	delete(p.layers, key)
}

// Mark records that a layer supplied key. The CLI calls it for flags.
func (p *Provenance) Mark(key string, l Layer, source string) {
	if p == nil {
		return
	}
	key = CanonicalKey(key)
	p.layers[key] = l
	if source != "" {
		p.envVars[key] = source
	}
}

// Provenance returns the layering record, allocating one if the config was
// built without going through Load.
func (c *Config) Provenance() *Provenance {
	if c == nil {
		return nil
	}
	if c.prov == nil {
		c.prov = newProvenance()
	}
	return c.prov
}

// MarkFlag records that a command-line flag set key this run.
func (c *Config) MarkFlag(key, flag string) {
	c.Provenance().Mark(key, LayerFlag, flag)
}

// ── user-level discovery ──────────────────────────────────────────────────

// UserConfigPaths lists candidate user-level config files, most specific first.
//
//	$SLMCODE_USER_CONFIG          explicit override (file path)
//	$XDG_CONFIG_HOME/slmcode/…    XDG
//	~/.slmcode/config.yaml        the layout `slmcode` already owns
//	~/.config/slmcode/config.yaml XDG default
func UserConfigPaths() []string {
	var out []string
	if x := strings.TrimSpace(os.Getenv("SLMCODE_USER_CONFIG")); x != "" {
		out = append(out, x)
	}
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		out = append(out, filepath.Join(x, "slmcode", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			filepath.Join(home, DirName, "config.yaml"),
			filepath.Join(home, ".config", "slmcode", "config.yaml"),
		)
	}
	return out
}

// UserConfigPath returns the first existing user-level config file, or "".
func UserConfigPath() string {
	for _, p := range UserConfigPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// DefaultUserConfigPath is where `slmcode config set --user` writes when no
// user file exists yet.
func DefaultUserConfigPath() string {
	if x := strings.TrimSpace(os.Getenv("SLMCODE_USER_CONFIG")); x != "" {
		return x
	}
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "slmcode", "config.yaml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, DirName, "config.yaml")
	}
	return ""
}

// readDocument reads a config file into a raw key→value document.
func readDocument(path string) (map[string]any, bool, error) {
	data, fromBackup, err := readConfigBytes(path)
	if err != nil {
		return nil, false, err
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fromBackup, err
	}
	return raw, fromBackup, nil
}

// LoadUserLayer applies the user-level config onto c and records provenance.
// It never fails a run: a missing file is silent, a corrupt one is a warning.
func (c *Config) LoadUserLayer() {
	prov := c.Provenance()
	path := UserConfigPath()
	if path == "" {
		return
	}
	raw, _, err := readDocument(path)
	if err != nil {
		if !os.IsNotExist(err) {
			prov.Warnings = append(prov.Warnings, "user config "+path+": "+err.Error())
		}
		return
	}
	migrate(raw)
	// The user layer must never dictate a project root or a stack pin.
	delete(raw, "root")
	applied, errs := c.ApplyValues(raw)
	for _, e := range errs {
		prov.Warnings = append(prov.Warnings, "user config "+path+": "+e.Error())
	}
	prov.UserPath = path
	for _, k := range applied {
		prov.Mark(k, LayerUser, "")
		prov.userKeys[k] = true
	}
}

// WriteUserValue sets one key in the user-level config file, preserving every
// other key and comment-free structure of the file. The file is created (with
// a header) when it does not exist yet.
func WriteUserValue(path, key string, value any) error {
	key = CanonicalKey(key)
	raw := map[string]any{}
	//nolint:gosec // path is the user's own config location, resolved by UserConfigPaths
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return err
		}
		if raw == nil {
			raw = map[string]any{}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	raw["config_version"] = CurrentConfigVersion
	raw[key] = value
	delete(raw, "root")

	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range sortedKeys(raw) {
		var v yaml.Node
		if err := v.Encode(raw[k]); err != nil {
			return err
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, &v)
	}
	node.HeadComment = UserSaveHeader
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{node}}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return atomicfile.WriteWithBackup(path, data, 0o600)
}

// UserSaveHeader tops the user-level config file.
const UserSaveHeader = ` slmcode user config — defaults for every project on this machine.
 A project's .slmcode/config.yaml overrides anything set here.

 slmcode config show --origin    which layer supplied each value
 slmcode config set --user <key> <value>`

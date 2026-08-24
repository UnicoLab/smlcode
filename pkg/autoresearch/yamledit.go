package autoresearch

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Surgical YAML editing, on purpose.
//
// The obvious implementation — unmarshal into a struct, change a field,
// marshal back — rewrites the whole file: comments gone, key order shuffled,
// block scalars reflowed. That is unacceptable for files a human wrote and will
// read again, and it makes every snapshot diff unreadable. So mutation goes
// through yaml.Node: the document keeps its shape and exactly one scalar moves.

// yamlDoc is one parsed YAML file, kept as nodes so an edit is surgical.
type yamlDoc struct {
	path string
	node *yaml.Node
	mode fs.FileMode
}

// defaultYAMLMode is what a newly created YAML file gets: harness state under
// .slmcode is owner-only.
const defaultYAMLMode fs.FileMode = 0o600

// loadYAMLDoc parses path. A missing file yields an empty document rather than
// an error, so a project with no config.yaml still has a mutable surface.
func loadYAMLDoc(path string) (*yamlDoc, error) {
	mode := defaultYAMLMode
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if st, serr := os.Stat(path); serr == nil {
			mode = st.Mode().Perm()
		}
	case os.IsNotExist(err):
		data = nil
	default:
		return nil, err
	}

	doc := &yamlDoc{path: path, mode: mode}
	if len(bytes.TrimSpace(data)) == 0 {
		doc.node = emptyDocument()
		return doc, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		doc.node = emptyDocument()
		return doc, nil
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: top level is not a mapping", path)
	}
	doc.node = &root
	return doc, nil
}

func emptyDocument() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

// root returns the document's top-level mapping.
func (d *yamlDoc) root() *yaml.Node { return d.node.Content[0] }

// spec returns the mapping that carries the agent fields.
//
// Two shapes are in the wild and both are supported: a project agent file
// (.slmcode/agents/*.yaml) puts the fields at the top level, while a bundled
// block (pkg/blocks/bundled/agents/*.yaml) nests them under `spec:`. Picking
// the wrong one would silently write a second `temperature` key that nothing
// reads.
func (d *yamlDoc) spec() *yaml.Node {
	root := d.root()
	if v := mapValue(root, "spec"); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	return root
}

// save writes the document back, preserving the file's mode.
func (d *yamlDoc) save() error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.node); err != nil {
		_ = enc.Close()
		return fmt.Errorf("encode %s: %w", d.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", d.path, err)
	}
	mode := d.mode
	if mode == 0 {
		mode = defaultYAMLMode
	}
	return atomicfile.Write(d.path, buf.Bytes(), mode)
}

// mapIndex returns the index of key's NAME node in a mapping, or -1.
func mapIndex(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// mapValue returns the value node for key, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	i := mapIndex(m, key)
	if i < 0 {
		return nil
	}
	return m.Content[i+1]
}

// scalarString reads a scalar value out of a mapping. A non-scalar (a list, a
// nested map) reports ok=false: those are not knobs, and pretending otherwise
// would let a proposer flatten someone's `skills:` list into a string.
func scalarString(m *yaml.Node, key string) (string, bool) {
	v := mapValue(m, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return "", false
	}
	return v.Value, true
}

// setScalar writes key = value into a mapping, creating the key when absent and
// otherwise replacing only the value node — so the key keeps its position and
// any comment attached to it.
func setScalar(m *yaml.Node, key, value string, kind KnobKind) {
	next := scalarNode(value, kind)
	i := mapIndex(m, key)
	if i < 0 {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			next)
		return
	}
	old := m.Content[i+1]
	old.Kind = yaml.ScalarNode
	old.Tag = next.Tag
	old.Value = next.Value
	old.Style = next.Style
	old.Content = nil
	old.Anchor = ""
	old.Alias = nil
}

// agentID prefers the file's own `id:` and falls back to the filename, so two
// agents cannot collide on the surface just because one file was renamed.
func agentID(spec *yaml.Node, filename string) string {
	if v, ok := scalarString(spec, "id"); ok && strings.TrimSpace(v) != "" {
		return strings.ToLower(strings.TrimSpace(v))
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	return strings.ToLower(base)
}

// scalarNode builds a value node with the tag the knob's kind implies, so
// `temperature: 0.15` stays a number and does not become the string "0.15".
func scalarNode(value string, kind KnobKind) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: value}
	switch kind {
	case KnobInt:
		n.Tag = "!!int"
	case KnobFloat:
		n.Tag = "!!float"
	case KnobBool:
		n.Tag = "!!bool"
	default:
		n.Tag = "!!str"
		// A multi-line prompt written inline is unreadable and re-quotes every
		// backslash; a literal block keeps it looking like the prose it is.
		if strings.Contains(value, "\n") {
			n.Style = yaml.LiteralStyle
		}
	}
	return n
}

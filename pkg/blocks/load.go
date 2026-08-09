package blocks

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// FindExtraDirs locates optional on-disk blocks roots (env + repo walk).
func FindExtraDirs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		d = filepath.Clean(strings.TrimSpace(d))
		if d == "" || d == "." || seen[d] {
			return
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	if v := os.Getenv("SLMCODE_BLOCKS"); v != "" {
		for _, p := range strings.Split(v, string(os.PathListSeparator)) {
			add(p)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		for _, c := range walkUpBlocks(wd) {
			add(c)
		}
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), "blocks"))
		for _, c := range walkUpBlocks(filepath.Dir(exe)) {
			add(c)
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
		add(filepath.Join(root, "blocks"))
	}
	return out
}

func walkUpBlocks(start string) []string {
	var out []string
	dir := start
	for i := 0; i < 8; i++ {
		out = append(out, filepath.Join(dir, "blocks"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// ProjectBlocksDir is <root>/.slmcode/blocks.
func ProjectBlocksDir(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(root, ".slmcode", "blocks")
}

// UserBlocksDirs returns global user block roots.
func UserBlocksDirs() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".slmcode", "blocks"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		roots = append(roots, filepath.Join(xdg, "slmcode", "blocks"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".config", "slmcode", "blocks"))
	}
	return roots
}

// RootsForProject returns discovery roots in priority order for a project.
func RootsForProject(projectRoot string) []Root {
	var roots []Root
	if d := ProjectBlocksDir(projectRoot); d != "" {
		roots = append(roots, Root{Path: d, Source: SourceProject})
	}
	for _, d := range UserBlocksDirs() {
		roots = append(roots, Root{Path: d, Source: SourceUser})
	}
	for _, d := range FindExtraDirs() {
		roots = append(roots, Root{Path: d, Source: SourceExtra})
	}
	return roots
}

// Root is one discovery directory with a source label.
type Root struct {
	Path   string
	Source string
	FS     fs.FS // optional; when set, Path is the FS subdir prefix
}

func kindSubdir(kind string) string {
	switch kind {
	case KindPipeline:
		return "pipelines"
	case KindAgent:
		return "agents"
	case KindQuality:
		return "quality"
	case KindPack:
		return "packs"
	default:
		return kind
	}
}

func isYAML(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

func idFromFilename(name string) string {
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return strings.ToLower(strings.TrimSpace(base))
}

// peekMeta reads kind/id from YAML without full decode.
func peekMeta(data []byte) (Meta, error) {
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

func readFileFS(fsys fs.FS, path string) ([]byte, error) {
	return fs.ReadFile(fsys, path)
}

func listYAMLInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}

func listYAMLInFS(fsys fs.FS, prefix string) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, prefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isYAML(d.Name()) {
			return nil
		}
		// only direct children of prefix
		rel, relErr := filepath.Rel(prefix, path)
		if relErr != nil || strings.Contains(rel, string(filepath.Separator)) {
			// also accept forward slash for embed FS
			if strings.Contains(rel, "/") && filepath.Dir(rel) != "." {
				return nil
			}
			if filepath.Dir(filepath.ToSlash(rel)) != "." {
				return nil
			}
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

// LoadPipelineFile decodes one pipeline block YAML.
func LoadPipelineFile(path string, data []byte, source string) (*PipelineBlock, error) {
	var b PipelineBlock
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.ID == "" {
		b.ID = idFromFilename(path)
	}
	b.Kind = KindPipeline
	b.Source = source
	b.Path = path
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

// LoadAgentFile decodes one agent block YAML.
func LoadAgentFile(path string, data []byte, source string) (*AgentBlock, error) {
	var b AgentBlock
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.ID == "" {
		b.ID = idFromFilename(path)
	}
	b.Kind = KindAgent
	b.Source = source
	b.Path = path
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

// LoadQualityFile decodes one quality block YAML.
func LoadQualityFile(path string, data []byte, source string) (*QualityBlock, error) {
	var b QualityBlock
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.ID == "" {
		b.ID = idFromFilename(path)
	}
	b.Kind = KindQuality
	b.Source = source
	b.Path = path
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

// LoadPackFile decodes one pack block YAML.
func LoadPackFile(path string, data []byte, source string) (*PackBlock, error) {
	var b PackBlock
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.ID == "" {
		b.ID = idFromFilename(path)
	}
	b.Kind = KindPack
	b.Source = source
	b.Path = path
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

package blocks

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Registry is the in-memory catalog of discovered building blocks.
type Registry struct {
	Pipelines map[string]*PipelineBlock
	Agents    map[string]*AgentBlock
	Quality   map[string]*QualityBlock
	Packs     map[string]*PackBlock
}

// NewRegistry constructs an empty catalog.
func NewRegistry() *Registry {
	return &Registry{
		Pipelines: map[string]*PipelineBlock{},
		Agents:    map[string]*AgentBlock{},
		Quality:   map[string]*QualityBlock{},
		Packs:     map[string]*PackBlock{},
	}
}

// Load builds a registry for a project root (includes embedded builtins).
func Load(projectRoot string) (*Registry, error) {
	reg := NewRegistry()
	if err := reg.loadBuiltin(); err != nil {
		return nil, err
	}
	for _, root := range RootsForProject(projectRoot) {
		if err := reg.loadRoot(root); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func (r *Registry) loadBuiltin() error {
	return r.loadFS(bundled, "bundled", SourceBuiltin)
}

func (r *Registry) loadRoot(root Root) error {
	if root.FS != nil {
		return r.loadFS(root.FS, root.Path, root.Source)
	}
	if strings.TrimSpace(root.Path) == "" {
		return nil
	}
	st, err := os.Stat(root.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return nil
	}
	for _, kind := range []string{KindPipeline, KindAgent, KindQuality, KindPack} {
		dir := filepath.Join(root.Path, kindSubdir(kind))
		files, err := listYAMLInDir(dir)
		if err != nil {
			return err
		}
		// Also allow flat layout: blocks/<kind>-*.yaml at root of blocks dir.
		if kind == KindPipeline {
			// flat files with kind in content still loaded via walk below
		}
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			_ = r.ingest(path, data, root.Source, kind)
		}
	}
	// Flat YAML files directly under blocks/ (kind from document).
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		path := filepath.Join(root.Path, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = r.ingest(path, data, root.Source, "")
	}
	return nil
}

func (r *Registry) loadFS(fsys fs.FS, prefix, source string) error {
	for _, kind := range []string{KindPipeline, KindAgent, KindQuality, KindPack} {
		sub := filepath.ToSlash(filepath.Join(prefix, kindSubdir(kind)))
		files, err := listYAMLInFS(fsys, sub)
		if err != nil {
			// missing subdir is fine
			continue
		}
		for _, path := range files {
			data, err := readFileFS(fsys, path)
			if err != nil {
				continue
			}
			_ = r.ingest(path, data, source, kind)
		}
	}
	return nil
}

// ingest parses and stores a block. Higher-priority sources overwrite lower ones
// when called in reverse priority order — Load calls builtin first, then project
// last so project wins.
func (r *Registry) ingest(path string, data []byte, source, expectKind string) error {
	m, err := peekMeta(data)
	if err != nil {
		return err
	}
	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	if kind == "" {
		kind = expectKind
	}
	if expectKind != "" && kind != "" && kind != expectKind {
		return fmt.Errorf("%s: kind %q != expected %q", path, kind, expectKind)
	}
	if kind == "" {
		return fmt.Errorf("%s: missing kind", path)
	}
	switch kind {
	case KindPipeline:
		b, err := LoadPipelineFile(path, data, source)
		if err != nil {
			return err
		}
		r.Pipelines[b.ID] = b
	case KindAgent:
		b, err := LoadAgentFile(path, data, source)
		if err != nil {
			return err
		}
		r.Agents[b.ID] = b
	case KindQuality:
		b, err := LoadQualityFile(path, data, source)
		if err != nil {
			return err
		}
		r.Quality[b.ID] = b
	case KindPack:
		b, err := LoadPackFile(path, data, source)
		if err != nil {
			return err
		}
		r.Packs[b.ID] = b
	default:
		return fmt.Errorf("%s: unknown kind %q", path, kind)
	}
	return nil
}

// Catalog returns sorted discovery entries, optionally filtered by kind.
func (r *Registry) Catalog(kind string) []CatalogEntry {
	if r == nil {
		return nil
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	var out []CatalogEntry
	add := func(m Meta) {
		if kind != "" && m.Kind != kind {
			return
		}
		out = append(out, m.ToEntry())
	}
	for _, b := range r.Pipelines {
		add(b.Meta)
	}
	for _, b := range r.Agents {
		add(b.Meta)
	}
	for _, b := range r.Quality {
		add(b.Meta)
	}
	for _, b := range r.Packs {
		add(b.Meta)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GetPack returns a pack by id.
func (r *Registry) GetPack(id string) (*PackBlock, bool) {
	if r == nil {
		return nil, false
	}
	b, ok := r.Packs[strings.ToLower(strings.TrimSpace(id))]
	return b, ok
}

// GetPipeline returns a pipeline block by id.
func (r *Registry) GetPipeline(id string) (*PipelineBlock, bool) {
	if r == nil {
		return nil, false
	}
	b, ok := r.Pipelines[strings.ToLower(strings.TrimSpace(id))]
	return b, ok
}

// GetQuality returns a quality block by id.
func (r *Registry) GetQuality(id string) (*QualityBlock, bool) {
	if r == nil {
		return nil, false
	}
	b, ok := r.Quality[strings.ToLower(strings.TrimSpace(id))]
	return b, ok
}

// GetAgent returns an agent block by id.
func (r *Registry) GetAgent(id string) (*AgentBlock, bool) {
	if r == nil {
		return nil, false
	}
	b, ok := r.Agents[strings.ToLower(strings.TrimSpace(id))]
	return b, ok
}

// DetectQuality picks the best quality pack for a workspace root.
func (r *Registry) DetectQuality(workspaceRoot string) *QualityBlock {
	if r == nil || workspaceRoot == "" {
		return nil
	}
	type scored struct {
		b *QualityBlock
		p int
	}
	var best *scored
	for _, q := range r.Quality {
		p := scoreDetect(workspaceRoot, q.Spec.Detect)
		if p <= 0 {
			continue
		}
		p += q.Spec.Detect.Priority
		if best == nil || p > best.p {
			best = &scored{b: q, p: p}
		}
	}
	if best == nil {
		return nil
	}
	return best.b
}

func scoreDetect(root string, d DetectSpec) int {
	score := 0
	for _, f := range d.Files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			score += 10
		}
	}
	if len(d.Extensions) == 0 {
		return score
	}
	extSet := map[string]bool{}
	for _, e := range d.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" && !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if e != "" {
			extSet[e] = true
		}
	}
	found := 0
	_ = filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			name := ""
			if de != nil {
				name = de.Name()
			}
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".slmcode" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if extSet[ext] {
			found++
			if found >= 3 {
				return fs.SkipAll
			}
		}
		return nil
	})
	score += found * 2
	return score
}

// ResolvePackRefs validates that a pack's references exist.
func (r *Registry) ResolvePackRefs(pack *PackBlock) error {
	if pack == nil {
		return fmt.Errorf("nil pack")
	}
	if pack.Spec.Pipeline != "" {
		if _, ok := r.GetPipeline(pack.Spec.Pipeline); !ok {
			return fmt.Errorf("pack %q: unknown pipeline %q", pack.ID, pack.Spec.Pipeline)
		}
	}
	if pack.Spec.Quality != "" {
		if _, ok := r.GetQuality(pack.Spec.Quality); !ok {
			return fmt.Errorf("pack %q: unknown quality %q", pack.ID, pack.Spec.Quality)
		}
	}
	for _, aid := range pack.Spec.Agents {
		if _, ok := r.GetAgent(aid); !ok {
			return fmt.Errorf("pack %q: unknown agent %q", pack.ID, aid)
		}
	}
	return nil
}

// PublicView is the Studio/API catalog payload.
type PublicView struct {
	Blocks       []CatalogEntry             `json:"blocks"`
	Packs        []map[string]any           `json:"packs"`
	Pipelines    []map[string]any           `json:"pipelines"`
	Agents       []map[string]any           `json:"agents"`
	Quality      []map[string]any           `json:"quality"`
	ActivePack   string                     `json:"active_pack,omitempty"`
	ActivePipeline string                   `json:"active_pipeline,omitempty"`
}

// View builds the discovery API response.
func (r *Registry) View(activePack, activePipeline string) PublicView {
	v := PublicView{
		Blocks:         r.Catalog(""),
		ActivePack:     activePack,
		ActivePipeline: activePipeline,
	}
	for _, id := range sortedKeys(r.Packs) {
		p := r.Packs[id]
		v.Packs = append(v.Packs, map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"version": p.Version, "author": p.Author, "language": p.Language,
			"tags": p.Tags, "icon": p.Icon, "source": p.Source, "path": p.Path,
			"spec": p.Spec, "builtin": p.Source == SourceBuiltin,
			"active": activePack != "" && activePack == p.ID,
			"shareable": p.Shareable != nil && *p.Shareable,
		})
	}
	for _, id := range sortedKeys(r.Pipelines) {
		p := r.Pipelines[id]
		v.Pipelines = append(v.Pipelines, map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description,
			"version": p.Version, "author": p.Author, "language": p.Language,
			"tags": p.Tags, "icon": p.Icon, "source": p.Source, "path": p.Path,
			"builtin": p.Source == SourceBuiltin,
			"active": activePipeline != "" && activePipeline == p.ID,
			"shareable": p.Shareable != nil && *p.Shareable,
		})
	}
	for _, id := range sortedKeys(r.Agents) {
		a := r.Agents[id]
		v.Agents = append(v.Agents, map[string]any{
			"id": a.ID, "name": a.Name, "description": a.Description,
			"version": a.Version, "author": a.Author, "language": a.Language,
			"tags": a.Tags, "icon": a.Icon, "source": a.Source, "path": a.Path,
			"builtin": a.Source == SourceBuiltin,
			"spec": a.Spec, "shareable": a.Shareable != nil && *a.Shareable,
		})
	}
	for _, id := range sortedKeys(r.Quality) {
		q := r.Quality[id]
		v.Quality = append(v.Quality, map[string]any{
			"id": q.ID, "name": q.Name, "description": q.Description,
			"version": q.Version, "author": q.Author, "language": q.Language,
			"tags": q.Tags, "icon": q.Icon, "source": q.Source, "path": q.Path,
			"builtin": q.Source == SourceBuiltin,
			"spec": q.Spec, "shareable": q.Shareable != nil && *q.Shareable,
		})
	}
	return v
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

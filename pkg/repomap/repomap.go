// Package repomap builds a compact, ranked map of a repository's symbols so a
// small local model can see the shape of a codebase without reading it.
//
// It is the pragmatic equivalent of Aider's tree-sitter repo map: per-file
// symbol extraction, a file-level reference graph, a PageRank-style pass to
// rank files by how central they are, and a terse list-shaped rendering under
// a dynamic token budget that shrinks as more real file bodies are already in
// the prompt.
//
// No tree-sitter, no cgo, no new dependencies — per-language scanners live in
// extract.go and cover Go, Python, JavaScript/TypeScript, Rust and Java.
package repomap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// DefaultBudgetTokens is the render budget used when the caller passes <= 0.
// Aider defaults to ~1000 tokens; 800–1500 is the useful band for a 32K SLM.
const DefaultBudgetTokens = 1000

// MaxFilesDefault bounds the walk so a monorepo cannot stall a run.
const MaxFilesDefault = 4000

// MaxFileBytes skips generated blobs (minified JS, vendored bundles).
const MaxFileBytes = 512 * 1024

// Options configures Build.
type Options struct {
	// Exclude are path substrings (slash form) that skip a directory or file.
	Exclude []string
	// IncludeExts restricts extraction to these extensions (".go", ".py"…).
	// Empty means every language the extractors understand.
	IncludeExts []string
	// MaxFiles bounds the number of extracted files (default MaxFilesDefault).
	MaxFiles int
	// CacheDir holds repomap.json. Default <root>/.slmcode.
	CacheDir string
	// DisableCache skips both reading and writing the on-disk cache.
	DisableCache bool
	// TokenCounter overrides the default chars/4 estimate used by Render.
	TokenCounter func(string) int
}

// Map is a ranked repository symbol map.
type Map struct {
	Root  string  `json:"root"`
	Files []File  `json:"files"`
	Built int64   `json:"built"`
	Total float64 `json:"-"`

	byPath  map[string]int
	defs    map[string][]int // symbol name -> file indexes defining it
	countFn func(string) int
}

var defaultExcludes = []string{
	"/.git/", "/node_modules/", "/vendor/", "/dist/", "/build/", "/target/",
	"/.venv/", "/venv/", "/__pycache__/", "/.slmcode/", "/testdata/fixtures/",
	"/.next/", "/coverage/", "/.idea/", "/.vscode/",
}

// Build walks root, extracts symbols, and ranks files.
func Build(root string, opts Options) (*Map, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = MaxFilesDefault
	}
	cache := loadCache(cachePath(abs, opts))

	m := &Map{Root: abs, countFn: opts.TokenCounter}
	if m.countFn == nil {
		m.countFn = estimateTokens
	}

	var files []File
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable subtrees are skipped, not fatal
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return nil
		}
		slash := "/" + filepath.ToSlash(rel) + "/"
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if excluded(slash, opts.Exclude) {
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		relSlash := filepath.ToSlash(rel)
		if excluded("/"+relSlash, opts.Exclude) {
			return nil
		}
		lang := LangForPath(relSlash)
		if lang == "" {
			return nil
		}
		if len(opts.IncludeExts) > 0 && !hasExt(relSlash, opts.IncludeExts) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > MaxFileBytes || info.Size() == 0 {
			return nil
		}
		if cached, ok := cache[relSlash]; ok &&
			cached.Size == info.Size() && cached.ModTime == info.ModTime().UnixNano() {
			files = append(files, cached)
			return nil
		}
		data, rerr2 := os.ReadFile(path) //nolint:gosec // path is walked from the user's own project root (abs); single-user local tool, no symlink-race trust boundary here
		if rerr2 != nil {
			return nil
		}
		f := ExtractSource(relSlash, string(data))
		f.Size = info.Size()
		f.ModTime = info.ModTime().UnixNano()
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	m.Files = files
	m.index()
	m.rank()
	if !opts.DisableCache {
		saveCache(cachePath(abs, opts), files)
	}
	return m, nil
}

func hasExt(rel string, exts []string) bool {
	e := strings.ToLower(filepath.Ext(rel))
	for _, want := range exts {
		if strings.EqualFold(e, want) {
			return true
		}
	}
	return false
}

func excluded(slashPath string, extra []string) bool {
	for _, p := range defaultExcludes {
		if strings.Contains(slashPath, p) {
			return true
		}
	}
	for _, p := range extra {
		p = strings.TrimSpace(p)
		if p != "" && strings.Contains(slashPath, p) {
			return true
		}
	}
	return false
}

func (m *Map) index() {
	m.byPath = make(map[string]int, len(m.Files))
	m.defs = make(map[string][]int)
	for i, f := range m.Files {
		m.byPath[f.Path] = i
		for _, s := range f.Symbols {
			if len(s.Name) < 3 {
				continue
			}
			m.defs[s.Name] = append(m.defs[s.Name], i)
		}
	}
}

// File returns the extracted view of one repo-relative path.
func (m *Map) File(rel string) (File, bool) {
	if m == nil {
		return File{}, false
	}
	i, ok := m.byPath[filepath.ToSlash(rel)]
	if !ok {
		return File{}, false
	}
	return m.Files[i], true
}

// Signatures renders just one file's identifiers (package, imports, symbol
// signatures with line numbers) — the just-in-time-retrieval form of a file:
// enough for the model to decide whether to open it, far cheaper than its body.
func (m *Map) Signatures(rel string) string {
	f, ok := m.File(rel)
	if !ok {
		return ""
	}
	return renderFile(f, 0)
}

// SignaturesForSource renders identifiers for source the caller already holds.
func SignaturesForSource(rel, src string) string {
	return renderFile(ExtractSource(rel, src), 0)
}

// RankFilesFor returns the n highest-ranked file paths for a query, blending
// the static graph rank with query-term hits on symbol names and paths.
func (m *Map) RankFilesFor(query string, n int) []string {
	if m == nil || len(m.Files) == 0 {
		return nil
	}
	if n <= 0 {
		n = 10
	}
	terms := queryTerms(query)
	type scored struct {
		path string
		s    float64
	}
	out := make([]scored, 0, len(m.Files))
	for _, f := range m.Files {
		s := f.Rank
		if len(terms) > 0 {
			hits := 0.0
			lowerPath := strings.ToLower(f.Path)
			for _, t := range terms {
				if strings.Contains(lowerPath, t) {
					hits += 2
				}
				for _, sym := range f.Symbols {
					if strings.EqualFold(sym.Name, t) {
						hits += 3
						break
					}
					if strings.Contains(strings.ToLower(sym.Name), t) {
						hits += 1
						break
					}
				}
			}
			s += hits
		}
		out = append(out, scored{f.Path, s})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].s == out[j].s {
			return out[i].path < out[j].path
		}
		return out[i].s > out[j].s
	})
	if len(out) > n {
		out = out[:n]
	}
	paths := make([]string, 0, len(out))
	for _, s := range out {
		if s.s <= 0 {
			continue
		}
		paths = append(paths, s.path)
	}
	return paths
}

func queryTerms(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range identifierR.FindAllString(query, -1) {
		t := strings.ToLower(tok)
		if len(t) < 3 || isCommonWord(tok) || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// Render emits the map under a token budget. Files listed in alreadyInContext
// are omitted (their bodies are already in the prompt) and each one shrinks the
// effective budget, so the map yields space to real code as context fills up.
func (m *Map) Render(budgetTokens int, alreadyInContext []string) string {
	if m == nil || len(m.Files) == 0 {
		return ""
	}
	if budgetTokens <= 0 {
		budgetTokens = DefaultBudgetTokens
	}
	skip := map[string]bool{}
	for _, p := range alreadyInContext {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p != "" {
			skip[p] = true
		}
	}
	// Dynamic shrink: every file already in context removes 8% of the budget,
	// floored at 25% so the map never disappears entirely.
	if n := len(skip); n > 0 {
		shrink := 100 - 8*n
		if shrink < 25 {
			shrink = 25
		}
		budgetTokens = budgetTokens * shrink / 100
	}
	if budgetTokens < 40 {
		return ""
	}

	ordered := make([]File, 0, len(m.Files))
	ordered = append(ordered, m.Files...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Rank == ordered[j].Rank {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Rank > ordered[j].Rank
	})

	var b strings.Builder
	b.WriteString("## Repo map\n\n")
	b.WriteString("Ranked file → symbol index. Read a file with ws_read before editing it.\n\n")
	used := m.countFn(b.String())
	shown := 0
	for _, f := range ordered {
		if skip[f.Path] || len(f.Symbols) == 0 {
			continue
		}
		// Per-file allowance tightens as the list grows, so the map is broad
		// rather than deep — breadth is what the model cannot get from ws_read.
		perFile := 12
		if shown < 8 {
			perFile = 20
		}
		section := renderFile(f, perFile)
		cost := m.countFn(section)
		if used+cost > budgetTokens {
			if shown == 0 {
				// Always show something: clip the top file.
				section = textutil.Truncate(section, budgetTokens*4, "\n  …\n")
				b.WriteString(section)
				shown++
			}
			break
		}
		b.WriteString(section)
		used += cost
		shown++
	}
	if shown == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderFile(f File, maxSymbols int) string {
	var b strings.Builder
	b.WriteString(f.Path)
	if f.Package != "" {
		b.WriteString("  (" + f.Package + ")")
	}
	b.WriteString("\n")
	syms := f.Symbols
	// Exported first, then by line, so the public surface always survives the cap.
	ranked := make([]Symbol, len(syms))
	copy(ranked, syms)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Exported != ranked[j].Exported {
			return ranked[i].Exported
		}
		return ranked[i].Line < ranked[j].Line
	})
	if maxSymbols > 0 && len(ranked) > maxSymbols {
		ranked = ranked[:maxSymbols]
	}
	// Restore source order for readability.
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Line < ranked[j].Line })
	for _, s := range ranked {
		sig := s.Signature
		if sig == "" {
			sig = s.Kind + " " + s.Name
		}
		sig = textutil.Truncate(strings.TrimSpace(sig), 160, "…")
		b.WriteString("  ")
		b.WriteString(sig)
		b.WriteString("  :")
		b.WriteString(itoa(s.Line))
		b.WriteString("\n")
	}
	if maxSymbols > 0 && len(f.Symbols) > maxSymbols {
		b.WriteString("  … +")
		b.WriteString(itoa(len(f.Symbols) - maxSymbols))
		b.WriteString(" more\n")
	}
	b.WriteString("\n")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

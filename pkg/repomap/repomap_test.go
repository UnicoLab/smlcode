package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symbolNames(f File) []string {
	out := make([]string, 0, len(f.Symbols))
	for _, s := range f.Symbols {
		out = append(out, s.Name)
	}
	return out
}

func hasSymbol(f File, name, kind string) bool {
	for _, s := range f.Symbols {
		if s.Name == name && (kind == "" || s.Kind == kind) {
			return true
		}
	}
	return false
}

func readFixture(t *testing.T, rel string) (string, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return rel, string(data)
}

func TestExtractSourceByLanguage(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		lang       string
		pkg        string
		want       []struct{ name, kind string }
		imports    []string
		minSyms    int
		exported   string
		unexported string
	}{
		{
			name:    "go",
			fixture: "goapp/core/engine.go",
			lang:    "go",
			pkg:     "core",
			want: []struct{ name, kind string }{
				{"Engine", KindType},
				{"Runner", KindInterface},
				{"NewEngine", KindFunc},
				{"Run", KindMethod},
				{"MaxWorkers", KindConst},
				{"defaultName", KindVar},
			},
			imports:    []string{"fmt", "strings"},
			minSyms:    6,
			exported:   "NewEngine",
			unexported: "helper",
		},
		{
			name:    "python",
			fixture: "py/service.py",
			lang:    "python",
			want: []struct{ name, kind string }{
				{"Config", KindClass},
				{"Service", KindClass},
				{"build_service", KindFunc},
				{"shutdown", KindFunc},
				{"start", KindMethod},
			},
			imports:    []string{"os"},
			minSyms:    5,
			exported:   "build_service",
			unexported: "_private",
		},
		{
			name:    "typescript",
			fixture: "js/widget.ts",
			lang:    "typescript",
			want: []struct{ name, kind string }{
				{"WidgetProps", KindInterface},
				{"WidgetState", KindType},
				{"Widget", KindClass},
				{"renderWidget", KindFunc},
				{"useWidget", KindFunc},
			},
			imports:    []string{"./helper"},
			minSyms:    5,
			exported:   "renderWidget",
			unexported: "internalOnly",
		},
		{
			name:    "rust",
			fixture: "rs/lib.rs",
			lang:    "rust",
			want: []struct{ name, kind string }{
				{"Token", KindType},
				{"Lexer", KindInterface},
				{"Mode", KindType},
				{"tokenize", KindFunc},
			},
			minSyms:    4,
			exported:   "tokenize",
			unexported: "private_helper",
		},
		{
			name:    "java",
			fixture: "java/Service.java",
			lang:    "java",
			pkg:     "com.example.app",
			want: []struct{ name, kind string }{
				{"Service", KindClass},
				{"getName", KindMethod},
			},
			imports:  []string{"java.util.List"},
			minSyms:  2,
			exported: "getName",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel, src := readFixture(t, tc.fixture)
			f := ExtractSource(rel, src)
			if f.Lang != tc.lang {
				t.Fatalf("lang=%q want %q", f.Lang, tc.lang)
			}
			if tc.pkg != "" && f.Package != tc.pkg {
				t.Fatalf("pkg=%q want %q", f.Package, tc.pkg)
			}
			if len(f.Symbols) < tc.minSyms {
				t.Fatalf("only %d symbols: %v", len(f.Symbols), symbolNames(f))
			}
			for _, w := range tc.want {
				if !hasSymbol(f, w.name, w.kind) {
					t.Errorf("missing %s %s in %v", w.kind, w.name, symbolNames(f))
				}
			}
			for _, imp := range tc.imports {
				found := false
				for _, got := range f.Imports {
					if got == imp {
						found = true
					}
				}
				if !found {
					t.Errorf("missing import %q in %v", imp, f.Imports)
				}
			}
			for _, s := range f.Symbols {
				if s.Line <= 0 {
					t.Errorf("symbol %s has no line", s.Name)
				}
				if s.Signature == "" {
					t.Errorf("symbol %s has no signature", s.Name)
				}
			}
			if tc.exported != "" {
				for _, s := range f.Symbols {
					if s.Name == tc.exported && !s.Exported {
						t.Errorf("%s should be exported", s.Name)
					}
				}
			}
			if tc.unexported != "" {
				for _, s := range f.Symbols {
					if s.Name == tc.unexported && s.Exported {
						t.Errorf("%s should be unexported", s.Name)
					}
				}
			}
		})
	}
}

func TestExtractGoMethodReceiver(t *testing.T) {
	rel, src := readFixture(t, "goapp/core/engine.go")
	f := ExtractSource(rel, src)
	for _, s := range f.Symbols {
		if s.Name == "Run" {
			if s.Kind != KindMethod || s.Receiver != "Engine" {
				t.Fatalf("Run: kind=%s recv=%q", s.Kind, s.Receiver)
			}
			return
		}
	}
	t.Fatal("Run not found")
}

func TestExtractUnknownLanguage(t *testing.T) {
	f := ExtractSource("README.md", "# hello\n")
	if f.Lang != "" || len(f.Symbols) != 0 {
		t.Fatalf("%+v", f)
	}
}

func buildFixtureMap(t *testing.T) *Map {
	t.Helper()
	m, err := Build("testdata", Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) == 0 {
		t.Fatal("no files extracted")
	}
	return m
}

func TestBuildAndRank(t *testing.T) {
	m := buildFixtureMap(t)
	if _, ok := m.File("goapp/core/engine.go"); !ok {
		t.Fatalf("engine.go missing from %d files", len(m.Files))
	}
	// core/engine.go is referenced by api/handler.go, so it must outrank it.
	core, _ := m.File("goapp/core/engine.go")
	api, _ := m.File("goapp/api/handler.go")
	if core.Rank <= api.Rank {
		t.Fatalf("expected core (%.4f) to outrank api (%.4f)", core.Rank, api.Rank)
	}
	// helper.ts is imported by widget.ts.
	helper, ok1 := m.File("js/helper.ts")
	widget, ok2 := m.File("js/widget.ts")
	if ok1 && ok2 && helper.Rank <= 0 {
		t.Fatalf("helper rank %.4f widget rank %.4f", helper.Rank, widget.Rank)
	}
}

func TestRankFilesFor(t *testing.T) {
	m := buildFixtureMap(t)
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"go symbol", "fix NewEngine so it trims the name", "goapp/core/engine.go"},
		{"python symbol", "build_service should validate the path", "py/service.py"},
		{"ts symbol", "renderWidget must escape the title", "js/widget.ts"},
		{"rust symbol", "tokenize should skip punctuation", "rs/lib.rs"},
		{"path hint", "update the java Service class", "java/Service.java"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := m.RankFilesFor(tc.query, 3)
			if len(got) == 0 {
				t.Fatalf("no ranked files for %q", tc.query)
			}
			if got[0] != tc.want {
				t.Fatalf("top=%q want %q (all=%v)", got[0], tc.want, got)
			}
		})
	}
}

func TestRenderBudgetAndShrink(t *testing.T) {
	m := buildFixtureMap(t)
	tests := []struct {
		name      string
		budget    int
		inContext []string
	}{
		{"tiny", 60, nil},
		{"default", DefaultBudgetTokens, nil},
		{"large", 4000, nil},
	}
	var prev int
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := m.Render(tc.budget, tc.inContext)
			tokens := estimateTokens(out)
			if tokens > tc.budget+40 {
				t.Fatalf("render %d tokens over budget %d", tokens, tc.budget)
			}
			if tc.budget >= DefaultBudgetTokens && !strings.Contains(out, "## Repo map") {
				t.Fatalf("missing header: %q", out)
			}
			if tokens < prev {
				t.Logf("note: %s rendered fewer tokens than previous case", tc.name)
			}
			prev = tokens
		})
	}

	full := m.Render(4000, nil)
	shrunk := m.Render(4000, []string{"goapp/core/engine.go", "py/service.py", "js/widget.ts"})
	if strings.Contains(shrunk, "goapp/core/engine.go") {
		t.Fatal("already-in-context file must be omitted")
	}
	if estimateTokens(shrunk) >= estimateTokens(full) {
		t.Fatalf("budget did not shrink: full=%d shrunk=%d", estimateTokens(full), estimateTokens(shrunk))
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	m := buildFixtureMap(t)
	first := m.Render(1200, nil)
	for i := 0; i < 5; i++ {
		if got := m.Render(1200, nil); got != first {
			t.Fatalf("render %d differed", i)
		}
	}
}

func TestSignatures(t *testing.T) {
	m := buildFixtureMap(t)
	sig := m.Signatures("goapp/core/engine.go")
	if !strings.Contains(sig, "func NewEngine(name string) *Engine") {
		t.Fatalf("missing signature:\n%s", sig)
	}
	if strings.Contains(sig, "return &Engine{") {
		t.Fatalf("signatures must not include bodies:\n%s", sig)
	}
	if m.Signatures("nope.go") != "" {
		t.Fatal("unknown path should render empty")
	}
	if !strings.Contains(SignaturesForSource("x.go", "package x\n\nfunc A() {}\n"), "func A()") {
		t.Fatal("SignaturesForSource failed")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a.go")
	if err := os.WriteFile(src, []byte("package a\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	opts := Options{CacheDir: cacheDir}
	m1, err := Build(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, CacheFile)); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	m2, err := Build(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1.Files) != len(m2.Files) || m2.Files[0].Symbols[0].Name != "Alpha" {
		t.Fatalf("cache reload mismatch: %+v", m2.Files)
	}
	// mtime/size change must invalidate.
	if err := os.WriteFile(src, []byte("package a\n\nfunc Beta() {}\nfunc Gamma() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m3, err := Build(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	names := symbolNames(m3.Files[0])
	if len(names) != 2 || names[0] != "Beta" {
		t.Fatalf("stale cache used: %v", names)
	}
}

func TestBuildExcludesVendorAndDotDirs(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"vendor/x/y.go", "node_modules/z/a.js", ".git/c.go", "keep/k.go"} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package k\n\nfunc K() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Build(root, Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 1 || m.Files[0].Path != "keep/k.go" {
		t.Fatalf("files=%v", func() []string {
			var o []string
			for _, f := range m.Files {
				o = append(o, f.Path)
			}
			return o
		}())
	}
}

func TestBuildEmptyRoot(t *testing.T) {
	m, err := Build(t.TempDir(), Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Render(1000, nil) != "" {
		t.Fatal("empty repo should render empty")
	}
	if m.RankFilesFor("anything", 5) != nil {
		t.Fatal("empty repo should rank nothing")
	}
}

func TestNilMapSafety(t *testing.T) {
	var m *Map
	if m.Render(1000, nil) != "" || m.Signatures("a.go") != "" || m.RankFilesFor("x", 3) != nil {
		t.Fatal("nil map must be inert")
	}
	if _, ok := m.File("a.go"); ok {
		t.Fatal("nil map File")
	}
}

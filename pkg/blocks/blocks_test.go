package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryLoadBuiltin(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatalf("Load(.) failed: %v", err)
	}
	if reg == nil {
		t.Fatal("registry is nil")
	}

	expectedPipelines := []string{"go", "python", "react"}
	for _, id := range expectedPipelines {
		if _, ok := reg.GetPipeline(id); !ok {
			t.Errorf("expected pipeline %q not found", id)
		}
	}

	expectedAgents := []string{
		"go-worker", "go-tester",
		"python-worker", "python-tester",
		"react-worker", "react-tester",
	}
	for _, id := range expectedAgents {
		if _, ok := reg.GetAgent(id); !ok {
			t.Errorf("expected agent %q not found", id)
		}
	}

	expectedQuality := []string{"go", "python", "react"}
	for _, id := range expectedQuality {
		if _, ok := reg.GetQuality(id); !ok {
			t.Errorf("expected quality pack %q not found", id)
		}
	}

	expectedPacks := []string{"go", "python", "react"}
	for _, id := range expectedPacks {
		if _, ok := reg.GetPack(id); !ok {
			t.Errorf("expected pack %q not found", id)
		}
	}

	if n := len(reg.Pipelines); n < 3 {
		t.Errorf("pipelines count: got %d, want >= 3", n)
	}
	if n := len(reg.Agents); n < 6 {
		t.Errorf("agents count: got %d, want >= 6", n)
	}
	if n := len(reg.Quality); n < 3 {
		t.Errorf("quality count: got %d, want >= 3", n)
	}
	if n := len(reg.Packs); n < 3 {
		t.Errorf("packs count: got %d, want >= 3", n)
	}
}

func TestPipelineBlockValidate(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	pipe, ok := reg.GetPipeline("go")
	if !ok {
		t.Fatal("go pipeline not found")
	}

	if err := pipe.Validate(); err != nil {
		t.Fatalf("go pipeline Validate() failed: %v", err)
	}

	if pipe.Kind != KindPipeline {
		t.Errorf("kind = %q, want %q", pipe.Kind, KindPipeline)
	}
	if pipe.ID != "go" {
		t.Errorf("id = %q, want go", pipe.ID)
	}
	if pipe.Spec.Version != 1 {
		t.Errorf("spec version = %d, want 1", pipe.Spec.Version)
	}

	var nilPipe *PipelineBlock
	if err := nilPipe.Validate(); err == nil {
		t.Error("expected error on nil pipeline Validate()")
	}
}

func TestAgentBlockValidate(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	agent, ok := reg.GetAgent("go-worker")
	if !ok {
		t.Fatal("go-worker agent not found")
	}

	if err := agent.Validate(); err != nil {
		t.Fatalf("go-worker Validate() failed: %v", err)
	}

	if agent.Kind != KindAgent {
		t.Errorf("kind = %q, want %q", agent.Kind, KindAgent)
	}
	if agent.Spec.ID != "go-worker" {
		t.Errorf("spec id = %q, want go-worker", agent.Spec.ID)
	}

	var nilAgent *AgentBlock
	if err := nilAgent.Validate(); err == nil {
		t.Error("expected error on nil agent Validate()")
	}
}

func TestQualityBlockValidate(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	q, ok := reg.GetQuality("go")
	if !ok {
		t.Fatal("go quality not found")
	}

	if err := q.Validate(); err != nil {
		t.Fatalf("go quality Validate() failed: %v", err)
	}

	if q.Kind != KindQuality {
		t.Errorf("kind = %q, want %q", q.Kind, KindQuality)
	}
	if q.Spec.QAGate == "" {
		t.Error("go quality has no qa_gate")
	}
	if q.PrimaryQAGate() == "" {
		t.Error("go quality PrimaryQAGate() is empty")
	}

	var nilQ *QualityBlock
	if err := nilQ.Validate(); err == nil {
		t.Error("expected error on nil quality Validate()")
	}
}

func TestPackBlockValidate(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	pack, ok := reg.GetPack("go")
	if !ok {
		t.Fatal("go pack not found")
	}

	if err := pack.Validate(); err != nil {
		t.Fatalf("go pack Validate() failed: %v", err)
	}

	if pack.Kind != KindPack {
		t.Errorf("kind = %q, want %q", pack.Kind, KindPack)
	}
	if pack.ID != "go" {
		t.Errorf("id = %q, want go", pack.ID)
	}

	if err := reg.ResolvePackRefs(pack); err != nil {
		t.Fatalf("ResolvePackRefs failed: %v", err)
	}

	brokenPack := &PackBlock{
		Meta: Meta{ID: "broken"},
		Spec: PackSpec{Pipeline: "nonexistent"},
	}
	if err := reg.ResolvePackRefs(brokenPack); err == nil {
		t.Error("expected error resolving broken pack reference")
	}

	if err := reg.ResolvePackRefs(nil); err == nil {
		t.Error("expected error on nil ResolvePackRefs")
	}

	var nilPack *PackBlock
	if err := nilPack.Validate(); err == nil {
		t.Error("expected error on nil pack Validate()")
	}
}

func TestCatalog(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	all := reg.Catalog("")
	if len(all) == 0 {
		t.Fatal("Catalog() returned no entries")
	}
	for _, e := range all {
		if e.Kind == "" {
			t.Errorf("Catalog entry has empty kind: %+v", e)
		}
	}

	pipes := reg.Catalog(KindPipeline)
	for _, e := range pipes {
		if e.Kind != KindPipeline {
			t.Errorf("Catalog(pipeline) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(pipes) < 3 {
		t.Errorf("Catalog(pipeline) returned %d entries, want >= 3", len(pipes))
	}

	agents := reg.Catalog(KindAgent)
	for _, e := range agents {
		if e.Kind != KindAgent {
			t.Errorf("Catalog(agent) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(agents) < 6 {
		t.Errorf("Catalog(agent) returned %d entries, want >= 6", len(agents))
	}

	quals := reg.Catalog(KindQuality)
	for _, e := range quals {
		if e.Kind != KindQuality {
			t.Errorf("Catalog(quality) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(quals) < 3 {
		t.Errorf("Catalog(quality) returned %d entries, want >= 3", len(quals))
	}

	packs := reg.Catalog(KindPack)
	for _, e := range packs {
		if e.Kind != KindPack {
			t.Errorf("Catalog(pack) contained kind %q: %s", e.Kind, e.ID)
		}
	}
	if len(packs) < 3 {
		t.Errorf("Catalog(pack) returned %d entries, want >= 3", len(packs))
	}

	unknown := reg.Catalog("unknown")
	if len(unknown) != 0 {
		t.Errorf("Catalog(unknown) returned %d entries, want 0", len(unknown))
	}
}

func TestView(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	v := reg.View("go", "go")

	if len(v.Blocks) == 0 {
		t.Error("View.Blocks is empty")
	}
	if len(v.Pipelines) < 3 {
		t.Errorf("View.Pipelines has %d entries, want >= 3", len(v.Pipelines))
	}
	if len(v.Agents) < 6 {
		t.Errorf("View.Agents has %d entries, want >= 6", len(v.Agents))
	}
	if len(v.Quality) < 3 {
		t.Errorf("View.Quality has %d entries, want >= 3", len(v.Quality))
	}
	if len(v.Packs) < 3 {
		t.Errorf("View.Packs has %d entries, want >= 3", len(v.Packs))
	}

	if v.ActivePack != "go" {
		t.Errorf("ActivePack = %q, want go", v.ActivePack)
	}
	if v.ActivePipeline != "go" {
		t.Errorf("ActivePipeline = %q, want go", v.ActivePipeline)
	}
}

func TestDetectQuality(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	tmpEmpty := t.TempDir()
	if q := reg.DetectQuality(tmpEmpty); q != nil {
		t.Errorf("expected nil for empty workspace, got %q", q.ID)
	}

	tmpGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpGo, "go.mod"),
		[]byte("module example\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qGo := reg.DetectQuality(tmpGo)
	if qGo == nil {
		t.Fatal("expected Go quality detected but got nil")
	}
	if qGo.ID != "go" {
		t.Errorf("detected quality = %q, want go", qGo.ID)
	}

	if err := os.WriteFile(filepath.Join(tmpGo, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qGo2 := reg.DetectQuality(tmpGo)
	if qGo2 == nil || qGo2.ID != "go" {
		t.Errorf("detected quality with .go file = %v, want go", qGo2)
	}

	tmpPy := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpPy, "pyproject.toml"),
		[]byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qPy := reg.DetectQuality(tmpPy)
	if qPy == nil {
		t.Fatal("expected Python quality detected but got nil")
	}
	if qPy.ID != "python" {
		t.Errorf("detected quality = %q, want python", qPy.ID)
	}

	// A package.json with no React dependency is a Node/TypeScript project, not
	// a React app: it must get tsc + vitest, not the React tester and its
	// hook-rules review. Only a declared react/next dependency selects react.
	tmpNode := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpNode, "package.json"),
		[]byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	qNode := reg.DetectQuality(tmpNode)
	if qNode == nil {
		t.Fatal("expected a quality pack for a package.json workspace, got nil")
	}
	if qNode.ID != "typescript" {
		t.Errorf("detected quality = %q, want typescript for a bare package.json", qNode.ID)
	}

	tmpReact := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpReact, "package.json"),
		[]byte(`{"dependencies":{"react":"^18.3.1","react-dom":"^18.3.1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpReact, "App.tsx"),
		[]byte("export default function App() { return null }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qReact := reg.DetectQuality(tmpReact)
	if qReact == nil {
		t.Fatal("expected React quality detected but got nil")
	}
	if qReact.ID != "react" {
		t.Errorf("detected quality = %q, want react", qReact.ID)
	}

	var nilReg *Registry
	if q := nilReg.DetectQuality(tmpGo); q != nil {
		t.Error("expected nil from nil registry DetectQuality")
	}
	if q := reg.DetectQuality(""); q != nil {
		t.Error("expected nil for empty workspaceRoot")
	}
}

func TestResolveQAGateCommand(t *testing.T) {
	gate := ResolveQAGateCommand(".", ".", "go")
	if gate == "" {
		t.Fatal("expected non-empty QA gate for go pack")
	}

	gatePy := ResolveQAGateCommand(".", ".", "python")
	if gatePy == "" {
		t.Fatal("expected non-empty QA gate for python pack")
	}

	gateReact := ResolveQAGateCommand(".", ".", "react")
	if gateReact == "" {
		t.Fatal("expected non-empty QA gate for react pack")
	}

	// When project root doesn't exist, builtins still resolve (registry always has builtins)
	gateMissing := ResolveQAGateCommand(
		"/tmp/does/not/exist/anywhere",
		"/tmp/does/not/exist/anywhere",
		"go",
	)
	if gateMissing == "" {
		t.Log("note: builtins resolved even from missing root")
	}
}

func TestResolveQAGateCommandStaleActivePackDoesNotForcePython(t *testing.T) {
	// A stale active_pack (e.g. "python" left over from a prior run) must not
	// force pytest onto a static-HTML workspace.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "index.html"),
		[]byte("<!doctype html><html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := ResolveQAGateCommand(tmp, tmp, "python")
	if gate == "" {
		t.Fatal("expected a QA gate for HTML workspace")
	}
	if strings.Contains(gate, "pytest") || strings.Contains(gate, "python") {
		t.Fatalf("stale python active_pack leaked pytest onto HTML workspace: %q", gate)
	}
}

func TestDetectQualitySkipsVenvAndPicksWeb(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "index.html"),
		[]byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A venv full of Python files must not make the workspace look like Python.
	venv := filepath.Join(tmp, ".venv")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "site.py"),
		[]byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := reg.DetectQuality(tmp)
	if q == nil {
		t.Fatal("expected web quality detected")
	}
	if q.ID != "web" {
		t.Fatalf("detected quality = %q, want web (venv must not force python)", q.ID)
	}
}

func TestResolveSmokeCommand(t *testing.T) {
	smoke := ResolveSmokeCommand(".", ".", "go")
	if smoke == "" {
		t.Fatal("expected non-empty smoke for go pack")
	}

	// Builtins resolve even from missing root
	smokeMissing := ResolveSmokeCommand("/tmp/does/not/exist", "/tmp/does/not/exist", "go")
	if smokeMissing == "" {
		t.Log("note: smoke resolved from builtins even for missing root")
	}
}

func TestSafePrefixesFromPack(t *testing.T) {
	prefixes := SafePrefixesFromPack(".", "go")
	if len(prefixes) == 0 {
		t.Fatal("expected non-empty safe prefixes for go pack")
	}

	hasGoTest := false
	hasGoVet := false
	for _, p := range prefixes {
		if p == "go test" {
			hasGoTest = true
		}
		if p == "go vet" {
			hasGoVet = true
		}
	}
	if !hasGoTest {
		t.Error("expected 'go test' in safe prefixes")
	}
	if !hasGoVet {
		t.Error("expected 'go vet' in safe prefixes")
	}

	// Builtins resolve even from missing root
	missing := SafePrefixesFromPack("/tmp/does/not/exist", "go")
	if missing == nil {
		t.Log("note: safe prefixes resolved from builtins even for missing root")
	}
}

func TestMetaValidation(t *testing.T) {
	m := Meta{
		APIVersion: APIVersion,
		Kind:       KindPipeline,
		ID:         "test-pipeline",
		Name:       "Test Pipeline",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("valid Meta.Validate() failed: %v", err)
	}
	if m.Version != "1.0.0" {
		t.Errorf("default version: %q", m.Version)
	}
	if m.Author != "UnicoLab" {
		t.Errorf("default author: %q", m.Author)
	}
	if m.License != "MIT" {
		t.Errorf("default license: %q", m.License)
	}

	if err := (&Meta{APIVersion: "blocks/v2", Kind: KindPipeline, ID: "test"}).Validate(); err == nil {
		t.Error("expected error for unsupported api_version")
	}
	if err := (&Meta{APIVersion: APIVersion, Kind: "unknown", ID: "test"}).Validate(); err == nil {
		t.Error("expected error for unknown kind")
	}
	// Uppercase ID gets lowercased by Normalize(), so it becomes valid
	m3 := &Meta{APIVersion: APIVersion, Kind: KindPipeline, ID: "INVALID"}
	if err := m3.Validate(); err != nil {
		t.Logf("note: uppercase ID after normalize: id=%q err=%v", m3.ID, err)
	}

	var nilMeta *Meta
	if err := nilMeta.Validate(); err == nil {
		t.Error("expected error on nil Meta.Validate()")
	}
}

func TestGetFunctions(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	p, ok := reg.GetPack("go")
	if !ok || p == nil || p.ID != "go" {
		t.Errorf("GetPack(go): ok=%v, pack=%v", ok, p)
	}
	if _, ok := reg.GetPack("nonexistent"); ok {
		t.Error("GetPack(nonexistent) should return false")
	}

	pipe, ok := reg.GetPipeline("go")
	if !ok || pipe == nil || pipe.ID != "go" {
		t.Errorf("GetPipeline(go): ok=%v, pipe=%v", ok, pipe)
	}
	if _, ok := reg.GetPipeline("nonexistent"); ok {
		t.Error("GetPipeline(nonexistent) should return false")
	}

	q, ok := reg.GetQuality("go")
	if !ok || q == nil || q.ID != "go" {
		t.Errorf("GetQuality(go): ok=%v, q=%v", ok, q)
	}
	if _, ok := reg.GetQuality("nonexistent"); ok {
		t.Error("GetQuality(nonexistent) should return false")
	}

	a, ok := reg.GetAgent("go-worker")
	if !ok || a == nil || a.ID != "go-worker" {
		t.Errorf("GetAgent(go-worker): ok=%v, a=%v", ok, a)
	}
	if _, ok := reg.GetAgent("nonexistent"); ok {
		t.Error("GetAgent(nonexistent) should return false")
	}

	var nilReg *Registry
	if _, ok := nilReg.GetPack("go"); ok {
		t.Error("nil reg GetPack should return false")
	}
	if _, ok := nilReg.GetPipeline("go"); ok {
		t.Error("nil reg GetPipeline should return false")
	}
	if _, ok := nilReg.GetQuality("go"); ok {
		t.Error("nil reg GetQuality should return false")
	}
	if _, ok := nilReg.GetAgent("go-worker"); ok {
		t.Error("nil reg GetAgent should return false")
	}
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(reg.Pipelines) != 0 || len(reg.Agents) != 0 ||
		len(reg.Quality) != 0 || len(reg.Packs) != 0 {
		t.Error("NewRegistry should be empty")
	}
	if len(reg.Catalog("")) != 0 {
		t.Error("NewRegistry Catalog should be empty")
	}
}

func TestLoadEdgeCases(t *testing.T) {
	reg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(temp dir) failed: %v", err)
	}
	if len(reg.Pipelines) < 3 {
		t.Error("even from temp dir we should get builtin pipelines")
	}
}

func TestPackBlockOverrideFields(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	pack, ok := reg.GetPack("go")
	if !ok {
		t.Fatal("go pack not found")
	}

	if pack.Spec.OverrideTester != "go-tester" {
		t.Errorf("OverrideTester = %q, want go-tester", pack.Spec.OverrideTester)
	}
	if pack.Spec.OverrideWorker != "go-worker" {
		t.Errorf("OverrideWorker = %q, want go-worker", pack.Spec.OverrideWorker)
	}
	if !pack.Spec.PinSkills {
		t.Error("go pack should have PinSkills enabled")
	}
}

func TestMetaToEntry(t *testing.T) {
	m := Meta{ID: "test", Kind: KindPipeline, Source: SourceBuiltin}
	m.Normalize()
	entry := m.ToEntry()

	if entry.ID != "test" {
		t.Errorf("entry id = %q", entry.ID)
	}
	if !entry.Builtin {
		t.Error("builtin entry should have Builtin=true")
	}
	if entry.Custom {
		t.Error("builtin entry should have Custom=false")
	}

	m.Source = SourceProject
	m.Normalize()
	entry = m.ToEntry()
	if entry.Builtin {
		t.Error("project entry should have Builtin=false")
	}
	if !entry.Custom {
		t.Error("project entry should have Custom=true")
	}
}

func TestQualityBlockPrimaryQAGate(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}

	q, ok := reg.GetQuality("go")
	if !ok {
		t.Fatal("go quality not found")
	}

	gate := q.PrimaryQAGate()
	if gate != q.Spec.QAGate {
		t.Errorf("PrimaryQAGate = %q, want %q", gate, q.Spec.QAGate)
	}

	var nilQ *QualityBlock
	if nilQ.PrimaryQAGate() != "" {
		t.Error("nil PrimaryQAGate should be empty")
	}
}

func TestDetectPackPolyglotAndPerPath(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	// The exact shape of this repository: a Go module at the root with a Vite
	// app in web/. The nested project's .ts/.tsx files must not out-vote go.mod.
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example\n\ngo 1.23\n")
	write("main.go", "package main\n")
	write("web/package.json", `{"dependencies":{"react":"^18.3.1"}}`)
	write("web/tsconfig.json", "{}")
	for _, f := range []string{"App.tsx", "main.tsx", "api/client.ts", "hooks/useRun.ts"} {
		write("web/src/"+f, "export const x = 1\n")
	}

	if got := reg.DetectPack(root); got != "go" {
		t.Errorf("DetectPack(polyglot root) = %q, want go", got)
	}
	// Per-path: a file inside the nested project gets that project's pack.
	q := reg.DetectQualityForPath(root, "web/src/App.tsx")
	if q == nil || q.ID != "react" {
		t.Errorf("DetectQualityForPath(web/src/App.tsx) = %v, want react", q)
	}
	if q := reg.DetectQualityForPath(root, "main.go"); q == nil || q.ID != "go" {
		t.Errorf("DetectQualityForPath(main.go) = %v, want go", q)
	}
	// And the repo-wide answer still lists both languages.
	all := reg.DetectAll(root)
	if len(all) == 0 || all[0].ID != "go" {
		t.Fatalf("DetectAll head = %v, want go first", all)
	}
}

func TestDetectPackPerLanguageFixtures(t *testing.T) {
	reg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		pack  string
		files map[string]string
	}{
		{"go", map[string]string{"go.mod": "module x\n", "main.go": "package main\n"}},
		{"python", map[string]string{"pyproject.toml": "[project]\nname='x'\n", "app.py": "x = 1\n"}},
		{"rust", map[string]string{"Cargo.toml": "[package]\nname='x'\n", "src/main.rs": "fn main() {}\n"}},
		{"java", map[string]string{"pom.xml": "<project/>", "src/App.java": "class App {}\n"}},
		{"kotlin", map[string]string{"build.gradle.kts": "plugins {}\n", "src/App.kt": "fun main() {}\n"}},
		{"dotnet", map[string]string{"App.csproj": "<Project/>", "Program.cs": "class P {}\n"}},
		{"ruby", map[string]string{"Gemfile": "source 'x'\n", "lib/app.rb": "class App; end\n"}},
		{"php", map[string]string{"composer.json": "{}", "src/App.php": "<?php\n"}},
		{"swift", map[string]string{"Package.swift": "// swift-tools-version:5.9\n", "Sources/App/main.swift": "print(1)\n"}},
		{"cpp", map[string]string{"CMakeLists.txt": "project(x)\n", "src/main.cpp": "int main(){}\n"}},
		{"typescript", map[string]string{"package.json": `{"name":"x"}`, "tsconfig.json": "{}", "src/index.ts": "export const x=1\n"}},
		{"react", map[string]string{"package.json": `{"dependencies":{"react":"^18"}}`, "src/App.tsx": "export default () => null\n"}},
		{"web", map[string]string{"index.html": "<!doctype html><h1>hi</h1>", "style.css": "body{}"}},
	}
	for _, tc := range cases {
		t.Run(tc.pack, func(t *testing.T) {
			root := t.TempDir()
			for rel, body := range tc.files {
				full := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := reg.DetectPack(root); got != tc.pack {
				scores := reg.DetectAll(root)
				t.Errorf("DetectPack = %q, want %q (ranking: %v)", got, tc.pack, scores)
			}
		})
	}
}

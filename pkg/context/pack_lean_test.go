package contextstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/repomap"
)

func newWorkspace(t *testing.T) (root, slm string, store *Store) {
	t.Helper()
	root = t.TempDir()
	slm = filepath.Join(root, ".slmcode")
	if err := os.MkdirAll(slm, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, slm, New(slm)
}

func writeDoc(t *testing.T, slm, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(slm, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
		if i%80 == 79 {
			b[i] = '\n'
		}
	}
	return string(b)
}

// bigGoFile puts the interesting symbol deep in the file, past any head cut.
func bigGoFile(target string, lines int) string {
	var b strings.Builder
	b.WriteString("// Copyright 2020 the authors.\n// Licensed under MIT.\n\npackage big\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "func filler%d() string { return \"noise %d\" }\n\n", i, i)
	}
	fmt.Fprintf(&b, "// %s is the function under change.\nfunc %s(name string) string {\n\treturn strings.TrimSpace(fmt.Sprintf(\"%%s\", name))\n}\n\n", target, target)
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "func tail%d() int { return %d }\n\n", i, i)
	}
	return b.String()
}

func TestBudgetAvailablePerRole(t *testing.T) {
	tests := []struct {
		name        string
		limit       int
		role        string
		wantAtLeast int
		wantAtMost  int
	}{
		{"32b worker", 32768, "worker", 20000, 32768},
		{"32b explorer", 32768, "explorer", 12000, 22000},
		{"14b worker", 16384, "worker", 9000, 16384},
		{"7b worker", 8192, "worker", 3500, 8192},
		{"7b memory", 8192, "memory", 1500, 4000},
		{"unknown limit falls back", 0, "worker", MinPackTokens, 5000},
		{"tiny limit clamps to floor", 100, "worker", MinPackTokens, MinPackTokens},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultBudget(tc.limit).Available(tc.role)
			if got < tc.wantAtLeast || got > tc.wantAtMost {
				t.Fatalf("Available=%d want [%d,%d]", got, tc.wantAtLeast, tc.wantAtMost)
			}
		})
	}
	// A 32K model must get materially more than a 7B one — the whole point.
	if DefaultBudget(32768).Available("worker") <= 4*DefaultBudget(8192).Available("worker") {
		t.Log("32k budget is not 4x the 8k budget (reserves are fixed) — expected")
	}
	if DefaultBudget(32768).Available("worker") <= DefaultBudget(8192).Available("worker") {
		t.Fatal("bigger window must yield a bigger budget")
	}
}

func TestTokenCounters(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"ascii", "package main\n\nfunc main() {}\n"},
		{"unicode", "héllo wörld 👍"},
		{"long", longText(4000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := DefaultTokenCounter(tc.in)
			h := HeuristicTokenCounter(tc.in)
			if tc.in == "" {
				if d != 0 || h != 0 {
					t.Fatalf("empty: %d %d", d, h)
				}
				return
			}
			if d <= 0 || h <= 0 {
				t.Fatalf("non-positive count: %d %d", d, h)
			}
		})
	}
}

func TestPackerBudgetsInTokens(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\n"+longText(60000))
	writeDoc(t, slm, DocContext, "# C\n\n"+longText(60000))
	writeFile(t, root, "big.go", bigGoFile("Target", 200))

	tests := []struct {
		name  string
		limit int
		role  string
	}{
		{"7b worker", 8192, "worker"},
		{"14b worker", 16384, "worker"},
		{"32b worker", 32768, "worker"},
		{"32b explorer", 32768, "explorer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPackerWithBudget(store, root, tc.limit)
			pack, err := p.BuildPack(BuildRequest{
				Role: tc.role, Query: "adjust Target", TaskTitle: "Fix Target",
				Docs: []string{DocQuery, DocContext}, Files: []string{"big.go"},
			})
			if err != nil {
				t.Fatal(err)
			}
			budget := DefaultBudget(tc.limit).Available(tc.role)
			if pack.BudgetTokens != budget {
				t.Fatalf("BudgetTokens=%d want %d", pack.BudgetTokens, budget)
			}
			if pack.TokensUsed > budget {
				t.Fatalf("packed %d tokens over budget %d", pack.TokensUsed, budget)
			}
			if pack.TokensUsed == 0 {
				t.Fatal("packed nothing")
			}
		})
	}

	// A bigger window must actually deliver more context.
	small, _ := NewPackerWithBudget(store, root, 8192).BuildPack(BuildRequest{
		Role: "worker", Query: "adjust Target", Docs: []string{DocQuery, DocContext}, Files: []string{"big.go"},
	})
	big, _ := NewPackerWithBudget(store, root, 32768).BuildPack(BuildRequest{
		Role: "worker", Query: "adjust Target", Docs: []string{DocQuery, DocContext}, Files: []string{"big.go"},
	})
	if big.TokensUsed <= small.TokensUsed {
		t.Fatalf("32k pack (%d) not larger than 8k pack (%d)", big.TokensUsed, small.TokensUsed)
	}
}

func TestPackerLegacyConstructorStillWorks(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\n"+longText(4000))
	writeDoc(t, slm, DocContext, "# C\n\n"+longText(4000))
	writeFile(t, root, "big.go", bigGoFile("Target", 100))

	tests := []struct {
		name string
		kb   int
	}{
		{"zero kb", 0}, {"1 kb", 1}, {"16 kb", 16}, {"32 kb", 32}, {"128 kb", 128},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPacker(store, root, tc.kb)
			pack, err := p.Build("worker", "adjust Target", LeanDocsForRole("worker"), []string{"big.go"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if !pack.LeanFiles {
				t.Fatal("worker should be a lean role")
			}
			if pack.TokensUsed > pack.BudgetTokens {
				t.Fatalf("over budget: %d > %d", pack.TokensUsed, pack.BudgetTokens)
			}
			if pack.BudgetUsed <= 0 {
				t.Fatal("no bytes packed")
			}
		})
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	root, slm, store := newWorkspace(t)
	for _, d := range []string{DocProject, DocContext, DocQuery, DocPlan, DocMemory} {
		writeDoc(t, slm, d, "# "+d+"\n\nbody of "+d+"\n")
	}
	for i := 0; i < 6; i++ {
		writeFile(t, root, fmt.Sprintf("pkg/m%d/f.go", i),
			fmt.Sprintf("package m%d\n\nfunc Target%d() {}\n", i, i))
	}
	files := []string{"pkg/m3/f.go", "pkg/m0/f.go", "pkg/m5/f.go", "pkg/m1/f.go", "pkg/m4/f.go", "pkg/m2/f.go"}
	docs := []string{DocQuery, DocContext, DocProject, DocPlan, DocMemory}

	p := NewPackerWithBudget(store, root, 32768)
	first := ""
	for i := 0; i < 25; i++ {
		p.ClearCache() // force a real rebuild each time
		pack, err := p.BuildPack(BuildRequest{
			Role: "worker", Query: "touch Target", Docs: docs, Files: files,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := pack.Render()
		if first == "" {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("render diverged on iteration %d\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
	// Files must be sorted by path.
	idx := -1
	for i := 0; i < 6; i++ {
		at := strings.Index(first, fmt.Sprintf("## File: pkg/m%d/f.go", i))
		if at < 0 {
			continue
		}
		if at < idx {
			t.Fatalf("files not sorted by path:\n%s", first)
		}
		idx = at
	}
}

func TestRenderOrderIsMostStableFirst(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocProject, "# Project\n\nstable project facts\n")
	writeDoc(t, slm, DocContext, "# Context\n\ncontext body\n")
	writeFile(t, root, "a.go", "package a\n\nfunc Alpha() {}\n")

	skills := "## Run collaboration contract\n\n- Touch only a.go\n\n## Skill: guidance\n\nBe precise.\n"
	p := NewPackerWithBudget(store, root, 32768)
	pack, err := p.BuildPack(BuildRequest{
		Role: "worker", Query: "the volatile user query", TaskID: "T1", TaskTitle: "Do it",
		Docs: []string{DocProject, DocContext}, Files: []string{"a.go"}, SkillsMarkdown: skills,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := pack.Render()

	if !strings.HasPrefix(out, "# Scoped context for role=worker") {
		t.Fatalf("header must stay first (pkg/loop and pkg/plan sniff it):\n%s", out)
	}
	positions := []struct {
		name   string
		needle string
	}{
		{"skills", "## Skill: guidance"},
		{"project doc", "## Doc: PROJECT.md"},
		{"context doc", "## Doc: CONTEXT.md"},
		{"file", "## File: a.go"},
		{"run contract", "## Run collaboration contract"},
		{"task", "Task: T1"},
		{"query", "## User query"},
	}
	prev := -1
	for _, p := range positions {
		at := strings.Index(out, p.needle)
		if at < 0 {
			t.Fatalf("missing %s (%q) in:\n%s", p.name, p.needle, out)
		}
		if at < prev {
			t.Fatalf("%s rendered out of order (most-stable-first violated):\n%s", p.name, out)
		}
		prev = at
	}
	if strings.Contains(out, "context budget used") {
		t.Fatalf("volatile budget footer must be gone:\n%s", out)
	}
}

func TestPriorityIsExtractedAndSeparated(t *testing.T) {
	tests := []struct {
		name         string
		markdown     string
		wantPriority string
		wantRest     string
	}{
		{
			name:         "at index zero",
			markdown:     "## Run collaboration contract\n\n- do X\n\n## Skill: a\n\nbody",
			wantPriority: "## Run collaboration contract\n\n- do X",
			wantRest:     "## Skill: a\n\nbody",
		},
		{
			name:         "after a leading heading",
			markdown:     "## Active skills\n\nintro line\n\n## Run collaboration contract\n\n- do X\n\n## Skill: a\n\nbody",
			wantPriority: "## Run collaboration contract\n\n- do X",
			wantRest:     "## Active skills\n\nintro line\n\n## Skill: a\n\nbody",
		},
		{
			name:         "after a leading newline",
			markdown:     "\n\n## Run collaboration contract\n\n- do X",
			wantPriority: "## Run collaboration contract\n\n- do X",
			wantRest:     "",
		},
		{
			name:         "no marker",
			markdown:     "## Skill: a\n\nbody",
			wantPriority: "",
			wantRest:     "## Skill: a\n\nbody",
		},
		{"empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotP, gotR := splitPriorityMarkdown(tc.markdown)
			if gotP != tc.wantPriority {
				t.Errorf("priority=%q want %q", gotP, tc.wantPriority)
			}
			if gotR != tc.wantRest {
				t.Errorf("rest=%q want %q", gotR, tc.wantRest)
			}
		})
	}
}

func TestPackerKeepsPriorityOutOfSkills(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocContext, "# Context\n\ncontext body\n")
	writeFile(t, root, "a.go", "package a\n")

	skills := "## Run collaboration contract\n\n- Touch only a.go\n- Verify with go test ./...\n\n" +
		"## Skill: noisy\n\n" + longText(3000)
	p := NewPackerWithBudget(store, root, 8192)
	pack, err := p.BuildPack(BuildRequest{
		Role: "worker", Query: "q", Docs: []string{DocContext},
		Files: []string{"a.go"}, SkillsMarkdown: skills,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Priority, "Touch only a.go") {
		t.Fatalf("priority handoff missing: %q", pack.Priority)
	}
	if strings.Contains(pack.Priority, "Skill: noisy") {
		t.Fatalf("ordinary skills leaked into priority: %q", pack.Priority)
	}
	if !strings.Contains(pack.Render(), "## Run collaboration contract") {
		t.Fatalf("priority not rendered:\n%s", pack.Render())
	}
}

func TestPackerSkillsRespectBudget(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocContext, "# Context\n\n"+longText(2000))
	writeFile(t, root, "big.go", bigGoFile("Target", 300))

	tests := []struct {
		name  string
		limit int
	}{
		{"tiny window", 600},
		{"7b", 8192},
		{"32b", 32768},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPackerWithBudget(store, root, tc.limit)
			pack, err := p.BuildPack(BuildRequest{
				Role: "worker", Query: "q", Docs: []string{DocContext},
				Files: []string{"big.go"}, SkillsMarkdown: "## Skill: must-fit\n\n" + longText(2000),
			})
			if err != nil {
				t.Fatal(err)
			}
			if pack.TokensUsed > pack.BudgetTokens {
				t.Fatalf("over budget %d > %d", pack.TokensUsed, pack.BudgetTokens)
			}
			if pack.Skills != "" && DefaultTokenCounter(pack.Skills) < MinSkillTokens {
				t.Fatalf("skill fragment below the noise floor: %d tokens", DefaultTokenCounter(pack.Skills))
			}
		})
	}
}

func TestNonLeanRoleAlwaysGetsFileFloor(t *testing.T) {
	root, slm, store := newWorkspace(t)
	// A PROJECT.md bloated by unbounded auto-learned appends.
	writeDoc(t, slm, DocProject, "# Project\n\n"+longText(120000))
	writeDoc(t, slm, DocQuery, "# Q\n\nchange Target\n")
	writeFile(t, root, "focus.go", bigGoFile("Target", 60))

	for _, role := range []string{"explorer", "docs", "placeholder", "planner"} {
		t.Run(role, func(t *testing.T) {
			p := NewPackerWithBudget(store, root, 16384,
				WithIdentifierOnlyRoles()) // force bodies so the floor is observable
			pack, err := p.BuildPack(BuildRequest{
				Role: role, Query: "change Target", Docs: []string{DocProject, DocQuery},
				Files: []string{"focus.go"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(pack.Files) == 0 {
				t.Fatalf("bloated doc starved out every file (role=%s, docs=%d bytes)",
					role, len(pack.Docs[DocProject]))
			}
			docTokens := DefaultTokenCounter(pack.Docs[DocProject])
			if docTokens > pack.BudgetTokens*DocSharePercent/100+8 {
				t.Fatalf("single doc took %d tokens of %d budget", docTokens, pack.BudgetTokens)
			}
		})
	}
}

func TestPackerCacheRefreshesWhenFocusFileChanges(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n")
	writeFile(t, root, "a.go", "package a\n\nconst Version = 1\n")

	p := NewPacker(store, root, 16)
	pack, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Files["a.go"], "Version = 1") {
		t.Fatalf("initial pack missing v1: %+v", pack.Files)
	}
	writeFile(t, root, "a.go", "package a\n\nconst Version = 2\n")
	next, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Files["a.go"], "Version = 2") {
		t.Fatalf("pack reused stale file content: %q", next.Files["a.go"])
	}
}

func TestPackerCacheRefreshesWhenContextDocChanges(t *testing.T) {
	root, _, store := newWorkspace(t)
	if err := store.Write(DocContext, "# Context\n\nold finding\n"); err != nil {
		t.Fatal(err)
	}
	p := NewPacker(store, root, 16)
	pack, err := p.Build("worker", "q", []string{DocContext}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Docs[DocContext], "old finding") {
		t.Fatalf("initial pack missing old doc: %+v", pack.Docs)
	}
	if err := store.Write(DocContext, "# Context\n\nnew wave lesson\n"); err != nil {
		t.Fatal(err)
	}
	next, err := p.Build("worker", "q", []string{DocContext}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Docs[DocContext], "new wave lesson") {
		t.Fatalf("pack reused stale doc content: %q", next.Docs[DocContext])
	}
}

func TestPackerCacheIsBoundedAndDigestKeyed(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n")
	p := NewPackerWithBudget(store, root, 16384)

	fatSkills := "## Skill: fat\n\n" + longText(4000)
	for i := 0; i < MaxCacheEntries+50; i++ {
		if _, err := p.Build("worker", fmt.Sprintf("query %d", i), []string{DocQuery}, nil, fatSkills); err != nil {
			t.Fatal(err)
		}
	}
	p.cacheMu.Lock()
	n := len(p.cache)
	var keyLen int
	for k := range p.cache {
		keyLen = len(k)
		break
	}
	p.cacheMu.Unlock()
	if n > MaxCacheEntries {
		t.Fatalf("cache unbounded: %d entries", n)
	}
	if keyLen != 64 {
		t.Fatalf("cache key should be a sha256 hex digest, got %d chars", keyLen)
	}
	p.ClearCache()
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if len(p.cache) != 0 {
		t.Fatal("cache not cleared")
	}
}

func TestPackerCacheHitsReturnEqualPacks(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\nbody\n")
	writeFile(t, root, "a.go", "package a\n\nfunc Alpha() {}\n")
	p := NewPackerWithBudget(store, root, 16384)
	a, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Render() != b.Render() {
		t.Fatal("cache hit produced a different render")
	}
	// Mutating the returned pack must not poison the cache.
	a.Files["a.go"] = "poisoned"
	c, _ := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if c.Files["a.go"] == "poisoned" {
		t.Fatal("cache returned a shared map")
	}
}

func TestNilPackerIsInert(t *testing.T) {
	var p *Packer
	pack, err := p.Build("worker", "q", nil, nil, "")
	if err != nil || pack == nil {
		t.Fatalf("nil packer must be inert: %v %v", pack, err)
	}
	p.ClearCache()
	p.SetRepoMap(nil)
	p.SetContextLimitTokens(1000)
	if p.BudgetTokensFor("worker") != MinPackTokens {
		t.Fatal("nil packer budget")
	}
}

func TestTaskPackJSONShapeIsCompatible(t *testing.T) {
	pack := &TaskPack{
		Query: "q", Role: "worker",
		Docs:  map[string]string{"A.md": "a"},
		Files: map[string]string{"a.go": "package a"},
	}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"query", "role", "docs", "files", "budget_used"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("json key %q dropped: %s", key, data)
		}
	}
	docs, ok := decoded["docs"].(map[string]any)
	if !ok || docs["A.md"] != "a" {
		t.Fatalf("docs shape changed: %s", data)
	}
}

func TestIdentifierOnlyPackingForExploratoryRoles(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\nunderstand the engine\n")
	writeFile(t, root, "engine.go", bigGoFile("Ignite", 40))

	tests := []struct {
		name      string
		role      string
		wantIDs   bool
		forceIDs  bool
		forceBody bool
	}{
		{name: "explorer defaults to identifiers", role: "explorer", wantIDs: true},
		{name: "docs defaults to identifiers", role: "docs", wantIDs: true},
		{name: "worker keeps bodies", role: "worker", wantIDs: false},
		{name: "corrector keeps bodies", role: "corrector", wantIDs: false},
		{name: "forced identifiers", role: "worker", forceIDs: true, wantIDs: true},
		{name: "forced bodies", role: "explorer", forceBody: true, wantIDs: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPackerWithBudget(store, root, 16384)
			pack, err := p.BuildPack(BuildRequest{
				Role: tc.role, Query: "understand Ignite", Docs: []string{DocQuery},
				Files: []string{"engine.go"}, IdentifiersOnly: tc.forceIDs, Bodies: tc.forceBody,
			})
			if err != nil {
				t.Fatal(err)
			}
			gotIDs := pack.Identifiers != ""
			if gotIDs != tc.wantIDs {
				t.Fatalf("identifiers=%v want %v (files=%d)", gotIDs, tc.wantIDs, len(pack.Files))
			}
			if tc.wantIDs {
				if len(pack.Files) != 0 {
					t.Fatal("identifier packing must not also ship bodies")
				}
				if !strings.Contains(pack.Identifiers, "func Ignite") {
					t.Fatalf("identifiers missing the signature:\n%s", pack.Identifiers)
				}
				if strings.Contains(pack.Identifiers, "return strings.TrimSpace") {
					t.Fatalf("identifiers leaked a body:\n%s", pack.Identifiers)
				}
				if !strings.Contains(pack.Render(), "ws_read") {
					t.Fatal("identifier section should tell the model how to open a file")
				}
			} else if len(pack.Files) == 0 {
				t.Fatal("body packing produced no files")
			}
		})
	}
}

func TestPackerAttachesRepoMap(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\nchange Alpha\n")
	writeFile(t, root, "pkg/a/a.go", "package a\n\nfunc Alpha() {}\n")
	writeFile(t, root, "pkg/b/b.go", "package b\n\nimport \"x/pkg/a\"\n\nfunc Beta() { a.Alpha() }\n")

	rm, err := repomap.Build(root, repomap.Options{DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	p := NewPackerWithBudget(store, root, 16384, WithRepoMap(rm))
	pack, err := p.BuildPack(BuildRequest{
		Role: "worker", Query: "change Alpha", Docs: []string{DocQuery}, Files: []string{"pkg/a/a.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pack.RepoMap == "" {
		t.Fatal("repo map not attached")
	}
	if strings.Contains(pack.RepoMap, "pkg/a/a.go") {
		t.Fatalf("packed file should be excluded from the map:\n%s", pack.RepoMap)
	}
	if !strings.Contains(pack.RepoMap, "pkg/b/b.go") {
		t.Fatalf("map should show unpacked files:\n%s", pack.RepoMap)
	}
	if !strings.Contains(pack.Render(), "## Repo map") {
		t.Fatal("repo map not rendered")
	}
	// Disabling it must work too.
	off := NewPackerWithBudget(store, root, 16384, WithRepoMap(rm), WithRepoMapTokens(0))
	packOff, _ := off.BuildPack(BuildRequest{Role: "worker", Query: "q", Docs: []string{DocQuery}})
	if packOff.RepoMap != "" {
		t.Fatal("WithRepoMapTokens(0) should disable the map")
	}
}

// WithRoleBudgets is what config's context_role_budget maps onto. Before it
// existed the orchestrator faked a role share by scaling the whole context
// window by want/default — arithmetically the same pack, but every other
// reader of Budget.ContextLimitTokens was then told the model had a window it
// does not have.
func TestWithRoleBudgetsOverridesOnlyTheNamedRoles(t *testing.T) {
	const window = 32768
	base := NewPackerWithBudget(nil, "", window, WithBudget(DefaultBudget(window)))
	tuned := NewPackerWithBudget(nil, "", window,
		WithBudget(DefaultBudget(window)),
		WithRoleBudgets(map[string]int{"  WORKER ": 25}))

	if got, want := tuned.BudgetTokensFor("worker"), base.BudgetTokensFor("worker"); got >= want {
		t.Fatalf("worker budget %d not reduced from %d", got, want)
	}
	if got, want := tuned.BudgetTokensFor("reviewer"), base.BudgetTokensFor("reviewer"); got != want {
		t.Fatalf("an unconfigured role changed: %d != %d", got, want)
	}
	// A 25%% share of a 100%% role is a quarter of the budget.
	quarter := base.BudgetTokensFor("worker") / 4
	if got := tuned.BudgetTokensFor("worker"); got < quarter-2 || got > quarter+2 {
		t.Fatalf("worker budget %d, want ≈%d (25%% of %d)", got, quarter, base.BudgetTokensFor("worker"))
	}
}

// Junk in the map must not silently zero a role's budget.
func TestWithRoleBudgetsIgnoresEmptyAndNonPositiveEntries(t *testing.T) {
	const window = 32768
	base := DefaultBudget(window)
	tuned := NewPackerWithBudget(nil, "", window,
		WithBudget(base),
		WithRoleBudgets(map[string]int{"": 40, "worker": 0, "reviewer": -5}))
	for _, role := range []string{"worker", "reviewer"} {
		if got, want := tuned.BudgetTokensFor(role), base.Available(role); got != want {
			t.Errorf("%s budget = %d, want the default %d", role, got, want)
		}
	}
}

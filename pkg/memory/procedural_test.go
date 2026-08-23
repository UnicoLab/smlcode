package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelFamily(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"Qwen3-Coder-30B-A3B-Instruct-MLX-4bit", "qwen3-coder"},
		{"qwen2.5-coder:14b", "qwen2.5-coder"},
		{"qwen2.5-coder:7b-instruct-q4_K_M", "qwen2.5-coder"},
		{"deepseek-chat", "deepseek"},
		{"gpt-4o-mini", "gpt-4o-mini"},
		{"ollama/llama3.1:8b", "llama3.1"},
		{"codellama", "codellama"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := ModelFamily(tc.in); got != tc.want {
				t.Errorf("ModelFamily(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Golang", "go"}, {"GO", "go"}, {"py", "python"}, {"Python3", "python"},
		{"JS", "javascript"}, {"node", "javascript"}, {"ts", "typescript"},
		{"rs", "rust"}, {"C++", "cpp"}, {"", ""}, {"elixir", "elixir"},
	}
	for _, tc := range tests {
		if got := NormalizeLanguage(tc.in); got != tc.want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProceduresNamespacing(t *testing.T) {
	p := openProcedures("", 0, nil, nil)
	goKey := ProcKey{Topic: TopicEditFormat, Option: "search_replace", ModelFamily: "qwen2.5-coder", Language: "go"}
	pyKey := ProcKey{Topic: TopicEditFormat, Option: "unified_diff", ModelFamily: "qwen2.5-coder", Language: "python"}
	for i := 0; i < 5; i++ {
		p.Record(goKey, true, "")
		p.Record(pyKey, true, "")
	}
	best, ok := p.Best(TopicEditFormat, "qwen2.5-coder", "go")
	if !ok || best.Key.Option != "search_replace" {
		t.Fatalf("go best = %+v (ok=%v), python lessons leaked", best, ok)
	}
	best, ok = p.Best(TopicEditFormat, "qwen2.5-coder", "python")
	if !ok || best.Key.Option != "unified_diff" {
		t.Fatalf("python best = %+v (ok=%v)", best, ok)
	}
	// A different model family must not inherit either.
	if _, ok := p.Best(TopicEditFormat, "gpt-4o-mini", "go"); ok {
		t.Error("another model family inherited a lesson it has no evidence for")
	}
}

func TestProceduresRequireEvidence(t *testing.T) {
	p := openProcedures("", 0, nil, nil)
	key := ProcKey{Topic: TopicEditFormat, Option: "whole_file", ModelFamily: "m", Language: "go"}
	p.Record(key, true, "")
	if _, ok := p.Best(TopicEditFormat, "m", "go"); ok {
		t.Fatalf("one sample should not be enough (MinProcedureSamples=%d)", MinProcedureSamples)
	}
	p.Record(key, true, "")
	p.Record(key, true, "")
	if _, ok := p.Best(TopicEditFormat, "m", "go"); !ok {
		t.Fatal("three samples should qualify")
	}
}

func TestProceduresRateOrdering(t *testing.T) {
	p := openProcedures("", 0, nil, nil)
	good := ProcKey{Topic: TopicEditFormat, Option: "search_replace", ModelFamily: "m", Language: "go"}
	bad := ProcKey{Topic: TopicEditFormat, Option: "unified_diff", ModelFamily: "m", Language: "go"}
	for i := 0; i < 10; i++ {
		p.Record(good, i < 9, "")
		p.Record(bad, i < 4, "")
	}
	ranked := p.Rank(TopicEditFormat, "m", "go")
	if len(ranked) != 2 || ranked[0].Key.Option != "search_replace" {
		t.Fatalf("ranking = %+v", ranked)
	}
	if ranked[0].Rate() <= ranked[1].Rate() {
		t.Errorf("rates not ordered: %.2f vs %.2f", ranked[0].Rate(), ranked[1].Rate())
	}
}

func TestProceduresRenderAndPersist(t *testing.T) {
	dir := t.TempDir()
	p := openProcedures(dir, 0, nil, nil)
	if p.Render("m", "go", 200) != "" {
		t.Fatal("empty procedural memory must render nothing")
	}
	key := ProcKey{Topic: TopicEditFormat, Option: "search_replace", ModelFamily: "m", Language: "go"}
	for i := 0; i < 10; i++ {
		p.Record(key, i != 0, "apply rate is high on this stack")
	}
	out := p.Render("m", "go", 200)
	if !strings.Contains(out, "search_replace") {
		t.Fatalf("render lost the option: %q", out)
	}
	if n := countTokens(nil, out); n > 200 {
		t.Errorf("render used %d tokens, budget 200", n)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"procedures.json", "PROCEDURES.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	p2 := openProcedures(dir, 0, nil, nil)
	got, ok := p2.Get(key)
	if !ok || got.Successes != 9 || got.Failures != 1 {
		t.Fatalf("reloaded = %+v (ok=%v)", got, ok)
	}
}

func TestProceduresSurviveCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "procedures.json"), []byte("[[[["), 0o600); err != nil {
		t.Fatal(err)
	}
	p := openProcedures(dir, 0, nil, nil)
	if p.Count() != 0 || len(p.Warnings()) == 0 {
		t.Fatalf("count=%d warnings=%v", p.Count(), p.Warnings())
	}
}

func TestProcKeyNormalize(t *testing.T) {
	k := ProcKey{Topic: " Edit_Format ", Option: "  Search_Replace ", ModelFamily: " Qwen ", Language: "Golang"}.Normalize()
	if k.Topic != "edit_format" || k.Option != "search_replace" || k.ModelFamily != "qwen" || k.Language != "go" {
		t.Fatalf("normalize = %+v", k)
	}
	if !strings.Contains(k.String(), "edit_format/search_replace") {
		t.Errorf("String() = %q", k.String())
	}
	a := ProcKey{Topic: "a", Option: "b"}
	b := ProcKey{Topic: "a", Option: "c"}
	if a.ID() == b.ID() {
		t.Error("different options must have different ids")
	}
}

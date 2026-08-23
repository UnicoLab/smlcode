package contextstore

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// sourceWithDeepSymbol builds a file whose interesting function sits well past
// any head-truncation window — the exact case that made ws_edit's exact
// old_str match impossible.
func sourceWithDeepSymbol(target string, before, after int) string {
	var b strings.Builder
	b.WriteString("// Copyright 2020 Example Inc.\n// SPDX-License-Identifier: MIT\n\npackage deep\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")
	for i := 0; i < before; i++ {
		fmt.Fprintf(&b, "func noise%d() { _ = %d }\n", i, i)
	}
	fmt.Fprintf(&b, "// %s does the thing.\nfunc %s(input string) string {\n\treturn strings.ToUpper(input)\n}\n", target, target)
	for i := 0; i < after; i++ {
		fmt.Fprintf(&b, "func trailing%d() { fmt.Println(%d) }\n", i, i)
	}
	return b.String()
}

var elisionRe = regexp.MustCompile(`// … lines (\d+)-(\d+) elided`)

func TestExcerptFindsDeepSymbol(t *testing.T) {
	src := sourceWithDeepSymbol("ProcessPayment", 120, 120)
	tests := []struct {
		name     string
		terms    []string
		maxBytes int
		wantHit  bool
	}{
		{"exact symbol", []string{"ProcessPayment"}, 4000, true},
		{"lowercase symbol", []string{"processpayment"}, 4000, true},
		{"multiple terms", []string{"ProcessPayment", "ToUpper"}, 4000, true},
		{"unrelated term", []string{"Kubernetes"}, 4000, false},
		{"no terms", nil, 4000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Excerpt(src, tc.terms, ExcerptOptions{MaxBytes: tc.maxBytes})
			if got == "" {
				t.Fatal("empty excerpt")
			}
			if len(got) > tc.maxBytes {
				t.Fatalf("excerpt %d bytes over cap %d", len(got), tc.maxBytes)
			}
			hasTarget := strings.Contains(got, "func ProcessPayment(input string) string")
			if hasTarget != tc.wantHit {
				t.Fatalf("target present=%v want %v:\n%s", hasTarget, tc.wantHit, got)
			}
			// Orientation prologue is always present.
			if !strings.Contains(got, "package deep") {
				t.Fatalf("missing package clause:\n%s", got)
			}
		})
	}
}

func TestExcerptHasRealLineNumbers(t *testing.T) {
	src := sourceWithDeepSymbol("Target", 100, 100)
	lines := strings.Split(src, "\n")
	got := Excerpt(src, []string{"Target"}, ExcerptOptions{MaxBytes: 4000})

	checked := 0
	for _, out := range strings.Split(got, "\n") {
		if out == "" || strings.HasPrefix(out, "// …") {
			continue
		}
		num, body, ok := strings.Cut(out, "\t")
		if !ok {
			t.Fatalf("line without a number prefix: %q", out)
		}
		var n int
		if _, err := fmt.Sscanf(num, "%d", &n); err != nil {
			t.Fatalf("bad line number %q", num)
		}
		if n < 1 || n > len(lines) {
			t.Fatalf("line number %d out of range 1..%d", n, len(lines))
		}
		if lines[n-1] != body {
			t.Fatalf("line %d says %q but source has %q", n, body, lines[n-1])
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only checked %d lines", checked)
	}
}

func TestExcerptMarksElisions(t *testing.T) {
	src := sourceWithDeepSymbol("Target", 200, 200)
	got := Excerpt(src, []string{"Target"}, ExcerptOptions{MaxBytes: 3000})
	matches := elisionRe.FindAllStringSubmatch(got, -1)
	if len(matches) == 0 {
		t.Fatalf("no elision markers in a heavily windowed excerpt:\n%s", got)
	}
	for _, m := range matches {
		var lo, hi int
		fmt.Sscanf(m[1], "%d", &lo)
		fmt.Sscanf(m[2], "%d", &hi)
		if lo > hi || lo < 1 {
			t.Fatalf("bad elision range %s-%s", m[1], m[2])
		}
	}
}

func TestExcerptFallsBackToHeadAndTail(t *testing.T) {
	src := sourceWithDeepSymbol("Target", 150, 150)
	lines := strings.Split(src, "\n")
	got := Excerpt(src, []string{"NothingMatchesThisAtAll"}, ExcerptOptions{MaxBytes: 4000})

	if !strings.Contains(got, "package deep") {
		t.Fatalf("fallback lost the head:\n%s", got)
	}
	// The last real line of the file must be present — head-only truncation is
	// exactly what this replaces.
	lastReal := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastReal = lines[i]
			break
		}
	}
	if !strings.Contains(got, lastReal) {
		t.Fatalf("fallback is head-only; missing tail line %q:\n%s", lastReal, got)
	}
}

func TestExcerptSmallFilePassesThrough(t *testing.T) {
	src := "package a\n\nfunc Alpha() {}\n"
	if got := Excerpt(src, []string{"Alpha"}, ExcerptOptions{MaxBytes: 4000}); got != src {
		t.Fatalf("small file should pass through verbatim, got:\n%s", got)
	}
	if Excerpt("", []string{"x"}, ExcerptOptions{}) != "" {
		t.Fatal("empty input")
	}
}

func TestExcerptRespectsByteCap(t *testing.T) {
	src := sourceWithDeepSymbol("Target", 400, 400)
	for _, cap := range []int{200, 500, 1500, 4000, 12000} {
		got := Excerpt(src, []string{"Target", "noise7", "trailing9"}, ExcerptOptions{MaxBytes: cap})
		if len(got) > cap {
			t.Fatalf("cap %d produced %d bytes", cap, len(got))
		}
	}
}

func TestExcerptPrefersDeclarationLines(t *testing.T) {
	src := "package a\n\n" +
		strings.Repeat("// mentions Handler in a comment\n", 60) +
		"func Handler(w int) {}\n" +
		strings.Repeat("// mentions Handler again\n", 60)
	got := Excerpt(src, []string{"Handler"}, ExcerptOptions{MaxBytes: 900, Window: 3, MaxWindows: 2})
	if !strings.Contains(got, "func Handler(w int) {}") {
		t.Fatalf("declaration should win over comment mentions:\n%s", got)
	}
}

func TestExtractTerms(t *testing.T) {
	tests := []struct {
		name    string
		inputs  []string
		want    []string
		wantNot []string
	}{
		{
			name:    "identifiers from a task",
			inputs:  []string{"Rename Hello to Greet", "in pkg/greet/greet.go"},
			want:    []string{"Rename", "Hello", "Greet", "greet"},
			wantNot: []string{"to", "in"},
		},
		{
			name:    "stopwords dropped",
			inputs:  []string{"please update the file and fix the function"},
			wantNot: []string{"please", "update", "the", "file", "fix", "function"},
		},
		{
			name:   "snake and camel",
			inputs: []string{"build_service and NewEngine"},
			want:   []string{"build_service", "NewEngine"},
		},
		{name: "empty", inputs: []string{"", "   "}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractTerms(tc.inputs...)
			joined := strings.Join(got, " ")
			for _, w := range tc.want {
				found := false
				for _, g := range got {
					if strings.EqualFold(g, w) {
						found = true
					}
				}
				if !found {
					t.Errorf("missing term %q in %v", w, got)
				}
			}
			for _, w := range tc.wantNot {
				for _, g := range got {
					if strings.EqualFold(g, w) {
						t.Errorf("term %q should have been filtered (%s)", w, joined)
					}
				}
			}
			// Deterministic + de-duplicated.
			seen := map[string]bool{}
			for _, g := range got {
				if seen[strings.ToLower(g)] {
					t.Errorf("duplicate term %q", g)
				}
				seen[strings.ToLower(g)] = true
			}
		})
	}
}

func TestPackerUsesRelevanceWindowsForLeanRoles(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocQuery, "# Q\n\nfix ProcessPayment\n")
	writeFile(t, root, "pay.go", sourceWithDeepSymbol("ProcessPayment", 200, 200))

	p := NewPackerWithBudget(store, root, 16384)
	pack, err := p.BuildPack(BuildRequest{
		Role: "worker", Query: "fix ProcessPayment",
		TaskTitle: "Fix ProcessPayment rounding", Acceptance: "ProcessPayment returns lowercase",
		Docs: []string{DocQuery}, Files: []string{"pay.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := pack.Files["pay.go"]
	if body == "" {
		t.Fatal("no file packed")
	}
	if !strings.Contains(body, "func ProcessPayment(input string) string") {
		t.Fatalf("worker cannot see the function it must edit:\n%s", body)
	}
	if !strings.Contains(body, "package deep") {
		t.Fatalf("lost orientation prologue:\n%s", body)
	}
	if !elisionRe.MatchString(body) {
		t.Fatalf("expected elision markers:\n%s", body)
	}

	// Turning windowing off must fall back to plain clipping.
	plain := NewPackerWithBudget(store, root, 16384, WithExcerpts(false))
	pp, err := plain.BuildPack(BuildRequest{
		Role: "worker", Query: "fix ProcessPayment", Docs: []string{DocQuery}, Files: []string{"pay.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if elisionRe.MatchString(pp.Files["pay.go"]) {
		t.Fatal("WithExcerpts(false) should not window")
	}
}

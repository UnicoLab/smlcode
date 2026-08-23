package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadProjectInstructions(t *testing.T) {
	root := seedRoot(t, map[string]string{
		"AGENTS.md":           "# Agents\nPrefer tiny edits.\n",
		".slmcode/PROJECT.md": "# Project\nGo app\n",
	})
	out := LoadProjectInstructions(root)
	if !strings.Contains(out, "AGENTS.md") || !strings.Contains(out, "tiny edits") {
		t.Fatalf("%s", out)
	}
}

func TestReadmeIsNotInstructions(t *testing.T) {
	readme := "# Badges\n" + strings.Repeat("[![build](https://img.shields.io/x)](https://y)\n", 200)
	root := seedRoot(t, map[string]string{
		"AGENTS.md": "# Agents\nPrefer tiny edits.\n",
		"README.md": readme,
	})
	out := LoadProjectInstructions(root)
	if strings.Contains(out, "img.shields.io") {
		t.Fatalf("README leaked into instructions:\n%s", out[:min(400, len(out))])
	}
	if !strings.Contains(out, "tiny edits") {
		t.Fatal("real instructions lost")
	}
	// Opt-in still works.
	opted := Load(Options{Root: root, IncludeReadme: true})
	if !strings.Contains(opted, "img.shields.io") {
		t.Fatal("IncludeReadme should opt README back in")
	}
}

func TestAgentsFilesLayerRatherThanShadow(t *testing.T) {
	root := seedRoot(t, map[string]string{
		"AGENTS.md":          "# Root agents\nROOT_RULE: use go test\n",
		".slmcode/AGENTS.md": "# Workspace agents\nWORKSPACE_RULE: never touch vendor\n",
	})
	out := LoadProjectInstructions(root)
	if !strings.Contains(out, "ROOT_RULE") {
		t.Fatalf("root AGENTS.md missing:\n%s", out)
	}
	if !strings.Contains(out, "WORKSPACE_RULE") {
		t.Fatalf(".slmcode/AGENTS.md was shadowed by basename dedup:\n%s", out)
	}
	// Both headers present, distinct.
	if !strings.Contains(out, "## AGENTS.md") || !strings.Contains(out, "## .slmcode/AGENTS.md") {
		t.Fatalf("headers wrong:\n%s", out)
	}
}

func TestBudgetIsNeverOvershot(t *testing.T) {
	big := func(marker string, n int) string {
		return "# " + marker + "\n" + strings.Repeat(marker+" rule line\n", n)
	}
	root := seedRoot(t, map[string]string{
		"AGENTS.md":           big("A", 2000),
		"CLAUDE.md":           big("B", 2000),
		"AGENT.md":            big("C", 2000),
		".cursorrules":        big("D", 2000),
		".slmcode/AGENTS.md":  big("E", 2000),
		".slmcode/PROJECT.md": big("F", 2000),
	})
	tests := []struct {
		name     string
		maxBytes int
		perFile  int
	}{
		{"default", 0, 0},
		{"tiny total", 500, 4000},
		{"tiny per file", 12000, 300},
		{"both tiny", 400, 200},
		{"generous", 100000, 50000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := Load(Options{Root: root, MaxBytes: tc.maxBytes, PerFileBytes: tc.perFile})
			limit := tc.maxBytes
			if limit <= 0 {
				limit = DefaultMaxBytes
			}
			// Allow only the inter-section separators over the accounted budget.
			if len(out) > limit+16 {
				t.Fatalf("budget %d overshot: %d bytes", limit, len(out))
			}
			perFile := tc.perFile
			if perFile <= 0 {
				perFile = DefaultPerFileBytes
			}
			for _, sec := range strings.Split(out, "\n\n## ") {
				if len(sec) > perFile+64 {
					t.Fatalf("per-file cap %d overshot: %d", perFile, len(sec))
				}
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name string
		glob string
		path string
		want bool
	}{
		{"exact", "pkg/a.go", "pkg/a.go", true},
		{"dir prefix", "pkg/context", "pkg/context/pack.go", true},
		{"dir prefix miss", "pkg/context", "pkg/compact/x.go", false},
		{"star segment", "pkg/*/pack.go", "pkg/context/pack.go", true},
		{"star does not cross", "pkg/*.go", "pkg/context/pack.go", false},
		{"double star", "pkg/**/*.go", "pkg/context/sub/pack.go", true},
		{"double star zero segments", "pkg/**/*.go", "pkg/pack.go", true},
		{"double star suffix", "**/*.tsx", "web/src/App.tsx", true},
		{"wrong ext", "**/*.tsx", "web/src/App.ts", false},
		{"bare star", "*", "anything/at/all.go", true},
		{"bare double star", "**", "anything/at/all.go", true},
		{"empty glob", "", "a.go", false},
		{"empty path", "*.go", "", false},
		{"leading dot slash", "./pkg/**", "pkg/a.go", true},
		{"windows sep", `pkg\**`, "pkg/a.go", true},
		{"char class", "pkg/[ab].go", "pkg/a.go", true},
		{"char class miss", "pkg/[ab].go", "pkg/c.go", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchGlob(tc.glob, tc.path); got != tc.want {
				t.Fatalf("MatchGlob(%q,%q)=%v want %v", tc.glob, tc.path, got, tc.want)
			}
		})
	}
}

func TestGateSections(t *testing.T) {
	md := `# Project rules

Always applies.

## Go rules <!-- paths: pkg/**/*.go, cmd/**/*.go -->

GO_RULE: run go vet

## Frontend rules <!-- paths: web/**/*.tsx -->

TSX_RULE: use tailwind

## Terraform <!-- paths: infra/**/*.tf -->

TF_RULE: plan before apply
`
	tests := []struct {
		name    string
		scope   []string
		want    []string
		wantNot []string
	}{
		{
			name:    "go scope",
			scope:   []string{"pkg/context/pack.go"},
			want:    []string{"Always applies", "GO_RULE"},
			wantNot: []string{"TSX_RULE", "TF_RULE"},
		},
		{
			name:    "frontend scope",
			scope:   []string{"web/src/App.tsx"},
			want:    []string{"Always applies", "TSX_RULE"},
			wantNot: []string{"GO_RULE", "TF_RULE"},
		},
		{
			name:    "mixed scope",
			scope:   []string{"pkg/a.go", "infra/main.tf"},
			want:    []string{"GO_RULE", "TF_RULE"},
			wantNot: []string{"TSX_RULE"},
		},
		{
			name:    "unmatched scope keeps only ungated",
			scope:   []string{"docs/readme.txt"},
			want:    []string{"Always applies"},
			wantNot: []string{"GO_RULE", "TSX_RULE", "TF_RULE"},
		},
		{
			name: "empty scope disables gating",
			want: []string{"Always applies", "GO_RULE", "TSX_RULE", "TF_RULE"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GateSections(md, tc.scope)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q:\n%s", w, got)
				}
			}
			if strings.Contains(got, "<!-- paths:") {
				t.Errorf("gate markers leaked into the prompt:\n%s", got)
			}
		})
	}
}

func TestGateFrontmatter(t *testing.T) {
	md := "---\npaths: web/**/*.tsx\n---\n\n# Frontend only\n\nFRONT_RULE: x\n"
	tests := []struct {
		name  string
		scope []string
		want  bool
	}{
		{"matching scope", []string{"web/src/App.tsx"}, true},
		{"non-matching scope", []string{"pkg/a.go"}, false},
		{"empty scope", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GateSections(md, tc.scope)
			has := strings.Contains(got, "FRONT_RULE")
			if has != tc.want {
				t.Fatalf("present=%v want %v (%q)", has, tc.want, got)
			}
			if strings.Contains(got, "paths: web") {
				t.Fatalf("frontmatter leaked:\n%s", got)
			}
		})
	}
}

func TestLoadForScope(t *testing.T) {
	root := seedRoot(t, map[string]string{
		"AGENTS.md": "# Rules\n\nAlways.\n\n## Go <!-- paths: **/*.go -->\n\nGO_RULE\n\n" +
			"## Web <!-- paths: **/*.tsx -->\n\nTSX_RULE\n",
	})
	out := LoadForScope(root, []string{"pkg/x.go"})
	if !strings.Contains(out, "GO_RULE") || strings.Contains(out, "TSX_RULE") {
		t.Fatalf("scope gating not applied:\n%s", out)
	}
	all := LoadProjectInstructions(root)
	if !strings.Contains(all, "TSX_RULE") {
		t.Fatalf("ungated load should keep everything:\n%s", all)
	}
}

func TestLoadMissingRoot(t *testing.T) {
	if got := LoadProjectInstructions(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

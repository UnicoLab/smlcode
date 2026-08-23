package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/blocks"
)

// TestEveryShippedLanguagePackIsReachable pins defect 7.
//
// The repo ships typescript, dotnet, kotlin, ruby, php and swift packs with
// real <lang>-worker / <lang>-tester agents, and nothing routed to them: the
// specialist maps only knew go/python/react/web/rust/java/cpp, so "write me a
// C# API" went to the generic worker with no .NET prompt at all.
func TestEveryShippedLanguagePackIsReachable(t *testing.T) {
	reg, err := blocks.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load blocks: %v", err)
	}
	known := map[string]bool{}
	for id := range reg.Agents {
		known[strings.ToLower(id)] = true
	}
	if !known["dotnet-worker"] {
		t.Skip("bundled agents unavailable in this build")
	}

	queries := map[string]string{
		"write me a C# API with ASP.NET":         "dotnet-worker",
		"add a Ktor route in Kotlin":             "kotlin-worker",
		"add an RSpec test to the Rails app":     "ruby-worker",
		"fix the Laravel controller in PHP":      "php-worker",
		"add a SwiftUI view":                     "swift-worker",
		"add a typescript CLI command":           "ts-worker",
		"add a React component with vite":        "react-worker",
		"add a go test for the parser in golang": "go-worker",
		"write a pytest for the flask view":      "python-worker",
	}
	for q, want := range queries {
		worker, tester := queryLanguageSpecialists(q)
		if worker != want {
			t.Errorf("queryLanguageSpecialists(%q) = %q, want %q", q, worker, want)
			continue
		}
		if !known[worker] || !known[tester] {
			t.Errorf("%q routes to %s/%s which is not a registered agent", q, worker, tester)
		}
	}

	// Every pack the repo ships must be reachable by project detection too, or
	// the pack is dead weight for anyone who does not name the language.
	for _, lang := range []string{
		"Go", "Python", "TypeScript", "JavaScript", "Rust", "Java",
		"Kotlin", "C#", "Ruby", "PHP", "Swift", "C++",
	} {
		worker, tester := projectLanguageSpecialists(lang)
		if worker == "" || tester == "" {
			t.Errorf("projectLanguageSpecialists(%q) has no specialist pair", lang)
			continue
		}
		if !known[worker] || !known[tester] {
			t.Errorf("%q routes to %s/%s which is not a registered agent", lang, worker, tester)
		}
	}
}

// TestDetectProjectLangFindsTheNewPacks covers the detection half: a pack that
// only a query keyword can select is unreachable for "add an endpoint".
func TestDetectProjectLangFindsTheNewPacks(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		dirs  []string
		want  string
	}{
		{"dotnet", map[string]string{"Api.csproj": "<Project/>"}, nil, "C#"},
		{"ruby", map[string]string{"Gemfile": "source 'https://rubygems.org'\n"}, nil, "Ruby"},
		{"php", map[string]string{"composer.json": "{}"}, nil, "PHP"},
		{"swift", map[string]string{"Package.swift": "// swift-tools-version:5.9\n"}, nil, "Swift"},
		{"kotlin gradle", map[string]string{"build.gradle.kts": ""}, []string{"src/main/kotlin"}, "Kotlin"},
		{"java gradle", map[string]string{"build.gradle.kts": ""}, []string{"src/main/java"}, "Java"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ResetLangCache(root)
			if got := detectProjectLang(root); got != tc.want {
				t.Fatalf("detectProjectLang = %q, want %q", got, tc.want)
			}
			if w, _ := projectLanguageSpecialists(tc.want); w == "" {
				t.Fatalf("%q has no specialist pair", tc.want)
			}
		})
	}
}

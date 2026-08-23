package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/blocks"
)

// `slmcode init` writes TWO things that have to agree: `active_pack`, chosen by
// the CLI, and `qa_gate_command`, chosen by the pack that InitWorkspace applies
// from the detected QUALITY block. They used to be picked by two different
// marker lists, and on six of the thirteen languages they disagreed — a Kotlin
// project got `active_pack: java` next to `./gradlew test`, a TypeScript project
// got `active_pack: web` next to `npm test`.
//
// The CLI's own list is gone; this test is the guard that keeps it gone.
func TestInitPackAgreesWithTheAppliedQualityBlock(t *testing.T) {
	reg, err := blocks.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]map[string]string{
		"go":         {"go.mod": "module x\n", "main.go": "package main\n"},
		"python":     {"pyproject.toml": "[project]\nname='x'\n", "app.py": "x = 1\n"},
		"rust":       {"Cargo.toml": "[package]\nname='x'\n", "src/main.rs": "fn main() {}\n"},
		"java":       {"pom.xml": "<project/>", "src/App.java": "class App {}\n"},
		"kotlin":     {"build.gradle.kts": "plugins {}\n", "src/App.kt": "fun main() {}\n"},
		"dotnet":     {"App.csproj": "<Project/>", "Program.cs": "class P {}\n"},
		"ruby":       {"Gemfile": "source 'x'\n", "lib/app.rb": "class App; end\n"},
		"php":        {"composer.json": "{}", "src/App.php": "<?php\n"},
		"swift":      {"Package.swift": "// swift-tools-version:5.9\n", "Sources/App/main.swift": "print(1)\n"},
		"cpp":        {"CMakeLists.txt": "project(x)\n", "src/main.cpp": "int main(){}\n"},
		"typescript": {"package.json": `{"name":"x"}`, "tsconfig.json": "{}", "src/index.ts": "export const x=1\n"},
		"react":      {"package.json": `{"dependencies":{"react":"^18"}}`, "src/App.tsx": "export default () => null\n"},
		"web":        {"index.html": "<!doctype html><h1>hi</h1>", "style.css": "body{}"},
	}
	ids := make([]string, 0, len(fixtures))
	for id := range fixtures {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, want := range ids {
		t.Run(want, func(t *testing.T) {
			root := t.TempDir()
			if real, err := filepath.EvalSymlinks(root); err == nil {
				root = real
			}
			for rel, body := range fixtures[want] {
				full := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// What `slmcode init` writes as active_pack.
			got := blocks.DetectPack(root, root)
			if got != want {
				t.Fatalf("init would write active_pack=%q, want %q", got, want)
			}

			// What InitWorkspace applies, and therefore which qa_gate_command
			// ends up next to it.
			q := reg.DetectQuality(root)
			if q == nil {
				t.Fatal("no quality block detected — init would write a pack with no gate")
			}
			applied := ""
			for id, p := range reg.Packs {
				if p.Spec.Quality == q.ID {
					applied = id
					break
				}
			}
			if applied != got {
				t.Errorf("active_pack=%q but the applied quality block belongs to pack %q "+
					"(qa_gate_command would be %q) — the two writers disagree",
					got, applied, q.Spec.QAGate)
			}
		})
	}
}

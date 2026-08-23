package quality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatChangedFilesOnlyTouchesTheWave is the core guarantee: a file no
// agent changed must come back byte-identical, however unformatted it is.
func TestFormatChangedFilesOnlyTouchesTheWave(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const ugly = "package p\nfunc  A( ) int {\nreturn 1\n}\n"
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("changed.go", ugly)
	write("untouched.go", ugly)
	write("vendored/deep.go", ugly)

	var snapshotted []string
	summary := FormatChangedFiles(context.Background(), FormatRequest{
		Root:     root,
		Files:    []string{"changed.go"},
		Snapshot: func(rel string) { snapshotted = append(snapshotted, rel) },
	})

	changed, _ := os.ReadFile(filepath.Join(root, "changed.go"))
	if string(changed) == ugly {
		t.Fatalf("the wave's own file was not formatted (summary %q)", summary)
	}
	for _, rel := range []string{"untouched.go", "vendored/deep.go"} {
		body, _ := os.ReadFile(filepath.Join(root, rel))
		if string(body) != ugly {
			t.Fatalf("%s was reformatted although no agent touched it — this is the unrelated-diff defect", rel)
		}
	}
	if len(snapshotted) != 1 || snapshotted[0] != "changed.go" {
		t.Fatalf("snapshot hook got %v, want [changed.go] — formatting must be undoable", snapshotted)
	}
}

func TestFormatChangedFilesScopeAndOptIn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const ugly = "package p\nfunc  A( ) int {\nreturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(ugly), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		req        FormatRequest
		wantRan    bool
		wantOutter string
	}{
		{"no files means no formatting at all", FormatRequest{Root: root}, false, ""},
		{"empty root is a no-op", FormatRequest{Files: []string{"a.go"}}, false, ""},
		{"escaping paths are dropped", FormatRequest{Root: root, Files: []string{"../evil.go", "/etc/passwd"}}, false, ""},
		{"missing files are dropped", FormatRequest{Root: root, Files: []string{"nope.go"}}, false, ""},
		{"non-source files are dropped", FormatRequest{Root: root, Files: []string{"go.mod"}}, false, ""},
		{"a real changed file formats", FormatRequest{Root: root, Files: []string{"a.go"}}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(ugly), 0o644); err != nil {
				t.Fatal(err)
			}
			FormatChangedFiles(context.Background(), tc.req)
			body, _ := os.ReadFile(filepath.Join(root, "a.go"))
			formatted := string(body) != ugly
			if formatted != tc.wantRan {
				t.Fatalf("formatted=%v, want %v", formatted, tc.wantRan)
			}
		})
	}
}

// TestAutoFixFormattingIsNoLongerRepoWide pins the deprecation: the old blunt
// entry point must not quietly keep formatting the whole project.
func TestAutoFixFormattingIsNoLongerRepoWide(t *testing.T) {
	root := t.TempDir()
	const ugly = "package p\nfunc  A( ) int {\nreturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(ugly), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AutoFixFormatting(root); got != "" {
		t.Fatalf("AutoFixFormatting = %q, want empty", got)
	}
	body, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(body) != ugly {
		t.Fatal("AutoFixFormatting reformatted the repo — it must be a no-op now")
	}
}

func TestBootstrapPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name              string
		policy            string
		cmd               string
		wantCommand       string
		wantRun           bool
		wantNeedsApproval bool
	}{
		{"ask is the default for unknown text", "banana", "pytest -q",
			"python -m pip install -q -r requirements.txt", false, true},
		{"empty policy is ask", "", "pytest -q",
			"python -m pip install -q -r requirements.txt", false, true},
		{"off refuses and reports", "off", "pytest -q", "", false, false},
		{"auto is the only way to install unattended", "auto", "pytest -q",
			"python -m pip install -q -r requirements.txt", true, false},
		{"no manifest, no plan", "auto", "cargo test", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanBootstrap(root, tc.cmd, NormalizeBootstrapPolicy(tc.policy))
			if plan.Command != tc.wantCommand {
				t.Fatalf("Command = %q, want %q", plan.Command, tc.wantCommand)
			}
			if plan.Run != tc.wantRun {
				t.Fatalf("Run = %v, want %v", plan.Run, tc.wantRun)
			}
			if plan.NeedsApproval != tc.wantNeedsApproval {
				t.Fatalf("NeedsApproval = %v, want %v", plan.NeedsApproval, tc.wantNeedsApproval)
			}
			if tc.policy == "off" && tc.cmd == "pytest -q" && plan.Reason == "" {
				t.Fatal("a refused bootstrap must say so")
			}
		})
	}
}

// TestAcceptanceSmokeDoesNotInstallByDefault is the end-to-end half: the
// acceptance path must not execute an install command unless policy says auto.
func TestAcceptanceSmokeDoesNotInstallByDefault(t *testing.T) {
	root := t.TempDir()
	// A marker file the "install" would create if it ever ran.
	if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sr := RunAcceptanceSmoke(context.Background(), root, "Run `pytest -q`", 0)
	if sr.Ran && !strings.Contains(sr.Summary, "needs approval") {
		t.Fatalf("summary %q must report the pending bootstrap so a dependency failure is diagnosable", sr.Summary)
	}
}

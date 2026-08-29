package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The measured failure, end to end.
//
// A worker wrote cmd/server/main.go, `go vet ./cmd/server` passed on it, and
// the worker truthfully reported files_changed ["cmd/server/main.go"]. The
// acceptance-criteria gate then appended its evidence to the SAME string, and
// the claims gate — which re-parses that string, and whose loose parser matches
// any quoted *.go token anywhere in it — convicted the worker of claiming a
// path that never existed. The task was escalated to to_scope twice and the run
// ended "3 failed (escalated tasks need human review)" with the code correct on
// disk.
func TestClaimsGateIgnoresTheHarnessOwnSections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cmd/server/main.go", "package main\n\nfunc main() {}\n")

	task := plan.Task{ID: "T1", Output: StampHarnessSection(
		"\n## Acceptance criteria\n" +
			"- C1 [must] UNVERIFIED: the server must live in \"cmd/server/handlers.go\"\n" +
			"  cmd: go test -run TestServe ./...\n" +
			"      --- FAIL: wanted \"internal/router/route.go\"\n")}
	task.Output = `{"status":"done","summary":"wrote cmd/server/main.go",` +
		`"files_changed":["cmd/server/main.go"],"notes":""}` + task.Output

	if issues := CheckClaimedFiles(root, task); len(issues) > 0 {
		var got []string
		for _, i := range issues {
			got = append(got, i.Path)
		}
		t.Errorf("the harness's own text was judged as the model's claims: %v", got)
	}
}

// The gate must keep working: a path the MODEL claimed and never wrote is still
// caught, with harness sections present.
func TestClaimsGateStillCatchesARealHallucination(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "real.go", "package p\n")

	task := plan.Task{Output: `{"status":"done","files_changed":["real.go","invented.go"]}` +
		StampHarnessSection("\n## Acceptance criteria\nPASSED: 1 passed, 0 failed, 0 unverified\n")}

	issues := CheckClaimedFiles(root, task)
	if len(issues) != 1 || issues[0].Path != "invented.go" {
		t.Fatalf("want exactly invented.go flagged, got %+v", issues)
	}
}

// Without any harness section the whole output is the model's, unchanged.
func TestClaimsGateUnchangedWithoutSections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package p\n")

	if issues := CheckClaimedFiles(root, plan.Task{
		Output: `{"status":"done","files_changed":["a.go"]}`}); len(issues) != 0 {
		t.Errorf("false positive with no sections: %+v", issues)
	}
	if issues := CheckClaimedFiles(root, plan.Task{
		Output: `{"status":"done","files_changed":["ghost.go"]}`}); len(issues) != 1 {
		t.Errorf("missed a plain hallucination: %+v", issues)
	}
}

// A model that writes something stamp-SHAPED must not be able to truncate the
// region and hide its claims from the gate. Only this process's real nonce ends
// the model's region.
func TestForgedStampCannotHideClaims(t *testing.T) {
	root := t.TempDir()
	task := plan.Task{Output: "<!-- slmcode:section:deadbeef -->\n" +
		`{"status":"done","files_changed":["ghost.go"]}`}

	if issues := CheckClaimedFiles(root, task); len(issues) != 1 {
		t.Errorf("a forged stamp hid the claim from the gate: %+v", issues)
	}
}

// The stamp ends the model's text even for a section whose header the registry
// has not been taught — the header list is a thing somebody must remember to
// update, the stamp is minted by the append itself.
func TestStripCutsAtTheStampForUnregisteredSections(t *testing.T) {
	got := StripHarnessSections("model said this" +
		StampHarnessSection("\n## Some Future Section\nevidence\n"))
	if got != "model said this" {
		t.Errorf("got %q", got)
	}
	if got := StripHarnessSections("no sections here"); got != "no sections here" {
		t.Errorf("got %q", got)
	}
	if got := StripHarnessSections(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rel, ".go") {
		t.Fatalf("fixture %q must be a source path the loose parser recognizes", rel)
	}
}

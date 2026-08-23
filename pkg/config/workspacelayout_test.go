package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The generated ignore file must actually make git ignore every probe. This is
// a real `git check-ignore`, not a string match: a pattern/probe pair that
// looks right and does not work (a directory rule without its trailing "/", a
// nested path a top-level rule cannot reach) is the exact failure mode that let
// `slmcode commit`'s `git add -A` stage run content.
func TestRenderedGitignoreCoversEveryProbe(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DirName, ".gitignore"),
		[]byte(RenderSlmGitignore()), 0o644); err != nil {
		t.Fatal(err)
	}

	ignored := func(rel string) bool {
		return exec.Command(git, "-C", root, "check-ignore", "-q", rel).Run() == nil
	}
	for _, e := range SlmIgnoreEntries {
		if !ignored(e.Probe) {
			t.Errorf("NOT IGNORED: %s (pattern %q, holds %s)", e.Probe, e.Pattern, e.What)
		}
	}
	// The doctor probe list must be derived from the same table.
	probes := SlmIgnoreProbes()
	if len(probes) != len(SlmIgnoreEntries) {
		t.Errorf("probe list has %d entries, ignore list has %d", len(probes), len(SlmIgnoreEntries))
	}
	for _, p := range probes {
		if !ignored(p) {
			t.Errorf("doctor probe %s is not ignored", p)
		}
	}
	// Shared, reviewable state must stay committable.
	for _, keep := range []string{
		".slmcode/config.yaml", ".slmcode/board.json", ".slmcode/hooks.json",
		".slmcode/skills/mine/SKILL.md", ".slmcode/agents/mine.yaml",
		".slmcode/blocks/agents/x.yaml", ".slmcode/.gitignore",
	} {
		if ignored(keep) {
			t.Errorf("OVER-IGNORED: %s is meant to be shareable", keep)
		}
	}
}

// The specific gaps the security review found must stay closed.
func TestGitignoreCoversRunContentDirectories(t *testing.T) {
	got := map[string]bool{}
	for _, e := range SlmIgnoreEntries {
		got[e.Pattern] = true
		if strings.TrimSpace(e.What) == "" {
			t.Errorf("entry %q has no description", e.Pattern)
		}
		if strings.TrimSpace(e.Probe) == "" {
			t.Errorf("entry %q has no probe", e.Pattern)
		}
		if !strings.HasPrefix(e.Probe, DirName+"/") {
			t.Errorf("probe %q is not under %s/", e.Probe, DirName)
		}
	}
	for _, want := range []string{
		"auth.json", "pending/", "sessions/", "queries/", "archives/", "errors/",
		"checkpoints/", "*.log",
		// added by the review: run content and learned state that `git add -A`
		// used to stage.
		"memory/", "evolve/", "metrics/", "summaries/", "waves/", "clarify/",
		"skills/learned/", "capabilities.json", "throughput.json", "repomap.json",
		"CONTEXT.md.bak",
	} {
		if !got[want] {
			t.Errorf("ignore list is missing %q", want)
		}
	}
}

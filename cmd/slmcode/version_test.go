package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVersionMetadata(t *testing.T) {
	if strings.TrimSpace(Version) == "" {
		t.Fatal("empty Version")
	}
	if !strings.Contains(Version, ".") {
		t.Fatalf("version looks wrong: %s", Version)
	}
	// The version is stamped in three places that a release must not let
	// drift: cmd/slmcode/version.go (the fallback when -ldflags is absent),
	// the Makefile's VERSION, and the Homebrew formula. scripts/prepare-release.sh
	// bumps all three; this catches a hand-edit that touched only one.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Clean(filepath.Join(root, "..", ".."))
	for _, tc := range []struct{ file, pattern string }{
		{"Makefile", `(?m)^VERSION \?= (\S+)`},
		{filepath.Join("Formula", "slmcode.rb"), `(?m)^  version "([^"]+)"`},
	} {
		data, rerr := os.ReadFile(filepath.Join(repo, tc.file)) // #nosec G304 -- repo-relative
		if rerr != nil {
			t.Skipf("cannot read %s: %v", tc.file, rerr)
		}
		m := regexp.MustCompile(tc.pattern).FindSubmatch(data)
		if m == nil {
			t.Errorf("%s: no version line matched %q", tc.file, tc.pattern)
			continue
		}
		if got := string(m[1]); got != Version {
			t.Errorf("%s declares version %q but cmd/slmcode/version.go says %q", tc.file, got, Version)
		}
	}
}

func TestUIEmbedPresent(t *testing.T) {
	entries, err := uiEmbed.ReadDir("ui")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	if !have["index.html"] {
		t.Fatal("missing ui files: map[index.html:true]")
	}
	if !have["assets"] {
		t.Skip("Studio UI assets not built — run `make bootstrap` or `make ui-react` to embed the real React UI (placeholder index.html is present and embeds fine)")
	}
}

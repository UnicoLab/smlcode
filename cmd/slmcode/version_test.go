package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/server"
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

// TestUIEmbedPresent pins the go:embed contract for the Studio UI directory.
//
// cmd/slmcode/ui/ has exactly one tracked file, .gitkeep, so that the directory
// exists on a fresh clone and `//go:embed all:ui` has something to embed — an
// embed pattern that matches nothing is a COMPILE error, so this is what keeps
// `go build ./cmd/slmcode` working with no Node toolchain in sight. The `all:`
// prefix is what makes a dotfile eligible; without it the directory would look
// empty to embed.
//
// Two states are legitimate and this asserts the right thing in each:
//
//	placeholder — no index.html: the server serves pkg/server's built-in page
//	built       — index.html AND assets/ from `make bootstrap` / `make ui-react`
//
// index.html without assets/ is neither: the shell would boot and then ask for
// /assets/*.js that do not exist — a blank screen.
func TestUIEmbedPresent(t *testing.T) {
	entries, err := uiEmbed.ReadDir("ui")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}
	if len(entries) == 0 {
		t.Fatal("go:embed all:ui embedded an empty directory")
	}
	if !have[".gitkeep"] {
		t.Errorf("cmd/slmcode/ui/.gitkeep is not embedded (got %v) — it is the only tracked "+
			"file in that directory and `all:` is what makes go:embed include a dotfile", keys(have))
	}

	uiFS, err := fs.Sub(uiEmbed, "ui")
	if err != nil {
		t.Fatal(err)
	}
	built := server.UIIsBuilt(uiFS)
	// The CLI's startup warning and the page the server serves must agree.
	if built == studioUIIsPlaceholder(uiFS) {
		t.Fatalf("server.UIIsBuilt=%v disagrees with studioUIIsPlaceholder=%v",
			built, studioUIIsPlaceholder(uiFS))
	}

	if !built {
		if have["assets"] {
			t.Fatal("cmd/slmcode/ui/assets/ is embedded without an index.html — " +
				"half-built UI; run `make bootstrap` or delete cmd/slmcode/ui/assets")
		}
		t.Log("Studio UI not built — the binary serves the built-in placeholder page " +
			"(pkg/server/placeholder.go). Run `make bootstrap` to embed the real React UI.")
		return
	}
	if !have["assets"] {
		t.Fatal("cmd/slmcode/ui/index.html is embedded without assets/ — the SPA shell " +
			"would load and then 404 its own bundle; run `make bootstrap`")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

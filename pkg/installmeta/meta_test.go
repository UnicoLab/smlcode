package installmeta

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	m := &Meta{
		Source:    "/src/slmcode",
		Prefix:    "/opt/homebrew",
		Mode:      "system",
		Version:   "0.5.0",
		GitCommit: "abc1234",
		Binary:    "/opt/homebrew/bin/slmcode",
	}
	if err := Save(m); err != nil {
		t.Fatal(err)
	}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != m.Source || got.Mode != "system" || got.Version != "0.5.0" {
		t.Fatalf("got %+v", got)
	}
	if got.InstalledAt == "" {
		t.Fatal("expected InstalledAt")
	}
}

// TestSavePermissions: install.json records the repo `slmcode update` downloads
// from and the source tree it rebuilds from. Anyone who can write it chooses
// what the next update runs, so it is 0600 in a 0700 directory — and the
// installer shell scripts (scripts/install.sh, scripts/install-remote.sh) match
// these modes when they write the same file without going through this package.
func TestSavePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	if err := Save(&Meta{Source: "/src", Prefix: "/opt", Mode: "user", Version: "0.17.0", Repo: "UnicoLab/smlcode"}); err != nil {
		t.Fatal(err)
	}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("install.json mode = %04o, want 0600", perm)
	}
	dir, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	// MkDirAll uses 0750; anything group- or world-writable would defeat the point.
	if perm := dir.Mode().Perm(); perm&0o022 != 0 {
		t.Errorf("config dir mode = %04o, want no group/other write bits", perm)
	}
}

// TestSaveOverwritesInPlace: a second Save must not leave the previous content
// or a stray temp file behind — `slmcode update` writes this on every run.
func TestSaveOverwritesInPlace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	if err := Save(&Meta{Version: "0.16.0", Method: "binary", Binary: "/a/slmcode"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Meta{Version: "0.17.0", Method: "binary", Binary: "/a/slmcode"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.17.0" {
		t.Errorf("Version = %q, want 0.17.0", got.Version)
	}
	entries, err := os.ReadDir(filepath.Join(tmp, DirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != FileName {
			t.Errorf("unexpected leftover in the config dir: %s", e.Name())
		}
	}
}

package installmeta

import (
	"os"
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

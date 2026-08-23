package main

import (
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

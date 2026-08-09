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
	want := map[string]bool{"index.html": true, "assets": true}
	for _, e := range entries {
		if e.IsDir() {
			delete(want, e.Name())
		} else {
			delete(want, e.Name())
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing ui files: %v", want)
	}
}

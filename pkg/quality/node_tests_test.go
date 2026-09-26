package quality

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSCommentsOnly(t *testing.T) {
	for _, s := range []string{"", " \n", "// no tests", "/* header */\n// empty", "\ufeff#!/usr/bin/env node\n/* empty */"} {
		if !jsCommentsOnly(s) {
			t.Errorf("not detected: %q", s)
		}
	}
	for _, s := range []string{"/* license */ require('node:assert').ok(true);", "'// not a comment'", "`/* template */`", "/x/.test('x')"} {
		if jsCommentsOnly(s) {
			t.Errorf("executable source rejected: %q", s)
		}
	}
}

func TestEmptyNodeFileCombinedGateAndSymlinkBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "web", "empty.test.js")
	if err := os.WriteFile(file, []byte("// no assertions"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := "go test ./... && node --test web/*.test.js"
	if got := emptyNodeTestFile(root, cmd); got != "web/empty.test.js" {
		t.Fatalf("combined gate missed empty file: %q", got)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := emptyNodeTestFile(root, cmd); got != "" {
		t.Fatalf("inspected outside project: %s", got)
	}
}

package authstore

import (
	"path/filepath"
	"testing"
)

func TestSetGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	slm := filepath.Join(dir, ".slmcode")
	if err := Set(slm, "openai", "sk-test-123"); err != nil {
		t.Fatal(err)
	}
	key, ok := Get(slm, "openai")
	if !ok || key != "sk-test-123" {
		t.Fatalf("got %q ok=%v", key, ok)
	}
	pub := PublicKeys(slm)
	if !pub["openai"] {
		t.Fatalf("public keys: %v", pub)
	}
}

func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, ok := Get(filepath.Join(dir, ".slmcode"), "openai")
	if ok {
		t.Fatal("expected miss")
	}
}

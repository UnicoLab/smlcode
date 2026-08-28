package authstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── Credentials on disk ──────────────────────────────────────────────────
//
// auth.json holds provider API keys. The permissions on it and on the directory
// above it are the whole protection on a shared machine, and nothing pinned
// them — Save had no test at all.
//
// The transient temp file matters as much as the final one: atomicfile writes
// to a temp in the same directory and renames, so a temp created world-readable
// would expose the key for the length of one write however tight the final mode
// is. os.CreateTemp opens at 0600 and the Chmod that follows only ever widens
// from there, which is the safe order — this pins it.

func TestSavedCredentialsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	slm := filepath.Join(t.TempDir(), ".slmcode")

	if err := Set(slm, "openai", "sk-test-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	fi, err := os.Stat(Path(slm))
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("auth.json mode = %04o, want 0600 — the key is readable by other accounts", got)
	}

	di, err := os.Stat(slm)
	if err != nil {
		t.Fatalf("stat .slmcode: %v", err)
	}
	if got := di.Mode().Perm(); got&0o007 != 0 {
		t.Errorf(".slmcode mode = %04o, want no world bits — the directory holds auth.json", got)
	}
}

// Save is the exported entry point and had no coverage; a round trip proves it
// both writes and is readable back.
func TestSaveRoundTrips(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := Save(slm, &Store{Keys: map[string]string{"openai": "sk-a", "groq": "gsk-b"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for provider, want := range map[string]string{"openai": "sk-a", "groq": "gsk-b"} {
		if got, ok := Get(slm, provider); !ok || got != want {
			t.Errorf("Get(%q) = %q/%v, want %q", provider, got, ok, want)
		}
	}
	if _, ok := Get(slm, "anthropic"); ok {
		t.Error("Get invented a key that was never stored")
	}
}

// A nil store must produce a valid empty file rather than a panic or a file
// containing "null", which Load would then have to special-case.
func TestSavingNothingProducesAValidEmptyStore(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := Save(slm, nil); err != nil {
		t.Fatalf("Save(nil): %v", err)
	}
	b, err := os.ReadFile(Path(slm))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("Save(nil) wrote a null store: %s", b)
	}
	s, err := Load(slm)
	if err != nil || s == nil || s.Keys == nil {
		t.Errorf("Load after Save(nil) = %+v, %v", s, err)
	}
}

// A key must never end up in an error message or anywhere else that gets
// logged — the store is the only place it belongs.
func TestErrorsNeverCarryTheKey(t *testing.T) {
	// An unwritable directory is the realistic failure.
	base := t.TempDir()
	blocked := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Set(filepath.Join(blocked, ".slmcode"), "openai", "sk-super-secret-value")
	if err == nil {
		t.Skip("the platform allowed the write; nothing to assert")
	}
	if strings.Contains(err.Error(), "sk-super-secret-value") {
		t.Errorf("the API key leaked into an error message: %v", err)
	}
}

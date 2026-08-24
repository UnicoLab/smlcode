package autoresearch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureAndRestoreIsByteForByte(t *testing.T) {
	root := newTestProject(t)
	path := filepath.Join(root, ".slmcode", "agents", "worker.yaml")
	original := readFile(t, path)

	snap, err := Capture([]string{path})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	writeFile(t, path, "id: worker\ntemperature: 0.99\n")
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("restore was not byte-for-byte:\n%s", got)
	}
	// Restoring twice must be safe: the revert runs from a defer, which can
	// fire on a path that already restored.
	if err := snap.Restore(); err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Fatal("the second restore corrupted the file")
	}
}

func TestRestoreRemovesAFileThatDidNotExist(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".slmcode", "config.yaml")

	snap, err := Capture([]string{path})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Files[0].Existed {
		t.Fatal("Capture claimed a missing file existed")
	}
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, "think_passes: 3\n")
	if err := snap.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		// Writing an empty file would leave a config.yaml the user never had,
		// which the next run would then load.
		t.Fatal("restoring a never-existed file left one behind")
	}
}

func TestCaptureRefusesAnOversizedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.yaml")
	writeFile(t, path, strings.Repeat("x", MaxSnapshotFileBytes+1))
	if _, err := Capture([]string{path}); err == nil {
		t.Fatal("Capture accepted a file past the snapshot cap")
	}
}

func TestPersistedSnapshotSurvivesTheProcess(t *testing.T) {
	root := newTestProject(t)
	agentPath := filepath.Join(root, ".slmcode", "agents", "worker.yaml")
	configPath := filepath.Join(root, ".slmcode", "config.yaml")
	originals := map[string]string{
		agentPath:  readFile(t, agentPath),
		configPath: readFile(t, configPath),
	}

	snap, err := Capture([]string{agentPath, configPath})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := snap.Persist(SnapshotDir(root)); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Simulate the SIGKILL case: files mutated, no in-process snapshot left.
	writeFile(t, agentPath, "id: worker\ntemperature: 0.99\n")
	writeFile(t, configPath, "provider: elsewhere\n")

	loaded, err := LoadSnapshot(SnapshotDir(root))
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if err := loaded.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for path, want := range originals {
		if got := readFile(t, path); got != want {
			t.Errorf("%s was not restored:\n%s", path, got)
		}
	}
	if got := len(loaded.Paths()); got != 2 {
		t.Errorf("Paths() = %d, want 2", got)
	}
}

// TestLoadSnapshotMovesACorruptManifestAside matches the rest of the harness: a
// file that will not parse is preserved as <name>.corrupt and reported, never
// silently deleted and never fatal to the process.
func TestLoadSnapshotMovesACorruptManifestAside(t *testing.T) {
	root := newTestProject(t)
	dir := SnapshotDir(root)
	snap, err := Capture([]string{filepath.Join(root, ".slmcode", "config.yaml")})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := snap.Persist(dir); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	manifest := filepath.Join(dir, "manifest.json")
	writeFile(t, manifest, "{ this is not json")

	if _, err := LoadSnapshot(dir); err == nil {
		t.Fatal("LoadSnapshot accepted a corrupt manifest")
	}
	if _, err := os.Stat(manifest + ".corrupt"); err != nil {
		t.Fatalf("the corrupt manifest was not preserved: %v", err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Error("the corrupt manifest was left in place")
	}
}

func TestLoadSnapshotRejectsAManifestWhoseCopiesAreGone(t *testing.T) {
	root := newTestProject(t)
	dir := SnapshotDir(root)
	path := filepath.Join(root, ".slmcode", "config.yaml")
	snap, err := Capture([]string{path})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := snap.Persist(dir); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	// Delete the stored copy but leave the manifest: a half-deleted snapshot
	// must fail loudly rather than restore an empty file over live content.
	if err := os.Remove(snap.Files[0].Stored); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := LoadSnapshot(dir); err == nil {
		t.Fatal("LoadSnapshot accepted a manifest with no stored copies")
	}
}

func TestPersistReplacesAnOlderSnapshot(t *testing.T) {
	root := newTestProject(t)
	dir := SnapshotDir(root)
	path := filepath.Join(root, ".slmcode", "config.yaml")

	first, err := Capture([]string{path})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := first.Persist(dir); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	writeFile(t, path, "provider: second\n")
	second, err := Capture([]string{path})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := second.Persist(dir); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	writeFile(t, path, "provider: third\n")
	loaded, err := LoadSnapshot(dir)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if err := loaded.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, path); !strings.Contains(got, "second") {
		t.Fatalf("restored the wrong generation: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 { // manifest + one stored copy
		t.Errorf("snapshot dir holds %d entries — stale copies accumulated", len(entries))
	}
}

func TestHashFilesDistinguishesMissingFromEmpty(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "gone.yaml")
	empty := filepath.Join(root, "empty.yaml")
	writeFile(t, empty, "")

	got, err := HashFiles([]string{missing, empty})
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}
	if got[missing] != "" {
		t.Errorf("a missing file hashed to %q", got[missing])
	}
	if got[empty] == "" {
		t.Error("an empty file hashed like a missing one")
	}
}

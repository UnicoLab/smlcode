package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content=%q", got)
	}
}

func TestWriteOncePreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers.json")
	if err := WriteOnce(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteOnce(path, []byte("second"), 0o644)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected exists error, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("content=%q", got)
	}
}

func TestWriteWithBackupPreservesPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteWithBackup(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteWithBackup(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("content=%q", got)
	}
	backup, err := os.ReadFile(BackupPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "first" {
		t.Fatalf("backup=%q", backup)
	}
}

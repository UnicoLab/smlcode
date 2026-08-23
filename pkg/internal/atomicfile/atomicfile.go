package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces path with data using a same-directory temp file and rename.
func Write(path string, data []byte, perm fs.FileMode) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := writeTemp(dir, filepath.Base(path), data, perm)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	keep = true
	syncDir(dir)
	return nil
}

// WriteWithBackup saves the previous file contents to path+".bak" before
// atomically replacing path. Missing previous files are allowed.
func WriteWithBackup(path string, data []byte, perm fs.FileMode) error {
	if path == "" {
		return nil
	}
	if prev, err := os.ReadFile(path); err == nil { //nolint:gosec // path is the caller's own target file, not external input
		if err := Write(BackupPath(path), prev, perm); err != nil {
			return err
		}
	}
	return Write(path, data, perm)
}

// WriteOnce writes path atomically only if path does not already exist.
func WriteOnce(path string, data []byte, perm fs.FileMode) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := writeTemp(dir, filepath.Base(path), data, perm)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmp)
	}()
	if err := os.Link(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// BackupPath returns the conventional last-known-good backup path.
func BackupPath(path string) string {
	if path == "" {
		return ""
	}
	return path + ".bak"
}

func writeTemp(dir, base string, data []byte, perm fs.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	closeErr := error(nil)
	defer func() {
		if closeErr != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		closeErr = errors.Join(err, f.Close())
		return "", closeErr
	}
	if err := f.Chmod(perm); err != nil {
		closeErr = errors.Join(err, f.Close())
		return "", closeErr
	}
	if err := f.Sync(); err != nil {
		closeErr = errors.Join(err, f.Close())
		return "", closeErr
	}
	if err := f.Close(); err != nil {
		closeErr = err
		return "", err
	}
	return path, nil
}

func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is derived from an already-resolved caller path, not external input
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

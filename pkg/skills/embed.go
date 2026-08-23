package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

//go:embed all:bundled
var bundled embed.FS

// MaterializeBundled syncs embedded default skills into dest (always overwrite).
// Project skills live beside this directory and win on name via Loader order.
func MaterializeBundled(dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil { // project skills dir, owner-only
		return err
	}
	return fs.WalkDir(bundled, "bundled", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel("bundled", path)
		target := filepath.Join(dest, rel)
		data, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil { // project skills dir, owner-only
			return err
		}
		return atomicfile.Write(target, data, 0o644)
	})
}

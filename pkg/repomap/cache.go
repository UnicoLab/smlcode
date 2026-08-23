package repomap

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// CacheFile is the on-disk name under CacheDir.
const CacheFile = "repomap.json"

// CacheSchemaVersion invalidates every entry when the extractors change shape.
const CacheSchemaVersion = 1

type diskCache struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Files   []File `json:"files"`
}

func cachePath(root string, opts Options) string {
	dir := opts.CacheDir
	if dir == "" {
		dir = filepath.Join(root, ".slmcode")
	}
	return filepath.Join(dir, CacheFile)
}

// loadCache returns path -> File keyed on mtime+size validity checked by the caller.
func loadCache(path string) map[string]File {
	out := map[string]File{}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return out
	}
	var dc diskCache
	if err := json.Unmarshal(data, &dc); err != nil || dc.Version != CacheSchemaVersion {
		return out
	}
	for _, f := range dc.Files {
		if f.Path == "" || f.ModTime == 0 {
			continue
		}
		f.Language = f.Lang
		out[f.Path] = f
	}
	return out
}

func saveCache(path string, files []File) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		// Only create the cache dir when it is a .slmcode-style workspace dir.
		if err := os.MkdirAll(dir, 0o750); err != nil { // .slmcode repo-map cache, owner-only
			return
		}
	}
	data, err := json.Marshal(diskCache{Version: CacheSchemaVersion, Files: files})
	if err != nil {
		return
	}
	_ = atomicfile.Write(path, data, 0o644)
}

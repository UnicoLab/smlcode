package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// EmbeddingCacheFile is the on-disk cache under the workspace dir.
const EmbeddingCacheFile = "embeddings.json"

// MaxCachedEmbeddings bounds the cache; oldest-inserted entries are evicted.
const MaxCachedEmbeddings = 4000

// CacheSchemaVersion invalidates every entry when the record shape changes.
const CacheSchemaVersion = 1

type cacheRecord struct {
	Vec []float64 `json:"v"`
	Seq int64     `json:"s"` // insertion order, for eviction
}

type diskCache struct {
	Version int                    `json:"version"`
	Entries map[string]cacheRecord `json:"entries"`
	Seq     int64                  `json:"seq"`
}

// CachedEmbedder wraps an Embedder with a disk-backed cache keyed by
// sha256(text) + embedder name.
//
// Search used to re-embed the ENTIRE corpus on every single query. With a
// local hashing embedder that is wasted CPU; with a remote endpoint it is a
// full round trip per chunk per query.
type CachedEmbedder struct {
	Inner Embedder
	Path  string // cache file; empty disables persistence

	mu     sync.Mutex
	loaded bool
	data   diskCache
	dirty  bool
}

// NewCachedEmbedder wraps inner with a cache stored under dir.
func NewCachedEmbedder(inner Embedder, dir string) *CachedEmbedder {
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "cache", EmbeddingCacheFile)
	}
	return &CachedEmbedder{Inner: inner, Path: path}
}

// Name reports the wrapped embedder's name (the cache is transparent).
func (c *CachedEmbedder) Name() string {
	if c == nil || c.Inner == nil {
		return "lexical"
	}
	return c.Inner.Name()
}

// CacheKey is the disk key for one text under one embedder.
func CacheKey(embedderName, text string) string {
	sum := sha256.Sum256([]byte(text))
	return embedderName + ":" + hex.EncodeToString(sum[:])
}

// Embed returns cached vectors where possible and embeds only the misses.
func (c *CachedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if c == nil || c.Inner == nil {
		return nil, errEmbeddingsUnavailable
	}
	// A lexical embedder builds a shared vocabulary across the batch, so its
	// vectors are only comparable within one call — never cache those.
	if c.Inner.Name() == "lexical" {
		return c.Inner.Embed(ctx, texts)
	}
	name := c.Inner.Name()
	out := make([][]float64, len(texts))
	var missIdx []int
	var missTexts []string

	c.mu.Lock()
	c.loadLocked()
	for i, t := range texts {
		if rec, ok := c.data.Entries[CacheKey(name, t)]; ok && len(rec.Vec) > 0 {
			out[i] = rec.Vec
			continue
		}
		missIdx = append(missIdx, i)
		missTexts = append(missTexts, t)
	}
	c.mu.Unlock()

	if len(missTexts) == 0 {
		return out, nil
	}
	fresh, err := c.Inner.Embed(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	if len(fresh) != len(missTexts) {
		return nil, errEmbeddingCount
	}
	c.mu.Lock()
	for j, idx := range missIdx {
		out[idx] = fresh[j]
		c.data.Seq++
		c.data.Entries[CacheKey(name, missTexts[j])] = cacheRecord{Vec: fresh[j], Seq: c.data.Seq}
		c.dirty = true
	}
	c.evictLocked()
	c.mu.Unlock()
	return out, nil
}

// Flush persists the cache. Safe to call repeatedly.
func (c *CachedEmbedder) Flush() error {
	if c == nil || c.Path == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o750); err != nil { // embedding cache dir, owner-only
		return err
	}
	c.data.Version = CacheSchemaVersion
	data, err := json.Marshal(c.data)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(c.Path, data, 0o644); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

func (c *CachedEmbedder) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.data = diskCache{Version: CacheSchemaVersion, Entries: map[string]cacheRecord{}}
	if c.Path == "" {
		return
	}
	raw, err := os.ReadFile(c.Path)
	if err != nil || len(raw) == 0 {
		return
	}
	var loaded diskCache
	if err := json.Unmarshal(raw, &loaded); err != nil || loaded.Version != CacheSchemaVersion {
		return
	}
	if loaded.Entries != nil {
		c.data = loaded
	}
}

func (c *CachedEmbedder) evictLocked() {
	if len(c.data.Entries) <= MaxCachedEmbeddings {
		return
	}
	type kv struct {
		key string
		seq int64
	}
	all := make([]kv, 0, len(c.data.Entries))
	for k, v := range c.data.Entries {
		all = append(all, kv{k, v.Seq})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	for _, e := range all[:len(all)-MaxCachedEmbeddings] {
		delete(c.data.Entries, e.key)
	}
}

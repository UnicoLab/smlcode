// Package authstore persists provider API keys in .slmcode/auth.json
// (prime-agent auth.json style) separate from config.yaml.
package authstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileName is the auth store basename under .slmcode/.
const FileName = "auth.json"

// Store maps provider → API key (never logged).
type Store struct {
	Keys map[string]string `json:"keys"`
}

var mu sync.Mutex

func normalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "google" {
		return "gemini"
	}
	return p
}

// Path returns .slmcode/auth.json for a project root or slm dir.
func Path(slmDir string) string {
	return filepath.Join(slmDir, FileName)
}

// Load reads auth.json (empty store on missing file).
func Load(slmDir string) (*Store, error) {
	mu.Lock()
	defer mu.Unlock()
	p := Path(slmDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Keys: map[string]string{}}, nil
		}
		return nil, err
	}
	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Keys == nil {
		s.Keys = map[string]string{}
	}
	return &s, nil
}

// Save writes auth.json with 0600 permissions.
func Save(slmDir string, s *Store) error {
	mu.Lock()
	defer mu.Unlock()
	if s == nil {
		s = &Store{Keys: map[string]string{}}
	}
	if s.Keys == nil {
		s.Keys = map[string]string{}
	}
	if err := os.MkdirAll(slmDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(slmDir), b, 0o600)
}

// Get returns a key for provider (normalized).
func Get(slmDir, provider string) (string, bool) {
	s, err := Load(slmDir)
	if err != nil || s == nil {
		return "", false
	}
	p := normalizeProvider(provider)
	if v := strings.TrimSpace(s.Keys[p]); v != "" {
		return v, true
	}
	for k, v := range s.Keys {
		if normalizeProvider(k) == p && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// Set stores a key for provider.
func Set(slmDir, provider, key string) error {
	s, err := Load(slmDir)
	if err != nil {
		return err
	}
	p := normalizeProvider(provider)
	if p == "" {
		p = strings.TrimSpace(provider)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		delete(s.Keys, p)
	} else {
		s.Keys[p] = key
	}
	return Save(slmDir, s)
}

// PublicKeys returns provider names that have keys (values redacted).
func PublicKeys(slmDir string) map[string]bool {
	s, err := Load(slmDir)
	if err != nil || s == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for k, v := range s.Keys {
		if strings.TrimSpace(v) != "" {
			out[normalizeProvider(k)] = true
		}
	}
	return out
}

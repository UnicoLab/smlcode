// Package updatecheck detects when a newer SLMCode release exists on GitHub.
//
// Checks are silent failures: callers only surface notices when an update is
// actually available, so a slow or failing network never blocks startup. The
// latest-tag result is cached on disk with a 6h TTL to keep repeated checks
// (version, update --check, TUI banner, Studio API) cheap.
package updatecheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIURL = "https://api.github.com/repos/UnicoLab/smlcode/releases/latest"
	// cacheTTL bounds how long a cached latest-tag result is reused without a
	// fresh network request.
	cacheTTL = 6 * time.Hour
	// httpTimeout bounds the GitHub API request.
	httpTimeout = 6 * time.Second
)

// Info is the result of a version check. Error is set on any failure so the
// caller can decide whether to surface a notice (typically only when
// UpdateAvailable is true).
type Info struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	CheckedAt       string `json:"checked_at"`
	Error           string `json:"error,omitempty"`
}

// cacheEntry is the on-disk cache shape (latest-tag result only, version-
// independent so it is reusable across installed versions).
type cacheEntry struct {
	Latest     string `json:"latest"`
	ReleaseURL string `json:"release_url"`
	CheckedAt  string `json:"checked_at"`
}

// Check reports whether a newer release exists for the given installed
// version. Honors SLMCODE_SKIP_UPDATE_CHECK=1 and skips dev builds.
func Check(current string) Info {
	return CheckWithURL(current, defaultAPIURL, defaultCachePath())
}

// CheckWithURL is Check with an injectable API URL and cache path so tests can
// use httptest servers and temp dirs.
func CheckWithURL(current, apiURL, cachePath string) Info {
	if skipUpdateCheck() || current == "" || current == "dev" || current == "unknown" {
		return Info{Current: current}
	}

	now := time.Now().UTC()
	if latest, releaseURL, checkedAt, ok := readCache(cachePath); ok {
		return Info{
			Current:         current,
			Latest:          latest,
			UpdateAvailable: CompareVersions(latest, current) > 0,
			ReleaseURL:      releaseURL,
			CheckedAt:       checkedAt,
		}
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(apiURL)
	if err != nil {
		return Info{Current: current, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{Current: current, Error: fmt.Sprintf("unexpected status %d from release API", resp.StatusCode)}
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Info{Current: current, Error: err.Error()}
	}

	latest := strings.TrimSpace(rel.TagName)
	if latest == "" {
		return Info{Current: current, Error: "release API returned empty tag_name"}
	}
	// Normalize the tag to a plain version so callers can safely render
	// "v"+latest without doubling the prefix.
	latest = strings.TrimPrefix(strings.TrimPrefix(latest, "v"), "V")

	writeCache(cachePath, latest, rel.HTMLURL)
	return Info{
		Current:         current,
		Latest:          latest,
		UpdateAvailable: CompareVersions(latest, current) > 0,
		ReleaseURL:      rel.HTMLURL,
		CheckedAt:       now.Format(time.RFC3339),
	}
}

// CompareVersions compares two version strings, returning -1/0/1. Leading
// "v"/"V" is stripped, parts are split on "." and compared numerically with
// missing parts treated as 0. A part with a non-numeric trailing suffix (e.g.
// "0.13.0-rc1") compares as less than the plain part ("0.13.0").
func CompareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.TrimPrefix(a, "v"), "V")
	b = strings.TrimPrefix(strings.TrimPrefix(b, "v"), "V")
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		na, sa := parsePart(partAt(pa, i))
		nb, sb := parsePart(partAt(pb, i))
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
		// Same numeric value: a plain part beats a prerelease-style suffix.
		switch {
		case sa == "" && sb != "":
			return 1
		case sa != "" && sb == "":
			return -1
		case sa != "" && sb != "" && sa != sb:
			if sa < sb {
				return -1
			}
			return 1
		}
	}
	return 0
}

func partAt(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

// parsePart returns the numeric prefix of a version part plus any remaining
// non-numeric suffix ("0-rc1" → 0, "-rc1"). A fully non-numeric part parses as
// 0 with a suffix, sorting it below the plain "0".
func parsePart(p string) (int, string) {
	i := 0
	for i < len(p) && p[i] >= '0' && p[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(p[:i])
	return n, p[i:]
}

// skipUpdateCheck is true when the user opted out via SLMCODE_SKIP_UPDATE_CHECK.
func skipUpdateCheck() bool {
	return os.Getenv("SLMCODE_SKIP_UPDATE_CHECK") != ""
}

// defaultCachePath returns the on-disk cache location, falling back to
// ~/.cache/slmcode/update.json when the user cache dir cannot be created.
// Returns "" when no cache location can be determined (caching disabled).
func defaultCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		path := filepath.Join(dir, "slmcode", "update.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			return path
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "slmcode", "update.json")
	}
	return ""
}

// readCache returns the cached latest tag, release URL and check time when a
// fresh (within TTL) entry exists.
func readCache(path string) (latest, releaseURL, checkedAt string, ok bool) {
	if path == "" {
		return "", "", "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return "", "", "", false
	}
	if e.Latest == "" || e.CheckedAt == "" {
		return "", "", "", false
	}
	t, err := time.Parse(time.RFC3339, e.CheckedAt)
	if err != nil {
		return "", "", "", false
	}
	if time.Since(t) >= cacheTTL {
		return "", "", "", false
	}
	// Normalize legacy cached tags that may include the "v" prefix.
	latest = strings.TrimPrefix(strings.TrimPrefix(e.Latest, "v"), "V")
	return latest, e.ReleaseURL, e.CheckedAt, true
}

// writeCache persists a successful latest-tag fetch.
func writeCache(path, latest, releaseURL string) {
	if path == "" || latest == "" {
		return
	}
	e := cacheEntry{
		Latest:     latest,
		ReleaseURL: releaseURL,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

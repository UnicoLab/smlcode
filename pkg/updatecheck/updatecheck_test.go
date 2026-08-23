package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.12.2", "0.13.0", -1},
		{"v0.13.0", "0.13.0", 0},
		{"0.13.0", "0.13.1", -1},
		{"0.13.0", "0.12.9", 1},
		{"0.9", "0.10.0", -1},
		{"0.13.0-rc1", "0.13.0", -1},
		{"0.13.0", "0.13.0-rc1", 1},
		{"0.13.0", "0.13", 0},            // missing part treated as 0
		{"0.13.1", "0.13", 1},            // missing part treated as 0
		{"", "", 0},                      // empty vs empty
		{"", "0.1.0", -1},                // empty vs version
		{"0.1.0", "", 1},                 // version vs empty
		{"V0.13.0", "v0.13.0", 0},        // case-insensitive v prefix
		{"0.13.0-rc1", "0.13.0-rc2", -1}, // suffix tiebreak
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// testReleaseServer returns an httptest server serving a fixed release payload.
// It is not auto-closed so tests can control closing (e.g. to prove cache reuse).
func testReleaseServer(tag, htmlURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": tag,
			"html_url": htmlURL,
		})
	}))
}

func TestCheckWithURLNewer(t *testing.T) {
	srv := testReleaseServer("v0.13.0", "https://github.com/UnicoLab/smlcode/releases/tag/v0.13.0")
	defer srv.Close()

	info := CheckWithURL("0.12.2", srv.URL, filepath.Join(t.TempDir(), "update.json"))
	if !info.UpdateAvailable {
		t.Fatalf("expected update available, got %+v", info)
	}
	if info.Latest != "0.13.0" {
		t.Errorf("Latest = %q, want 0.13.0", info.Latest)
	}
	if info.ReleaseURL == "" {
		t.Error("ReleaseURL not set")
	}
	if info.Error != "" {
		t.Errorf("unexpected error: %s", info.Error)
	}
	if info.CheckedAt == "" {
		t.Error("CheckedAt not set")
	}
}

func TestCheckWithURLEqual(t *testing.T) {
	srv := testReleaseServer("v0.12.2", "https://github.com/UnicoLab/smlcode/releases/tag/v0.12.2")
	defer srv.Close()

	info := CheckWithURL("0.12.2", srv.URL, filepath.Join(t.TempDir(), "update.json"))
	if info.UpdateAvailable {
		t.Fatalf("expected no update, got %+v", info)
	}
	if info.Latest != "0.12.2" {
		t.Errorf("Latest = %q, want 0.12.2", info.Latest)
	}
	if info.Error != "" {
		t.Errorf("unexpected error: %s", info.Error)
	}
}

func TestCheckWithURLCacheReuse(t *testing.T) {
	srv := testReleaseServer("v0.13.0", "https://github.com/UnicoLab/smlcode/releases/tag/v0.13.0")
	cachePath := filepath.Join(t.TempDir(), "update.json")

	first := CheckWithURL("0.12.2", srv.URL, cachePath)
	if !first.UpdateAvailable {
		t.Fatalf("first check: expected update, got %+v", first)
	}

	// Server closed: the second check must reuse the cached result, not the network.
	srv.Close()
	second := CheckWithURL("0.12.2", srv.URL, cachePath)
	if second.Error != "" {
		t.Fatalf("cached check returned error: %s", second.Error)
	}
	if !second.UpdateAvailable || second.Latest != "0.13.0" {
		t.Errorf("cached check = %+v, want latest 0.13.0 with update", second)
	}
	if second.ReleaseURL == "" {
		t.Error("cached check: ReleaseURL not preserved")
	}
}

func TestCheckWithURLSkipEnv(t *testing.T) {
	t.Setenv("SLMCODE_SKIP_UPDATE_CHECK", "1")
	// URL points at nothing: if the skip env is honored no request is made.
	info := CheckWithURL("0.12.2", "http://127.0.0.1:1", filepath.Join(t.TempDir(), "update.json"))
	if info.Latest != "" {
		t.Errorf("expected empty Latest with skip env, got %q", info.Latest)
	}
	if info.Error != "" {
		t.Errorf("expected no error with skip env, got %q", info.Error)
	}
	if info.UpdateAvailable {
		t.Error("expected no update with skip env")
	}
}

func TestCheckWithURLSkipsUnreleasableBuilds(t *testing.T) {
	for _, current := range []string{"", "dev", "unknown"} {
		// URL points at nothing: short-circuit must prevent any request.
		info := CheckWithURL(current, "http://127.0.0.1:1", filepath.Join(t.TempDir(), "update.json"))
		if info.Error != "" || info.Latest != "" {
			t.Errorf("CheckWithURL(%q) should short-circuit, got %+v", current, info)
		}
	}
}

func TestCheckWithURLServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	info := CheckWithURL("0.12.2", srv.URL, filepath.Join(t.TempDir(), "update.json"))
	if info.Error == "" {
		t.Fatal("expected error for non-200 response")
	}
	if info.Latest != "" || info.UpdateAvailable {
		t.Errorf("expected no latest/update on error, got %+v", info)
	}
	if info.Current != "0.12.2" {
		t.Errorf("Current = %q, want 0.12.2", info.Current)
	}
}

// TestNegativeCacheShortCircuits pins the behavior failTTL exists for: after a
// failed lookup, the NEXT call must not dial at all. Before the negative cache,
// every `slmcode version` on a machine behind a firewall paid the full
// httpTimeout, once per invocation. Counting requests is the only way to tell a
// short-circuit from a second failure that merely looks the same.
func TestNegativeCacheShortCircuits(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	cachePath := filepath.Join(t.TempDir(), "update.json")

	first := CheckWithURL("0.17.0", srv.URL, cachePath)
	if first.Error == "" {
		t.Fatal("first check: expected an error")
	}
	second := CheckWithURL("0.17.0", srv.URL, cachePath)
	if second.Error == "" {
		t.Fatal("second check: expected the cached failure to be reported")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server received %d requests, want 1 — the failure was not negative-cached", got)
	}
	if second.UpdateAvailable || second.Latest != "" {
		t.Errorf("cached failure must not claim an update: %+v", second)
	}
}

// TestNegativeCachePreservesKnownLatest: a failure must not throw away a
// previously fetched tag. A stale-but-real answer beats no answer.
func TestNegativeCachePreservesKnownLatest(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update.json")
	ok := testReleaseServer("v0.18.0", "https://github.com/UnicoLab/smlcode/releases/tag/v0.18.0")
	if info := CheckWithURL("0.17.0", ok.URL, cachePath); !info.UpdateAvailable {
		t.Fatalf("seed check: expected update, got %+v", info)
	}
	ok.Close()

	// A failure now lands on top of the successful entry.
	writeFailureCache(cachePath, "simulated outage")

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "0.18.0") {
		t.Errorf("failure cache dropped the known latest tag: %s", data)
	}
}

// TestCheckAgainstShippedVersion walks the comparisons this release actually
// makes: 0.17.0 installed, various tags upstream.
func TestCheckAgainstShippedVersion(t *testing.T) {
	for _, tc := range []struct {
		tag  string
		want bool
	}{
		{"v0.17.1", true},
		{"v0.18.0", true},
		{"v1.0.0", true},
		{"v0.17.0", false},
		{"v0.16.9", false},
		{"v0.17.0-rc1", false}, // a prerelease of the installed version is not newer
	} {
		srv := testReleaseServer(tc.tag, "https://example.invalid/"+tc.tag)
		info := CheckWithURL("0.17.0", srv.URL, filepath.Join(t.TempDir(), "update.json"))
		srv.Close()
		if info.Error != "" {
			t.Fatalf("%s: unexpected error %s", tc.tag, info.Error)
		}
		if info.UpdateAvailable != tc.want {
			t.Errorf("installed 0.17.0 vs latest %s: UpdateAvailable = %v, want %v",
				tc.tag, info.UpdateAvailable, tc.want)
		}
		if strings.HasPrefix(info.Latest, "v") {
			t.Errorf("%s: Latest kept its v prefix (%q) — callers render \"v\"+Latest", tc.tag, info.Latest)
		}
	}
}

// TestCacheFilePermissions: the cache lives in the user's cache dir and is
// written with atomicfile at 0600. It is not a secret, but it IS an input to
// "should I tell the user to update", and world-writable inputs to prompts are
// how a user gets talked into running something.
func TestCacheFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	cachePath := filepath.Join(t.TempDir(), "update.json")
	srv := testReleaseServer("v0.18.0", "https://example.invalid/v0.18.0")
	defer srv.Close()
	CheckWithURL("0.17.0", srv.URL, cachePath)

	st, err := os.Stat(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file mode = %04o, want 0600", perm)
	}
}

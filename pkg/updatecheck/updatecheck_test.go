package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

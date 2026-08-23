package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// unbuiltUI is what `//go:embed all:ui` yields on a fresh clone: the tracked
// .gitkeep and nothing else.
func unbuiltUI() fstest.MapFS {
	return fstest.MapFS{".gitkeep": &fstest.MapFile{}}
}

func TestUIIsBuilt(t *testing.T) {
	for _, tc := range []struct {
		name string
		ui   fs.FS
		want bool
	}{
		{"nil FS", nil, false},
		{"fresh clone (.gitkeep only)", unbuiltUI(), false},
		{"empty FS", fstest.MapFS{}, false},
		{"built SPA", studioUI(), true},
	} {
		if got := UIIsBuilt(tc.ui); got != tc.want {
			t.Errorf("UIIsBuilt(%s)=%v want %v", tc.name, got, tc.want)
		}
	}
}

// With no SPA embedded, `GET /` must still answer a navigation with a page
// that says what is missing. It used to 404 at the root unless a placeholder
// index.html was checked into cmd/slmcode/ui/ — a tracked file that
// `make ui-react` then overwrote with build output.
func TestPlaceholderServedWhenUINotBuilt(t *testing.T) {
	s := New(newHarness(t), unbuiltUI())
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status=%d, want 200 placeholder", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range []string{"Studio UI has not been built", "make bootstrap"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("placeholder missing %q: %s", marker, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("placeholder content-type=%q", ct)
	}
	// Never cached: the very next build replaces it with the real shell.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("placeholder Cache-Control=%q", cc)
	}
	// A non-document path must not receive an HTML body.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/assets/index-abc.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("asset path status=%d, want 404", rec.Code)
	}
	// /api/* still routes normally — the CLI and API are unaffected by a
	// missing SPA, which is exactly what the page tells the reader.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/api/health status=%d with an unbuilt UI", rec.Code)
	}
}

// The placeholder is wired INSIDE the token gate, exactly like the real shell.
// Serving it ahead of the gate would put an unauthenticated page on a port that
// otherwise refuses everything; hiding it behind a redirect the SPA never makes
// would leave a blank screen.
func TestPlaceholderIsBehindTheTokenGate(t *testing.T) {
	s := NewWithOptions(newHarness(t), unbuiltUI(), Options{GenerateToken: true})
	tok := s.Token()
	if tok == "" {
		t.Fatal("no token generated")
	}

	// Unauthenticated navigation: the token gate, not the placeholder.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET / status=%d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Session token required") {
		t.Fatalf("expected the token gate, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Studio UI has not been built") {
		t.Fatal("placeholder served without a token")
	}

	// Authenticated navigation (the CLI's `?t=` URL): the placeholder, and a
	// session cookie so the browser stops carrying the token in the URL.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/?t="+tok, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap GET /?t= status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Studio UI has not been built") {
		t.Fatalf("placeholder not served after bootstrap: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), tok) {
		t.Fatal("placeholder page leaks the session token")
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("no session cookie minted on the placeholder navigation")
	}
}

// The page has to render with no network and no assets: it is served precisely
// when nothing else can be.
func TestPlaceholderIsSelfContainedAndThemeNeutral(t *testing.T) {
	for _, forbidden := range []string{"<script", "http://", "https://", "<img", "<link"} {
		if strings.Contains(studioPlaceholderPage, forbidden) {
			t.Errorf("placeholder page contains %q — it must reference nothing external", forbidden)
		}
	}
	// Readable on a light desktop and a dark one.
	for _, want := range []string{"color-scheme:light dark", "prefers-color-scheme:dark"} {
		if !strings.Contains(studioPlaceholderPage, want) {
			t.Errorf("placeholder page missing %q — it must be theme-neutral", want)
		}
	}
}

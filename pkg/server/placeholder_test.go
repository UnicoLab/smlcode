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

// A client-side ROUTE has no file behind it, and the file server 404s on one.
// Every page except `/` therefore died on a browser refresh, on a bookmark, and
// on a link shared with a colleague — `/board`, `/teams`, `/settings` all
// answered the bare 404 page while in-app navigation to the identical URL
// worked, which is as confusing as a bug gets.
func TestClientRoutesServeTheShellSoARefreshWorks(t *testing.T) {
	s := New(newHarness(t), studioUI())

	for _, route := range []string{"/board", "/teams", "/settings", "/docs/PLAN.md", "/runs/abc/trace"} {
		rec := httptest.NewRecorder()
		// What a browser actually sends when the address bar is used. The
		// header is the whole reason `/docs/PLAN.md` can be a route rather than
		// a missing file, and asserting without it would test a rule no browser
		// exercises.
		req := newAPIRequest(http.MethodGet, route, nil)
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want the SPA shell — a refresh must not 404", route, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "SPA") {
			t.Errorf("GET %s served %q, not the shell", route, rec.Body.String())
		}
	}
}

// The check that keeps the fallback honest. Answering a request for a hashed
// asset with HTML makes the browser report a syntax error in a script that was
// never there — a failure whose message points nowhere near its cause.
func TestAMissingAssetStill404sRatherThanServingTheShell(t *testing.T) {
	s := New(newHarness(t), studioUI())

	for _, missing := range []string{"/assets/index-deadbeef.js", "/nope.css", "/fonts/gone.woff2"} {
		rec := httptest.NewRecorder()
		// How a browser fetches a subresource: never `navigate`, and never
		// asking for HTML.
		req := newAPIRequest(http.MethodGet, missing, nil)
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Accept", "*/*")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 — an asset that does not exist is not a route", missing, rec.Code)
		}
	}

	// And a real asset is still served as itself.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("GET /app.js = %d %q", rec.Code, rec.Body.String())
	}
}

// The fallback must not become a hole in the API surface: an unknown /api path
// is a client bug and has to say so, not return HTML a fetch() will choke on.
func TestUnknownAPIPathsStill404(t *testing.T) {
	s := New(newHarness(t), studioUI())
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/not-a-thing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/not-a-thing = %d, want 404", rec.Code)
	}
}

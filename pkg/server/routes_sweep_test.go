package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/harness"
)

// ── Every GET route, on a server that has never run anything ─────────────
//
// The Studio is opened before the first run far more often than after one:
// that IS the first-run path. Every read handler therefore meets a workspace
// with no board, no result, no squad plan, no calibration and no session —
// and a handler that assumes any of those exist takes the whole Studio down
// with it, because a panic in one handler kills the process serving all of
// them.
//
// The route table is read from server.go rather than listed here on purpose. A
// hand-maintained list would drift the moment somebody adds a route, and the
// route it forgot would be exactly the untested one.

var getRouteRe = regexp.MustCompile(`HandleFunc\("GET (/api/[^"]*)"`)

// registeredGETRoutes reads the route table out of the source.
func registeredGETRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	var out []string
	for _, m := range getRouteRe.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	if len(out) < 30 {
		t.Fatalf("found only %d GET routes; the pattern has drifted from the source", len(out))
	}
	return out
}

// fillPathParams substitutes something plausible for {id}-style wildcards, so a
// wildcard route is exercised rather than skipped.
func fillPathParams(route string) string {
	r := strings.NewReplacer(
		"{id}", "does-not-exist",
		"{name}", "does-not-exist",
		"{kind}", "pipeline",
	)
	return r.Replace(route)
}

func TestEveryGETRouteSurvivesAFreshWorkspace(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	handler := s.Handler()

	for _, route := range registeredGETRoutes(t) {
		path := fillPathParams(route)
		t.Run(route, func(t *testing.T) {
			// The event stream never returns; it is the one route this cannot
			// drive with a recorder.
			if strings.HasSuffix(route, "/events") {
				t.Skip("SSE stream — covered by the stream tests")
			}
			rec := httptest.NewRecorder()
			// A panic here would take down the process serving every other
			// route, so failing loudly on one is the whole point.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("GET %s panicked on a fresh workspace: %v", path, r)
				}
			}()
			handler.ServeHTTP(rec, newAPIRequest(http.MethodGet, path, nil))

			// 404/400 are legitimate answers for a resource that does not
			// exist. A 500 is the handler admitting it did not expect this.
			if rec.Code >= 500 {
				t.Errorf("GET %s = %d on a fresh workspace: %s",
					path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

// A workspace can be left with malformed state — a run killed mid-write, a
// hand-edited board, a file restored from a bad backup. Absence is handled by
// the sweep above; malformed content is a different question, and a handler
// that parses without checking takes the Studio down for every other route.
func TestEveryGETRouteSurvivesACorruptWorkspace(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}

	// Truncated JSON, wrong-typed JSON, and YAML that is not a mapping: the
	// three shapes a half-written or hand-edited file actually takes.
	slm := h.Config.SlmDir()
	for name, body := range map[string]string{
		"board.json":    `{"tasks":[{"id":"T1","files":`,
		"squads.json":   `{"squads":"not-a-list"}`,
		"pipeline.yaml": "- this is a sequence, not a mapping\n",
		"CONTEXT.md":    "\x00\x01\x02 not text\n",
	} {
		if err := os.WriteFile(filepath.Join(slm, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := New(h, nil)
	handler := s.Handler()
	for _, route := range registeredGETRoutes(t) {
		path := fillPathParams(route)
		t.Run(route, func(t *testing.T) {
			if strings.HasSuffix(route, "/events") {
				t.Skip("SSE stream — covered by the stream tests")
			}
			rec := httptest.NewRecorder()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("GET %s panicked on a corrupt workspace: %v", path, r)
				}
			}()
			handler.ServeHTTP(rec, newAPIRequest(http.MethodGet, path, nil))
			// A 500 is acceptable here — the state really is broken and saying
			// so is honest. A panic is not: it takes every other route with it.
		})
	}
}

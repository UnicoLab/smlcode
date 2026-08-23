package server

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// UIIsBuilt reports whether the UI filesystem handed to New/NewWithOptions
// actually contains a built Studio SPA.
//
// The test is "is there an index.html", because that is exactly what the Vite
// build writes into cmd/slmcode/ui/ and what the SPA needs in order to boot.
// A fresh clone embeds only cmd/slmcode/ui/.gitkeep — the directory has to
// exist and be non-empty for `//go:embed all:ui` to compile, but there is no
// page in it. That state is normal, not an error: the CLI, the TUI and the
// whole Studio API work; only the React bundle is missing, and `make bootstrap`
// produces it.
//
// This used to be answered by grepping a checked-in placeholder index.html for
// a magic string, which meant the placeholder had to be a TRACKED file that
// `make ui-react` then overwrote — dirtying every builder's tree. The page now
// lives in Go source (studioPlaceholderPage below) and "not built" is simply
// "no index.html embedded".
func UIIsBuilt(ui fs.FS) bool {
	if ui == nil {
		return false
	}
	f, err := ui.Open("index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// studioPlaceholderPage is served for a navigation when no SPA is embedded.
//
// Self-contained on purpose: there are no assets to reference (that is the
// whole point of this state), and Studio must not reach out to a CDN. It is
// theme-neutral — the palette follows prefers-color-scheme in both directions,
// so it is readable on a light desktop and on a dark one. It carries no token
// and no state; like the SPA shell it sits behind the session-token gate.
const studioPlaceholderPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>SLMCode Studio — UI not built</title>
<style>
 :root{color-scheme:light dark;--bg:#ffffff;--fg:#16181d;--muted:#5c6370;
       --card:#f5f6f8;--line:#e2e5ea;--accent:#6d3bd4}
 @media (prefers-color-scheme:dark){
  :root{--bg:#0f1115;--fg:#e6e8ee;--muted:#aeb4c2;--card:#1b1f27;--line:#2a2f3a;--accent:#a78bfa}
 }
 body{font:15px/1.6 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
      margin:0;display:grid;place-items:center;min-height:100vh;
      background:var(--bg);color:var(--fg)}
 main{max-width:38rem;padding:2rem}
 h1{font-size:1.15rem;margin:0 0 .75rem}
 p{color:var(--muted)}
 pre{background:var(--card);border:1px solid var(--line);border-radius:6px;
     padding:.7rem .9rem;overflow-x:auto;color:var(--fg);
     font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
 code{background:var(--card);border-radius:4px;padding:.1rem .35rem;
      font:0.92em ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
 ul{color:var(--muted);padding-left:1.1rem}
 .hint{border-left:3px solid var(--accent);padding-left:.8rem}
</style></head><body><main>
<h1>The Studio UI has not been built</h1>
<p>This binary was built without the Studio web app. The server, the API and the
CLI are all running normally — only the React bundle that would render this page
is missing, so you are seeing this note instead.</p>
<p>Build it from the repository root:</p>
<pre>make bootstrap</pre>
<p class="hint">That installs <code>web/</code>'s npm dependencies and runs the Vite
build into <code>cmd/slmcode/ui/</code>, which is <code>go:embed</code>ed into the
binary. Then rebuild and restart: <code>make build &amp;&amp; ./bin/slmcode studio</code>.</p>
<ul>
<li>It needs Node.js 18+ on your PATH. Nothing else in SLMCode does.</li>
<li>Already have the dependencies installed? <code>make ui-react</code> just rebuilds.</li>
<li>Meanwhile the API is live — <code>/api/health</code>, <code>/api/status</code>,
<code>/api/board</code> — and so is <code>slmcode</code> on the command line.</li>
</ul>
</main></body></html>
`

// placeholderHandler answers requests when no SPA is embedded.
//
// It is registered on `GET /` in place of the file server, so it sits behind
// exactly the same session-token gate as the real shell would (see
// Server.secure): an unauthenticated navigation still gets the token gate, and
// this page is only ever reachable once a valid token has been presented. It
// must never be wired ahead of that gate.
//
// Only document navigations get the page. Anything else — a stray
// /favicon.ico, an asset path from a stale cached shell — gets a 404 rather
// than an HTML body pretending to be a script.
func (s *Server) placeholderHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" && !strings.HasSuffix(r.URL.Path, ".html") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, studioPlaceholderPage)
	})
}

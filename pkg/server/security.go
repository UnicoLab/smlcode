package server

import (
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// TokenHeader is the canonical header carrying the Studio session token.
const TokenHeader = "X-SLMCode-Token" //nolint:gosec // header name, not a credential value

// TokenQueryParam is the bootstrap parameter of the URL the CLI prints
// (`http://127.0.0.1:7420/?t=…`). Presenting it once mints TokenCookieName;
// after that the SPA needs no token of its own.
const TokenQueryParam = "t"

// TokenCookieName carries the session token after a successful `?t=` bootstrap.
//
// The cookie replaces the old `<meta name="slmcode-token">` injection. That
// tag made `GET /` an unauthenticated token-dispenser: any other local process
// could `curl http://127.0.0.1:7420/`, scrape the token out of the HTML and
// then drive the agent, so the token provided exactly zero of the
// "defense in depth against other local processes" it was documented to give.
//
// HttpOnly keeps it out of `document.cookie` (so an XSS in a rendered diff
// cannot exfiltrate it), SameSite=Strict keeps it off every cross-site
// request, and Path=/ makes it cover the SPA and /api/ alike. It is a session
// cookie: closing the browser drops it, and the CLI's tokenised URL re-issues.
const TokenCookieName = "slmcode_studio" //nolint:gosec // cookie name, not a credential value

// tokenSource names where a valid token arrived from.
type tokenSource int

const (
	tokenNone tokenSource = iota
	// tokenFromBootstrap is a header / bearer / `?t=` presentation — the form
	// the CLI's printed URL and a non-browser client use. Seeing one is what
	// mints the cookie.
	tokenFromBootstrap
	// tokenFromCookie is the already-bootstrapped browser tab.
	tokenFromCookie
)

// secure wraps the mux with the Studio security policy:
//
//  1. loopback-only Host (blocks DNS rebinding against 127.0.0.1),
//  2. same-origin enforcement (blocks any cross-site page from driving the
//     agent), with an opt-in allowance for the Vite dev origins,
//  3. no permissive CORS headers at all unless --dev-cors is on,
//  4. a session token on EVERY request when auth is enabled — the HTML shell
//     included. An unauthenticated navigation gets a static "open the URL the
//     CLI printed" page instead of the SPA, and never a token.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opts.AllowNonLoopback && !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden: studio only serves loopback hosts", http.StatusForbidden)
			return
		}

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		allowedOrigin, originOK := s.checkOrigin(r, origin)
		if !originOK {
			http.Error(w, "forbidden: cross-origin request rejected", http.StatusForbidden)
			return
		}
		if allowedOrigin != "" {
			// Only ever echo an explicitly allowed origin — never "*".
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+TokenHeader+", Authorization, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Add("Vary", "Origin")
		// The response body now depends on the session cookie (SPA vs gate
		// page), so it must never be cached as if it did not.
		w.Header().Add("Vary", "Cookie")
		// Studio must never be framed, and must never leak its URL (which can
		// carry ?t=<token>) to third parties.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch s.tokenSourceOf(r) {
		case tokenFromBootstrap:
			// A valid token arrived out of band (the CLI's `?t=` URL, a header,
			// a bearer). Hand the browser a cookie so the token can stop
			// traveling in URLs — and so nothing has to be embedded in HTML.
			s.setSessionCookie(w)
		case tokenFromCookie:
			// Already bootstrapped.
		case tokenNone:
			if isDocumentRequest(r) {
				writeTokenGate(w)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="slmcode-studio"`)
			http.Error(w, "unauthorized: missing or invalid studio session token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// setSessionCookie issues the HttpOnly session cookie.
//
// Not Secure: Studio is plain HTTP on loopback, and a Secure cookie would be
// dropped outright. Loopback is the trust boundary here, not TLS.
func (s *Server) setSessionCookie(w http.ResponseWriter) {
	if !s.AuthEnabled() {
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly+SameSite=Strict are set; Secure is intentionally omitted because Studio is plain HTTP on loopback (see setSessionCookie doc above) and a Secure cookie would be dropped outright — loopback, not TLS, is the trust boundary here
		Name:     TokenCookieName,
		Value:    s.opts.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// isDocumentRequest reports whether this is a browser navigation to an HTML
// page, so an unauthenticated hit can be answered with readable instructions
// instead of a bare 401 body.
func isDocumentRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	return r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html")
}

// tokenGatePage is served for an unauthenticated navigation. It is a complete,
// self-contained document on purpose: every asset is behind the same token, so
// it can reference nothing. It must never contain the token.
//
//nolint:gosec // G101 false positive: static HTML, contains no credential
const tokenGatePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>SLMCode Studio — session token required</title>
<style>
 body{font:15px/1.6 ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
      margin:0;display:grid;place-items:center;min-height:100vh;background:#0f1115;color:#e6e8ee}
 main{max-width:34rem;padding:2rem}
 h1{font-size:1.15rem;margin:0 0 .75rem}
 code{background:#1b1f27;padding:.15rem .4rem;border-radius:4px}
 p{color:#aeb4c2}
</style></head><body><main>
<h1>Session token required</h1>
<p>Studio can read this repository, rewrite its configuration, store provider
API keys and start agent runs, so it will not open without the session token.</p>
<p>Open the tokenised URL the CLI printed when it started Studio — it looks like
<code>http://127.0.0.1:7420/?t=&hellip;</code>. Scroll back in that terminal, or
restart with <code>slmcode studio</code> to print a fresh one.</p>
</main></body></html>
`

// writeTokenGate answers an unauthenticated navigation.
func writeTokenGate(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, tokenGatePage)
}

// checkOrigin validates the Origin header. It returns the origin to echo in
// Access-Control-Allow-Origin (empty for same-origin, which needs no CORS) and
// whether the request may proceed.
func (s *Server) checkOrigin(r *http.Request, origin string) (string, bool) {
	// Sec-Fetch-Site is sent by every current browser and covers cross-origin
	// "simple" GETs that carry no Origin header at all.
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "same-origin", "none":
		// fine (or a non-browser client)
	case "same-site", "cross-site":
		if origin == "" || !s.originAllowed(r, origin) {
			return "", false
		}
	}

	if origin == "" || origin == "null" {
		return "", origin != "null"
	}
	if sameOrigin(r, origin) {
		return "", true
	}
	if s.originAllowed(r, origin) {
		return origin, true
	}
	return "", false
}

func (s *Server) originAllowed(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	if sameOrigin(r, origin) {
		return true
	}
	if s.opts.DevCORS {
		for _, o := range DevOrigins {
			if strings.EqualFold(o, origin) {
				return true
			}
		}
	}
	for _, o := range s.opts.ExtraOrigins {
		if o != "" && strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// sameOrigin compares an Origin header against the request's own host.
// Scheme is not compared strictly because Studio is plain HTTP on loopback and
// a local reverse proxy may terminate TLS; host+port equality is what matters.
func sameOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// tokenSourceOf validates the session token and reports how it was presented.
// With auth disabled every request counts as already-bootstrapped, so no
// cookie is minted for a token that does not exist.
func (s *Server) tokenSourceOf(r *http.Request) tokenSource {
	if !s.AuthEnabled() {
		return tokenFromCookie
	}
	want := s.opts.Token
	got := strings.TrimSpace(r.Header.Get(TokenHeader))
	if got == "" {
		if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
			if v, ok := strings.CutPrefix(auth, "Bearer "); ok {
				got = strings.TrimSpace(v)
			}
		}
	}
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get(TokenQueryParam))
	}
	if got != "" {
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return tokenFromBootstrap
		}
		return tokenNone
	}
	if c, err := r.Cookie(TokenCookieName); err == nil &&
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(c.Value)), []byte(want)) == 1 {
		return tokenFromCookie
	}
	return tokenNone
}

// isLoopbackHost reports whether an HTTP Host header targets the local machine.
func isLoopbackHost(hostport string) bool {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		// Host is mandatory in HTTP/1.1; a missing one is not trustworthy.
		return false
	}
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	// Only the exact name "localhost" and literal loopback IPs.
	//
	// A wildcard `*.localhost` used to be accepted. RFC 6761 says resolvers
	// SHOULD map it to loopback, but nothing stops a public resolver from
	// answering for `evil.localhost`, and a page served from that name is
	// same-origin with Studio — which turns the two outer rings of the policy
	// (loopback host + same origin) into no-ops and leaves only the token.
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

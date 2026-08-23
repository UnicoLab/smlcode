package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// TokenHeader is the canonical header carrying the Studio session token.
const TokenHeader = "X-SLMCode-Token" //nolint:gosec // header name, not a credential value

// TokenQueryParam carries the token for clients that cannot set headers
// (EventSource / <img> style loads).
const TokenQueryParam = "t"

// TokenMetaName is the <meta> tag the SPA reads on first load.
const TokenMetaName = "slmcode-token" //nolint:gosec // meta-tag name, not a credential value

// secure wraps the mux with the Studio security policy:
//
//  1. loopback-only Host (blocks DNS rebinding against 127.0.0.1),
//  2. same-origin enforcement (blocks any cross-site page from driving the
//     agent), with an opt-in allowance for the Vite dev origins,
//  3. no permissive CORS headers at all unless --dev-cors is on,
//  4. a session token on every /api/* request when auth is enabled.
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
		// Studio must never be framed, and must never leak its URL (which can
		// carry ?t=<token>) to third parties.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") && !s.tokenOK(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="slmcode-studio"`)
			http.Error(w, "unauthorized: missing or invalid studio session token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
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

// tokenOK validates the session token when auth is enabled.
func (s *Server) tokenOK(r *http.Request) bool {
	if !s.AuthEnabled() {
		return true
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
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
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

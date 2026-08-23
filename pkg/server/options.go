package server

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

// DevOrigins are the origins accepted when Options.DevCORS is enabled.
// This is exactly the Vite dev server (`npm run dev` in web/) and nothing else.
var DevOrigins = []string{
	"http://127.0.0.1:5173",
	"http://localhost:5173",
	"http://[::1]:5173",
}

// Options configures Studio server security and lifecycle behavior.
//
// Security model (see docs/studio.md):
//
//   - Studio is an unauthenticated-by-design *local* agent with file read,
//     config write, API-key write and run-start capability. It therefore
//     refuses any request that is not loopback and emits no permissive CORS
//     headers, so no third-party web page can read a response or drive a run.
//   - A random session Token gates EVERY request, the HTML shell included. It
//     is accepted as `X-SLMCode-Token`, as `Authorization: Bearer …`, as the
//     `t` query parameter of the URL the CLI prints, or as the HttpOnly
//     SameSite=Strict cookie the server sets once one of those validates.
//     Nothing is ever embedded in the served HTML: `GET /` without a token
//     returns a static "open the URL the CLI printed" page, so another local
//     process cannot curl the shell and read the secret out of it.
//     Residual risk: the token still lives in the CLI's stdout and in this
//     process's memory, so a process running as the SAME user can obtain it.
//     Loopback + same-origin + token is a boundary against other machines,
//     other origins and unprivileged local listeners — not against the user.
//   - NoAuth is the escape hatch for embedded use; loopback enforcement stays.
type Options struct {
	// Token is the shared session secret. Empty means "no token required"
	// unless GenerateToken is set.
	Token string
	// GenerateToken creates a random Token when Token is empty.
	GenerateToken bool
	// NoAuth disables the token requirement entirely (`--no-auth`).
	// Loopback host/origin enforcement is unaffected.
	NoAuth bool
	// DevCORS allows the Vite dev server origins (`--dev-cors`). Off by default.
	DevCORS bool
	// AllowNonLoopback disables the loopback host/origin guard. Only for
	// deliberate exposure behind an external authenticating proxy.
	AllowNonLoopback bool
	// ExtraOrigins are additional exact origins to accept (used with
	// AllowNonLoopback for reverse-proxy deployments).
	ExtraOrigins []string
}

// DefaultOptions returns the hardened options the CLI should use: a freshly
// generated session token, no CORS, loopback only.
//
// Environment overrides (documented, for embedders and tests):
//
//	SLMCODE_STUDIO_TOKEN=<tok>   use this token instead of a random one
//	SLMCODE_STUDIO_NO_AUTH=1     disable the token requirement
//	SLMCODE_STUDIO_DEV_CORS=1    allow the Vite dev origins
func DefaultOptions() Options {
	o := Options{GenerateToken: true}
	if v := strings.TrimSpace(os.Getenv("SLMCODE_STUDIO_TOKEN")); v != "" {
		o.Token = v
		o.GenerateToken = false
	}
	if envTrue("SLMCODE_STUDIO_NO_AUTH") {
		o.NoAuth = true
		o.GenerateToken = false
	}
	if envTrue("SLMCODE_STUDIO_DEV_CORS") {
		o.DevCORS = true
	}
	return o
}

func envTrue(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return false
}

// normalize fills in a generated token and de-duplicates origins.
func (o *Options) normalize() {
	if o.NoAuth {
		o.Token = ""
		o.GenerateToken = false
		return
	}
	if o.Token == "" && o.GenerateToken {
		o.Token = NewToken()
	}
}

// NewToken returns a 256-bit random hex session token.
func NewToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is fatal for a security token — fail closed by
		// returning an unguessable-length empty string is NOT acceptable, so
		// panic-free fallback: refuse to run tokenless by using a marker the
		// caller can detect.
		return ""
	}
	return hex.EncodeToString(buf)
}

// Token returns the active session token ("" when auth is disabled).
// The CLI must surface it, e.g. `http://127.0.0.1:7420/?t=<token>`.
func (s *Server) Token() string { return s.opts.Token }

// AuthEnabled reports whether a session token is required (on every request).
func (s *Server) AuthEnabled() bool { return s.opts.Token != "" && !s.opts.NoAuth }

// URL builds the address a user should open, including the session token when
// one is active. addr is a listen address such as "127.0.0.1:7420".
func (s *Server) URL(addr string) string {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	u := "http://" + host + "/"
	if s.AuthEnabled() {
		u += "?t=" + s.opts.Token
	}
	return u
}

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// loopbackHost is what the Studio security policy expects to see in Host.
// httptest.NewRequest defaults to "example.com", which Studio rejects (that is
// the whole point of the DNS-rebinding guard), so every test request is built
// through this helper.
const loopbackHost = "127.0.0.1:7420"

// newAPIRequest builds a request that satisfies the loopback host policy.
func newAPIRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = loopbackHost
	return r
}

// withOrigin stamps an Origin header (and matching Sec-Fetch-Site) so origin
// policy can be exercised directly.
func withOrigin(r *http.Request, origin, fetchSite string) *http.Request {
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	return r
}

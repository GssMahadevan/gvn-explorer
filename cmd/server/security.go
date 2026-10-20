package main

import (
	"net/http"
	"strconv"
)

// csp is the Content-Security-Policy for every response. The GUI only
// ever loads its own Vite bundle (script-src 'self'), fetches its own
// API (connect-src 'self'), and renders antd/CodeMirror styles that are
// injected at runtime (style-src 'unsafe-inline' is unavoidable there —
// styles cannot execute scripts, and markdown HTML never renders at all
// because react-markdown drops raw HTML). object-src/frame-ancestors/
// base-uri/form-action close the plugin, clickjacking, <base>-hijack
// and form-post-to-anywhere holes.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; " +
	"object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// securityHeaders hardens every response against the standard web attacks:
//   - nosniff: browsers must not reinterpret a downloaded file (say an
//     uploaded .html) as active content of another type;
//   - X-Frame-Options + frame-ancestors 'none': no frame on any origin
//     can embed us, so clickjacking has no surface (mirrors the API's
//     refusal of cross-origin form posts);
//   - Referrer-Policy: our paths (which carry logical file names) never
//     leak to third parties;
//   - CSP above.
//
// HSTS is added only when the request actually arrived over TLS and the
// operator opted in with -hsts-days (a self-signed cert on a bare IP is
// a poor fit for a pinned HSTS policy, hence opt-in).
func securityHeaders(hstsDays int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if hstsDays > 0 && r.TLS != nil {
			h.Set("Strict-Transport-Security", strconv.Itoa(hstsDays*24*3600)+"; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

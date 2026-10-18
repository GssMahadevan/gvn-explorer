package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"time"
)

// statusRecorder captures the status code written by handlers.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func withMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return recoverMW(log, requestIDMW(requestLogMW(log, originMW(csrfGuard(next)))))
}

// ---- request correlation id ----

type ctxKey int

const requestIDKey ctxKey = iota

// incomingRID accepts well-formed upstream ids (proxies, load balancers) so
// one id follows a request across services; anything odd is regenerated.
var incomingRID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func newRID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil { // crypto/rand never fails in practice
		return "r" + time.Now().UTC().Format("150405.000000")
	}
	return hex.EncodeToString(b)
}

// requestIDMW guarantees every request carries an id: it echoes an acceptable
// X-Request-Id, otherwise generates one, always sets the response header so
// clients (and their support tickets) can quote it, and puts it in the
// context for the log middlewares.
func requestIDMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if !incomingRID.MatchString(rid) {
			rid = newRID()
		}
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, rid)))
	})
}

func ridFrom(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// clientIP strips the source port from the peer address (same rule as the
// handlers' rate limiter key).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- security guards ----

// originMW rejects cross-origin state-changing requests (CSRF defense in
// depth on top of SameSite=Lax cookies). curl/scripts send no Origin header,
// so they are unaffected.
func originMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, `{"error":{"code":"csrf","message":"cross-origin request blocked"}}`, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrfGuard blocks POST/PUT/DELETE carrying form content-types, which a
// browser would submit automatically from a hostile page; the API requires
// JSON (or multipart uploads, which are not auto-submitted cross-origin).
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			ct := r.Header.Get("Content-Type")
			if ct == "application/x-www-form-urlencoded" || ct == "text/plain" || ct == "" && r.ContentLength > 0 && r.URL.Path != "/api/v1/fs/upload" {
				http.Error(w, `{"error":{"code":"csrf","message":"content-type not accepted"}}`, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- logging ----

// requestLogMW logs exactly one structured line per request: the correlation
// id, the caller, the outcome and the cost. Level by outcome — server
// failures are errors, rate limiting is a warning, everything else (including
// expected 401/403 from scripts and probes) stays info so the stream keeps
// signal. Debug level drops GET noise entirely for chatty file explorers.
func requestLogMW(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		d := time.Since(start)

		attrs := []any{
			"rid", ridFrom(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", float64(d.Microseconds()) / 1000.0,
			"ip", clientIP(r),
		}
		if q := r.URL.RawQuery; q != "" {
			attrs = append(attrs, "query", q)
		}
		switch {
		case rec.status >= 500:
			log.Error("request", attrs...)
		case rec.status == http.StatusTooManyRequests:
			log.Warn("request", attrs...)
		default:
			if !log.Enabled(r.Context(), slog.LevelInfo) { // -log-level=debug quiets GETs
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					log.Debug("request", attrs...)
					return
				}
			}
			log.Info("request", attrs...)
		}
	})
}

// recoverMW turns panics into 500s without killing the process.
func recoverMW(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic", "rid", ridFrom(r), "path", r.URL.Path,
					"value", rec, "stack", string(debug.Stack()))
				http.Error(w, `{"error":{"code":"internal","message":"unexpected error"}}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

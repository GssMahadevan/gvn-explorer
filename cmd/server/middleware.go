package main

import (
	"log/slog"
	"net/http"
	"net/url"
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
	return recoverMW(log, requestLogMW(log, originMW(csrfGuard(next))))
}

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

// requestLogMW logs one line per request.
func requestLogMW(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond),
		)
	})
}

// recoverMW turns panics into 500s without killing the process.
func recoverMW(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic", "value", rec, "stack", string(debug.Stack()))
				http.Error(w, `{"error":{"code":"internal","message":"unexpected error"}}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

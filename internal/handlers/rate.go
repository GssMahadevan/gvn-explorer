package handlers

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Per-principal and per-IP token buckets (DoS control for devices that
// hammer the download endpoint, or brute force against login). In-memory:
// a restart resets the buckets, which is acceptable at this scale — the
// DB stays untouched and there is no extra infrastructure.

// limiterSet keeps one token bucket per key, evicting idle entries.
type limiterSet struct {
	limbs map[string]*entry
	mu    sync.Mutex
	rps   rate.Limit
	burst int
	idle  time.Duration
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// newLimiterSet returns nil when rps or burst is not positive (= disabled).
func newLimiterSet(rps float64, burst int) *limiterSet {
	if rps <= 0 || burst <= 0 {
		return nil
	}
	return &limiterSet{limbs: map[string]*entry{}, rps: rate.Limit(rps), burst: burst, idle: 10 * time.Minute}
}

// authSlots bounds how many password verifications/creations run at once
// (-auth-concurrency). Per-IP buckets cap cost per source; this caps cost
// in total: bcrypt is the most expensive thing the server does per request
// (~100 ms, more on phone/MIPS hardware), and thousands of well-behaved
// low-rate sources can still saturate every core together. Excess requests
// get an immediate 503 + Retry-After instead of queueing unboundedly, so
// file serving always keeps its CPU.
type authSlots struct{ ch chan struct{} }

// newAuthSlots returns nil when n is not positive (= disabled).
func newAuthSlots(n int) *authSlots {
	if n <= 0 {
		return nil
	}
	return &authSlots{ch: make(chan struct{}, n)}
}

func (s *authSlots) wrap(next http.HandlerFunc) http.HandlerFunc {
	if s == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.ch <- struct{}{}:
			defer func() { <-s.ch }()
			next(w, r)
		default:
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusServiceUnavailable, "overloaded",
				"too many concurrent login operations, retry in a few seconds")
		}
	}
}

func (ls *limiterSet) allow(key string) bool {
	if ls == nil {
		return true
	}
	now := time.Now()
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.limbs[key]
	if !ok {
		if len(ls.limbs) >= 10000 { // sweep idle limiters before growing unbounded
			for k, v := range ls.limbs {
				if now.Sub(v.seen) > ls.idle {
					delete(ls.limbs, k)
				}
			}
		}
		e = &entry{lim: rate.NewLimiter(ls.rps, ls.burst)}
		ls.limbs[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func writeRateLimited(w http.ResponseWriter, retry time.Duration) {
	s := int(retry.Seconds())
	if s < 1 {
		s = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(s))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
}

// clientIP strips the port from RemoteAddr. Behind a reverse proxy this is
// the proxy's address — set the proxy to pass X-Forwarded-For and extend
// this if that deployment matters.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// perIP gates unauthenticated endpoints (login) by source address.
func (a *API) perIP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authIP.allow(clientIP(r)) {
			writeRateLimited(w, time.Second)
			return
		}
		next(w, r)
	}
}

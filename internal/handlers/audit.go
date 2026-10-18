package handlers

// Audit plumbing: every authz/authn mutation is recorded in the events
// table (append-only) for forensic analysis, and admins can query it back,
// scoped to their tenant.

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/user/llm-crud1/internal/audit"
)

// audit writes one event, filling actor and source IP from the request.
// A failed audit write is an operational error (logged loudly) but does not
// fail the business operation — losing one line must not lock admins out
// of their own system while the DB is otherwise healthy.
func (a *API) audit(r *http.Request, ev audit.Event) {
	if a.Events == nil {
		return
	}
	if u := userFrom(r.Context()); u.ID != 0 {
		if ev.ActorID == nil {
			id := u.ID
			ev.ActorID = &id
		}
		if ev.Actor == "" {
			ev.Actor = u.Tenant + "/" + u.Username
		}
		if ev.TenantID == 0 {
			ev.TenantID = u.TenantID
		}
	}
	ev.IP = clientIP(r)
	if err := a.Events.Record(r.Context(), ev); err != nil {
		slog.Error("audit record failed", "action", ev.Action, "err", err)
	}
}

// handleEventsList: GET /api/v1/events (admin).
// Query: limit, action, target_type, target, since (RFC3339).
// Super admins see everything; tenant admins only events scoped to their
// tenant — including actions a super admin performed on their users/grants.
func (a *API) handleEventsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
			return
		}
		limit = n
	}
	f := audit.Filter{
		Limit:      limit,
		Action:     q.Get("action"),
		TargetType: q.Get("target_type"),
		Target:     q.Get("target"),
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "since must be RFC3339")
			return
		}
		f.Since = t.Unix()
	}
	events, err := a.Events.List(r.Context(), scopeTenantID(userFrom(r.Context())), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

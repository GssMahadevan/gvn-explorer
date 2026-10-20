package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
)

type loginRequest struct {
	Tenant   string `json:"tenant"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !a.decodeBody(w, r, &req) {
		return
	}
	if req.Tenant == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "tenant is required")
		return
	}
	u, err := a.Auth.Verify(r.Context(), req.Tenant, req.Username, req.Password)
	if errors.Is(err, auth.ErrNotFound) {
		// forensic record of failed attempts (wrong password, unknown user,
		// expired or suspended account): the would-be identity plus source
		// IP. Scoped to the targeted tenant so its admin can see brute
		// forces against their own users.
		tid, _ := a.Auth.TenantID(r.Context(), req.Tenant) // 0 for unknown tenant
		a.audit(r, audit.Event{Action: "login_failed",
			Actor: req.Tenant + "/" + req.Username, TenantID: tid,
			TargetType: "user", TargetName: req.Username})
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid username or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	ttl := a.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token, err := a.Auth.CreateSession(r.Context(), u.ID, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "session creation failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
	a.audit(r, audit.Event{Action: "login", Actor: u.Tenant + "/" + u.Username,
		TenantID: u.TenantID, TargetType: "user", TargetID: u.ID, TargetName: u.Username,
		Details: map[string]any{"is_admin": u.IsAdmin, "is_super": u.IsSuper}})
	writeJSON(w, http.StatusOK, u)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.Auth.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

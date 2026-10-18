package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/user/llm-crud1/internal/auth"
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
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
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

// Package handlers: REST API for the sandboxed file explorer with
// DB-backed auth and ACLs.
package handlers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/user/llm-crud1/internal/acl"
	"github.com/user/llm-crud1/internal/auth"
	"github.com/user/llm-crud1/internal/fsx"
)

const sessionCookie = "sid"

type API struct {
	Auth       *auth.Store
	ACL        *acl.Store
	FS         *fsx.Tree
	SessionTTL time.Duration
	MaxFileMB  int // read/write size limit
}

// ---- JSON helpers (same envelope as before) ----

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e errorBody
	e.Error.Code, e.Error.Message = code, msg
	writeJSON(w, status, e)
}

func (a *API) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	limit := int64(a.MaxFileMB)
	if limit <= 0 {
		limit = 10
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// mapFsErr translates filesystem errors into HTTP responses.
func mapFsErr(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, "forbidden", "filesystem permission denied")
	case errors.Is(err, fsx.ErrExists):
		writeError(w, http.StatusConflict, "exists", "path already exists")
	case errors.Is(err, fsx.ErrNotEmpty):
		writeError(w, http.StatusConflict, "not_empty", "directory is not empty")
	case errors.Is(err, fsx.ErrNotDir):
		writeError(w, http.StatusBadRequest, "not_a_dir", "path is not a directory")
	default:
		return false
	}
	return true
}

// ---- auth plumbing ----

// currentUser resolves the session cookie, if any.
func (a *API) currentUser(r *http.Request) (auth.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.User{}, false
	}
	u, err := a.Auth.SessionUser(r.Context(), c.Value)
	if err != nil {
		return auth.User{}, false
	}
	return u, true
}

type ctxKey int

const userKey ctxKey = 1

// withUser stashes the authenticated user on the request context.
func (a *API) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.currentUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		next(w, r.WithContext(contextWithUser(r.Context(), u)))
	}
}

func (a *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u.IsAdmin || u.IsSuper {
			next(w, r)
		} else {
			writeError(w, http.StatusForbidden, "admin_required", "admin privileges required")
		}
	})
}

// requireSuper gates platform-wide administration (tenant CRUD).
func (a *API) requireSuper(next http.HandlerFunc) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()).IsSuper {
			next(w, r)
		} else {
			writeError(w, http.StatusForbidden, "super_required", "platform super admin required")
		}
	})
}

// logicalPath extracts and normalizes the ?path= query parameter.
func logicalPath(r *http.Request) (string, error) {
	return acl.Normalize(r.URL.Query().Get("path"))
}

// check verifies the caller's effective ACL bits on a logical path and
// writes the 403 response when denied.
func (a *API) check(w http.ResponseWriter, r *http.Request, path string, needRead, needWrite, needDelete bool) bool {
	u := userFrom(r.Context())
	bits, err := a.ACL.Effective(r.Context(), a.Auth, path, u)
	if err != nil {
		slog.Error("acl lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "acl lookup failed")
		return false
	}
	var missing []string
	if needRead && !bits.Read {
		missing = append(missing, "read")
	}
	if needWrite && !bits.Write {
		missing = append(missing, "write")
	}
	if needDelete && !bits.Delete {
		missing = append(missing, "delete")
	}
	if missing != nil {
		writeError(w, http.StatusForbidden, "forbidden",
			"missing permission(s) "+strings.Join(missing, ", ")+" on "+path)
		return false
	}
	return true
}

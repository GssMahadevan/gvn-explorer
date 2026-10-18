package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/user/llm-crud1/internal/acl"
	"github.com/user/llm-crud1/internal/auth"
)

// ---- perms ----

// handlePermsShow: GET /api/v1/perms?path=/dir
// Explicit grants on that path + the caller's effective bits.
func (a *API) handlePermsShow(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	grants, err := a.ACL.ListByPath(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	u := userFrom(r.Context())
	eff, err := a.ACL.Effective(r.Context(), a.Auth, p, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "grants": grants, "effective": eff})
}

type grantBody struct {
	Path          string `json:"path"`
	PrincipalType string `json:"principal_type"`
	Principal     string `json:"principal"` // username or group name
	Read          bool   `json:"read"`
	Write         bool   `json:"write"`
	Delete        bool   `json:"delete"`
}

// handlePermsPut: PUT /api/v1/perms (admin) — upsert one grant.
func (a *API) handlePermsPut(w http.ResponseWriter, r *http.Request) {
	var b grantBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	p, err := acl.Normalize(b.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	var pid int64
	switch b.PrincipalType {
	case "user":
		u, err := a.Auth.UserByName(r.Context(), b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_user", b.Principal)
			return
		}
		pid = u.ID
	case "group":
		g, err := a.Auth.GroupByName(r.Context(), b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_group", b.Principal)
			return
		}
		pid = g.ID
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "principal_type must be user or group")
		return
	}
	gr, err := a.ACL.Upsert(r.Context(), p, b.PrincipalType, pid, b.Read, b.Write, b.Delete)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gr)
}

// handlePermsDelete: DELETE /api/v1/perms/{id} (admin)
func (a *API) handlePermsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	if err := a.ACL.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- users ----

type userBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func (a *API) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := a.Auth.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (a *API) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var b userBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if b.Username == "" || b.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "username and password required")
		return
	}
	u, err := a.Auth.CreateUser(r.Context(), b.Username, b.Password, b.IsAdmin)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "username taken or invalid input")
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	if err := a.Auth.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type passwordBody struct {
	Password string `json:"password"`
}

func (a *API) handleUsersPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b passwordBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if b.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "password required")
		return
	}
	if err := a.Auth.SetPassword(r.Context(), id, b.Password); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

type nameBody struct {
	Name string `json:"name"`
}

func (a *API) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	groups, err := a.Auth.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (a *API) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var b nameBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	g, err := a.Auth.CreateGroup(r.Context(), b.Name)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "group name taken")
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (a *API) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	if err := a.Auth.DeleteGroup(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type memberBody struct {
	Username string `json:"username"`
}

func (a *API) handleGroupAddMember(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b memberBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	u, err := a.Auth.UserByName(r.Context(), b.Username)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_such_user", b.Username)
		return
	}
	if err := a.Auth.AddMember(r.Context(), gid, u.ID); err != nil {
		writeError(w, http.StatusConflict, "exists", "already a member or bad group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleGroupRemoveMember(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "userId must be numeric")
		return
	}
	if err := a.Auth.RemoveMember(r.Context(), gid, uid); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/llm-crud1/internal/acl"
	"github.com/user/llm-crud1/internal/auth"
)

// scopeTenantID returns 0 (see all tenants) for super admins, and the
// caller's tenant id for tenant admins, for use by list queries.
func scopeTenantID(u auth.User) int64 {
	if u.IsSuper {
		return 0
	}
	return u.TenantID
}

// ---- tenants (super admin only) ----

type nameBody struct {
	Name string `json:"name"`
}

func (a *API) handleTenantsList(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Auth.ListTenants(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) handleTenantsCreate(w http.ResponseWriter, r *http.Request) {
	var b nameBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if b.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "name required")
		return
	}
	t, err := a.Auth.CreateTenant(r.Context(), b.Name)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "tenant name taken")
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// handleTenantsDelete drops the tenant with its users, groups, memberships
// and ACL grants (cascades in the store).
func (a *API) handleTenantsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	if err := a.Auth.DeleteTenant(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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
	Tenant        string `json:"tenant"`    // principal's tenant; defaults to caller's
	Read          bool   `json:"read"`
	Write         bool   `json:"write"`
	Delete        bool   `json:"delete"`
}

// handlePermsPut: PUT /api/v1/perms (admin) — upsert one grant.
// Tenant admins may only grant to principals inside their own tenant.
func (a *API) handlePermsPut(w http.ResponseWriter, r *http.Request) {
	var b grantBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	u := userFrom(r.Context())
	tenant := b.Tenant
	if tenant == "" {
		tenant = u.Tenant
	} else if !u.IsSuper && !strings.EqualFold(tenant, u.Tenant) {
		writeError(w, http.StatusForbidden, "forbidden", "grants may only target your own tenant")
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
		usr, err := a.Auth.UserByName(r.Context(), tenant, b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_user", tenant+"/"+b.Principal)
			return
		}
		pid = usr.ID
	case "group":
		g, err := a.Auth.GroupByName(r.Context(), tenant, b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_group", tenant+"/"+b.Principal)
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
	gr.PrincipalTenant = tenant
	gr.PrincipalName = b.Principal
	writeJSON(w, http.StatusOK, gr)
}

// handlePermsDelete: DELETE /api/v1/perms/{id} (admin).
// Tenant admins can only revoke grants whose principal lives in their tenant.
func (a *API) handlePermsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	gr, err := a.ACL.GrantByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	u := userFrom(r.Context())
	if !u.IsSuper && !strings.EqualFold(gr.PrincipalTenant, u.Tenant) {
		writeError(w, http.StatusForbidden, "forbidden", "grant belongs to another tenant")
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
	Tenant   string `json:"tenant"` // required for super admins; ignored for tenant admins
	IsAdmin  bool   `json:"is_admin"`
}

// resolveCreateTenant decides which tenant a new user/group belongs to:
// tenant admins are pinned to their own; super admins may name any tenant
// and default to their own when the field is omitted.
func resolveCreateTenant(u auth.User, requested string) string {
	if !u.IsSuper || requested == "" {
		return u.Tenant
	}
	return requested
}

func (a *API) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := a.Auth.ListUsers(r.Context(), scopeTenantID(userFrom(r.Context())))
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
	u := userFrom(r.Context())
	nu, err := a.Auth.CreateUser(r.Context(), resolveCreateTenant(u, b.Tenant), b.Username, b.Password, b.IsAdmin, false)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "username taken, unknown tenant, or invalid input")
		return
	}
	writeJSON(w, http.StatusCreated, nu)
}

// targetInScope verifies a tenant admin only touches principals of their own
// tenant; super admins pass unconditionally.
func (a *API) targetInScope(w http.ResponseWriter, r *http.Request, targetTenantID int64, what string) bool {
	u := userFrom(r.Context())
	if u.IsSuper || targetTenantID == u.TenantID {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", what+" belongs to another tenant")
	return false
}

func (a *API) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	target, err := a.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if !a.targetInScope(w, r, target.TenantID, "user") {
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
	target, err := a.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if !a.targetInScope(w, r, target.TenantID, "user") {
		return
	}
	if err := a.Auth.SetPassword(r.Context(), id, b.Password); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

type groupBody struct {
	Name   string `json:"name"`
	Tenant string `json:"tenant"` // required for super admins; ignored for tenant admins
}

func (a *API) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	groups, err := a.Auth.ListGroups(r.Context(), scopeTenantID(userFrom(r.Context())))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (a *API) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var b groupBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if b.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "name required")
		return
	}
	u := userFrom(r.Context())
	g, err := a.Auth.CreateGroup(r.Context(), resolveCreateTenant(u, b.Tenant), b.Name)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "group name taken in tenant, or unknown tenant")
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
	g, err := a.Auth.GroupByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if !a.targetInScope(w, r, g.TenantID, "group") {
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

// handleGroupAddMember enrolls a user in the group. Membership is always
// resolved inside the group's own tenant, so the same username in another
// tenant cannot be added by mistake.
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
	g, err := a.Auth.GroupByID(r.Context(), gid)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such group")
		return
	}
	if !a.targetInScope(w, r, g.TenantID, "group") {
		return
	}
	u, err := a.Auth.UserByName(r.Context(), g.Tenant, b.Username)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_such_user", g.Tenant+"/"+b.Username)
		return
	}
	if err := a.Auth.AddMember(r.Context(), gid, u.ID); err != nil {
		if errors.Is(err, auth.ErrTenantMismatch) {
			writeError(w, http.StatusConflict, "tenant_mismatch", err.Error())
			return
		}
		writeError(w, http.StatusConflict, "exists", "already a member")
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
	g, err := a.Auth.GroupByID(r.Context(), gid)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such group")
		return
	}
	if !a.targetInScope(w, r, g.TenantID, "group") {
		return
	}
	if err := a.Auth.RemoveMember(r.Context(), gid, uid); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

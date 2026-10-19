package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/llm-crud1/internal/acl"
	"github.com/user/llm-crud1/internal/audit"
	"github.com/user/llm-crud1/internal/auth"
	"github.com/user/llm-crud1/internal/query"
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
	Name        string `json:"name"`
	Description string `json:"description"` // free-text context for operators
}

// descriptionBody is the update payload for every entity kind (users,
// groups, tenants): description-only edits go through PUT /{id}.
type descriptionBody struct {
	Description string `json:"description"`
}

// List endpoints share one contract: limit/offset paging plus q/col search
// and sort/dir ordering (validated against these whitelists; the stores
// implement the same keys). Entity lists additionally accept active=true|false
// as an exact-match filter. X-Total-Count reports the filtered total.

var tenantsListSpec = ListSpec{
	Sorts:        map[string]bool{"name": true, "created": true, "active": true},
	Cols:         []string{"name", "description"},
	DefSort:      "name",
	ActiveFilter: true,
}

func (a *API) handleTenantsList(w http.ResponseWriter, r *http.Request) {
	o, ok := parseList(w, r, tenantsListSpec)
	if !ok {
		return
	}
	ts, total, err := a.Auth.ListTenants(r.Context(), o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	setTotal(w, total)
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
	t, err := a.Auth.CreateTenant(r.Context(), b.Name, strings.TrimSpace(b.Description))
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "tenant name taken")
		return
	}
	a.audit(r, audit.Event{Action: "tenant_create", TenantID: t.ID,
		TargetType: "tenant", TargetID: t.ID, TargetName: t.Name})
	writeJSON(w, http.StatusCreated, t)
}

// handleTenantsUpdate: PUT /api/v1/tenants/{id} {"description":...}
func (a *API) handleTenantsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b descriptionBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if err := a.Auth.UpdateTenant(r.Context(), id, strings.TrimSpace(b.Description)); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "tenant_update", TenantID: id,
		TargetType: "tenant", TargetID: id})
	w.WriteHeader(http.StatusNoContent)
}

// handleTenantsActive: POST /api/v1/tenants/{id}/active {"active":bool} —
// the tenant-wide kill switch. Rows, users, groups, grants and history all
// survive; the tenant's logins, live sessions and API keys stop working.
func (a *API) handleTenantsActive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b activeBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	// footgun guard, same spirit as users: you cannot suspend the tenant you
	// are standing in (it would lock every admin in it out, super included).
	if !b.Active && id == userFrom(r.Context()).TenantID {
		writeError(w, http.StatusUnprocessableEntity, "validation", "you cannot deactivate your own tenant")
		return
	}
	if err := a.Auth.SetTenantActive(r.Context(), id, b.Active); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "tenant_active", TenantID: id,
		TargetType: "tenant", TargetID: id, Details: map[string]any{"active": b.Active}})
	w.WriteHeader(http.StatusNoContent)
}

// handleTenantsDelete drops the tenant with its users, groups, memberships
// and ACL grants (cascades in the store).
func (a *API) handleTenantsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	// snapshot the name before the cascade, for the forensic record
	name := ""
	if ts, _, err := a.Auth.ListTenants(r.Context(), query.List{Sort: "name"}); err == nil {
		for _, t := range ts {
			if t.ID == id {
				name = t.Name
			}
		}
	}
	if err := a.Auth.DeleteTenant(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "tenant_delete", TenantID: id,
		TargetType: "tenant", TargetID: id, TargetName: name,
		Details: map[string]any{"cascaded": "users, groups, memberships, grants"}})
	w.WriteHeader(http.StatusNoContent)
}

// ---- expiry ----

// Expiry is embedded in grant and user create bodies.
// "expires_days": 3  -> expires 3 days from now (fractions allowed)
// "expires_at": "2026-10-22T00:00:00Z" -> exact cutoff, wins over days
// Omit both for no expiry.
type Expiry struct {
	ExpiresDays *float64 `json:"expires_days"`
	ExpiresAt   string   `json:"expires_at"`
}

func (e Expiry) resolve() (*time.Time, error) {
	if e.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, e.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("expires_at must be RFC3339, e.g. 2026-10-22T00:00:00Z")
		}
		u := t.UTC()
		return &u, nil
	}
	if e.ExpiresDays == nil || *e.ExpiresDays == 0 {
		return nil, nil
	}
	if *e.ExpiresDays < 0 {
		return nil, fmt.Errorf("expires_days must be positive; use expires_at for a past cutoff")
	}
	u := time.Now().UTC().Add(time.Duration(*e.ExpiresDays * float64(24*time.Hour)))
	return &u, nil
}

// ---- perms ----

var permsListSpec = ListSpec{
	Sorts:   map[string]bool{"principal": true, "type": true, "expires": true},
	Cols:    []string{"principal", "tenant"},
	DefSort: "principal",
}

// handlePermsShow: GET /api/v1/perms?path=/dir
// Explicit grants on that path (paged/searchable/sortable) + the caller's
// effective bits.
func (a *API) handlePermsShow(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	o, ok := parseList(w, r, permsListSpec)
	if !ok {
		return
	}
	grants, total, err := a.ACL.ListByPath(r.Context(), p, o)
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
	setTotal(w, total)
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
	Expiry
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
	exp, err := b.Expiry.resolve()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	p, err := acl.Normalize(b.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	var pid, ptid int64
	switch b.PrincipalType {
	case "user":
		usr, err := a.Auth.UserByName(r.Context(), tenant, b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_user", tenant+"/"+b.Principal)
			return
		}
		pid, ptid = usr.ID, usr.TenantID
	case "group":
		g, err := a.Auth.GroupByName(r.Context(), tenant, b.Principal)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_such_group", tenant+"/"+b.Principal)
			return
		}
		pid, ptid = g.ID, g.TenantID
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "principal_type must be user or group")
		return
	}
	gr, err := a.ACL.Upsert(r.Context(), p, b.PrincipalType, pid, b.Read, b.Write, b.Delete, exp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	details := map[string]any{"principal_type": b.PrincipalType,
		"read": b.Read, "write": b.Write, "delete": b.Delete}
	if exp != nil {
		details["expires_at"] = exp.Format(time.RFC3339)
	}
	a.audit(r, audit.Event{Action: "perm_set", TenantID: ptid, TargetType: "grant",
		TargetID: gr.ID, TargetName: tenant + "/" + b.Principal, Path: p, Details: details})
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
	a.audit(r, audit.Event{Action: "perm_delete", TenantID: gr.PrincipalTID, TargetType: "grant",
		TargetID: id, TargetName: gr.PrincipalTenant + "/" + gr.PrincipalName, Path: gr.Path,
		Details: map[string]any{"principal_type": gr.PrincipalType,
			"read": gr.Read, "write": gr.Write, "delete": gr.Delete}}) // the revoked bits, for the record
	w.WriteHeader(http.StatusNoContent)
}

// ---- users ----

type userBody struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Tenant      string `json:"tenant"` // required for super admins; ignored for tenant admins
	Description string `json:"description"`
	IsAdmin     bool   `json:"is_admin"`
	Expiry      // expires_days / expires_at: customer accounts with a lifespan
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

var usersListSpec = ListSpec{
	Sorts:        map[string]bool{"username": true, "tenant": true, "created": true, "active": true, "description": true},
	Cols:         []string{"username", "tenant", "description"},
	DefSort:      "username",
	ActiveFilter: true,
	TenantFilter: true,
}

func (a *API) handleUsersList(w http.ResponseWriter, r *http.Request) {
	o, ok := parseList(w, r, usersListSpec)
	if !ok {
		return
	}
	users, total, err := a.Auth.ListUsers(r.Context(), scopeTenantID(userFrom(r.Context())), o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	setTotal(w, total)
	writeJSON(w, http.StatusOK, users)
}

func (a *API) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var b userBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	if b.Username == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "username required")
		return
	}
	// empty password = device user: no interactive login, only auth_factors
	u := userFrom(r.Context())
	exp, err := b.Expiry.resolve()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	nu, err := a.Auth.CreateUser(r.Context(), resolveCreateTenant(u, b.Tenant), b.Username, b.Password, strings.TrimSpace(b.Description), b.IsAdmin, false, exp)
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "username taken, unknown tenant, or invalid input")
		return
	}
	details := map[string]any{"is_admin": b.IsAdmin, "device": b.Password == ""}
	if exp != nil {
		details["expires_at"] = exp.Format(time.RFC3339)
	}
	a.audit(r, audit.Event{Action: "user_create", TenantID: nu.TenantID,
		TargetType: "user", TargetID: nu.ID, TargetName: nu.Username, Details: details})
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
	a.audit(r, audit.Event{Action: "user_delete", TenantID: target.TenantID,
		TargetType: "user", TargetID: id, TargetName: target.Username,
		Details: map[string]any{"is_admin": target.IsAdmin}}) // names the victim, since the row is gone
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
	a.audit(r, audit.Event{Action: "user_password", TenantID: target.TenantID,
		TargetType: "user", TargetID: id, TargetName: target.Username}) // never the password itself
	w.WriteHeader(http.StatusNoContent)
}

type activeBody struct {
	Active bool `json:"active"`
}

// handleUsersActive: POST /api/v1/users/{id}/active {"active":bool} —
// suspend/reinstate a user. Suspension is a kill switch, not a deletion:
// row, grants, factors and history survive; login, live sessions and bearer
// keys stop working on the next request.
func (a *API) handleUsersActive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b activeBody
	if !a.decodeBody(w, r, &b) {
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
	// footgun guard: nobody suspends their own account; another admin of a
	// different... same... any admin can reinstate, but you cannot lock
	// yourself out by accident.
	if !b.Active && id == userFrom(r.Context()).ID {
		writeError(w, http.StatusUnprocessableEntity, "validation", "you cannot deactivate your own account")
		return
	}
	if err := a.Auth.SetUserActive(r.Context(), id, b.Active); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "user_active", TenantID: target.TenantID,
		TargetType: "user", TargetID: id, TargetName: target.Username,
		Details: map[string]any{"active": b.Active}})
	u, err := a.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleUsersUpdate: PUT /api/v1/users/{id} {"description":...}
func (a *API) handleUsersUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b descriptionBody
	if !a.decodeBody(w, r, &b) {
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
	if err := a.Auth.UpdateUser(r.Context(), id, strings.TrimSpace(b.Description)); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "user_update", TenantID: target.TenantID,
		TargetType: "user", TargetID: id, TargetName: target.Username})
	w.WriteHeader(http.StatusNoContent)
}

// ---- auth factors (device/API-key auth; ed25519 + oauth2 reserved) ----

type factorBody struct {
	Method string `json:"method"` // 'apikey' for now
	Alg    string `json:"alg"`    // reserved: key algo or oauth2 provider (google, entra...)
	Label  string `json:"label"`  // optional, lets old+new keys coexist during rotation
	Expiry        // expires_days / expires_at: keys can lapse like customer accounts
}

// factorTarget loads the target user and enforces tenant scoping, like
// other user-management routes. The full user (not just the id) is needed
// for audit scoping and login-vs-device context in factor events.
func (a *API) factorTarget(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return auth.User{}, false
	}
	target, err := a.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return auth.User{}, false
	}
	if !a.targetInScope(w, r, target.TenantID, "user") {
		return auth.User{}, false
	}
	return target, true
}

func (a *API) handleFactorsList(w http.ResponseWriter, r *http.Request) {
	target, ok := a.factorTarget(w, r)
	if !ok {
		return
	}
	fs, err := a.Auth.ListFactors(r.Context(), target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fs)
}

func (a *API) handleFactorsCreate(w http.ResponseWriter, r *http.Request) {
	target, ok := a.factorTarget(w, r)
	if !ok {
		return
	}
	var b factorBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	switch b.Method {
	case "apikey":
	case "ed25519", "oauth2":
		writeError(w, http.StatusUnprocessableEntity, "not_implemented",
			"method "+b.Method+" is reserved for a future release")
		return
	default:
		writeError(w, http.StatusUnprocessableEntity, "validation", "unknown method "+b.Method)
		return
	}
	exp, err := b.Expiry.resolve()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	f, key, err := a.Auth.CreateAPIKey(r.Context(), target.ID, b.Label, exp)
	if err != nil {
		if errors.Is(err, auth.ErrFactorExists) {
			writeError(w, http.StatusConflict, "exists", "a key with that label already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	details := map[string]any{"method": b.Method, "label": b.Label}
	if exp != nil {
		details["expires_at"] = exp.Format(time.RFC3339)
	}
	// never the key itself: the hash is not stored plaintext either
	a.audit(r, audit.Event{Action: "factor_create", TenantID: target.TenantID,
		TargetType: "factor", TargetID: f.ID, TargetName: target.Username, Details: details})
	// plaintext key is shown exactly once; the DB keeps only its hash
	writeJSON(w, http.StatusCreated, struct {
		Factor auth.Factor `json:"factor"`
		Key    string      `json:"key"`
	}{f, key})
}

func (a *API) handleFactorsDelete(w http.ResponseWriter, r *http.Request) {
	target, ok := a.factorTarget(w, r)
	if !ok {
		return
	}
	fid, err := strconv.ParseInt(r.PathValue("fid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "fid must be numeric")
		return
	}
	if err := a.Auth.DeleteFactor(r.Context(), fid); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "factor_delete", TenantID: target.TenantID,
		TargetType: "factor", TargetID: fid, TargetName: target.Username})
	w.WriteHeader(http.StatusNoContent)
}

// ---- groups ----

type groupBody struct {
	Name        string `json:"name"`
	Tenant      string `json:"tenant"` // required for super admins; ignored for tenant admins
	Description string `json:"description"`
}

var groupsListSpec = ListSpec{
	Sorts:        map[string]bool{"name": true, "tenant": true, "active": true, "description": true},
	Cols:         []string{"name", "tenant", "description"},
	DefSort:      "name",
	ActiveFilter: true,
	TenantFilter: true,
}

func (a *API) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	o, ok := parseList(w, r, groupsListSpec)
	if !ok {
		return
	}
	groups, total, err := a.Auth.ListGroups(r.Context(), scopeTenantID(userFrom(r.Context())), o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	setTotal(w, total)
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
	g, err := a.Auth.CreateGroup(r.Context(), resolveCreateTenant(u, b.Tenant), b.Name, strings.TrimSpace(b.Description))
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "group name taken in tenant, or unknown tenant")
		return
	}
	a.audit(r, audit.Event{Action: "group_create", TenantID: g.TenantID,
		TargetType: "group", TargetID: g.ID, TargetName: g.Name})
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
	a.audit(r, audit.Event{Action: "group_delete", TenantID: g.TenantID,
		TargetType: "group", TargetID: id, TargetName: g.Name,
		Details: map[string]any{"members": len(g.Members)}}) // names the victim, since the row is gone
	w.WriteHeader(http.StatusNoContent)
}

// handleGroupsActive: POST /api/v1/groups/{id}/active {"active":bool} —
// suspend/reinstate a group. While inactive every grant through it is
// ignored (auth.GroupIDsFor filters it out), exactly like an expired grant,
// but the row, its members and its grants survive for reinstatement.
func (a *API) handleGroupsActive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b activeBody
	if !a.decodeBody(w, r, &b) {
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
	if err := a.Auth.SetGroupActive(r.Context(), id, b.Active); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "group_active", TenantID: g.TenantID,
		TargetType: "group", TargetID: id, TargetName: g.Name,
		Details: map[string]any{"active": b.Active}})
	up, err := a.Auth.GroupByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, up)
}

// handleGroupsUpdate: PUT /api/v1/groups/{id} {"description":...}
func (a *API) handleGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	var b descriptionBody
	if !a.decodeBody(w, r, &b) {
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
	if err := a.Auth.UpdateGroup(r.Context(), id, strings.TrimSpace(b.Description)); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "group_update", TenantID: g.TenantID,
		TargetType: "group", TargetID: id, TargetName: g.Name})
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
	a.audit(r, audit.Event{Action: "member_add", TenantID: g.TenantID,
		TargetType: "member", TargetID: u.ID, TargetName: u.Username,
		Details: map[string]any{"group": g.Name, "group_id": gid}})
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
	victim := "" // snapshot for the record; the membership row is about to go
	if mv, err := a.Auth.UserByID(r.Context(), uid); err == nil {
		victim = mv.Username
	}
	if err := a.Auth.RemoveMember(r.Context(), gid, uid); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	a.audit(r, audit.Event{Action: "member_remove", TenantID: g.TenantID,
		TargetType: "member", TargetID: uid, TargetName: victim,
		Details: map[string]any{"group": g.Name, "group_id": gid}})
	w.WriteHeader(http.StatusNoContent)
}

package handlers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
	"github.com/GssMahadevan/gvn-explorer/internal/query"
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
	if !auth.HomeSafe(b.Name) {
		writeError(w, http.StatusUnprocessableEntity, "validation",
			"name must be 1-32 chars of lowercase letters, digits or dashes (a-z, 0-9, -), start with a letter or digit, and not one of the reserved words help, tenants")
		return
	}
	t, err := a.Auth.CreateTenant(r.Context(), b.Name, strings.TrimSpace(b.Description))
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "tenant name taken")
		return
	}
	if err := a.provisionHome(r.Context(), t.Name, t.ID); err != nil {
		// The tenant row exists; the home is repairable (the startup
		// backfill finishes it), so this is a loud log, not a rollback.
		slog.Error("home provisioning", "tenant", t.Name, "err", err)
	}
	a.audit(r, audit.Event{Action: "tenant_create", TenantID: t.ID,
		TargetType: "tenant", TargetID: t.ID, TargetName: t.Name})
	writeJSON(w, http.StatusCreated, t)
}

// provisionHome: every tenant owns the directory /tenants/{name} under
// the reserved home namespace. Creating a tenant creates it, plus one
// ordinary visible grant: the tenant's members get r on their home — by
// the usual inheritance that covers the whole home subtree, and it is the
// only membership default there is. The grant is provisioned exactly
// once — an admin who revokes it stays revoked. The hard "only members,
// ever" boundary lives in acl.Effective (HomeOwner), not in these rows:
// the rows only decide what members get. provisioning a home also
// guarantees the /tenants namespace exists and is enterable by everyone
// (a plain world read row, so members can reach their own home through
// the tree; the list filter then shows each user only what they may see).
func (a *API) provisionHome(ctx context.Context, name string, tid int64) error {
	return a.provisionHomeACL(ctx, name, tid, a.ACL)
}

// provisionHomeACL takes the ACL store to write on, so import-apply can
// provision a tenant doc's home inside its transaction. The directory
// itself is a filesystem effect and does not roll back — harmless, since
// Mkdir is idempotent and the next apply or restart sees the same state.
func (a *API) provisionHomeACL(ctx context.Context, name string, tid int64, ACL *acl.Store) error {
	if err := a.ensureHomeRoot(ctx, ACL); err != nil {
		return err
	}
	if err := a.FS.Mkdir(auth.HomePath(name)); err != nil && !errors.Is(err, fsx.ErrExists) {
		return err
	}
	return ACL.Ensure(ctx, auth.HomePath(name), "tenant", tid, true, false, false)
}

// ensureHomeRoot creates /tenants once and gives the world principal a
// read row on it exactly once — the same provision-never-re-impose rule
// as home defaults: a super who deletes that row stays deleted (members
// then need deep links or their own grants to get in).
func (a *API) ensureHomeRoot(ctx context.Context, ACL *acl.Store) error {
	_, err := a.FS.Stat(auth.HomeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		if err := a.FS.Mkdir(auth.HomeRoot); err != nil && !errors.Is(err, fsx.ErrExists) {
			return err
		}
		return ACL.Ensure(ctx, auth.HomeRoot, "all", 0, true, false, false)
	}
	return err
}

// migrateHome: one-time upgrade for instances created before homes moved
// under /tenants. If the legacy /{name} directory exists and the new
// location does not, rename it and rewrite the path prefix of everything
// keyed to it: grant rows and file drafts. Events keep their historical
// paths — the audit trail is history, not state. If both locations exist
// nothing is merged or clobbered: say so loudly and let an admin resolve
// it. Returns true if the home was moved (caller then treats the new
// location as already-provisioned).
func (a *API) migrateHome(ctx context.Context, name string) (bool, error) {
	legacy, home := "/"+name, auth.HomePath(name)
	if _, err := a.FS.Stat(legacy); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	_, newErr := a.FS.Stat(home)
	if newErr == nil {
		slog.Warn("both the legacy home and the new one exist; not merging, review by hand",
			"legacy", legacy, "home", home)
		return false, nil
	}
	if !errors.Is(newErr, fs.ErrNotExist) {
		return false, newErr
	}
	if err := a.FS.Rename(legacy, home); err != nil {
		return false, err
	}
	grants, err := a.ACL.RehomePrefix(ctx, legacy, home)
	if err != nil {
		return false, err
	}
	drafts, err := a.Drafts.RehomePrefix(ctx, legacy, home)
	if err != nil {
		return false, err
	}
	slog.Info("moved tenant home to the /tenants namespace",
		"tenant", name, "from", legacy, "to", home,
		"grants_rewritten", grants, "drafts_rewritten", drafts)
	return true, nil
}

// BootstrapHomes: called once at server start. Ensures the /tenants
// namespace exists, migrates any pre-namespace homes, then gives every
// existing tenant whose name can back a directory a home plus its default
// browse grant, if it has none yet (fresh installs: zero work; upgrades
// of older servers: the backfill). There is deliberately no world grant
// on "/": nearest-ancestor inheritance would make that read every
// shared-zone file for every user and break the deny-by-default core of
// the model. The default view comes from the home grant itself (members
// can list and read their own tree), and shared-zone openness is
// whatever admins grant to the "all" principal per path, one explicit
// row at a time.
func (a *API) BootstrapHomes(ctx context.Context) error {
	if err := a.ensureHomeRoot(ctx, a.ACL); err != nil {
		return err
	}
	tenants, _, err := a.Auth.ListTenants(ctx, query.List{Sort: "name"})
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if !auth.HomeSafe(t.Name) {
			slog.Warn("tenant name cannot back a home directory; leaving it unpartitioned", "tenant", t.Name)
			continue
		}
		if _, err := a.migrateHome(ctx, t.Name); err != nil {
			return err
		}
		home := auth.HomePath(t.Name)
		if _, err := a.FS.Stat(home); err == nil {
			// Home already provisioned once — never re-impose defaults an
			// admin may have revoked. But the partition rule is row-based,
			// so /tenants/{name} is a home the moment the tenant row
			// exists: say so if members cannot browse it, because that is
			// surprising and easily fixed.
			if has, err := a.ACL.HasGrant(ctx, home, "tenant", t.ID); err == nil && !has {
				slog.Warn("home is partitioned but no member grant exists: grant r to the tenant principal to let members browse it",
					"tenant", t.Name, "path", home)
			}
			continue
		}
		if err := a.provisionHome(ctx, t.Name, t.ID); err != nil {
			return err
		}
	}
	return nil
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
	ExpiresDays *float64 `json:"expires_days,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
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
	if isHelpPath(p) {
		// built-in docs tree: readable by everyone, grants never apply
		setTotal(w, 0)
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "grants": []any{}, "effective": acl.Bits{Read: true}})
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

// principalProblem resolves a grant request to principal ids without
// writing anything: code/msg describe why it fails ("" = ok). Every
// grant path must share these checks — tenant scoping, pseudo-principals,
// reserved ids — so a template can never smuggle in a grant a
// hand-entered one would reject.
func (a *API) principalProblem(ctx context.Context, u auth.User, tenant, ptype, name string) (pid, ptid int64, code, msg string) {
	switch ptype {
	case "user":
		usr, err := a.Auth.UserByName(ctx, tenant, name)
		if errors.Is(err, auth.ErrNotFound) {
			return 0, 0, "no_such_user", tenant + "/" + name
		}
		if err != nil {
			return 0, 0, "internal", err.Error()
		}
		pid, ptid = usr.ID, usr.TenantID
	case "group":
		g, err := a.Auth.GroupByName(ctx, tenant, name)
		if errors.Is(err, auth.ErrNotFound) {
			return 0, 0, "no_such_group", tenant + "/" + name
		}
		if err != nil {
			return 0, 0, "internal", err.Error()
		}
		pid, ptid = g.ID, g.TenantID
	case "tenant":
		// "any user in tenant T": like user/group principals, a tenant
		// admin may only target their own tenant; cross-tenant rows are
		// super's prerogative (and never inside a home — see homeGrantOK).
		if !u.IsSuper && !strings.EqualFold(name, u.Tenant) {
			return 0, 0, "forbidden", "grants may only target your own tenant"
		}
		tid, err := a.Auth.TenantID(ctx, name)
		if err != nil {
			return 0, 0, "no_such_tenant", name
		}
		pid, ptid = tid, tid
	case "all":
		// world rows: every authenticated user. Reserved principal_id 0
		// (no entity has id 0). Allowed on shared-zone paths for any
		// admin who can manage them; refused inside homes (see below).
		pid, ptid = 0, 0
	default:
		return 0, 0, "bad_request", "principal_type must be user, group, tenant or all"
	}
	return pid, ptid, "", ""
}

// resolvePrincipal is the request-writing wrapper of principalProblem,
// used by PUT /perms where one bad grant answers the whole request.
func (a *API) resolvePrincipal(w http.ResponseWriter, r *http.Request, u auth.User, tenant, ptype, name string) (pid, ptid int64, ok bool) {
	pid, ptid, code, msg := a.principalProblem(r.Context(), u, tenant, ptype, name)
	if code != "" {
		status := map[string]int{"no_such_user": http.StatusNotFound, "no_such_group": http.StatusNotFound,
			"no_such_tenant": http.StatusNotFound, "forbidden": http.StatusForbidden,
			"bad_request": http.StatusBadRequest, "internal": http.StatusInternalServerError}[code]
		writeError(w, status, code, msg)
		return 0, 0, false
	}
	return pid, ptid, true
}

// homeProblem reports why a grant at path p breaks the tenant-home
// partition for a principal of tenant ptid ("" = ok). The check runs
// for one-off grants and imported template rows alike, so the partition
// in acl.Effective can never be contradicted by what an import writes.
func (a *API) homeProblem(ctx context.Context, p string, ptid int64) (code, msg string) {
	owner, ownerID, err := a.ACL.HomeOwner(ctx, p)
	if err != nil {
		return "internal", err.Error()
	}
	if owner != "" && ptid != ownerID {
		return "validation", "grants inside the tenant home " + auth.HomePath(owner) + " may only target that tenant's members"
	}
	return "", ""
}

// homeGrantOK is the request-writing wrapper of homeProblem.
func (a *API) homeGrantOK(w http.ResponseWriter, r *http.Request, p string, ptid int64) bool {
	code, msg := a.homeProblem(r.Context(), p, ptid)
	if code != "" {
		status := map[string]int{"internal": http.StatusInternalServerError, "validation": http.StatusUnprocessableEntity}[code]
		writeError(w, status, code, msg)
		return false
	}
	return true
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
	if isHelpPath(p) {
		helpReadOnly(w)
		return
	}
	pid, ptid, ok := a.resolvePrincipal(w, r, u, tenant, b.PrincipalType, b.Principal)
	if !ok {
		return
	}
	if b.PrincipalType == "tenant" {
		tenant = b.Principal
	}
	// Tenant homes are for members — absolute, super included. Inside
	// /{T} every principal must be a member of T: a user/group row from T,
	// or the tenant principal T itself. "all" and foreign principals are
	// refused here so the partition in acl.Effective can never be
	// contradicted by a grant an admin creates.
	if !a.homeGrantOK(w, r, p, ptid) {
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
	targetName := tenant + "/" + b.Principal
	switch b.PrincipalType {
	case "all":
		targetName = "all authenticated users"
		gr.PrincipalName, gr.PrincipalTenant = "*", "*"
	case "tenant":
		targetName = "tenant:" + b.Principal
	}
	a.audit(r, audit.Event{Action: "perm_set", TenantID: ptid, TargetType: "grant",
		TargetID: gr.ID, TargetName: targetName, Path: p, Details: details})
	if b.PrincipalType != "all" {
		gr.PrincipalTenant = tenant
		gr.PrincipalName = b.Principal
	}
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
	Expiry             // expires_days / expires_at: customer accounts with a lifespan
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

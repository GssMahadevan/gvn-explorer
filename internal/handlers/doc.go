package handlers

// Entity and ACL documents: one JSON envelope stating an entity and the
// ACLs that concern it — the "related info" a row's menu copies out, and
// the same shape applied back as a reusable template. kind+version keep
// the format open-ended.
//
// The rules the format exists to enforce:
//   - names, never database ids — a document exported on one server
//     means the same thing on another;
//   - no secrets, ever — imported users land as device users (empty
//     password, the same convention POST /users uses for key-only
//     accounts), an admin or pairing flow gives them a login;
//   - validate-first imports: unknown principals fail the whole request
//     with every name listed — nothing is half-applied, and principals
//     are never created as a side effect (the document's own subject
//     excepted);
//   - mode merge upserts only what the document states; mode replace
//     additionally deletes exactly the rows the target carries that the
//     document omits. Inherited ACLs are never touched by either —
//     nearest-ancestor resolution is the recursion; documents move
//     explicit grants, nothing else.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
)

const docVersion = 1

// docGrant is one grant line — the same shape PUT /perms accepts, so
// hand-written templates and exported ones speak one language. acl/dir
// documents carry one path for all lines; entity documents place their
// grants across the tree, so those lines carry their own path.
type docGrant struct {
	Path          string `json:"path,omitempty"`
	PrincipalType string `json:"principal_type"`
	Principal     string `json:"principal"`
	Tenant        string `json:"tenant,omitempty"` // defaults to the caller's
	Read          bool   `json:"read"`
	Write         bool   `json:"write"`
	Delete        bool   `json:"delete"`
	Expiry
}

// docAcl covers kind "acl" (the grants on a path) and kind "dir" (the
// same, but importing first creates the directory).
type docAcl struct {
	Kind    string     `json:"kind"`
	Version int        `json:"version"`
	Path    string     `json:"path"`
	Grants  []docGrant `json:"grants"`
}

type docUser struct {
	Kind        string     `json:"kind"`
	Version     int        `json:"version"`
	Tenant      string     `json:"tenant"`
	Username    string     `json:"username"`
	Description string     `json:"description,omitempty"`
	IsAdmin     bool       `json:"is_admin,omitempty"`
	IsSuper     bool       `json:"is_super,omitempty"`
	Active      *bool      `json:"active,omitempty"` // nil = default (active); documents state omissions, not falses
	Expiry
	MemberOf []string   `json:"member_of,omitempty"`
	Grants   []docGrant `json:"grants"`
}

type docGroup struct {
	Kind        string     `json:"kind"`
	Version     int        `json:"version"`
	Tenant      string     `json:"tenant"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Active      *bool      `json:"active,omitempty"` // see docUser.Active
	Members     []string   `json:"members,omitempty"`
	Grants      []docGrant `json:"grants"`
}

type docTenant struct {
	Kind        string     `json:"kind"`
	Version     int        `json:"version"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Active      *bool      `json:"active,omitempty"` // see docUser.Active
	Grants      []docGrant `json:"grants"`
}

// grantSpecs turns stored grant rows into document lines (labels come
// from the same joins the /perms listing already runs). Entity documents
// place their grants across the tree, so withPaths carries each row's
// path into the line; path documents state one path on the document and
// need none per line.
func grantSpecs(gs []acl.Grant, withPaths bool) []docGrant {
	out := make([]docGrant, 0, len(gs))
	for _, g := range gs {
		s := docGrant{
			PrincipalType: g.PrincipalType,
			Principal:     g.PrincipalName,
			Tenant:        g.PrincipalTenant,
			Read:          g.Read,
			Write:         g.Write,
			Delete:        g.Delete,
		}
		if withPaths {
			s.Path = g.Path
		}
		if g.ExpiresAt != nil {
			s.ExpiresAt = g.ExpiresAt.UTC().Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out
}

// splitTN parses a "tenant/name" selector; a bare name is the caller's
// tenant, mirroring how grant bodies default.
func splitTN(u auth.User, s string) (string, string, bool) {
	if t, n, ok := strings.Cut(s, "/"); ok && t != "" && n != "" {
		return t, n, true
	}
	if s == "" {
		return "", "", false
	}
	return u.Tenant, s, true
}

// scopeCheck rejects cross-tenant reads for tenant admins — the same
// boundary every admin listing draws.
func scopeCheck(w http.ResponseWriter, u auth.User, tenant string) bool {
	if !u.IsSuper && !strings.EqualFold(tenant, u.Tenant) {
		writeError(w, http.StatusForbidden, "forbidden", "documents only cover your own tenant")
		return false
	}
	return true
}

// ---- export ----

// handleExport: GET /api/v1/export?path=… | ?user=t/u | ?group=t/g | ?tenant=t
// One canonical document per entity. path documents need only the read
// view every logged-in user already has (GET /perms); entity documents
// follow the panel scoping: tenant admins see their tenant, tenant
// documents are super's.
func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sel := 0
	for _, k := range []string{"path", "user", "group", "tenant"} {
		if q.Get(k) != "" {
			sel++
		}
	}
	if sel != 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "exactly one of path, user, group, tenant")
		return
	}
	u := userFrom(r.Context())

	switch {
	case q.Get("path") != "":
		p, err := acl.Normalize(q.Get("path"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_path", err.Error())
			return
		}
		var grants []acl.Grant
		if !isHelpPath(p) {
			grants, err = a.ACL.ListAllByPath(r.Context(), p)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, docAcl{Kind: "acl", Version: docVersion, Path: p, Grants: grantSpecs(grants, false)})

	case q.Get("user") != "":
		tenant, name, ok := splitTN(u, q.Get("user"))
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "user selector is empty")
			return
		}
		if !scopeCheck(w, u, tenant) {
			return
		}
		usr, err := a.Auth.UserByName(r.Context(), tenant, name)
		if err != nil {
			writeError(w, http.StatusNotFound, "no_such_user", tenant+"/"+name)
			return
		}
		memberOf, err := a.Auth.GroupNamesFor(r.Context(), usr.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		grants, err := a.ACL.GrantsFor(r.Context(), "user", usr.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		d := docUser{Kind: "user", Version: docVersion, Tenant: usr.Tenant, Username: usr.Username,
			Description: usr.Description, IsAdmin: usr.IsAdmin, IsSuper: usr.IsSuper,
			Active: boolPtr(usr.Active), MemberOf: memberOf, Grants: grantSpecs(grants, true)}
		if usr.ExpiresAt != nil {
			d.ExpiresAt = usr.ExpiresAt.UTC().Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, d)

	case q.Get("group") != "":
		tenant, name, ok := splitTN(u, q.Get("group"))
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "group selector is empty")
			return
		}
		if !scopeCheck(w, u, tenant) {
			return
		}
		g, err := a.Auth.GroupByName(r.Context(), tenant, name)
		if err != nil {
			writeError(w, http.StatusNotFound, "no_such_group", tenant+"/"+name)
			return
		}
		members, err := a.Auth.GroupMembers(r.Context(), g.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Username)
		}
		grants, err := a.ACL.GrantsFor(r.Context(), "group", g.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, docGroup{Kind: "group", Version: docVersion, Tenant: g.Tenant,
			Name: g.Name, Description: g.Description, Active: boolPtr(g.Active),
			Members: names, Grants: grantSpecs(grants, true)})

	default: // tenant
		if !u.IsSuper {
			writeError(w, http.StatusForbidden, "forbidden", "tenant documents are for super admins")
			return
		}
		name := q.Get("tenant")
		t, err := a.Auth.TenantByName(r.Context(), name)
		if err != nil {
			writeError(w, http.StatusNotFound, "no_such_tenant", name)
			return
		}
		grants, err := a.ACL.GrantsFor(r.Context(), "tenant", t.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, docTenant{Kind: "tenant", Version: docVersion, Name: t.Name,
			Description: t.Description, Active: boolPtr(t.Active), Grants: grantSpecs(grants, true)})
	}
}

// ---- import ----

// importReport is what one successful import says: what it created,
// what it wrote, and exactly which rows replace-mode removed.
type importReport struct {
	Kind        string   `json:"kind"`
	Mode        string   `json:"mode"`
	Path        string   `json:"path,omitempty"`
	Subject     string   `json:"subject,omitempty"`
	Created     bool     `json:"created,omitempty"`
	DeviceUser  bool     `json:"device_user,omitempty"`
	Memberships int      `json:"memberships,omitempty"`
	Applied     int      `json:"applied"`
	Removed     []string `json:"removed,omitempty"`
}

type resolvedGrant struct {
	spec docGrant
	p    string
	pid  int64
	ptid int64
	exp  *time.Time
	self bool // line names the document's own subject; pid filled at apply
}

// docSubject identifies who a document is about. Its grant lines may
// legitimately name the subject itself — the one principal an import is
// allowed to create — so those lines validate before the subject exists
// (id 0 = not yet created; a placeholder is used and fixSelf fills the
// real id once creation has happened).
type docSubject struct {
	ptype, tenant, name string
	id, tid             int64
}

// fixSelf rewrites placeholder pids once the subject's id is known.
func fixSelf(rg []resolvedGrant, id int64) {
	for i := range rg {
		if rg[i].self {
			rg[i].pid = id
		}
	}
}

// isSelf reports whether a grant line names the subject. Names compare
// exactly (they came from an export of this very entity, or a
// hand-written doc that must match one); tenant defaults or "*" count
// as "the document's tenant".
func (d *docSubject) isSelf(s docGrant) bool {
	return d != nil && s.PrincipalType == d.ptype && s.Principal == d.name &&
		(s.Tenant == "" || s.Tenant == "*" || strings.EqualFold(s.Tenant, d.tenant))
}

// resolveDocGrants validates every line of a document through exactly
// the rules a hand-entered grant passes — principal existence, tenant
// scoping, expiry syntax, path syntax, the home partition — but
// quietly: all problems come back together, so one request reports the
// whole list and nothing is half-written.
func (a *API) resolveDocGrants(r *http.Request, u auth.User, docPath string, specs []docGrant, self *docSubject) ([]resolvedGrant, []string) {
	var out []resolvedGrant
	var problems []string
	for i, s := range specs {
		p := s.Path
		if docPath != "" {
			p = docPath
		}
		np, err := acl.Normalize(p)
		if err != nil {
			problems = append(problems, specLabel(i, s)+" path: "+err.Error())
			continue
		}
		if isHelpPath(np) {
			problems = append(problems, specLabel(i, s)+" path: the built-in /help tree takes no grants")
			continue
		}
		tenant := s.Tenant
		if tenant == "" || tenant == "*" {
			tenant = u.Tenant
		}
		switch s.PrincipalType {
		case "user", "group":
			if !u.IsSuper && !strings.EqualFold(tenant, u.Tenant) {
				problems = append(problems, specLabel(i, s)+": grants may only target your own tenant")
				continue
			}
		}
		pid, ptid, code, msg := int64(0), int64(0), "", ""
		if self.isSelf(s) {
			// The document's own subject: the only principal allowed to
			// be new. No real entity has id 0, so 0 doubles as the
			// not-yet-created placeholder; fixSelf fills it after create.
			pid, ptid = self.id, self.tid
		} else {
			pid, ptid, code, msg = a.principalProblem(r.Context(), u, tenant, s.PrincipalType, s.Principal)
		}
		if code != "" {
			problems = append(problems, specLabel(i, s)+": "+code+" "+msg)
			continue
		}
		exp, err := s.Expiry.resolve()
		if err != nil {
			problems = append(problems, specLabel(i, s)+" expiry: "+err.Error())
			continue
		}
		if code, msg := a.homeProblem(r.Context(), np, ptid); code != "" {
			problems = append(problems, specLabel(i, s)+": "+msg)
			continue
		}
		out = append(out, resolvedGrant{spec: s, p: np, pid: pid, ptid: ptid, exp: exp, self: self.isSelf(s)})
	}
	return out, problems
}

func specLabel(i int, s docGrant) string {
	return "grant " + strconv.Itoa(i+1) + " (" + s.PrincipalType + " " + s.Tenant + "/" + s.Principal + ")"
}

// applyResolved writes the document's grants and, in replace mode,
// removes the target's rows the document omits. current is the target's
// present state (path rows or subject-naming rows, depending on kind).
func (a *API) applyResolved(r *http.Request, rg []resolvedGrant, mode string, current []acl.Grant) (applied int, removed []string, err error) {
	if mode == "replace" {
		keep := map[acl.Grant]bool{} // identity by (path, type, pid); the zero fields don't matter
		for _, g := range rg {
			keep[acl.Grant{Path: g.p, PrincipalType: g.spec.PrincipalType, PrincipalID: g.pid}] = true
		}
		seen := map[acl.Grant]bool{}
		for _, g := range current {
			k := acl.Grant{Path: g.Path, PrincipalType: g.PrincipalType, PrincipalID: g.PrincipalID}
			if keep[k] || seen[k] {
				continue
			}
			seen[k] = true
			if err := a.ACL.Delete(r.Context(), g.ID); err != nil {
				return applied, removed, err
			}
			label := g.PrincipalType + " " + g.PrincipalTenant + "/" + g.PrincipalName
			if g.PrincipalType == "all" {
				label = "all authenticated users"
			}
			removed = append(removed, label+" @ "+g.Path)
		}
	}
	for _, g := range rg {
		if _, err := a.ACL.Upsert(r.Context(), g.p, g.spec.PrincipalType, g.pid,
			g.spec.Read, g.spec.Write, g.spec.Delete, g.exp); err != nil {
			return applied, removed, err
		}
		applied++
	}
	return applied, removed, nil
}

// handleImport: POST /api/v1/import {"doc": …, "mode": "merge"|"replace"}
// Admin-scoped like the mutations its contents mirror. One audit event
// per call (import_apply) plus the canonical create events for any
// subject the document newly brings into existence — history reads the
// same however an entity arrived.
func (a *API) handleImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string          `json:"mode"`
		Doc  json.RawMessage `json:"doc"`
	}
	if !a.decodeBody(w, r, &body) {
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		writeError(w, http.StatusBadRequest, "bad_request", `mode must be "merge" or "replace"`)
		return
	}
	var head struct {
		Kind    string `json:"kind"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(body.Doc, &head); err != nil {
		writeError(w, http.StatusBadRequest, "bad_doc", "doc must be a JSON object")
		return
	}
	if head.Version > docVersion {
		writeError(w, http.StatusUnprocessableEntity, "validation",
			"document version is newer than this server understands")
		return
	}
	switch head.Kind {
	case "acl", "dir":
		var d docAcl
		if err := json.Unmarshal(body.Doc, &d); err != nil {
			badDoc(w, err)
			return
		}
		a.importAcl(w, r, d, mode)
	case "user":
		var d docUser
		if err := json.Unmarshal(body.Doc, &d); err != nil {
			badDoc(w, err)
			return
		}
		a.importUser(w, r, d, mode)
	case "group":
		var d docGroup
		if err := json.Unmarshal(body.Doc, &d); err != nil {
			badDoc(w, err)
			return
		}
		a.importGroup(w, r, d, mode)
	case "tenant":
		var d docTenant
		if err := json.Unmarshal(body.Doc, &d); err != nil {
			badDoc(w, err)
			return
		}
		a.importTenant(w, r, d, mode)
	default:
		writeError(w, http.StatusUnprocessableEntity, "validation",
			"kind must be acl, dir, user, group or tenant")
	}
}

func badDoc(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, "bad_doc", "doc does not match its kind: "+err.Error())
}

func (a *API) importAcl(w http.ResponseWriter, r *http.Request, d docAcl, mode string) {
	u := userFrom(r.Context())
	p, err := acl.Normalize(d.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if isHelpPath(p) {
		helpReadOnly(w)
		return
	}
	rg, problems := a.resolveDocGrants(r, u, d.Path, d.Grants, nil)
	if len(problems) > 0 {
		a.reportProblems(w, r, d.Kind, p, problems)
		return
	}
	if d.Kind == "dir" && p != "/" {
		// A dir document means "this structure plus these ACLs" —
		// create the whole chain, shallowest first, so a template like
		// /shared/templates works in one application.
		chain := acl.Ancestors(p) // deepest → "/"
		for i := len(chain) - 1; i >= 0; i-- {
			d2 := chain[i]
			if d2 == "/" {
				continue
			}
			if err := a.FS.Mkdir(d2); err != nil && !errors.Is(err, fsx.ErrExists) {
				if !mapFsErr(w, err) {
					writeError(w, http.StatusInternalServerError, "internal", err.Error())
				}
				return
			}
		}
	}
	current, err := a.ACL.ListAllByPath(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	applied, removed, err := a.applyResolved(r, rg, mode, current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	a.auditImport(r, d.Kind, p, "", applied, removed)
	writeJSON(w, http.StatusOK, importReport{Kind: d.Kind, Mode: mode, Path: p,
		Applied: applied, Removed: removed})
}

func (a *API) importUser(w http.ResponseWriter, r *http.Request, d docUser, mode string) {
	u := userFrom(r.Context())
	if d.Username == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "username required")
		return
	}
	// an omitted (or "*") tenant is the applying account's own — the same
	// default the grant lines inside the document use.
	if d.Tenant == "" || d.Tenant == "*" {
		d.Tenant = u.Tenant
	}
	if !scopeCheck(w, u, d.Tenant) {
		return
	}
	tid, err := a.Auth.TenantID(r.Context(), d.Tenant)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", "no such tenant: "+d.Tenant)
		return
	}
	exp, err := d.Expiry.resolve()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	// Read the subject before validating: a document may name itself in
	// its grant lines, and those must check out whether or not the
	// subject exists yet (see docSubject).
	subject, err := a.Auth.UserByName(r.Context(), d.Tenant, d.Username)
	self := &docSubject{ptype: "user", tenant: d.Tenant, name: d.Username, tid: tid}
	switch {
	case err == nil:
		self.id, self.tid = subject.ID, subject.TenantID
	case errors.Is(err, auth.ErrNotFound):
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Validate the whole document before creating anything: memberships
	// name groups that must already exist (identity creation stays
	// deliberate; only the document's own subject may be new).
	var groups []auth.Group
	var problems []string
	for _, name := range d.MemberOf {
		g, err := a.Auth.GroupByName(r.Context(), d.Tenant, name)
		if err != nil {
			problems = append(problems, "member_of group does not exist: "+d.Tenant+"/"+name)
			continue
		}
		groups = append(groups, g)
	}
	rg, ps := a.resolveDocGrants(r, u, "", d.Grants, self)
	problems = append(problems, ps...)
	if len(problems) > 0 {
		a.reportProblems(w, r, "user", "", problems)
		return
	}

	created := false
	if self.id == 0 {
		// No password in documents — imported accounts land as device
		// users (the existing key-only convention); an admin or a
		// pairing gives them a login.
		subject, err = a.Auth.CreateUser(r.Context(), d.Tenant, d.Username, "", d.Description,
			d.IsAdmin, d.IsSuper && u.IsSuper, exp)
		if err != nil {
			writeError(w, http.StatusConflict, "exists", "username taken or invalid input")
			return
		}
		fixSelf(rg, subject.ID)
		created = true
		if d.Active != nil && !*d.Active {
			if err := a.Auth.SetUserActive(r.Context(), subject.ID, false); err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
		}
		a.audit(r, audit.Event{Action: "user_create", TenantID: subject.TenantID,
			TargetType: "user", TargetID: subject.ID, TargetName: subject.Username,
			Details: map[string]any{"is_admin": subject.IsAdmin, "device": true, "via": "document"}})
	}

	memberships := 0
	for _, g := range groups {
		// Idempotent on re-apply: a membership the subject already has
		// is skipped, not a constraint error.
		isMember, err := a.Auth.IsMember(r.Context(), g.ID, subject.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if isMember {
			continue
		}
		if err := a.Auth.AddMember(r.Context(), g.ID, subject.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		memberships++
	}
	subjectGrants, err := a.ACL.GrantsFor(r.Context(), "user", subject.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	applied, removed, err := a.applyResolved(r, rg, mode, subjectGrants)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	subjectLabel := d.Tenant + "/" + d.Username
	a.auditImport(r, "user", "", subjectLabel, applied, removed)
	writeJSON(w, http.StatusOK, importReport{Kind: "user", Mode: mode, Subject: subjectLabel,
		Created: created, DeviceUser: created, Memberships: memberships,
		Applied: applied, Removed: removed})
}

func (a *API) importGroup(w http.ResponseWriter, r *http.Request, d docGroup, mode string) {
	u := userFrom(r.Context())
	if d.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "name required")
		return
	}
	if d.Tenant == "" || d.Tenant == "*" { // same default as grant lines and user docs
		d.Tenant = u.Tenant
	}
	if !scopeCheck(w, u, d.Tenant) {
		return
	}
	tid, err := a.Auth.TenantID(r.Context(), d.Tenant)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", "no such tenant: "+d.Tenant)
		return
	}
	// Read the subject before validating (see importUser).
	g, err := a.Auth.GroupByName(r.Context(), d.Tenant, d.Name)
	self := &docSubject{ptype: "group", tenant: d.Tenant, name: d.Name, tid: tid}
	switch {
	case err == nil:
		self.id, self.tid = g.ID, g.TenantID
	case errors.Is(err, auth.ErrNotFound):
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	var members []auth.User
	var problems []string
	for _, name := range d.Members {
		usr, err := a.Auth.UserByName(r.Context(), d.Tenant, name)
		if err != nil {
			problems = append(problems, "member does not exist: "+d.Tenant+"/"+name)
			continue
		}
		members = append(members, usr)
	}
	rg, ps := a.resolveDocGrants(r, u, "", d.Grants, self)
	problems = append(problems, ps...)
	if len(problems) > 0 {
		a.reportProblems(w, r, "group", "", problems)
		return
	}

	created := false
	if self.id == 0 {
		g, err = a.Auth.CreateGroup(r.Context(), d.Tenant, d.Name, d.Description)
		if err != nil {
			writeError(w, http.StatusConflict, "exists", "group name taken or invalid input")
			return
		}
		fixSelf(rg, g.ID)
		created = true
		if d.Active != nil && !*d.Active {
			if err := a.Auth.SetGroupActive(r.Context(), g.ID, false); err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
		}
		a.audit(r, audit.Event{Action: "group_create", TenantID: g.TenantID,
			TargetType: "group", TargetID: g.ID, TargetName: g.Name,
			Details: map[string]any{"via": "document"}})
	}
	added := 0
	for _, usr := range members {
		isMember, err := a.Auth.IsMember(r.Context(), g.ID, usr.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if isMember {
			continue
		}
		if err := a.Auth.AddMember(r.Context(), g.ID, usr.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		added++
	}
	subjectGrants, err := a.ACL.GrantsFor(r.Context(), "group", g.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	applied, removed, err := a.applyResolved(r, rg, mode, subjectGrants)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	subjectLabel := d.Tenant + "/" + d.Name
	a.auditImport(r, "group", "", subjectLabel, applied, removed)
	writeJSON(w, http.StatusOK, importReport{Kind: "group", Mode: mode, Subject: subjectLabel,
		Created: created, Memberships: added, Applied: applied, Removed: removed})
}

func (a *API) importTenant(w http.ResponseWriter, r *http.Request, d docTenant, mode string) {
	u := userFrom(r.Context())
	if d.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "name required")
		return
	}
	if !u.IsSuper {
		writeError(w, http.StatusForbidden, "forbidden", "tenant documents are for super admins")
		return
	}
	if !auth.HomeSafe(d.Name) {
		writeError(w, http.StatusUnprocessableEntity, "validation",
			"tenant names may only contain lowercase letters, digits and dashes")
		return
	}
	// Read the subject before validating (see importUser): a tenant doc's
	// grant lines naming the tenant itself are legitimate even when the
	// tenant does not exist yet — provisionHome provisions its home.
	tid, err := a.Auth.TenantID(r.Context(), d.Name)
	self := &docSubject{ptype: "tenant", tenant: d.Name, name: d.Name}
	if err == nil {
		self.id, self.tid = tid, tid
	}
	rg, problems := a.resolveDocGrants(r, u, "", d.Grants, self)
	if len(problems) > 0 {
		a.reportProblems(w, r, "tenant", "", problems)
		return
	}

	created := false
	if self.id == 0 {
		t, err := a.Auth.CreateTenant(r.Context(), d.Name, d.Description)
		if err != nil {
			writeError(w, http.StatusConflict, "exists", "tenant name taken or invalid input")
			return
		}
		tid, created = t.ID, true
		fixSelf(rg, t.ID)
		if d.Active != nil && !*d.Active {
			if err := a.Auth.SetTenantActive(r.Context(), t.ID, false); err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
		}
		// same follow-through as POST /tenants: the home directory and
		// its default browse grant come with the tenant.
		if err := a.provisionHome(r.Context(), t.Name, t.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		a.audit(r, audit.Event{Action: "tenant_create", TenantID: t.ID,
			TargetType: "tenant", TargetID: t.ID, TargetName: t.Name,
			Details: map[string]any{"via": "document"}})
	}
	subjectGrants, err := a.ACL.GrantsFor(r.Context(), "tenant", tid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	applied, removed, err := a.applyResolved(r, rg, mode, subjectGrants)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	a.auditImport(r, "tenant", "", "tenant:"+d.Name, applied, removed)
	writeJSON(w, http.StatusOK, importReport{Kind: "tenant", Mode: mode, Subject: "tenant:" + d.Name,
		Created: created, Applied: applied, Removed: removed})
}

// reportProblems fails the whole import with the complete list — the
// all-or-nothing contract of validate-first.
func (a *API) reportProblems(w http.ResponseWriter, r *http.Request, kind, path string, problems []string) {
	a.audit(r, audit.Event{Action: "import_apply", TenantID: scopeTenantID(userFrom(r.Context())),
		TargetType: "doc", TargetName: kind + " " + path,
		Details: map[string]any{"rejected": true, "problems": problems}})
	writeError(w, http.StatusUnprocessableEntity, "validation", strings.Join(problems, "; "))
}

// auditImport records one applied import. Details stay free of secrets
// like every other event; row-level history already exists per grant
// through normal audit if anyone needs it — documents deliberately do
// not double-record every grant.
func (a *API) auditImport(r *http.Request, kind, path, subject string, applied int, removed []string) {
	a.audit(r, audit.Event{Action: "import_apply", TenantID: scopeTenantID(userFrom(r.Context())),
		TargetType: "doc", TargetName: kind + " " + firstNonEmpty(subject, path),
		Path:      path,
		Details: map[string]any{"kind": kind, "applied": applied,
			"removed": len(removed), "removed_list": removed}})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func boolPtr(b bool) *bool { return &b }

// Package acl resolves platform-independent permissions: per-path grants to
// users/groups stored in SQLite, with nearest-ancestor inheritance.
package acl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/db"
	"github.com/GssMahadevan/gvn-explorer/internal/query"
)

type Grant struct {
	ID              int64      `json:"id"`
	Path            string     `json:"path"`
	PrincipalType   string     `json:"principal_type"` // "user" | "group" | "tenant" | "all"
	PrincipalID     int64      `json:"principal_id"`
	PrincipalName   string     `json:"principal_name,omitempty"`
	PrincipalTenant string     `json:"principal_tenant,omitempty"`
	PrincipalTID    int64      `json:"-"` // principal's tenant id (audit scoping)
	Read            bool       `json:"read"`
	Write           bool       `json:"write"`
	Delete          bool       `json:"delete"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"` // nil = never
	Expired         bool       `json:"expired"`              // expires_at in the past
	PrincipalActive bool       `json:"principal_active"`     // false = suspended principal, grant inert
}

// Bits is the effective permission set for one path.
type Bits struct {
	Read   bool   `json:"read"`
	Write  bool   `json:"write"`
	Delete bool   `json:"delete"`
	Source string `json:"source,omitempty"` // path the grant was found at ("" = admin)
}

// Normalize turns any client-supplied path into the canonical logical form:
// "/" for root, no trailing slash, no ".", "..", or backslashes.
// Returns an error for anything that tries to escape the root.
func Normalize(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	var out []string
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("path must not contain '..'")
		default:
			if strings.ContainsRune(part, 0) {
				return "", fmt.Errorf("invalid path")
			}
			out = append(out, part)
		}
	}
	return "/" + strings.Join(out, "/"), nil
}

// Ancestors lists path and each of its parents, deepest first:
// "/a/b/c" -> ["/a/b/c", "/a/b", "/a", "/"].
func Ancestors(path string) []string {
	var out []string
	for {
		out = append(out, path)
		if path == "/" {
			return out
		}
		path = path[:strings.LastIndex(path, "/")]
		if path == "" {
			path = "/"
		}
	}
}

type Store struct{ DB db.Execer }

func NewStore(conn *db.DB) *Store { return &Store{DB: conn} }

// WithTx returns a store whose queries run inside tx — used to make
// multi-write operations (import apply) atomic.
func (s *Store) WithTx(tx *db.Tx) *Store { c := *s; c.DB = tx; return &c }

// Upsert replaces any existing grant for (path, principal) with the new bits
// and expiry. expiresAt nil = grant never expires.
func (s *Store) Upsert(ctx context.Context, path, ptype string, pid int64, r, w, d bool, expiresAt *time.Time) (Grant, error) {
	ri, wi, di := b2i(r), b2i(w), b2i(d)
	var exp any // NULL when nil
	if expiresAt != nil {
		exp = expiresAt.Unix()
	}
	id, err := s.DB.Insert(ctx, `
		INSERT INTO perms (path, principal_type, principal_id, can_read, can_write, can_delete, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (path, principal_type, principal_id)
		DO UPDATE SET can_read=?, can_write=?, can_delete=?, expires_at=?`,
		path, ptype, pid, ri, wi, di, time.Now().UTC().Format(time.RFC3339Nano), exp,
		ri, wi, di, exp)
	if err != nil {
		return Grant{}, err
	}
	return Grant{ID: id, Path: path, PrincipalType: ptype, PrincipalID: pid,
		Read: r, Write: w, Delete: d, ExpiresAt: expiresAt,
		Expired: expiresAt != nil && expiresAt.Before(time.Now())}, nil
}

// Ensure inserts the grant unless this exact (path, principal) row already
// exists, never touching an existing one. Used to provision defaults once:
// a default an admin later revokes stays revoked across restarts.
func (s *Store) Ensure(ctx context.Context, path, ptype string, pid int64, r, w, d bool) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO perms (path, principal_type, principal_id, can_read, can_write, can_delete, created_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (path, principal_type, principal_id) DO NOTHING`,
		path, ptype, pid, b2i(r), b2i(w), b2i(d), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// HasGrant reports whether the exact (path, principal) row exists.
func (s *Store) HasGrant(ctx context.Context, path, ptype string, pid int64) (bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		"SELECT id FROM perms WHERE path=? AND principal_type=? AND principal_id=?",
		path, ptype, pid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM perms WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("grant not found")
	}
	return nil
}

// grantFrom is shared by the grant listing and its COUNT companion.
// Principal labels: a user/group row shows its name and tenant; a tenant
// row shows the tenant it targets; a world row shows the reserved "*" so
// the UI can label it as "all users".
const grantFrom = `FROM perms p
	      LEFT JOIN users u ON p.principal_type='user'    AND u.id = p.principal_id
	      LEFT JOIN groups g ON p.principal_type='group'  AND g.id = p.principal_id
	      LEFT JOIN tenants th ON p.principal_type='tenant' AND th.id = p.principal_id
	      LEFT JOIN tenants tu ON tu.id = u.tenant_id
	      LEFT JOIN tenants tg ON tg.id = g.tenant_id`

const principalNameSQL = `COALESCE(u.username, g.name, th.name,
	         CASE WHEN p.principal_type='all' THEN '*' END, '?')`
const principalTenantSQL = `COALESCE(tu.name, tg.name, th.name,
	         CASE WHEN p.principal_type='all' THEN '*' END, '?')`

const grantSelect = `SELECT p.id, p.path, p.principal_type, p.principal_id,
	         p.can_read, p.can_write, p.can_delete, p.expires_at,
	         ` + principalNameSQL + `,
	         ` + principalTenantSQL + `,
	         CASE WHEN p.principal_type='user' THEN COALESCE(u.active,0)
	              WHEN p.principal_type='group' THEN COALESCE(g.active,0)
	              WHEN p.principal_type='tenant' THEN COALESCE(th.active,0)
	              ELSE 1 END,
	         COALESCE(u.tenant_id, g.tenant_id, th.id, 0)
	      `

func scanGrants(rows *sql.Rows) ([]Grant, error) {
	out := []Grant{}
	now := time.Now().Unix()
	for rows.Next() {
		var (
			gr      Grant
			r, w, d int
			pact    int
			exp     sql.NullInt64
		)
		if err := rows.Scan(&gr.ID, &gr.Path, &gr.PrincipalType, &gr.PrincipalID,
			&r, &w, &d, &exp, &gr.PrincipalName, &gr.PrincipalTenant, &pact, &gr.PrincipalTID); err != nil {
			return nil, err
		}
		gr.Read, gr.Write, gr.Delete = r != 0, w != 0, d != 0
		gr.PrincipalActive = pact != 0
		if exp.Valid {
			t := time.Unix(exp.Int64, 0).UTC()
			gr.ExpiresAt = &t
			gr.Expired = exp.Int64 <= now
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// ListByPath returns one page of the explicit grants attached to a single
// path plus the total grant count on that path.
// Sort keys: principal, type, expires. Search columns: principal, tenant.
func (s *Store) ListByPath(ctx context.Context, path string, o query.List) ([]Grant, int, error) {
	conds := []string{"p.path = ?"}
	args := []any{path}
	if o.Q != "" {
		like := o.Like()
		switch o.Col {
		case "principal":
			conds = append(conds, `(`+principalNameSQL+` LIKE ? ESCAPE '\')`)
			args = append(args, like)
		case "tenant":
			conds = append(conds, `(`+principalTenantSQL+` LIKE ? ESCAPE '\')`)
			args = append(args, like)
		default: // "" = every searchable column
			conds = append(conds, `(`+principalNameSQL+` LIKE ? ESCAPE '\' OR `+principalTenantSQL+` LIKE ? ESCAPE '\')`)
			args = append(args, like, like)
		}
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) "+grantFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := map[string]string{"principal": principalNameSQL, "type": "p.principal_type", "expires": "p.expires_at"}[o.Sort]
	q := grantSelect + grantFrom + where + " ORDER BY " + order + o.Order() + ", p.id"
	if o.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, o.Limit, o.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanGrants(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListAllByPath: every explicit grant on a path, no paging. Entity and
// ACL documents are complete statements about a path, and a replace-mode
// import must see the whole row set to know what it removes.
func (s *Store) ListAllByPath(ctx context.Context, path string) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx,
		grantSelect+grantFrom+" WHERE p.path = ? ORDER BY p.id", path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGrants(rows)
}

// GrantsFor: every grant naming one principal, across the whole tree —
// the "what can this identity touch" half of an entity document.
func (s *Store) GrantsFor(ctx context.Context, ptype string, pid int64) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx, grantSelect+grantFrom+
		" WHERE p.principal_type = ? AND p.principal_id = ? ORDER BY p.path, p.id", ptype, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGrants(rows)
}

// GrantByID fetches one grant with its principal's tenant resolved, for
// tenant-scoped authorization of mutations.
func (s *Store) GrantByID(ctx context.Context, id int64) (Grant, error) {
	rows, err := s.DB.QueryContext(ctx, grantSelect+grantFrom+" WHERE p.id = ?", id)
	if err != nil {
		return Grant{}, err
	}
	defer rows.Close()
	list, err := scanGrants(rows)
	if err != nil {
		return Grant{}, err
	}
	if len(list) == 0 {
		return Grant{}, errors.New("grant not found")
	}
	return list[0], nil
}

// Effective computes the permission bits for user on path.
// Resolution order, walking ancestors deepest-first:
//  1. explicit grants for the user at this level  (wins over groups here)
//  2. union of the user's group grants at this level
//  3. grants to the caller's own tenant ("any member of tenant T")
//  4. world grants ("any authenticated user")
//  5. continue up; no grants anywhere = deny everything.
//
// Each layer shadows the less specific ones at the same level, which is
// what makes zero-grant exclusion work: an explicit all-zero row for the
// user at a path denies there even when a group/tenant/world row at the
// same path would allow. That per-layer structure is also the seam a
// future explicit deny (negative bits per user/group/tenant) plugs into:
// the subtraction happens inside a layer, not between them.
//
// Before any grant is consulted, the tenant-home partition applies: a path
// whose first segment names a HomeSafe tenant belongs to that tenant
// exclusively — see HomeOwner.
//
// Grants whose expires_at has passed are treated as if absent entirely
// (they neither grant bits nor stop inheritance).
//
// Only platform super admins bypass. Tenant admins do NOT: the shared tree
// outside homes is cross-tenant by design, so their file access follows
// grants like anyone else's.
func (s *Store) Effective(ctx context.Context, a *auth.Store, path string, u auth.User) (Bits, error) {
	if u.IsSuper {
		return Bits{Read: true, Write: true, Delete: true}, nil
	}
	if owner, _, err := s.HomeOwner(ctx, path); err != nil {
		return Bits{}, err
	} else if owner != "" && owner != u.Tenant {
		// Hard partition: inside someone else's home a non-member has no
		// bits at all, whatever rows exist. Callers see 403s; the list
		// entry filter hides the home directory itself entirely.
		return Bits{}, nil
	}
	groupIDs, err := a.GroupIDsFor(ctx, u.ID)
	if err != nil {
		return Bits{}, err
	}
	for _, p := range Ancestors(path) {
		// 1. direct user grants at this level
		bits, found, err := s.collect(ctx, p, "user", []int64{u.ID})
		if err != nil {
			return Bits{}, err
		}
		if found {
			bits.Source = p
			return bits, nil
		}
		// 2. group grants at this level
		if len(groupIDs) > 0 {
			bits, found, err := s.collect(ctx, p, "group", groupIDs)
			if err != nil {
				return Bits{}, err
			}
			if found {
				bits.Source = p
				return bits, nil
			}
		}
		// 3. the caller's own-tenant rows: "any user in tenant T".
		bits, found, err = s.collect(ctx, p, "tenant", []int64{u.TenantID})
		if err != nil {
			return Bits{}, err
		}
		if found {
			bits.Source = p
			return bits, nil
		}
		// 4. world rows: "any authenticated user". Stored under the
		// reserved principal_id 0 — no user, group or tenant has id 0.
		bits, found, err = s.collect(ctx, p, "all", []int64{0})
		if err != nil {
			return Bits{}, err
		}
		if found {
			bits.Source = p
			return bits, nil
		}
	}
	return Bits{}, nil
}

// HomeOwner: if the first segment of path is (or lies under) a tenant home,
// returns that tenant's name and id. Name-based on purpose: the boundary
// exists the moment the tenant row does — before/without any directory —
// and names that fail auth.HomeSafe (legacy rows, reserved "help") are
// never homes, consistently with creation-time validation and the startup
// backfill. Returns ("", 0, nil) for unpartitioned paths.
func (s *Store) HomeOwner(ctx context.Context, path string) (string, int64, error) {
	if path == "/" {
		return "", 0, nil
	}
	seg := path[1:]
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if !auth.HomeSafe(seg) {
		return "", 0, nil
	}
	var name string
	var id int64
	err := s.DB.QueryRowContext(ctx, "SELECT name, id FROM tenants WHERE name = ?", seg).
		Scan(&name, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	return name, id, nil
}

func (s *Store) collect(ctx context.Context, path, ptype string, ids []int64) (Bits, bool, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	q := fmt.Sprintf(`SELECT can_read, can_write, can_delete FROM perms
		WHERE path=? AND principal_type=? AND principal_id IN (%s)
		  AND (expires_at IS NULL OR expires_at > ?)`, ph)
	args := append([]any{path, ptype}, toAny(ids)...)
	args = append(args, time.Now().Unix())
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return Bits{}, false, err
	}
	defer rows.Close()
	var b Bits
	found := false
	for rows.Next() {
		var r, w, d int
		if err := rows.Scan(&r, &w, &d); err != nil {
			return b, false, err
		}
		found = true
		b.Read = b.Read || r != 0
		b.Write = b.Write || w != 0
		b.Delete = b.Delete || d != 0
	}
	return b, found, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func toAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, v := range ids {
		out[i] = v
	}
	return out
}

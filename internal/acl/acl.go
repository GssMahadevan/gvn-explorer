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

	"github.com/user/llm-crud1/internal/auth"
)

type Grant struct {
	ID              int64      `json:"id"`
	Path            string     `json:"path"`
	PrincipalType   string     `json:"principal_type"` // "user" | "group"
	PrincipalID     int64      `json:"principal_id"`
	PrincipalName   string     `json:"principal_name,omitempty"`
	PrincipalTenant string     `json:"principal_tenant,omitempty"`
	Read            bool       `json:"read"`
	Write           bool       `json:"write"`
	Delete          bool       `json:"delete"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"` // nil = never
	Expired         bool       `json:"expired"`              // expires_at in the past
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

type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// Upsert replaces any existing grant for (path, principal) with the new bits
// and expiry. expiresAt nil = grant never expires.
func (s *Store) Upsert(ctx context.Context, path, ptype string, pid int64, r, w, d bool, expiresAt *time.Time) (Grant, error) {
	ri, wi, di := b2i(r), b2i(w), b2i(d)
	var exp any // NULL when nil
	if expiresAt != nil {
		exp = expiresAt.Unix()
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO perms (path, principal_type, principal_id, can_read, can_write, can_delete, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (path, principal_type, principal_id)
		DO UPDATE SET can_read=?, can_write=?, can_delete=?, expires_at=?`,
		path, ptype, pid, ri, wi, di, time.Now().UTC().Format(time.RFC3339Nano), exp,
		ri, wi, di, exp)
	if err != nil {
		return Grant{}, err
	}
	id, _ := res.LastInsertId()
	return Grant{ID: id, Path: path, PrincipalType: ptype, PrincipalID: pid,
		Read: r, Write: w, Delete: d, ExpiresAt: expiresAt,
		Expired: expiresAt != nil && expiresAt.Before(time.Now())}, nil
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

// ListByPath returns the explicit grants attached to a single path.
func (s *Store) ListByPath(ctx context.Context, path string) ([]Grant, error) {
	return s.queryGrants(ctx, "WHERE p.path = ?", []any{path})
}

// ListAll returns every grant (admin UI overview).
func (s *Store) ListAll(ctx context.Context) ([]Grant, error) {
	return s.queryGrants(ctx, "", nil)
}

func (s *Store) queryGrants(ctx context.Context, where string, args []any) ([]Grant, error) {
	q := `SELECT p.id, p.path, p.principal_type, p.principal_id,
	         p.can_read, p.can_write, p.can_delete, p.expires_at,
	         COALESCE(u.username, g.name, '?'),
	         COALESCE(tu.name, tg.name, '?')
	      FROM perms p
	      LEFT JOIN users u ON p.principal_type='user'  AND u.id = p.principal_id
	      LEFT JOIN groups g ON p.principal_type='group' AND g.id = p.principal_id
	      LEFT JOIN tenants tu ON tu.id = u.tenant_id
	      LEFT JOIN tenants tg ON tg.id = g.tenant_id ` + where + `
	      ORDER BY p.path, p.principal_type, p.principal_id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	now := time.Now().Unix()
	for rows.Next() {
		var (
			gr      Grant
			r, w, d int
			exp     sql.NullInt64
		)
		if err := rows.Scan(&gr.ID, &gr.Path, &gr.PrincipalType, &gr.PrincipalID,
			&r, &w, &d, &exp, &gr.PrincipalName, &gr.PrincipalTenant); err != nil {
			return nil, err
		}
		gr.Read, gr.Write, gr.Delete = r != 0, w != 0, d != 0
		if exp.Valid {
			t := time.Unix(exp.Int64, 0).UTC()
			gr.ExpiresAt = &t
			gr.Expired = exp.Int64 <= now
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// GrantByID fetches one grant with its principal's tenant resolved, for
// tenant-scoped authorization of mutations.
func (s *Store) GrantByID(ctx context.Context, id int64) (Grant, error) {
	list, err := s.queryGrants(ctx, "WHERE p.id = ?", []any{id})
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
//  3. continue up; no grants anywhere = deny everything.
//
// Grants whose expires_at has passed are treated as if absent entirely
// (they neither grant bits nor stop inheritance).
//
// Only platform super admins bypass. Tenant admins do NOT: the file tree is
// shared across tenants, so their file access follows grants like anyone else.
func (s *Store) Effective(ctx context.Context, a *auth.Store, path string, u auth.User) (Bits, error) {
	if u.IsSuper {
		return Bits{Read: true, Write: true, Delete: true}, nil
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
	}
	return Bits{}, nil
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

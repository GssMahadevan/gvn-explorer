// Package auth: tenants, tenant-scoped users and groups, memberships,
// bcrypt passwords, cookie sessions.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrTenantMismatch = errors.New("user and group must belong to the same tenant")
)

type Tenant struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type User struct {
	ID        int64      `json:"id"`
	TenantID  int64      `json:"tenant_id"`
	Tenant    string     `json:"tenant"`
	Username  string     `json:"username"`
	IsAdmin   bool       `json:"is_admin"` // tenant admin
	IsSuper   bool       `json:"is_super"` // platform super admin
	Active    bool       `json:"active"`   // false = suspended: no action allowed, but history kept
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // nil = never
	Expired   bool       `json:"expired"`              // ExpiresAt in the past
}

type Group struct {
	ID       int64    `json:"id"`
	TenantID int64    `json:"tenant_id"`
	Tenant   string   `json:"tenant"`
	Name     string   `json:"name"`
	Active   bool     `json:"active"` // false = grants through it are ignored
	Members  []Member `json:"members,omitempty"`
}

type Member struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

const tsFmt = time.RFC3339Nano

// ---- tenants ----

func (s *Store) CreateTenant(ctx context.Context, name string) (Tenant, error) {
	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx, "INSERT INTO tenants (name, created_at) VALUES (?,?)",
		name, now.Format(tsFmt))
	if err != nil {
		return Tenant{}, err
	}
	id, _ := res.LastInsertId()
	return Tenant{ID: id, Name: name, CreatedAt: now}, nil
}

func (s *Store) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name, created_at FROM tenants ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var (
			t     Tenant
			caStr string
		)
		if err := rows.Scan(&t.ID, &t.Name, &caStr); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(tsFmt, caStr)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) tenantIDByName(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, "SELECT id FROM tenants WHERE name = ?", name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// DeleteTenant drops the tenant and, with it, its users/groups/memberships
// (FK cascade) plus every ACL grant that targeted one of its principals.
func (s *Store) DeleteTenant(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM perms WHERE
			(principal_type='user'  AND principal_id IN (SELECT id FROM users  WHERE tenant_id=?)) OR
			(principal_type='group' AND principal_id IN (SELECT id FROM groups WHERE tenant_id=?))`, id, id)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM tenants WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- users ----

type rowScanner interface{ Scan(dest ...any) error }

const userCols = `u.id, u.tenant_id, t.name, u.username, u.is_admin, u.is_super, u.active, u.created_at, u.expires_at`
const userFrom = `FROM users u JOIN tenants t ON t.id = u.tenant_id`

// CreateUser adds a user inside tenant. admin marks a tenant admin; super
// marks the platform super admin (bootstrap path only — the API never sets it).
// expiresAt nil = account never expires. An empty password means "device
// user": a random hash is stored, so interactive login can never succeed —
// access goes through auth_factors (API keys) instead.
func (s *Store) CreateUser(ctx context.Context, tenant, username, password string, admin, super bool, expiresAt *time.Time) (User, error) {
	tid, err := s.tenantIDByName(ctx, tenant)
	if err != nil {
		return User{}, err
	}
	if password == "" {
		if password, err = randomKey(); err != nil { // unknowable; login impossible
			return User{}, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	var exp any // NULL when nil
	if expiresAt != nil {
		exp = expiresAt.Unix()
	}
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO users (tenant_id, username, pass_hash, is_admin, is_super, created_at, expires_at) VALUES (?,?,?,?,?,?,?)",
		tid, username, string(hash), b2i(admin), b2i(super), now.Format(tsFmt), exp)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, TenantID: tid, Tenant: tenant, Username: username,
		IsAdmin: admin, IsSuper: super, Active: true, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Verify checks tenant+username+password and returns the user,
// or ErrNotFound on mismatch. Expired and deactivated accounts report
// ErrNotFound too, so callers cannot enumerate which accounts exist but
// are lapsed or suspended.
func (s *Store) Verify(ctx context.Context, tenant, username, password string) (User, error) {
	var (
		u       User
		hash    string
		ad, sup int
		act     int
		caStr   string
		exp     sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT u.id, u.tenant_id, t.name, u.username, u.pass_hash, u.is_admin, u.is_super, u.active, u.created_at, u.expires_at "+
			userFrom+" WHERE u.username = ? AND t.name = ?", username, tenant).
		Scan(&u.ID, &u.TenantID, &u.Tenant, &u.Username, &hash, &ad, &sup, &act, &caStr, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		u.ExpiresAt = &t
		u.Expired = exp.Int64 <= time.Now().Unix()
	}
	if u.Expired || act == 0 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrNotFound
	}
	u.IsAdmin, u.IsSuper, u.Active = ad != 0, sup != 0, act != 0
	u.CreatedAt, _ = time.Parse(tsFmt, caStr)
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	row := s.DB.QueryRowContext(ctx, "SELECT "+userCols+" "+userFrom+" WHERE u.id = ?", id)
	u, err := s.scanUserRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) UserByName(ctx context.Context, tenant, username string) (User, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT "+userCols+" "+userFrom+" WHERE u.username = ? AND t.name = ?", username, tenant)
	u, err := s.scanUserRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) scanUserRow(sc rowScanner) (User, error) {
	var (
		id, tid  int64
		tenant   string
		username string
		ad, sup  int
		act      int
		caStr    string
		exp      sql.NullInt64
	)
	if err := sc.Scan(&id, &tid, &tenant, &username, &ad, &sup, &act, &caStr, &exp); err != nil {
		return User{}, err
	}
	u := User{ID: id, TenantID: tid, Tenant: tenant, Username: username,
		IsAdmin: ad != 0, IsSuper: sup != 0, Active: act != 0}
	u.CreatedAt, _ = time.Parse(tsFmt, caStr)
	if exp.Valid {
		t := time.Unix(exp.Int64, 0).UTC()
		u.ExpiresAt = &t
		u.Expired = exp.Int64 <= time.Now().Unix()
	}
	return u, nil
}

// ListUsers lists users; tenantID 0 means all tenants (super admin view).
func (s *Store) ListUsers(ctx context.Context, tenantID int64) ([]User, error) {
	q := "SELECT " + userCols + " " + userFrom
	var args []any
	if tenantID != 0 {
		q += " WHERE u.tenant_id = ?"
		args = append(args, tenantID)
	}
	q += " ORDER BY t.name, u.username"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := s.scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetPassword(ctx context.Context, id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE users SET pass_hash = ? WHERE id = ?", string(hash), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserActive suspends (false) or reinstates (true) a user. Inactive is a
// kill switch, not a deletion: the row, its grants, factors and history stay,
// but login, live sessions and bearer keys stop working on the next request.
func (s *Store) SetUserActive(ctx context.Context, id int64, active bool) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE users SET active = ? WHERE id = ?", b2i(active), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TenantID resolves a tenant name to its id (ErrNotFound when unknown);
// exported for audit scoping of unauthenticated attempts.
func (s *Store) TenantID(ctx context.Context, name string) (int64, error) {
	return s.tenantIDByName(ctx, name)
}

// ---- groups ----

const groupCols = `g.id, g.tenant_id, t.name, g.name, g.active`
const groupFrom = `FROM groups g JOIN tenants t ON t.id = g.tenant_id`

func scanGroup(sc rowScanner) (Group, error) {
	var (
		g   Group
		act int
	)
	if err := sc.Scan(&g.ID, &g.TenantID, &g.Tenant, &g.Name, &act); err != nil {
		return Group{}, err
	}
	g.Active = act != 0
	return g, nil
}

// CreateGroup adds a group named name inside tenant.
func (s *Store) CreateGroup(ctx context.Context, tenant, name string) (Group, error) {
	tid, err := s.tenantIDByName(ctx, tenant)
	if err != nil {
		return Group{}, err
	}
	res, err := s.DB.ExecContext(ctx, "INSERT INTO groups (tenant_id, name) VALUES (?,?)", tid, name)
	if err != nil {
		return Group{}, err
	}
	id, _ := res.LastInsertId()
	return Group{ID: id, TenantID: tid, Tenant: tenant, Name: name, Active: true}, nil
}

// ListGroups lists groups with members; tenantID 0 means all tenants.
func (s *Store) ListGroups(ctx context.Context, tenantID int64) ([]Group, error) {
	q := "SELECT " + groupCols + " " + groupFrom
	var args []any
	if tenantID != 0 {
		q += " WHERE g.tenant_id = ?"
		args = append(args, tenantID)
	}
	q += " ORDER BY t.name, g.name"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	index := map[int64]int{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		index[g.ID] = len(out)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(index) == 0 {
		return out, nil
	}
	mrows, err := s.DB.QueryContext(ctx, `SELECT gm.group_id, u.id, u.username
		FROM group_members gm JOIN users u ON u.id = gm.user_id ORDER BY gm.group_id, u.username`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var gid, uid int64
		var name string
		if err := mrows.Scan(&gid, &uid, &name); err != nil {
			return nil, err
		}
		if i, ok := index[gid]; ok {
			out[i].Members = append(out[i].Members, Member{ID: uid, Username: name})
		}
	}
	return out, mrows.Err()
}

func (s *Store) GroupByID(ctx context.Context, id int64) (Group, error) {
	g, err := scanGroup(s.DB.QueryRowContext(ctx, "SELECT "+groupCols+" "+groupFrom+" WHERE g.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	return g, err
}

func (s *Store) GroupByName(ctx context.Context, tenant, name string) (Group, error) {
	g, err := scanGroup(s.DB.QueryRowContext(ctx,
		"SELECT "+groupCols+" "+groupFrom+" WHERE g.name = ? AND t.name = ?", name, tenant))
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	return g, err
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM groups WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGroupActive suspends or reinstates a group; while inactive, every
// grant through it is ignored (see GroupIDsFor).
func (s *Store) SetGroupActive(ctx context.Context, id int64, active bool) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE groups SET active = ? WHERE id = ?", b2i(active), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMember enrolls a user in a group; both must belong to the same tenant.
func (s *Store) AddMember(ctx context.Context, groupID, userID int64) error {
	var gt, ut sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		"SELECT (SELECT tenant_id FROM groups WHERE id=?), (SELECT tenant_id FROM users WHERE id=?)",
		groupID, userID).Scan(&gt, &ut)
	if err != nil {
		return err
	}
	if !gt.Valid || !ut.Valid {
		return ErrNotFound
	}
	if gt.Int64 != ut.Int64 {
		return ErrTenantMismatch
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO group_members (group_id, user_id) VALUES (?,?)", groupID, userID)
	return err
}

func (s *Store) RemoveMember(ctx context.Context, groupID, userID int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM group_members WHERE group_id=? AND user_id=?", groupID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupIDsFor lists the *active* groups a user belongs to (for ACL
// resolution). Membership is same-tenant by construction (see AddMember).
// An inactive group contributes nothing: its grants are ignored exactly like
// an expired one, while the row and its grants survive for reinstatement.
func (s *Store) GroupIDsFor(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT gm.group_id FROM group_members gm JOIN groups g ON g.id = gm.group_id
		 WHERE gm.user_id = ? AND g.active = 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- sessions ----

func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession issues a session token for the user, valid for ttl.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx,
		"INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?,?,?,?)",
		id, userID, now.Format(tsFmt), now.Add(ttl).Format(tsFmt))
	return id, err
}

// SessionUser resolves a session token to its user; expired or unknown
// session -> ErrNotFound. An account-level expiry (customer accounts with a
// lifespan) also kills live sessions, checked here on every request.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	var (
		userID int64
		expStr string
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT user_id, expires_at FROM sessions WHERE id = ?", token).Scan(&userID, &expStr)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	exp, err := time.Parse(tsFmt, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", token)
		return User{}, ErrNotFound
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if u.Expired || !u.Active { // lapsed or suspended account kills live sessions
		s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", token)
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", token)
	return err
}
